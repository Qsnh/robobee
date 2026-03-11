# Message Queue & Debounce Design

**Date:** 2026-03-11
**Status:** Approved
**Topic:** Message recording, queue-based concurrency control, and debounce merging

---

## Background

The current system processes each incoming platform message by spawning an async goroutine directly. There is no concurrency control per session — two messages to the same session can trigger two concurrent executions. There is also no audit record of raw incoming messages.

This design introduces:
1. **Message recording** — every incoming message is persisted to DB immediately
2. **Queue-based concurrency control** — at most one execution per `session_key+worker_id` at any time
3. **Rolling debounce** — messages arriving within the debounce window are merged into a single input before execution

---

## Requirements

- Every platform message must be recorded to the database on arrival (audit + queue state)
- A queue per `session_key+worker_id` prevents concurrent execution for the same session
- Debounce uses a rolling window (default 3s, configurable): each new message resets the timer
- Messages within the debounce window are concatenated in arrival order
- While an execution is running, new debounced messages accumulate in a pending slot (still merged)
- When execution completes, the pending slot (if any) is immediately dispatched
- Execution failure does not discard the pending slot — processing continues regardless

---

## Data Model

### New Table: `platform_messages`

```sql
CREATE TABLE platform_messages (
    id           TEXT PRIMARY KEY,      -- UUID
    session_key  TEXT NOT NULL,         -- e.g. "feishu:chatID:openId"
    platform     TEXT NOT NULL,         -- feishu / dingtalk / mail
    worker_id    TEXT,                  -- populated after routing
    content      TEXT NOT NULL,         -- raw message content
    status       TEXT NOT NULL DEFAULT 'received',
    merged_into  TEXT,                  -- FK to surviving message if merged
    received_at  DATETIME NOT NULL,
    processed_at DATETIME               -- set when status reaches terminal state
);
CREATE INDEX idx_platform_messages_session
    ON platform_messages(session_key, worker_id, status);
```

### Status Flow

```
received → routed → debouncing → merged → executing → done
                                                    ↘ failed
```

- `received`: message stored, routing not yet done
- `routed`: worker_id assigned, message entering debounce buffer
- `debouncing`: message is in the rolling debounce window
- `merged`: debounce fired; surviving message awaits or is in execution; merged-away messages have `merged_into` set
- `executing`: merged content is currently being executed
- `done`: execution completed successfully
- `failed`: execution completed with an error (audit-distinguishable from `done`)

Both `done` and `failed` are terminal. `OnExecutionDone` is called for both; the caller passes a success flag to determine which terminal status to write.

---

## Architecture

### New Package: `internal/platform/queue`

#### `SessionQueue`

One instance per `session_key+worker_id` pair. Manages debounce timer and pending execution slot.

```
┌─────────────────────────────────────────────────────┐
│ SessionQueue                                        │
│                                                     │
│  debounceTimer   *time.Timer   — rolling timer      │
│  debounceIDs     []string      — message IDs in     │
│                                  debounce buffer    │
│  debounceContent string        — concatenated text  │
│                                                     │
│  pendingContent  string        — waiting for exec   │
│  pendingIDs      []string      — corresponding IDs  │
│                                                     │
│  isExecuting     bool                               │
│  mu              sync.Mutex                         │
└─────────────────────────────────────────────────────┘
```

**Invariants:**
- At most 1 active execution per SessionQueue
- At most 1 pending slot (new debounced content appends into it)
- Queue never unboundedly grows

**Memory lifecycle:** A `SessionQueue` is eligible for removal from the manager map when all of the following are true: `isExecuting == false`, `debounceContent == ""`, `pendingContent == ""`. The manager checks this condition at the end of `OnExecutionDone` and removes the entry if idle.

#### `MessageQueueManager`

Global manager for all SessionQueues.

