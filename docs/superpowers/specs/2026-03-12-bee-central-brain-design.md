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
    scheduled_at      INTEGER,  -- milliseconds; countdown: absolute trigger time
    cron_expr         TEXT,     -- scheduled tasks only (5-field cron)
    next_run_at       INTEGER,  -- milliseconds; scheduled tasks only, updated after each run
    reply_session_key TEXT,     -- optional override reply target; required for 'scheduled' type
    execution_id      TEXT REFERENCES worker_executions(id),  -- most recent execution
    created_at        INTEGER NOT NULL,
    updated_at        INTEGER NOT NULL
);

CREATE INDEX idx_tasks_status_type ON tasks(status, type);
CREATE INDEX idx_tasks_message_id  ON tasks(message_id);
CREATE INDEX idx_tasks_worker_id   ON tasks(worker_id);
```

### `platform_messages` Status Updates

**Added values:**
- `feeding` — batch claimed by Feeder, bee process running
- `bee_processed` — bee has processed this message (tasks created or skipped)

**Notes:**
- `bee_processed` is not fully terminal: the row remains updatable so the Dispatcher can write `execution_id` and `session_id` back (needed for `--resume` on subsequent immediate tasks in the same session).
- `routed` — no longer produced by new code; existing rows kept for history.

### Relationships

```
platform_messages (1) ──→ (N) tasks ──→ (1) workers
                                    └──→ (N) worker_executions
```

---

## 3. Bee Process

### Identity & Persona

Bee's name and persona are defined in `config.yaml`. The system writes `CLAUDE.md` to bee's work directory before each process launch:

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

`CLAUDE.md` is written (or overwritten) each time before a bee process starts.

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
- Exit code 0 = success; non-zero = failure (Feeder rolls back message status and cleans up orphaned tasks — see §4).

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

If a message cannot be routed to any worker or is outside bee's mandate, bee creates no task for that message. The message still transitions to `bee_processed` so it is not re-consumed.

---

## 4. Feeder

### Configuration

```yaml
bee:
  feeder:
    interval: 10s      # Poll interval
    batch_size: 10     # Max messages per bee invocation
    timeout: 5m        # Bee process timeout
    queue_warn_threshold: 100  # Log warning when received queue exceeds this
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
Build batch prompt, record batch_message_ids = [ids claimed]
Spawn bee process (exec.CommandContext with timeout)
    ↓
Wait for exit
  ├─ exit 0 → UPDATE messages status → bee_processed
  └─ timeout / error →
       DELETE FROM tasks WHERE message_id IN batch_message_ids AND status = 'pending'
       UPDATE messages status → received (rollback)
       log error
```

### Partial Failure Safety

On bee process failure or timeout, the Feeder deletes any `pending` tasks created for the failed batch (identified by `message_id IN batch_message_ids`) before rolling back message status to `received`. This prevents orphaned tasks from re-running when messages are retried.

### Concurrency Safety

- The `SELECT + UPDATE → feeding` is a single DB transaction, preventing duplicate consumption across restarts or future multi-instance deployments.
- Only one bee process runs at a time; Feeder waits for the current bee to exit before starting the next tick.
- Rollback guard: the Feeder only resets rows whose status is still `feeding`. Rows that advanced to `bee_processed` (e.g., partially processed before a crash) are not touched.

### Queue Health

- After each tick, if `COUNT(status=received) > queue_warn_threshold`, emit a warning log. This surfaces silent queue buildup before it becomes a production issue.

### Startup Recovery

On server start, **before any goroutines are launched**, the Feeder resets any `feeding` messages back to `received` and deletes associated `pending` tasks for those messages. This step must complete before the TaskScheduler's startup recovery runs (see §5) to avoid a delete/reset race on tasks that were promoted to `running` from a `feeding`-batch's `pending` state.

---

## 5. Task Types & TaskScheduler

### Task Types

| Type | Trigger | Key Fields | Example |
|------|---------|-----------|---------|
| `immediate` | On creation | — | "让 bob 查天气" |
| `countdown` | `scheduled_at <= now` | `scheduled_at` | "三小时后总结周报" |
| `scheduled` | `next_run_at <= now` | `cron_expr`, `next_run_at` | "每小时检查 issue" |

Bee provides `scheduled_at` as an absolute millisecond timestamp. The Go MCP handler calculates it from bee's relative time expression (e.g., "3小时后" → `now + 3h`) during tool call processing.

For `scheduled` tasks, the Go layer parses `cron_expr` using `robfig/cron` and computes the initial `next_run_at` on task creation.

### `create_task` Validation Rules

| `type` | `scheduled_at` | `cron_expr` | `reply_session_key` | Validation |
|--------|---------------|-------------|---------------------|-----------|
| `immediate` | ignored | ignored | optional | Always valid |
| `countdown` | **required** | ignored | optional | `scheduled_at` must be `>= server_now_ms + 5000` (+5s minimum lead); error if missing or too soon |
| `scheduled` | ignored | **required** | **required** | MCP error if `cron_expr` or `reply_session_key` missing; task created with `status=cancelled` if cron parse fails |

All time validation uses the MCP server's `time.Now().UnixMilli()` (server clock).

### TaskScheduler

A Go goroutine polling the `tasks` table every 10 seconds:

```
ticker fires (every 10s)
    ↓
