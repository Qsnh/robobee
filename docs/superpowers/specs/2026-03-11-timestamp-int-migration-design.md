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

- `Create`: replace `w.CreatedAt = time.Now().UTC()` with `w.CreatedAt = time.Now().UnixMilli()`; the follow-on assignment `w.UpdatedAt = w.CreatedAt` remains valid (both are now `int64`) and is unchanged
- `Update` / `UpdateStatus`: replace `time.Now().UTC()` with `time.Now().UnixMilli()`
- `scanWorker`: `database/sql` natively scans a SQLite `INTEGER` into a Go `int64`, so no scan logic changes are needed
- The `"time"` import is retained — `time.Now().UnixMilli()` still requires it

### 5. `internal/store/execution_store.go`

- `create`: remove `now := time.Now().UTC()` and `startedAtStr` string formatting; replace with:
  ```go
  millis := time.Now().UnixMilli()
  ```
  Assign `StartedAt: &millis` in the struct literal. A temporary variable is required — `&time.Now().UnixMilli()` is not valid Go.
- `UpdateResult`: replace `completedAt` string variable with `time.Now().UnixMilli()` passed directly to `db.Exec`
- The `"time"` import is retained

### 6. `internal/store/message_store.go`

- `Create`: replace `time.Now().UTC().Format(...)` with `time.Now().UnixMilli()`
- `MarkTerminal`: replace the `now` string variable with `time.Now().UnixMilli()` passed directly to `db.ExecContext`
- `InsertClearSentinel`: add `received_at` to the INSERT column list and supply `time.Now().UnixMilli()` as the value. This is a required correctness fix: the current code omits `received_at` from the INSERT and silently relies on the SQL `DEFAULT` expression. Removing that default (as planned) will cause a `NOT NULL` constraint violation at runtime. The existing tests pass today only because the SQL default handles it silently — they will fail without this fix. The tests themselves do not assert on timestamp format and require no changes.
- `GetSession` uses `ORDER BY received_at DESC` — sort correctness is preserved since larger integers represent more recent times

### 7. `web/src/lib/types.ts`

- `Worker.created_at: string` → `number`
- `Worker.updated_at: string` → `number`
- `WorkerExecution.started_at: string | null` → `number | null`
- `WorkerExecution.completed_at: string | null` → `number | null`

### 8. Frontend pages

`new Date(timestamp)` accepts both string ISO and numeric ms values, so display calls like `new Date(worker.created_at).toLocaleString()` require no logic change.

Two sort comparisons use `String.prototype.localeCompare` which is not valid on `number`. These must be changed to numeric subtraction:

- `web/src/pages/executions.tsx` (lines 34–38): replace `bTime.localeCompare(aTime)` with `(b[0].started_at ?? 0) - (a[0].started_at ?? 0)`
- `web/src/pages/worker-detail.tsx` (lines 49–52): same change

### 9. `internal/store/execution_store_test.go`

Two tests assert that the raw DB value is a formatted datetime string:
- `TestExecutionStore_Create_StartedAtMillisecondPrecision`
- `TestExecutionStore_UpdateResult_CompletedAtMillisecondPrecision`

Rewrite both to scan the raw column value into `int64` and assert it is a positive Unix millisecond timestamp (`> 0`).

### 10. `internal/store/message_store_test.go`

Two tests assert that the raw DB value is a formatted datetime string:
- `TestMessageStore_Create_ReceivedAtMillisecondPrecision`
- `TestMessageStore_MarkTerminal_ProcessedAtMillisecondPrecision`

Rewrite both to scan the raw column value into `int64` and assert it is a positive Unix millisecond timestamp (`> 0`).

### 11. Local DB cleanup

Delete `data/robobee.db`, `data/robobee.db-shm`, `data/robobee.db-wal`. The app recreates them on next startup.

## Out of Scope

- `schema_migrations.applied_at` — internal bookkeeping column, not part of this change
- `PendingMessage` model — does not carry timestamp fields
- `platform_messages` store methods return `model.PendingMessage` (no timestamp fields) or raw scans — no model struct exposes `received_at` or `processed_at`
