# platform_messages Schema Cleanup

**Date:** 2026-03-12

## Background

Worker execution is now driven by the `tasks` table. Each task carries its own `worker_id`, making the `worker_id` column on `platform_messages` redundant. The table also lacks standard audit columns (`created_at`, `updated_at`) present on every other entity table.

**No production database exists.** The product has not launched, so there is no live data to preserve. All schema changes are made by editing existing migration definitions in-place. No new migrations are needed.

## Schema Changes

### Edit migration version 3 — `platform_messages` CREATE TABLE

**Remove:** `worker_id TEXT NOT NULL DEFAULT ''`

**Add:**
- `created_at INTEGER NOT NULL` — Unix ms, set on INSERT
- `updated_at INTEGER NOT NULL` — Unix ms, set on INSERT and on every UPDATE

**Final SQL:**
```sql
CREATE TABLE IF NOT EXISTS platform_messages (
    id              TEXT PRIMARY KEY,
    session_key     TEXT NOT NULL,
    platform        TEXT NOT NULL,
    content         TEXT NOT NULL,
    execution_id    TEXT NOT NULL DEFAULT '',
    session_id      TEXT NOT NULL DEFAULT '',
    status          TEXT NOT NULL DEFAULT 'received',
    merged_into     TEXT NOT NULL DEFAULT '',
    platform_msg_id TEXT NOT NULL DEFAULT '',
    raw             TEXT NOT NULL DEFAULT '',
    received_at     INTEGER NOT NULL,
    processed_at    INTEGER,
    created_at      INTEGER NOT NULL,
    updated_at      INTEGER NOT NULL
)
```

### Edit migration version 8 — drop the index

Migration version 8 creates `idx_platform_messages_worker_status ON platform_messages(session_key, worker_id, status)`. Since `worker_id` is being removed from the table, replace the SQL with:

```sql
DROP INDEX IF EXISTS idx_platform_messages_worker_status
```

This mirrors migration 11's pattern (`DROP INDEX IF EXISTS idx_workers_schedule`). The version entry is kept so the version number remains stable.

## Go Code Changes

### `internal/store/message_store.go`

**Delete methods:**
- `SetWorkerID()` — worker routing is now via `tasks.worker_id`
- `GetUnfinished()` — dead code; has no callers outside its own test file; startup recovery uses the `tasks` table

**Update `GetSession()`:**
- Remove `worker_id` from the `SELECT` clause
- Remove the `workerID` local variable from the `Scan` call and remove `WorkerID: workerID` from the returned `platform.Session`
- The `WHERE` clause is unchanged (`execution_id != '' OR status = 'clear'`)

**Update INSERT methods — add `created_at` and `updated_at` set to `time.Now().UnixMilli()`:**

- `Create()` — add `created_at`, `updated_at` to column list and values. Note: uses `INSERT OR IGNORE`; when a duplicate row is skipped, no update occurs and no error is returned — this behavior is unchanged.
- `CreateBatch()` — add `created_at`, `updated_at` per row; note that `worker_id` was already absent from this INSERT, so the net change is 9 → 11 columns per row; update the placeholder string and args slice accordingly
- `InsertClearSentinel()` — add `created_at`, `updated_at` to the partial-column INSERT (required; both columns are `NOT NULL`)

**Update UPDATE methods — append `updated_at = <now>` to every UPDATE statement:**

- `SetStatus()`
- `UpdateStatusBatch()` — all delegating methods (`MarkBeeProcessed`, `ResetFeedingBatch`, `ResetFeedingToReceived`) are covered by this change
- `MarkMerged()` — both UPDATE statements inside this method
- `MarkTerminal()`
- `SetExecution()`
- `SetMessageExecution()`
- `ClaimBatch()` — the batch UPDATE inside the transaction

### `internal/model/pending_message.go`

**Delete file.** `PendingMessage` is only referenced inside `message_store.go` (in `GetUnfinished`, which is being deleted). A codebase-wide search confirms no other callers.

### `internal/platform/interfaces.go`

**Remove `WorkerID string` from the `Session` struct.** This field was populated from `platform_messages.worker_id`. A codebase-wide search confirms the field is not read by any caller — the dispatcher uses `task.WorkerID` from the dispatched task, not from the session.

### `internal/store/message_store_test.go`

**Delete these tests entirely:**
- `TestMessageStore_SetWorkerID`
- `TestMessageStore_GetUnfinished`
- `TestMessageStore_InsertClearSentinel_NotRecoverable` — its sole purpose was verifying the clear sentinel does not appear in `GetUnfinished`; that method no longer exists. The `InsertClearSentinel` method itself is tested by `TestMessageStore_GetSession_AfterClear`.

**Update `TestMessageStore_GetSession_AfterExecution`:**
- Delete the `s.SetWorkerID(ctx, "msg-1", "worker-abc")` call — it is redundant; `SetExecution` is what causes `GetSession` to return a non-nil result (via `execution_id != ''`)
- Delete the `sess.WorkerID` assertion block

**Update `TestMessageStore_SetExecution`:**
- Delete the `s.SetWorkerID(ctx, "msg-1", "worker-abc")` call — the comment on that line ("Need worker_id set so GetSession returns a result") is incorrect; `GetSession` filters on `execution_id != ''`, which `SetExecution` already satisfies
- Delete or update the now-incorrect comment

**Update `TestMessageStore_GetSession_AfterClear`:**
- Delete the `s.SetWorkerID(ctx, "msg-1", "worker-abc")` call — not needed for the test's intent

**Update `TestMessageStore_GetSession_FirstMessageNoExecution`:**
- Delete the `s.SetWorkerID(ctx, "msg-1", "worker-abc")` call — a plain `Create` row with empty `execution_id` is already sufficient to prove `GetSession` returns nil

**Update `TestMessageStore_SetMessageExecution_OnlyWhenBeeProcessed`:**
- The raw `INSERT INTO platform_messages` statement must include `created_at` and `updated_at` values (both `NOT NULL`). Add them to the column list and pass `time.Now().UnixMilli()` or a constant test timestamp.

## Non-Goals

- No changes to the `tasks` table or `task_store.go`
- No changes to startup recovery logic (already handled by the tasks table)
- No DB trigger for `updated_at`; Go code manages it explicitly, consistent with `workers` and `tasks` tables
