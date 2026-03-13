# Smart Clear Session Design

## Problem

Currently, sending "clear" directly clears the session context without checking whether tasks are running. This can interrupt in-progress work unexpectedly. The clear message is intercepted by the feeder and bypasses bee entirely, leaving no room for intelligent decision-making.

## Goal

Move clear logic from a hard-coded intercept chain into the bee agent, allowing it to:
1. Check for active tasks before clearing
2. Warn the user if tasks exist
3. On second request, force-cancel all tasks (including killing running worker processes) then clear

## Design

### 1. Remove Old Clear Intercept Chain

**msgingest/gateway.go**
- Remove `CommandClear` constant and its usage in `detectCommand()`
- Remove clear-specific handling in `handleCommand()` (debounce cancel, special emit)
- "clear" messages flow through normal debounce as ordinary messages
- Note: a rapid "hello" then "clear" within the debounce window will merge into one message. This is acceptable — bee can parse the intent from a combined message.

**bee/feeder.go**
- Remove `detectClear()` helper
- Remove `clearMsgs` / `regularMsgs` separation in `tick()`
- Remove `clearCh` channel field and the `dispatchCh` constructor parameter (feeder no longer sends dispatch tasks directly)
- All messages (including "clear") go to bee via `processBeeGroup()`

**dispatcher/dispatcher.go**
- Remove `TaskType == "clear"` branch in `handleInbound()`

**cmd/server/app.go**
- Remove `dispatchCh` parameter from `NewFeeder` call since feeder no longer needs it

### 2. Extend `list_tasks` MCP Tool

Add optional `session_key` parameter, mutually exclusive with `message_id`. Support comma-separated `status` values (e.g., `"pending,running"`).

**MCP tool schema change:**
```json
{
  "name": "list_tasks",
  "inputSchema": {
    "properties": {
      "message_id": { "type": "string", "description": "Filter by message ID" },
      "session_key": { "type": "string", "description": "Filter by session key (mutually exclusive with message_id)" },
      "status": { "type": "string", "description": "Filter by status. Supports comma-separated values, e.g. 'pending,running'" }
    }
  }
}
```

Validation in handler: return error if neither `message_id` nor `session_key` is provided, or if both are provided. Avoid `oneOf` in the schema as LLMs may not follow it reliably.

**Store layer:**
- Add `ListBySessionKey(ctx, sessionKey, status) ([]Task, error)` to `TaskStore`
- Query joins `tasks` with `platform_messages` on `tasks.message_id = platform_messages.id` to filter by `platform_messages.session_key`
- `status` parameter supports comma-separated values; split in Go and build `IN (?, ?)` clause

### 3. New `clear_session` MCP Tool

**Schema:**
```json
{
  "name": "clear_session",
  "description": "Cancel all active tasks (terminating running worker processes), clear dispatcher queues, and reset all session contexts for the given session. Use this to fully reset a conversation session.",
  "inputSchema": {
    "type": "object",
    "properties": {
      "session_key": { "type": "string", "description": "The session key to clear" }
    },
    "required": ["session_key"]
  }
}
```

**Implementation (`mcp/tools.go`) — strict ordering:**

1. Bulk cancel all pending/running tasks in DB via `CancelBySessionKey(sessionKey)` — returns count
2. Query running tasks that had a non-empty `execution_id` (get these before step 1, or query executions directly)
   - Revised approach: before bulk cancel, call `ListBySessionKey(sessionKey, "running")` to collect execution IDs
3. For each running task with a non-empty `execution_id`:
   - Call `ExecutionStopper.StopExecution(executionID)` to kill the worker subprocess
   - Log and continue on error (process may have already exited)
4. Bulk cancel DB tasks via `CancelBySessionKey(sessionKey)`
5. Call `SessionClearer.ClearSession(sessionKey)` to purge dispatcher in-memory queues AND clear session contexts
6. Return summary: `{"cancelled_tasks": N, "cleared": true}`

**Execution order rationale:** Steps 2-3 (stop executions) must happen before step 5 (clear queues). Otherwise a stopped execution's `handleResult` could dequeue and start a pending task from the same session before the queue is cleared. The complete sequence is: collect running execution IDs → stop processes → cancel DB state → clear dispatcher queues + session contexts.

