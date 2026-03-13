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
- "clear" messages flow through as ordinary messages

**bee/feeder.go**
- Remove `detectClear()` helper
- Remove `clearMsgs` / `regularMsgs` separation in `tick()`
- Remove `clearCh` channel field and its initialization
- All messages (including "clear") go to bee via `processBeeGroup()`

**dispatcher/dispatcher.go**
- Remove `TaskType == "clear"` branch in `handleInbound()`

### 2. Extend `list_tasks` MCP Tool

Add optional `session_key` parameter, mutually exclusive with `message_id`.

**MCP tool schema change:**
```json
{
  "name": "list_tasks",
  "inputSchema": {
    "properties": {
      "message_id": { "type": "string", "description": "Filter by message ID" },
      "session_key": { "type": "string", "description": "Filter by session key" },
      "status": { "type": "string", "description": "Filter by status" }
    },
    "oneOf": [
      { "required": ["message_id"] },
      { "required": ["session_key"] }
    ]
  }
}
```

**Store layer:**
- Add `ListBySessionKey(ctx, sessionKey, status) ([]Task, error)` to `TaskStore`
- Query joins `tasks` with `platform_messages` on `tasks.message_id = platform_messages.id` to filter by `platform_messages.session_key`

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

**Implementation (`mcp/tools.go`):**

1. Query all pending/running tasks for the session (reuse `ListBySessionKey`)
2. For each running task with a non-empty `execution_id`:
   - Call `ExecutionStopper.StopExecution(executionID)` to kill the worker subprocess
   - Update task status to "cancelled"
3. For each pending task:
   - Update task status to "cancelled"
4. Call `SessionClearer.ClearSession(sessionKey)` to purge dispatcher in-memory queues
5. Call `SessionStore.ClearSessionContexts(sessionKey)` to delete all session context rows
6. Return summary: `{"cancelled_tasks": N, "cleared": true}`

### 4. Dispatcher `ClearSession` Method

The MCP server cannot access dispatcher's in-memory queue map directly. Add a thread-safe method:

```go
// SessionClearer is the subset of Dispatcher used by the MCP server.
type SessionClearer interface {
    ClearSession(sessionKey string)
}

// ClearSession removes all queued (not yet executing) tasks for the given session.
func (d *Dispatcher) ClearSession(sessionKey string) {
    // Send a signal to the dispatcher's run loop to clear the session queues.
    // Use a channel to ensure thread safety (dispatcher state is owned by Run goroutine).
}
```

**Thread safety approach:** Dispatcher state (`queues` map) is only accessed in the `Run()` goroutine's select loop. To maintain this invariant, introduce a `clearCh chan string` on Dispatcher. `ClearSession` sends the sessionKey on this channel; `Run()` receives it and deletes matching queue entries. This avoids needing a mutex on the queues map.

```go
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
    d.clearCh <- sessionKey
}

func (d *Dispatcher) clearQueues(sessionKey string) {
    prefix := sessionKey + "|"
    for key := range d.queues {
        if strings.HasPrefix(key, prefix) {
            delete(d.queues, key)
        }
    }
}
```

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

### 6. Bee System Prompt Update

Add instructions to bee's system prompt (in `bee_process.go` or prompt template) for handling clear intent:

```
When the user sends a message indicating they want to clear/reset the conversation
(e.g., "clear", "清除", "重置上下文", etc.):

1. First, call list_tasks with the session_key and status "pending" or "running"
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

### 7. Store Layer Changes

**task_store.go — new method:**
```go
func (s *TaskStore) ListBySessionKey(ctx context.Context, sessionKey string, status string) ([]Task, error)
```

SQL:
```sql
SELECT t.* FROM tasks t
JOIN platform_messages pm ON t.message_id = pm.id
WHERE pm.session_key = ?
  AND (? = '' OR t.status = ?)
ORDER BY t.created_at DESC
```

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

Returns the number of rows affected.

## Files Changed

| File | Change |
|------|--------|
| `internal/msgingest/gateway.go` | Remove CommandClear detection |
| `internal/msgingest/gateway_test.go` | Update tests |
| `internal/bee/feeder.go` | Remove clear interception, remove clearCh |
| `internal/bee/feeder_test.go` | Update tests |
| `internal/dispatcher/dispatcher.go` | Remove clear branch, add ClearSession method + clearCh |
| `internal/dispatcher/dispatcher_test.go` | Update tests |
| `internal/mcp/tools.go` | Extend list_tasks, add clear_session tool |
| `internal/mcp/tools_test.go` | Add tests for new functionality |
| `internal/mcp/server.go` | Add ExecutionStopper and SessionClearer dependencies |
| `internal/store/task_store.go` | Add ListBySessionKey, CancelBySessionKey |
| `internal/store/task_store_test.go` | Add tests |
| `internal/bee/bee_process.go` | Update bee system prompt |
| `cmd/server/app.go` | Wire new dependencies to MCP server |

## Edge Cases

1. **clear_session called while bee itself is running**: bee calls clear_session which clears its own session context. This is expected — next message starts a fresh bee session.

2. **StopExecution fails** (process already exited): `Process.Kill()` returns an error but the task is already done. Log the error, continue with the rest of the cleanup. Still mark task as cancelled in DB.

3. **Race between task completion and clear_session**: A task may complete between the list and the cancel. `CancelBySessionKey` uses `status IN ('pending', 'running')` so a completed task won't be affected. `StopExecution` on an already-finished execution returns "no active runtime" error — log and continue.

4. **Dispatcher queue vs DB state**: `ClearSession` removes pending tasks from the in-memory queue. `CancelBySessionKey` updates DB state. Both are needed for consistency.

5. **list_tasks status filter for clear check**: Bee should call `list_tasks` twice (once for "pending", once for "running") or the tool should support comma-separated status values. Recommendation: support `status` as comma-separated (e.g., `"pending,running"`).
