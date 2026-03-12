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

The project's `applyMigrations` framework executes each migration's `.sql` string via a single `tx.Exec(m.sql)`. SQLite's `database/sql` driver does not support multiple statements in a single `Exec` call, so the table-rebuild must use multiple migrations or a loop. **The simplest fix is to add the migration as three separate `migration` entries (versions 11, 12, 13)** with one SQL statement each:

```go
{version: 11, name: "20260312000002_workers_create_new",
 sql: `CREATE TABLE workers_new (
     id TEXT PRIMARY KEY,
     name TEXT NOT NULL,
     work_dir TEXT NOT NULL,
     status TEXT NOT NULL DEFAULT 'idle',
     description TEXT NOT NULL DEFAULT '',
     prompt TEXT NOT NULL DEFAULT '',
     created_at INTEGER NOT NULL,
     updated_at INTEGER NOT NULL
 )`},
{version: 12, name: "20260312000003_workers_copy_data",
 sql: `INSERT INTO workers_new (id, name, work_dir, status, description, prompt, created_at, updated_at)
       SELECT id, name, work_dir, status, description, prompt, created_at, updated_at FROM workers`},
{version: 13, name: "20260312000004_workers_drop_old",
 sql: `DROP TABLE workers`},
{version: 14, name: "20260312000005_workers_rename",
 sql: `ALTER TABLE workers_new RENAME TO workers`},
```

Note: The INSERT uses named columns (not positional), so column ordering differences between the old and new schema are not an issue.

Dropping the old `workers` table automatically drops the `idx_workers_schedule` partial index that was created in migration 6. No explicit DROP INDEX is needed.

### 2. Model Layer

**`internal/model/worker.go`**

Remove fields:
- `CronExpression string`
- `ScheduleDescription string`
- `ScheduleEnabled bool`

### 3. Store Layer

**`internal/store/worker_store.go`**

- `workerColumns` constant: remove `cron_expression`, `schedule_description`, `schedule_enabled`
- `scanWorker`: remove scanning of those three fields
- `Create` INSERT SQL: remove `cron_expression`, `schedule_description`, `schedule_enabled` from column list and `?` params
- `Update` SET SQL: remove `cron_expression=?`, `schedule_description=?`, `schedule_enabled=?` and their args
- Delete `ListScheduledWorkers` method entirely

### 4. AI Interface Layer

**`internal/ai/interfaces.go`**

- Delete the `CronResolver` interface

**`internal/ai/claude_code_client.go`**

- Remove the `CronFromDescription` method implementation
- Remove any imports only used by that method

### 5. Worker Manager

**`internal/worker/manager.go`**

- Remove `cronResolver ai.CronResolver` field from `Manager` struct
- `NewManager`: remove `cronResolver ai.CronResolver` parameter
- Delete `ResolveCron(ctx, description)` method
- `CreateWorker`: remove `scheduleDescription string` and `scheduleEnabled bool` parameters; remove the `if scheduleEnabled && scheduleDescription != ""` block that calls `m.cronResolver.CronFromDescription`; remove `CronExpression` and `ScheduleDescription` from the `model.Worker{}` passed to `workerStore.Create`
- `ExecuteWorker`: remove the dead `triggerInput != "scheduled"` guard from the prompt-building condition (line 133). Simplify to just `if triggerInput != ""`

### 6. Scheduler Package

**`internal/scheduler/cron.go`** — delete the entire file. This removes the `scheduler` package entirely.

### 7. API Layer

**`internal/api/router.go`**

- Delete the `WorkerScheduler` interface definition
- Remove `scheduler WorkerScheduler` field from `Server` struct
- `NewServer`: remove `sched WorkerScheduler` parameter; remove `scheduler: sched` assignment

**`internal/api/worker_handler.go`**

- `createWorkerRequest` struct: remove `ScheduleDescription` and `ScheduleEnabled` fields
- `createWorker`: remove the `if req.ScheduleEnabled { ... }` validation block; update `manager.CreateWorker` call to remove the two schedule arguments; remove `s.applySchedule(w)` call
- `updateWorker` request struct: remove `ScheduleDescription` and `ScheduleEnabled` fields; remove the `if req.ScheduleDescription != ""` block that calls `ResolveCron`; remove the `if req.ScheduleEnabled != nil` block; remove `s.applySchedule(updated)` call
- `deleteWorker`: remove the `s.scheduler.RemoveWorker(id)` call (direct call on line 130, distinct from the one inside `applySchedule`)
- Delete the `applySchedule` method entirely

