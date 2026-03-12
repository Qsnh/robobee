# Design: Migrate Timestamp Fields to Integer (Unix ms)

**Date:** 2026-03-11

## Summary

Change six timestamp columns across three tables from `DATETIME` (text) to `INTEGER` (Unix milliseconds). The app has not launched, so old data does not need to be preserved.

## Affected Fields

| Table | Column | Old Type | New Type |
|---|---|---|---|
| `workers` | `created_at` | `DATETIME` | `INTEGER` |
| `workers` | `updated_at` | `DATETIME` | `INTEGER` |
| `worker_executions` | `started_at` | `DATETIME` | `INTEGER` |
| `worker_executions` | `completed_at` | `DATETIME` | `INTEGER` |
| `platform_messages` | `received_at` | `DATETIME` | `INTEGER` |
| `platform_messages` | `processed_at` | `DATETIME` | `INTEGER` |

## Approach

**Direct edit of existing migrations (Approach B)** — since the app is pre-launch, modify migrations 1, 2, and 3 in place and delete the local DB file. No data migration needed.

## Changes

### 1. `internal/store/db.go`

- Migration 1 (`workers`): change `created_at` and `updated_at` from `DATETIME NOT NULL DEFAULT (datetime('now'))` to `INTEGER NOT NULL`
- Migration 2 (`worker_executions`): change `started_at` and `completed_at` from `DATETIME DEFAULT (strftime(...))` to `INTEGER`
- Migration 3 (`platform_messages`): change `received_at` from `DATETIME NOT NULL DEFAULT (strftime(...))` to `INTEGER NOT NULL`, and `processed_at` from `DATETIME` to `INTEGER`
- Remove all `DATETIME` default expressions — values are always written explicitly by Go code

### 2. `internal/model/worker.go`

- `CreatedAt time.Time` → `CreatedAt int64`
- `UpdatedAt time.Time` → `UpdatedAt int64`
- Remove `"time"` import

### 3. `internal/model/execution.go`

- `StartedAt *time.Time` → `StartedAt *int64`
- `CompletedAt *time.Time` → `CompletedAt *int64`
- Remove `"time"` import

### 4. `internal/store/worker_store.go`

- `Create`: `w.CreatedAt = time.Now().UTC()` → `w.CreatedAt = time.Now().UnixMilli()`
- `Update` / `UpdateStatus`: `time.Now().UTC()` → `time.Now().UnixMilli()`
- `scanWorker`: scans directly into `int64` fields (no change needed to scan logic)

### 5. `internal/store/execution_store.go`

- `create`: replace `startedAtStr` string formatting with `time.Now().UnixMilli()`; update `StartedAt` field to `*int64`
- `UpdateResult`: replace `completedAt` string formatting with `time.Now().UnixMilli()`

### 6. `internal/store/message_store.go`

- `Create`: replace `time.Now().UTC().Format(...)` with `time.Now().UnixMilli()`
- `MarkTerminal`: replace `now` string with `time.Now().UnixMilli()`

### 7. `web/src/lib/types.ts`

- `Worker.created_at: string` → `number`
- `Worker.updated_at: string` → `number`
- `WorkerExecution.started_at: string | null` → `number | null`
- `WorkerExecution.completed_at: string | null` → `number | null`

### 8. Frontend pages

`new Date(timestamp)` works identically for both string ISO and numeric ms values — no logic changes required. Only type annotations update.

### 9. Local DB cleanup

Delete `data/robobee.db`, `data/robobee.db-shm`, `data/robobee.db-wal`. The app recreates them on next startup.

## Out of Scope

- `schema_migrations.applied_at` — internal bookkeeping column, not part of this change
- `PendingMessage` model — does not carry timestamp fields