```
┌────────────────────────────────────────────────────────────┐
│ MessageQueueManager                                        │
│                                                            │
│  queues    map[string]*SessionQueue  — key: sessionKey+wID │
│  mu        sync.RWMutex                                    │
│  store     MessageStore                                    │
│  executor  func(sessionKey, workerID, content)             │
│  debounce  time.Duration             — from config         │
│                                                            │
│  Enqueue(sessionKey, workerID, msgID, content)             │
│  OnExecutionDone(sessionKey, workerID, success bool)       │
│  CancelSession(sessionKey, workerID)  — for clear cmd      │
│  RecoverFromDB()                                           │
└────────────────────────────────────────────────────────────┘
```

#### `MessageStore` Interface

```go
type MessageStore interface {
    CreateMessage(ctx context.Context, msg *PlatformMessage) error
    UpdateMessageStatus(ctx context.Context, id string, status string) error
    UpdateMessageWorker(ctx context.Context, id string, workerID string) error
    MarkMerged(ctx context.Context, primaryID string, mergedIDs []string) error
    MarkTerminal(ctx context.Context, ids []string, status string) error  // done or failed
    GetUnfinished(ctx context.Context) ([]*PlatformMessage, error)
}
```

---

## Merged Content Format

When multiple messages are merged, the canonical format is:

```
<first message content>

---

<second message content>

---

<third message content>
```

Each message is separated by `\n\n---\n\n`. This format is passed verbatim as `content` to the executor callback, which wraps it in the existing `ExecuteWorker` prompt construction. The executor must not add additional separators.

---

## Message Flow

```
Platform message arrives
        │
        ▼
1. Write to platform_messages (status: received)
   Send ACK to user immediately: "⏳ 正在处理，请稍候…"
        │
        ▼
2. BotRouter.Route() → worker_id
   Update message status: routed
        │
        ▼
3. QueueManager.Enqueue(sessionKey, workerID, msgID, content)
        │
        ├─ Lock SessionQueue
        ├─ debounceContent = append with separator (see Merged Content Format)
        ├─ debounceIDs = append(debounceIDs, msgID)
        ├─ Update all debounceIDs status: debouncing
        ├─ Reset debounceTimer (Stop + Reset to config.DebounceWindow)
        └─ Unlock
                │
                ▼ (debounce window expires)
4. Timer callback fires:
        │
        ├─ Lock SessionQueue
        ├─ Merge debounceContent → mergedContent
        ├─ Update DB: primary msg status=merged, others merged_into=primaryID
        ├─ Clear debounce buffer
        │
        ├─ [isExecuting == false]
        │       isExecuting = true
        │       Update DB status: executing
        │       Unlock
        │       go executor(sessionKey, workerID, mergedContent)
        │
        └─ [isExecuting == true]
                Append mergedContent to pendingContent (separator format)
                Append IDs to pendingIDs
                Unlock
                (status remains merged, waiting)

5. Execution completes → OnExecutionDone(sessionKey, workerID, success bool)
        │
        ├─ Lock SessionQueue
        ├─ Write terminal status (done or failed) + processed_at for executing IDs
        │
        ├─ [pendingContent != ""]
        │       take pendingContent + pendingIDs
        │       clear pending slot
        │       isExecuting = true
        │       Update DB status: executing
        │       Unlock
        │       go executor(sessionKey, workerID, pendingContent)
        │
        └─ [pendingContent == ""]
                isExecuting = false
                Remove SessionQueue from manager map (idle cleanup)
                Unlock
```

**ACK timing:** The acknowledgment (`⏳ 正在处理，请稍候…`) is sent immediately after recording the message (step 1), before debounce fires. This informs the user that their message was received, even if execution is delayed by the debounce window or by an in-progress execution. No second ACK is sent when debounce fires or when execution actually starts.

---

## Executor Callback & Session State

The `executor` callback signature is:

```go
func(sessionKey, workerID, mergedContent string)
```

The executor is responsible for the full `Pipeline.Handle` logic, including:
- Querying `platform_sessions` for `LastExecutionID`
- Calling `Manager.ExecuteWorker` (new session) or `Manager.ReplyExecution` (continuation)

This preserves the existing reply-chaining behavior. The `mergedContent` replaces the original single-message content as the input. The executor is wired up in `platform/manager.go` and closes over the pipeline reference.

