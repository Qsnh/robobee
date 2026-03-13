# Bee Central Brain — Design Spec

**Date:** 2026-03-12
**Status:** Approved

---

## Overview

Replace the current single-worker AI router (`msgrouter`) with a central brain called **bee**. Bee is a short-lifecycle Claude process that processes batches of incoming messages, creates structured tasks via MCP tools, and delegates execution to workers. A Go-native TaskScheduler handles immediate, countdown, and recurring task dispatch.

---

## 1. Architecture

### Pipeline (Before)

```
Platform Receivers
      ↓
   Ingester (debounce + deduplicate → platform_messages DB)
      ↓
   Router (one-shot Claude: picks one worker per message)
      ↓
   Dispatcher (serialize per session/worker, execute worker)
      ↓
   Sender (reply to platform)
```

### Pipeline (After)

```
Platform Receivers
      ↓
   Ingester (debounce + deduplicate → platform_messages DB)
      ↓
   [platform_messages DB — status=received]
      ↓
   Feeder (ticker, pulls up to N messages, spawns bee)
      ↓
   Bee Process (short-lifecycle Claude + MCP tools)
      ↓  create_task via MCP
   [tasks DB]
      ↓
   TaskScheduler (Go goroutine, polls for due tasks)
      ↓
   Dispatcher (executes worker per task)
      ↓
   Sender (reply to original session_key)
```

### Component Changes

| Action | Component |
|--------|-----------|
| **Remove** | `internal/msgrouter/` |
| **Add** | `internal/bee/` (Feeder + BeeProcess) |
| **Add** | `internal/taskscheduler/` |
| **Extend** | `internal/mcp/` (3 new task tools) |
| **Modify** | `internal/dispatcher/` (input type change) |
| **Modify** | `internal/store/` (new tasks table) |
| **Modify** | `internal/config/` (new bee + feeder config) |
| **Modify** | `cmd/server/main.go` (wire new components) |

---

## 2. Data Model

### New `tasks` Table

```sql
CREATE TABLE tasks (
    id           TEXT PRIMARY KEY,
    message_id   TEXT NOT NULL REFERENCES platform_messages(id),
    worker_id    TEXT NOT NULL REFERENCES workers(id),
    instruction  TEXT NOT NULL,
    type         TEXT NOT NULL CHECK(type IN ('immediate','countdown','scheduled')),
    status       TEXT NOT NULL DEFAULT 'pending'
                      CHECK(status IN ('pending','running','completed','failed','cancelled')),
    scheduled_at INTEGER,      -- milliseconds; countdown: trigger time
    cron_expr    TEXT,         -- scheduled tasks only (5-field cron)
    next_run_at  INTEGER,      -- milliseconds; scheduled tasks only
    execution_id TEXT REFERENCES worker_executions(id),
    created_at   INTEGER NOT NULL,
    updated_at   INTEGER NOT NULL
);

CREATE INDEX idx_tasks_status_type ON tasks(status, type);
CREATE INDEX idx_tasks_message_id  ON tasks(message_id);
CREATE INDEX idx_tasks_worker_id   ON tasks(worker_id);
```

### `platform_messages` Status Updates

**Added values:**
- `feeding` — batch claimed by Feeder, bee process running
- `bee_processed` — bee created all tasks successfully

**Removed from active use:**
- `routed` — no longer produced (existing rows kept for history)

### Relationships

```
platform_messages (1) ──→ (N) tasks ──→ (1) workers
                                    └──→ (N) worker_executions
```

---

## 3. Bee Process

### Identity & Persona

Bee's name and persona are defined in `config.yaml`. On startup, the system writes `CLAUDE.md` to bee's work directory:

```yaml
bee:
  name: "bee"
  work_dir: ~/.robobee/bee
  persona: |
    你是 bee，一个专注于任务调度的智能助手。
    你的职责是分析用户消息，将其拆解为具体任务并分配给合适的 worker。
    你只做两件事：管理 worker 和创建任务。
    如果消息内容无法路由到任何 worker，或请求超出上述职责，拒绝提供服务。
```

`CLAUDE.md` is written (or overwritten) each time a bee process is about to start.

### Process Invocation

```bash
claude \
  --dangerously-skip-permissions \
  --output-format stream-json \
  --mcp-server robobee=http://localhost:{port}/mcp/sse \
  --mcp-api-key <api_key> \
  -p "<BATCH_PROMPT>"
```

- No `--resume` flag — bee is stateless across batches.
- Launched via `exec.CommandContext` with a configurable timeout (default 5m).
- Exit code 0 = success; non-zero = failure (Feeder rolls back message status).

### Batch Prompt Format

```
以下是 {N} 条待处理用户消息，请为每条消息创建相应的任务。

--- 消息 1 ---
来源: {platform} | 会话: {session_key} | 消息ID: {message_id}
内容: {content}

--- 消息 2 ---
来源: {platform} | 会话: {session_key} | 消息ID: {message_id}
内容: {content}

请使用 create_task 工具为每条消息中的每个任务指派创建任务记录。
```

### Output Contract

Bee **only calls MCP tools**. No text output is parsed. The MCP server handles tool calls directly (writes to DB). Bee should not produce any user-facing text responses.

### Out-of-Scope Handling

If a message cannot be routed to any worker or is outside bee's mandate, bee creates no task for that message. The message status transitions to `bee_processed` regardless (so it is not re-consumed), but no tasks are created.

---

## 4. Feeder

### Configuration

```yaml
bee:
  feeder:
    interval: 10s      # Poll interval
    batch_size: 10     # Max messages per bee invocation
    timeout: 5m        # Bee process timeout
```