**Note on cancelled vs failed status:** A running task that is being killed may have its status set to "failed" by the worker's monitor goroutine before `CancelBySessionKey` runs. The `CancelBySessionKey` SQL guards with `status IN ('pending', 'running')`, so already-failed tasks are unaffected. The returned count may slightly undercount. This is acceptable.

### 4. Dispatcher `ClearSession` Method

The MCP server cannot access dispatcher's in-memory queue map directly. Add a thread-safe method that also handles session context clearing.

```go
// SessionClearer is the subset of Dispatcher used by the MCP server.
type SessionClearer interface {
    ClearSession(sessionKey string)
}
```

**Thread safety approach:** Dispatcher state (`queues` map) is only accessed in the `Run()` goroutine's select loop. Introduce a buffered `clearCh chan string` on Dispatcher. `ClearSession` sends the sessionKey on this channel; `Run()` receives it and deletes matching queue entries + clears session contexts.

```go
func New(...) *Dispatcher {
    return &Dispatcher{
        // ... existing fields ...
        clearCh: make(chan string, 8), // buffered to avoid blocking MCP handler
    }
}

func (d *Dispatcher) Run(ctx context.Context) {
    d.ctx = ctx
    for {
        select {
        case task, ok := <-d.in:
            if !ok { return }
            d.handleInbound(task)
        case res := <-d.results:
            d.handleResult(res)
        case sessionKey := <-d.clearCh:
            d.clearQueues(sessionKey)
        case <-ctx.Done():
            return
        }
    }
}

func (d *Dispatcher) ClearSession(sessionKey string) {
    select {
    case d.clearCh <- sessionKey:
    default:
        log.Printf("dispatcher: clearCh full, dropping clear for %s", sessionKey)
    }
}

func (d *Dispatcher) clearQueues(sessionKey string) {
    prefix := sessionKey + "|"
    for key := range d.queues {
        if strings.HasPrefix(key, prefix) {
            delete(d.queues, key)
        }
    }
    // Also clear session contexts (previously done in handleInbound's clear branch)
    if err := d.sessionStore.ClearSessionContexts(d.ctx, sessionKey); err != nil {
        log.Printf("dispatcher: clear session contexts for %s: %v", sessionKey, err)
    }
}
```

**Key design decisions:**
- Buffered channel (capacity 8) prevents deadlock when MCP handler sends on the channel
- `select` with `default` in `ClearSession` ensures non-blocking; drops with a log if channel is full (unlikely in practice)
- Session context clearing is done inside `clearQueues` so the MCP server does NOT need a direct `SessionStore` dependency — keeps the interface surface minimal

### 5. MCP Server Dependency Changes

**New interfaces for MCP server:**

```go
type ExecutionStopper interface {
    StopExecution(executionID string) error
}

type SessionClearer interface {
    ClearSession(sessionKey string)
}
```

**Server struct additions:**
```go
type Server struct {
    // ... existing fields ...
    execStopper    ExecutionStopper  // worker.Manager
    sessionClearer SessionClearer    // dispatcher.Dispatcher
}
```

**Wiring in `cmd/server/app.go`:**
- Pass `worker.Manager` as `ExecutionStopper`
- Pass `dispatcher.Dispatcher` as `SessionClearer`

Note: `SessionStore` is NOT added to MCP server — session context clearing is handled by the dispatcher's `ClearSession` method.

### 6. Bee System Prompt Update

Add instructions to bee's system prompt (in `bee_process.go` or prompt template) for handling clear intent:

```
When the user sends a message indicating they want to clear/reset the conversation
(e.g., "clear", "清除", "重置上下文", etc.):

1. First, call list_tasks with the session_key and status "pending,running"
   to check for active tasks.

2. If NO active tasks exist:
   - Call clear_session with the session_key
   - Call send_message to confirm: "已清除会话上下文。"

3. If active tasks exist:
   - Call send_message to inform the user:
     "当前有 N 个任务正在处理中，清除上下文将终止这些任务。是否确认清除？"
   - Wait for the user's next message.

4. If the user confirms (sends "clear" again or similar confirmation):
   - Call clear_session with the session_key (this will cancel all tasks,
     terminate running worker processes, and clear all session contexts)
   - Call send_message to confirm: "已终止所有任务并清除会话上下文。"
```