The `OnExecutionDone` callback receives `sessionKey` and `workerID`. These values must be stored in the `worker_executions` record (or passed through the monitor goroutine closure) so `monitorExecution` in `worker/manager.go` can call `OnExecutionDone` at the end of execution, after cleanup. The `worker_executions` table already stores `session_id`; `session_key` and `worker_id` should be added as columns or passed via a closure captured at `launchRuntime` time.

---

## Startup Recovery

On service start, `RecoverFromDB()` queries unfinished messages:

```sql
SELECT session_key, worker_id, id, content, status
FROM platform_messages
WHERE status IN ('routed', 'debouncing', 'merged', 'executing')
  AND worker_id IS NOT NULL
ORDER BY received_at ASC
```

Recovery behavior (all cases assume `isExecuting == false` at startup):
- `routed` / `debouncing` → debounce window already elapsed; concatenate contents in arrival order. Update DB: primary=merged, others merged_into=primary. Then **start execution directly** (set `isExecuting = true`, update DB status: executing, call executor).
- `merged` → content already merged; **start execution directly** (set `isExecuting = true`, update DB status: executing, call executor).
- `executing` → treat as needing re-execution (the previous process crashed); **start execution directly**. Accept risk of double-execution: the worker is idempotent by design (Claude sessions are resumable), and the alternative (skipping) causes silent message loss.

If multiple session keys are recovered, each starts its own executor goroutine independently (no cross-session serialization needed).

Note on timer race: within a single `SessionQueue`, all debounce timer callbacks and `Enqueue` calls are serialized by `SessionQueue.mu`. There is no residual timer race.

---

## `clear` Command Interaction

When a `clear` command arrives for a session:

1. The pipeline short-circuits before queueing (existing behavior preserved)
2. `QueueManager.CancelSession(sessionKey, workerID)` is called:
   - Stops the debounce timer if active
   - Clears `debounceContent` and `debounceIDs`
   - Clears `pendingContent` and `pendingIDs`
   - Updates DB: all non-terminal messages for this session → status `failed`, `processed_at` set to current time
   - Does **not** cancel in-flight executions (the existing clear logic handles that via the session store)

---

## Configuration

```yaml
message_queue:
  debounce_window: 3s   # Go duration format; default 3s
```

```go
type MessageQueueConfig struct {
    DebounceWindow time.Duration `yaml:"debounce_window"`
}
```

---

## Integration Points

| File | Change |
|------|--------|
| `internal/platform/pipeline.go` | Write message to DB on arrival; send ACK; call `QueueManager.Enqueue` instead of executing directly |
| `internal/platform/manager.go` | Initialize `MessageQueueManager`; wire executor callback that calls existing pipeline logic |
| `internal/worker/manager.go` | Call `QueueManager.OnExecutionDone(sessionKey, workerID, success)` at end of `monitorExecution`; store sessionKey+workerID in execution record or closure |
| `internal/store/db.go` | Add `platform_messages` table DDL and migration |
| `internal/store/message_store.go` | New file implementing `MessageStore` interface |
| `internal/config/` | Add `MessageQueueConfig` to main config struct |

---

## Error Handling

- Execution failure (`failed` status) does **not** discard the pending slot; `OnExecutionDone(success=false)` still triggers the next pending item
- DB write failures on message recording are logged but do not block message processing
- If `BotRouter.Route()` fails, message stays at `received` status and is not enqueued; this is an existing failure mode and out of scope for this design

---

## Testing

- Unit test `SessionQueue`: debounce timer reset, content concatenation, pending slot behavior, idle cleanup
- Unit test `MessageQueueManager`: concurrent enqueues for different sessions, `CancelSession` drains correctly
- Integration test: two rapid messages to same session → only one execution triggered with merged content
- Integration test: message arrives during execution → queued, dispatched after completion
- Integration test: `clear` command during debounce window → debounce cancelled, pending cleared
- Integration test: service restart with `routed`/`debouncing` messages → recovery merges and executes
- Integration test: service restart with `executing` message → re-executed after recovery