### Workflow

```
ticker fires
    ↓
BEGIN TRANSACTION
  SELECT status=received ORDER BY received_at ASC LIMIT batch_size
  UPDATE status → feeding
COMMIT
    ↓
No messages? → skip
    ↓
Write bee's CLAUDE.md to work_dir
Build batch prompt
Spawn bee process (exec.CommandContext with timeout)
    ↓
Wait for exit
  ├─ exit 0 → UPDATE messages status → bee_processed
  └─ timeout / error → UPDATE messages status → received (rollback), log error
```

### Concurrency Safety

- The `SELECT + UPDATE → feeding` is a single DB transaction, preventing duplicate consumption across restarts or future multi-instance deployments.
- Only one bee process runs at a time; Feeder waits for the current bee to exit before starting the next tick.

---

## 5. Task Types & TaskScheduler

### Task Types

| Type | Trigger | Key Fields | Example |
|------|---------|-----------|---------|
| `immediate` | On creation | — | "让 bob 查天气" |
| `countdown` | `scheduled_at <= now` | `scheduled_at` | "三小时后总结周报" |
| `scheduled` | `next_run_at <= now` | `cron_expr`, `next_run_at` | "每小时检查 issue" |

Bee provides `scheduled_at` as an absolute millisecond timestamp when calling `create_task`. The Go layer calculates it from the relative expression (e.g., "3小时后" → `now + 3h`) during MCP tool handling.

For `scheduled` tasks, the Go layer parses `cron_expr` using `robfig/cron` and computes the initial `next_run_at` on task creation.

### TaskScheduler

A Go goroutine polling the `tasks` table every 10 seconds:

```
ticker fires (every 10s)
    ↓
Query pending tasks:
  WHERE status = 'pending'
    AND (
      type = 'immediate'
      OR (type = 'countdown' AND scheduled_at <= now_ms)
      OR (type = 'scheduled' AND next_run_at <= now_ms)
    )
    ↓
For each task:
  ├─ Send DispatchTask to Dispatcher channel
  ├─ UPDATE status → running  (immediate / countdown)
  └─ type=scheduled:
       UPDATE next_run_at = next occurrence after now
       status remains pending (re-triggered each cycle)
```

### Cancellation

`scheduled` tasks can be cancelled via the MCP `cancel_task` tool, setting `status = 'cancelled'`. TaskScheduler skips cancelled tasks.

---

## 6. Dispatcher Changes

### New Input Type

```go
// Before
type RoutedMessage struct {
    WorkerID   string
    SessionKey string
    Content    string
    // ...
}

// After
type DispatchTask struct {
    TaskID      string
    WorkerID    string
    SessionKey  string   // original message session_key for reply routing
    Instruction string   // bee-generated instruction for the worker
}
```

### Serialization

Per-`(SessionKey, WorkerID)` serial execution is preserved. New tasks for the same session+worker queue behind the current execution.

### Session Context

- `immediate` tasks: use `--resume` within the same session (preserves conversation history).
- `countdown` and `scheduled` tasks: no `--resume` — each trigger is a fresh session to avoid stale context.

### Result Writeback

On completion, Dispatcher writes `execution_id` back to `tasks.execution_id` and updates `tasks.status` to `completed` or `failed`.

---

## 7. New MCP Tools

Added to `internal/mcp/tools.go`:

### `create_task`

```json
{
  "name": "create_task",
  "description": "Create a task assigning a worker to handle a user instruction",
  "inputSchema": {
    "message_id":    { "type": "string", "required": true },
    "worker_id":     { "type": "string", "required": true },
    "instruction":   { "type": "string", "required": true },
    "type":          { "type": "string", "enum": ["immediate","countdown","scheduled"], "required": true },
    "scheduled_at":  { "type": "integer", "description": "Unix ms; required for countdown" },
    "cron_expr":     { "type": "string",  "description": "5-field cron; required for scheduled" }
  }
}
```

### `list_tasks`

```json
{
  "name": "list_tasks",
  "description": "List tasks, optionally filtered by message_id or status",
  "inputSchema": {
    "message_id": { "type": "string" },
    "status":     { "type": "string" }
  }
}
```

### `cancel_task`

```json
{
  "name": "cancel_task",
  "description": "Cancel a scheduled task",
  "inputSchema": {
    "task_id": { "type": "string", "required": true }
  }
}
```

---

## 8. Configuration Reference

Full new `config.yaml` additions:

```yaml
bee:
  name: "bee"
  work_dir: ~/.robobee/bee
  persona: |
    你是 bee，一个专注于任务调度的智能助手。
    ...
  feeder:
    interval: 10s
    batch_size: 10
    timeout: 5m
```

---

## 9. Error Handling

| Scenario | Behavior |
|----------|----------|
| Bee process times out | Messages rolled back to `received`; error logged |
| Bee calls `create_task` with unknown `worker_id` | MCP returns error; bee may retry with `list_workers` |
| Worker execution fails | `tasks.status → failed`; result sent to platform with error message |
| `scheduled` task cron parse error | Task created with `status=cancelled`; error logged |
| DB unavailable during task dispatch | TaskScheduler logs error; retries on next tick |

---

## 10. Testing Strategy

- **Unit:** Feeder batch-pull logic, cron `next_run_at` calculation, bee prompt generation
- **Integration:** MCP `create_task` tool end-to-end (bee creates task → TaskScheduler picks it up → Dispatcher executes → result written back)
- **Scenario tests:** Multi-task message (multiple `create_task` calls), countdown task fires after delay, scheduled task recurs correctly, out-of-scope message produces no task