BEGIN TRANSACTION
  SELECT tasks WHERE status='pending' AND due (as above)
  UPDATE immediate/countdown tasks: status → running
  UPDATE scheduled tasks: next_run_at → next occurrence after time.Now() via robfig/cron
    (miss policy: skip — compute from now, not from the missed next_run_at; at most one fire per poll tick)
COMMIT
    ↓
For each task from the committed set:
  Send DispatchTask to Dispatcher channel
  (if ctx cancelled before send, task stays running/pending and will be
   recovered on next startup reset — at-least-once semantics)
```

**Atomicity rule:** Status updates happen in the DB transaction *before* tasks are sent to the Dispatcher channel. A crash between commit and channel-send leaves the task in `running` (for immediate/countdown) or with an advanced `next_run_at` (for scheduled). On the next startup, the TaskScheduler startup recovery resets `running → pending`, which causes the task to be re-dispatched on the first poll tick. This provides **at-least-once** delivery: a task may fire more than once in the crash case, but it will never be silently lost. Workers must be written with this in mind (idempotent where possible).

### Startup Recovery

On server start, TaskScheduler runs **after the Feeder startup reset completes**:
1. Resets all `running` tasks to `pending` (these were abandoned mid-execution during a prior crash).
2. The next poll tick will re-dispatch them normally.

### Scheduled Task Lifecycle

- A `scheduled` task runs indefinitely until explicitly cancelled via `cancel_task`.
- `next_run_at` is computed by the **TaskScheduler** during the atomic dispatch transaction (not by the Dispatcher or after execution completes). The scheduler advances `next_run_at` at the moment of dispatch, not at the moment of completion.
- If a `scheduled` task's worker is deleted, all `pending` and `running` tasks for that worker transition to `cancelled`.
- If dispatch fails permanently (e.g., worker not found at dispatch time), the task transitions to `failed` and does not re-trigger.

### Cancellation

`scheduled` tasks (and any `pending` task) can be cancelled via the MCP `cancel_task` tool, setting `status = 'cancelled'`. TaskScheduler skips cancelled tasks.

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
    TaskID          string
    WorkerID        string
    SessionKey      string                  // original message session_key for reply routing
    Instruction     string                  // bee-generated instruction for the worker
    ReplyTo         platform.InboundMessage // original inbound message for Sender
    TaskType        string                  // "immediate" | "countdown" | "scheduled"
    ReplySessionKey string                  // from tasks.reply_session_key; overrides ReplyTo session if non-empty
}
```

`ReplyTo` is populated by the TaskScheduler from the `tasks → platform_messages` join. If `tasks.reply_session_key` is non-empty, the Dispatcher uses it as the delivery session key instead of the one derived from `ReplyTo`.

### Serialization

Per-`(SessionKey, WorkerID)` serial execution is preserved. New tasks for the same session+worker queue behind the current execution.

### Session Context

- `immediate` tasks: resolve prior `session_id` by querying `platform_messages WHERE id = task.message_id`. Use `--resume <session_id>` if one exists; otherwise start a fresh session.
- `countdown` and `scheduled` tasks: no `--resume` — each trigger is a fresh session to avoid stale context.

