# Design: Remove Worker Scheduling

**Date:** 2026-03-12
**Status:** Approved

## Overview

Remove all scheduled/cron task functionality from the Worker entity. Workers become purely on-demand, triggered only by manual messages or platform inbound messages. This eliminates the `CronExpression`, `ScheduleDescription`, and `ScheduleEnabled` fields from the Worker model, along with all related scheduler infrastructure.

## Motivation

Scheduling is not a core requirement at this stage. Removing it simplifies the Worker model, eliminates a dependency on an AI-powered cron resolver, and reduces overall system complexity.

## Scope of Changes

### 1. Database Migration

Add migration version 11 to `internal/store/db.go`.

SQLite 3.35+ supports `ALTER TABLE ... DROP COLUMN`. Since the project uses `mattn/go-sqlite3`, which links the system SQLite, we will use a safe table-rebuild approach to maximize compatibility:

```sql
-- Migration 11: remove scheduling columns from workers
CREATE TABLE workers_new (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL,
    work_dir TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'idle',
    description TEXT NOT NULL DEFAULT '',
    prompt TEXT NOT NULL DEFAULT '',
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL
);
INSERT INTO workers_new SELECT id, name, work_dir, status, description, prompt, created_at, updated_at FROM workers;
DROP TABLE workers;
ALTER TABLE workers_new RENAME TO workers;
```

The partial index `idx_workers_schedule` (version 6) becomes invalid after the column drop, so it must also be recreated/dropped. Since SQLite drops indexes with the table, the rebuild handles this automatically.

### 2. Model Layer

**`internal/model/worker.go`**

Remove fields:
- `CronExpression string`
- `ScheduleDescription string`
- `ScheduleEnabled bool`

### 3. Store Layer

**`internal/store/worker_store.go`**

- `workerColumns`: remove `cron_expression`, `schedule_description`, `schedule_enabled`
- `scanWorker`: remove scanning of those three fields
- `Create` SQL: remove three columns from INSERT
- `Update` SQL: remove three columns from SET clause
- Delete `ListScheduledWorkers` method entirely

### 4. AI Interface Layer

**`internal/ai/interfaces.go`**

- Delete the `CronResolver` interface

**`internal/ai/claude_code_client.go`**

- Remove the `CronFromDescription` method implementation

### 5. Worker Manager

**`internal/worker/manager.go`**

- Remove `cronResolver ai.CronResolver` field from `Manager` struct
- `NewManager`: remove `cronResolver` parameter
- Delete `ResolveCron(ctx, description)` method
- `CreateWorker`: remove `scheduleDescription string` and `scheduleEnabled bool` parameters; remove cron resolution logic inside

### 6. Scheduler Package

**`internal/scheduler/cron.go`** — delete the entire file/package

### 7. API Layer

**`internal/api/router.go`**

- Remove `WorkerScheduler` interface
- Remove `scheduler WorkerScheduler` field from `Server`
- `NewServer`: remove `sched WorkerScheduler` parameter

**`internal/api/worker_handler.go`**

- `createWorkerRequest`: remove `ScheduleDescription` and `ScheduleEnabled` fields
- `createWorker`: remove schedule validation block; update `manager.CreateWorker` call
- `updateWorker`: remove `ScheduleDescription`/`ScheduleEnabled` fields from request struct; remove `ResolveCron` call and related fields update
- Delete `applySchedule` method
- `deleteWorker`: remove `s.scheduler.RemoveWorker(id)` call

### 8. Entry Point

**`cmd/server/main.go`**

- Remove `scheduler` import
- Remove `sched := scheduler.New(...)` and `sched.Start()`
- Remove `sched` argument from `api.NewServer(...)`
- Remove `sched.Stop()` from shutdown handler
- Remove the comment "Initialize Claude Code client for routing and cron" → update to just "routing"
- `aiClient` is still needed for message routing (`WorkerRouter`), so it stays

### 9. Frontend

**`web/src/lib/types.ts`**

Remove from `Worker` interface:
- `cron_expression: string`
- `schedule_description?: string`
- `schedule_enabled: boolean`

**`web/src/lib/api.ts`**

Remove from `workers.create` data type:
- `schedule_description?: string`
- `schedule_enabled?: boolean`

**`web/src/hooks/use-workers.ts`**

Remove from `useCreateWorker` mutation data type:
- `schedule_description?: string`
- `schedule_enabled?: boolean`

**`web/src/pages/workers.tsx`**

- Remove state: `scheduleEnabled`, `scheduleDescription`
- Remove from `handleCreate`: `schedule_enabled`, `schedule_description`
- Remove schedule UI: checkbox and conditional `scheduleDescription` input block
- Remove schedule reset in `setOpen(false)` cleanup
- Worker card: replace `w.schedule_enabled ? ... : t("common.onDemand")` with just `t("common.onDemand")`

**`web/src/pages/worker-detail.tsx`**

- Remove the schedule info line from the Info tab

## Files to Modify

| File | Action |
|------|--------|
| `internal/store/db.go` | Add migration v11 |
| `internal/model/worker.go` | Remove 3 fields |
| `internal/store/worker_store.go` | Remove 3 columns from queries, delete `ListScheduledWorkers` |
| `internal/ai/interfaces.go` | Delete `CronResolver` interface |
| `internal/ai/claude_code_client.go` | Delete `CronFromDescription` method |
| `internal/worker/manager.go` | Remove `cronResolver`, `ResolveCron`, simplify `CreateWorker` |
| `internal/scheduler/cron.go` | Delete file |
| `internal/api/router.go` | Remove `WorkerScheduler`, `scheduler` field and param |
| `internal/api/worker_handler.go` | Remove schedule fields, `applySchedule`, `RemoveWorker` call |
| `cmd/server/main.go` | Remove scheduler init/stop, update `NewServer` and `NewManager` calls |
| `web/src/lib/types.ts` | Remove 3 fields from `Worker` interface |
| `web/src/lib/api.ts` | Remove schedule fields from create type |
| `web/src/hooks/use-workers.ts` | Remove schedule fields from mutation type |
| `web/src/pages/workers.tsx` | Remove schedule UI and state |
| `web/src/pages/worker-detail.tsx` | Remove schedule display |

## Testing

- Existing Go unit tests in `internal/store/worker_store_test.go` must still pass after removing scheduling columns
- Verify the app compiles with `go build ./...`
- Manually verify worker create/list/update flows work without schedule fields