**`internal/api/locales/en.json` and `internal/api/locales/zh.json`**

Remove orphaned message keys:
- `ScheduleDescriptionRequired`
- `PromptRequired` (if only used for schedule validation — verify usage)
- `FailedToGenerateCronExpression`

### 8. Entry Point

**`cmd/server/main.go`**

- Remove `"github.com/robobee/core/internal/scheduler"` import
- Remove `sched := scheduler.New(workerStore, mgr)` and `sched.Start()` lines
- Update `api.NewServer(...)` call: remove `sched` argument
- Remove `sched.Stop()` from the shutdown handler
- Update comment "Initialize Claude Code client for routing and cron" → "Initialize Claude Code client for routing"
- `aiClient` is still needed for message routing (`WorkerRouter`), so it stays

### 9. Dependencies

Run `go mod tidy` after all Go changes are made. This will remove the now-unused `github.com/robfig/cron/v3` dependency from `go.mod` and `go.sum`.

### 10. Frontend

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
- Remove the `setScheduleEnabled(false)` and `setScheduleDescription("")` calls in the post-create reset block
- Remove schedule UI in the create form: the checkbox div and the `{scheduleEnabled && (...)}` conditional block
- Worker card (list view, line 174): replace the `w.schedule_enabled ? ... : t("common.onDemand")` expression with just `t("common.onDemand")`

**`web/src/pages/worker-detail.tsx`**

- Remove the schedule info `<p>` block in the Info tab (lines 196-199) that renders `worker.schedule_enabled` and `worker.cron_expression`

**`web/src/locales/en.json` and `web/src/locales/zh.json`**

Remove orphaned keys:
- `common.schedule`
- `common.onDemand` — keep if used elsewhere; if only used for schedule display, remove
- `workers.form.enableSchedule`
- `workers.form.scheduleDescription`
- `workers.form.scheduleDescriptionPlaceholder`
- `workers.form.promptPlaceholder` — update if it references scheduling, otherwise keep
- `workerDetail.scheduleEnabled`
- `workerDetail.scheduleDisabled`

## Files to Modify

| File | Action |
|------|--------|
| `internal/store/db.go` | Add migrations v11–v14 |
| `internal/model/worker.go` | Remove 3 fields |
| `internal/store/worker_store.go` | Remove 3 columns from queries, delete `ListScheduledWorkers` |
| `internal/ai/interfaces.go` | Delete `CronResolver` interface |
| `internal/ai/claude_code_client.go` | Delete `CronFromDescription` method |
| `internal/worker/manager.go` | Remove `cronResolver`, `ResolveCron`, simplify `CreateWorker` and `ExecuteWorker` |
| `internal/scheduler/cron.go` | Delete file |
| `internal/api/router.go` | Remove `WorkerScheduler`, `scheduler` field and param |
| `internal/api/worker_handler.go` | Remove schedule fields, `applySchedule`, both `RemoveWorker` call sites |
| `internal/api/locales/en.json` | Remove 3 orphaned keys |
| `internal/api/locales/zh.json` | Remove 3 orphaned keys |
| `cmd/server/main.go` | Remove scheduler init/stop, update `NewServer` and `NewManager` calls |
| `go.mod` + `go.sum` | Run `go mod tidy` to remove `robfig/cron/v3` |
| `web/src/lib/types.ts` | Remove 3 fields from `Worker` interface |
| `web/src/lib/api.ts` | Remove schedule fields from create type |
| `web/src/hooks/use-workers.ts` | Remove schedule fields from mutation type |
| `web/src/pages/workers.tsx` | Remove schedule form UI, state, and card display |
| `web/src/pages/worker-detail.tsx` | Remove schedule display in Info tab |
| `web/src/locales/en.json` | Remove orphaned schedule keys |
| `web/src/locales/zh.json` | Remove orphaned schedule keys |

## Testing

- Existing Go unit tests in `internal/store/worker_store_test.go` must still pass after removing scheduling columns
- Verify the app compiles cleanly with `go build ./...`
- Manually verify worker create/list/update flows work without schedule fields