Note: `send_message` requires `message_id`, which bee already has from the prompt metadata. `clear_session` does not delete platform messages, so the message_id remains valid after clearing.

### 7. Store Layer Changes

**task_store.go — new method:**
```go
func (s *TaskStore) ListBySessionKey(ctx context.Context, sessionKey string, status string) ([]Task, error)
```

SQL (with comma-separated status support):
```sql
SELECT t.* FROM tasks t
JOIN platform_messages pm ON t.message_id = pm.id
WHERE pm.session_key = ?
  AND t.status IN (?, ?)  -- dynamically built from comma-separated status
ORDER BY t.created_at DESC
```

If `status` is empty, omit the `AND t.status IN (...)` clause.

**task_store.go — new method for bulk cancel:**
```go
func (s *TaskStore) CancelBySessionKey(ctx context.Context, sessionKey string) (int64, error)
```

SQL:
```sql
UPDATE tasks SET status = 'cancelled', updated_at = ?
WHERE message_id IN (SELECT id FROM platform_messages WHERE session_key = ?)
  AND status IN ('pending', 'running')
```

Returns the number of rows affected. Note: scheduled (cron) tasks in "pending" status are also cancelled. The task scheduler filters by `status = 'pending'` so cancelled cron tasks will not be picked up again.

## Files Changed

| File | Change |
|------|--------|
| `internal/msgingest/gateway.go` | Remove CommandClear detection |
| `internal/msgingest/gateway_test.go` | Update tests |
| `internal/bee/feeder.go` | Remove clear interception, remove dispatchCh parameter |
| `internal/bee/feeder_test.go` | Update tests |
| `internal/dispatcher/dispatcher.go` | Remove clear branch, add clearCh + ClearSession method |
| `internal/dispatcher/dispatcher_test.go` | Update tests |
| `internal/mcp/tools.go` | Extend list_tasks, add clear_session tool |
| `internal/mcp/tools_test.go` | Add tests for new functionality |
| `internal/mcp/server.go` | Add ExecutionStopper and SessionClearer dependencies |
| `internal/store/task_store.go` | Add ListBySessionKey, CancelBySessionKey |
| `internal/store/task_store_test.go` | Add tests |
| `internal/bee/bee_process.go` | Update bee system prompt |
| `cmd/server/app.go` | Wire new dependencies, remove feeder dispatchCh |

## Edge Cases

1. **clear_session called while bee itself is running**: Bee calls `clear_session` which (via dispatcher) clears its own session context. However, the feeder's `processBeeGroup` will call `UpsertSessionContext` after bee exits, re-creating bee's session. Net effect: worker sessions are cleared; bee's session persists until the next clear cycle. This is acceptable — bee is effectively stateless per-prompt and the session is only used for tool call continuity.

2. **StopExecution fails** (process already exited): `Process.Kill()` returns an error but the task is already done. Log the error, continue with the rest of the cleanup.

3. **Race between task completion and clear_session**: A task may complete between the list and the cancel. `CancelBySessionKey` uses `status IN ('pending', 'running')` so a completed task won't be affected. `StopExecution` on an already-finished execution returns "no active runtime" error — log and continue. The returned cancelled count may slightly undercount.

4. **Dispatcher queue vs DB state**: `ClearSession` removes pending tasks from the in-memory queue and clears session contexts. `CancelBySessionKey` updates DB state. Both are needed for consistency.

5. **Debounce merging**: "clear" no longer bypasses debounce. If "hello" and "clear" arrive within the debounce window, they merge into one message. Bee can parse the clear intent from the combined message.

6. **Scheduled (cron) tasks**: Cron tasks in "pending" status are cancelled by `CancelBySessionKey`. The scheduler filters by `status = 'pending'` so cancelled cron tasks are not re-triggered.