### Result Writeback

On completion, Dispatcher:
1. Writes `execution_id` back to `tasks.execution_id`.
2. Updates `tasks.status` to `completed` or `failed`.
3. For `immediate` tasks: writes `execution_id` and `session_id` back to `platform_messages` using `UPDATE ... WHERE status = 'bee_processed'` — this is a no-op if the row was rolled back by the Feeder, preventing write/rollback races.

---

## 7. New MCP Tools

Added to `internal/mcp/tools.go`:

### `create_task`

```json
{
  "name": "create_task",
  "description": "Create a task assigning a worker to handle a user instruction",
  "inputSchema": {
    "message_id":    { "type": "string",  "required": true },
    "worker_id":     { "type": "string",  "required": true },
    "instruction":   { "type": "string",  "required": true },
    "type":          { "type": "string",  "enum": ["immediate","countdown","scheduled"], "required": true },
    "scheduled_at":  { "type": "integer", "description": "Unix ms; required for countdown, must be >= server_now + 5s" },
    "cron_expr":     { "type": "string",  "description": "5-field cron; required for scheduled" },
    "reply_session_key": { "type": "string", "description": "Optional override session_key for result delivery (useful for scheduled tasks targeting a specific notification channel)" }
  }
}
```

`reply_session_key` is optional. If omitted, the Dispatcher derives the reply target from `platform_messages.session_key` via the `message_id` join. For recurring `scheduled` tasks where the originating session may be stale, Bee should supply an explicit `reply_session_key`.

### `list_tasks`

```json
{
  "name": "list_tasks",
  "description": "List tasks for a given message, optionally filtered by status",
  "inputSchema": {
    "message_id": { "type": "string", "required": true },
    "status":     { "type": "string" }
  }
}
```

`message_id` is **required** to prevent bee from seeing tasks belonging to other sessions.

### `cancel_task`

```json
{
  "name": "cancel_task",
  "description": "Cancel a pending or scheduled task",
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
    queue_warn_threshold: 100
```

---

## 9. Error Handling

| Scenario | Behavior |
|----------|----------|
| Bee process times out | Delete orphaned pending tasks for batch; messages rolled back to `received`; error logged |
| Bee process exits non-zero | Same as timeout |
| `feeding` messages at startup | Reset to `received`; associated pending tasks deleted |
| `running` tasks at startup | Reset to `pending`; re-dispatched on next TaskScheduler tick |
| `create_task` with unknown `worker_id` | MCP returns error; bee may retry with `list_workers` |
| `create_task` with `scheduled_at` in the past | MCP returns validation error |
| `create_task` with invalid `cron_expr` | Task created with `status=cancelled`; error logged |
| Worker deleted while tasks pending | All pending/running tasks for that worker → `cancelled` |
| Worker execution fails | `tasks.status → failed`; error message sent to platform |
| Sender delivery fails (platform session expired, user left chat) | Task status remains `completed`; send error logged only — delivery failure does not mark the task as failed |
| DB unavailable during task dispatch | TaskScheduler logs error; retries on next tick |
| Queue depth exceeds `queue_warn_threshold` | Warning logged; no blocking action |
| Startup ordering | Feeder reset runs first (sync), TaskScheduler reset runs second (sync), then goroutines launched |

---

## 10. Testing Strategy

- **Unit:** Feeder batch-pull logic; cron `next_run_at` calculation; bee prompt generation; `create_task` validation rules (all type/field combinations)
- **Integration:** MCP `create_task` end-to-end (bee creates task → TaskScheduler picks it up → Dispatcher executes → result written back to task + platform)
- **Scenario tests:**
  - Multi-task message: single message spawns tasks for multiple workers
  - Countdown task fires after configured delay
  - Scheduled task recurs and `next_run_at` advances correctly
  - Out-of-scope message produces no task, message still marked `bee_processed`
  - Bee timeout: orphaned tasks cleaned up, messages retried on next tick
  - Startup recovery: abandoned `feeding` messages and `running` tasks recovered correctly
  - Worker deletion cancels associated tasks
