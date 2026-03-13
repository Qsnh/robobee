# platform_messages Schema Cleanup

**Date:** 2026-03-12

## Background

Worker execution is now driven by the `tasks` table. Each task carries its own `worker_id`, making the `worker_id` column on `platform_messages` redundant. The table also lacks standard audit columns (`created_at`, `updated_at`) present on every other entity table.

Since the product has not launched, there is no live data to migrate. Changes are made directly to the `CREATE TABLE` statement in the migration file.

## Schema Changes

### platform_messages (migration version 3)

**Remove:** `worker_id TEXT NOT NULL DEFAULT ''`

**Add:**
- `created_at INTEGER NOT NULL` — Unix ms, set on INSERT
- `updated_at INTEGER NOT NULL` — Unix ms, set on INSERT and updated on every UPDATE

**Final schema:**
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

### Index removal

Migration version 8 (`idx_platform_messages_worker_status`) references `worker_id` and must be dropped.

## Go Code Changes

### `internal/store/message_store.go`

- **Delete** `SetWorkerID()` — no longer needed; worker routing is via `tasks.worker_id`
- **Delete** `GetUnfinished()` — dead code; startup recovery now uses the `tasks` table
- **Update** `GetSession()` — remove `worker_id` from `SELECT` and from the returned `platform.Session`
- **Update all INSERTs** — set `created_at` and `updated_at` to `time.Now().UnixMilli()`
- **Update all UPDATEs** — include `updated_at = <now>` in every UPDATE statement

### `internal/model/pending_message.go`

- **Delete file** — `PendingMessage` has no callers outside `message_store.go` itself (which is also being removed)

### `internal/platform/interfaces.go`

- **Remove** `WorkerID string` from the `Session` struct — the field is populated from `platform_messages.worker_id` (being removed) and is unused by the dispatcher

### `internal/store/message_store_test.go`

- **Delete** `TestMessageStore_SetWorkerID`
- **Delete** `TestMessageStore_GetUnfinished`
- **Remove** all calls to `SetWorkerID` used as test setup in remaining tests
- **Remove** assertions on `sess.WorkerID`

## Non-Goals

- No changes to the `tasks` table or `task_store.go`
- No changes to startup recovery logic (already handled by tasks)
- No trigger-based auto-update of `updated_at`; Go code manages it explicitly, consistent with `workers` and `tasks` tables
