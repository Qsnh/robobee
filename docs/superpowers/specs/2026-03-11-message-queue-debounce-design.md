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
    processed_at DATETIME               -- set when status → done
);
CREATE INDEX idx_platform_messages_session
    ON platform_messages(session_key, worker_id, status);
```

### Status Flow

```
received → routed → debouncing → merged → executing → done
```

- `received`: message stored, routing not yet done
- `routed`: worker_id assigned, message entering debounce buffer
- `debouncing`: message is in the rolling debounce window
- `merged`: debounce fired; surviving message awaits or is in execution; merged-away messages have `merged_into` set
- `executing`: merged content is currently being executed
- `done`: execution completed (success or failure)

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
│  OnExecutionDone(sessionKey, workerID)                     │
│  RecoverFromDB()                                           │
└────────────────────────────────────────────────────────────┘
```

---

## Message Flow

```
Platform message arrives
        │
        ▼
1. Write to platform_messages (status: received)
        │
        ▼
2. BotRouter.Route() → worker_id
   Update status: routed
        │
        ▼
3. QueueManager.Enqueue(sessionKey, workerID, msgID, content)
        │
        ├─ Lock SessionQueue
        ├─ debounceContent += "\n" + content
        ├─ debounceIDs = append(debounceIDs, msgID)
        ├─ Update all debounceIDs status: debouncing
        ├─ Reset debounceTimer (Stop + Reset to config.DebounceWindow)
        └─ Unlock
                │
                ▼ (debounce window expires)
4. Timer callback fires:
        │
        ├─ Merge debounceContent → mergedContent
        ├─ Update DB: primary msg status=merged, others merged_into=primaryID
        ├─ Clear debounce buffer
        │
        ├─ [isExecuting == false]
        │       isExecuting = true
        │       Update DB status: executing
        │       go executor(sessionKey, workerID, mergedContent)
        │
        └─ [isExecuting == true]
                pendingContent = pendingContent + "\n" + mergedContent
                pendingIDs = append(pendingIDs, ...)
                (status remains merged, waiting)

5. Execution completes → OnExecutionDone(sessionKey, workerID)
        │
        ├─ Update executed message IDs status: done, processed_at=now
        │
        ├─ [pendingContent != ""]
        │       take pendingContent + pendingIDs
        │       clear pending slot
        │       isExecuting = true
        │       Update DB status: executing
        │       go executor(sessionKey, workerID, pendingContent)
        │
        └─ [pendingContent == ""]
                isExecuting = false
```

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

Recovery behavior:
- `routed` / `debouncing` → debounce window already elapsed; merge immediately into queue (skip timer)
- `merged` / `executing` → place directly into pending slot for immediate execution

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
| `internal/platform/pipeline.go` | Write message to DB on arrival; call `QueueManager.Enqueue` instead of executing directly |
| `internal/platform/manager.go` | Initialize `MessageQueueManager`; pass executor callback |
| `internal/worker/manager.go` | Call `QueueManager.OnExecutionDone` after execution completes (success or failure) |
| `internal/store/db.go` | Add `platform_messages` table DDL and migration |
| `internal/config/` | Add `MessageQueueConfig` to main config struct |

---

## Error Handling

- Execution failure does **not** discard the pending slot; `OnExecutionDone` is called regardless of outcome
- If `Enqueue` is called before routing completes (no worker_id yet), the message is still recorded with status `received` and updated once routing resolves
- DB write failures on message recording are logged but do not block message processing

---

## Testing

- Unit test `SessionQueue`: debounce timer reset, content concatenation, pending slot behavior
- Unit test `MessageQueueManager`: concurrent enqueues, recovery from DB state
- Integration test: two rapid messages to same session → only one execution triggered with merged content
- Integration test: message arrives during execution → queued, dispatched after completion
- Integration test: service restart with pending messages → recovery triggers execution
