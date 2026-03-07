# Worker-Task Merge Design

## Problem

The current 1:N relationship between Worker and Task is incorrect. Each Worker is an abstraction of a specific business function and should do exactly one thing. The Task entity adds unnecessary complexity.

## Decision

Merge Task into Worker. Delete the Task entity entirely. A Worker is created with its purpose (prompt) and trigger mechanism (cron or message) fully specified.

## New Worker Model

```sql
CREATE TABLE workers (
    id                TEXT PRIMARY KEY,
    name              TEXT NOT NULL,
    description       TEXT,
    prompt            TEXT NOT NULL,           -- was task.plan
    email             TEXT NOT NULL UNIQUE,
    runtime_type      TEXT NOT NULL DEFAULT 'claude_code',
    work_dir          TEXT NOT NULL,
    trigger_type      TEXT NOT NULL,           -- "cron" | "message"
    cron_expression   TEXT,                    -- required when trigger_type = "cron"
    recipients        TEXT,                    -- JSON array of notification emails
    requires_approval INTEGER DEFAULT 0,
    status            TEXT DEFAULT 'idle',
    created_at        DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at        DATETIME DEFAULT CURRENT_TIMESTAMP
);
```

## Execution Model (renamed)

```sql
CREATE TABLE worker_executions (
    id              TEXT PRIMARY KEY,
    worker_id       TEXT NOT NULL REFERENCES workers(id) ON DELETE CASCADE,
    session_id      TEXT NOT NULL,
    trigger_input   TEXT,                     -- message content, email body, or "scheduled"
    status          TEXT DEFAULT 'pending',
    result          TEXT,
    ai_process_pid  INTEGER,
    started_at      DATETIME DEFAULT CURRENT_TIMESTAMP,
    completed_at    DATETIME
);
```

`trigger_input` is new: records what triggered this execution (email content, API message, or "scheduled" for cron).

## API Routes

| Method | Path | Description |
|--------|------|-------------|
| POST | /api/workers | Create worker (with prompt, trigger_type) |
| GET | /api/workers | List all workers |
| GET | /api/workers/:id | Get worker details |
| PUT | /api/workers/:id | Update worker |
| DELETE | /api/workers/:id | Delete worker (cascade executions) |
| POST | /api/workers/:id/message | Send message to trigger execution (message type only) |
| GET | /api/workers/:id/executions | Get worker's execution history |
| GET | /api/executions | List all executions |
| GET | /api/executions/:id | Get execution details |
| POST | /api/executions/:id/approve | Approve execution |
| POST | /api/executions/:id/reject | Reject execution |
| GET | /api/executions/:id/logs | WebSocket log stream |
| GET | /api/executions/:id/emails | Get execution emails |

Removed routes: all `/api/workers/:id/tasks/*` and `/api/tasks/*` routes.

## Trigger Types

Only two trigger types remain:

- **cron**: Worker executes on schedule. Scheduler loads workers with `trigger_type="cron"` and registers cron jobs.
- **message**: Worker executes when receiving a message via API (`POST /api/workers/:id/message`) or email (SMTP).

`manual` trigger type is removed. The `POST /api/workers/:id/message` endpoint returns 400 for cron-type workers.

## Business Logic Changes

### Manager
- `ExecuteTask(taskID)` becomes `ExecuteWorker(workerID, triggerInput)`
- Reads prompt directly from Worker, no Task lookup needed

### Scheduler
- Loads Workers (not Tasks) with `trigger_type="cron"`
- Calls `ExecuteWorker(workerID, "scheduled")`

### Mail Handler
- Finds Worker by recipient email
- Calls `ExecuteWorker(workerID, emailContent)`

### Runtime
- Input changes from `Task.Plan` to `Worker.Prompt`

## Files Affected

### Backend - Delete
- `internal/model/task.go`
- `internal/store/task_store.go`
- `internal/api/task_handler.go`

### Backend - Modify
- `internal/model/worker.go` - add prompt, trigger_type, cron_expression, recipients, requires_approval
- `internal/model/execution.go` - FK to worker_id, add trigger_input
- `internal/store/db.go` - rebuild schema
- `internal/store/worker_store.go` - update queries
- `internal/store/execution_store.go` - update FK references
- `internal/api/router.go` - update routes
- `internal/api/worker_handler.go` - merge task creation logic, add message endpoint
- `internal/api/execution_handler.go` - update queries
- `internal/worker/manager.go` - ExecuteTask → ExecuteWorker
- `internal/worker/runtime.go` - use Worker.Prompt
- `internal/scheduler/cron.go` - load Workers instead of Tasks
- `internal/mail/*.go` - update trigger logic

### Frontend - Delete
- `src/hooks/use-tasks.ts`
- Task-related components

### Frontend - Modify
- `src/lib/types.ts` - update type definitions
- `src/hooks/use-workers.ts` - update request structure
- Worker create/edit forms - merge Task fields
- Worker detail page - show executions directly

## Migration Strategy

Development stage, no production data. Drop and recreate all tables.
