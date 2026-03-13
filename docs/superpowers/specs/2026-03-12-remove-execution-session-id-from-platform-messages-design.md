# Design: Remove platform_messages.execution_id and session_id

**Date:** 2026-03-12
**Status:** Approved

## Background

`platform_messages` has two columns — `execution_id` and `session_id` — that were originally used to track which Claude session was associated with each message, enabling `--resume` on subsequent turns.

A dedicated `session_contexts` table (migration 12) now serves as the authoritative store for session resume state. The dispatcher reads from `session_contexts` via `GetSessionContext` and writes via `UpsertSessionContext`. The `platform_messages` columns are still written (`SetMessageExecution`) but never read back in production code. `GetSession()`, which reads them, is only called from tests.

These columns are dead weight: written but never consumed, they cause confusion and accumulate stale data.

## Goal

Remove `execution_id` and `session_id` from `platform_messages` and delete all code that exists solely to read or write those columns.

## Approach

Use `ALTER TABLE ... DROP COLUMN` (SQLite 3.35+). Atomic, safe under WAL mode, keeps migration history simple.

## Changes

### 1. DB Migrations (`internal/store/db.go`)

Append two new migrations:

```
version 13 — ALTER TABLE platform_messages DROP COLUMN execution_id
version 14 — ALTER TABLE platform_messages DROP COLUMN session_id
```

Migration 3 (the original `CREATE TABLE`) is left unchanged as historical record.

### 2. MessageStore dead code (`internal/store/message_store.go`)

Delete three methods that exist solely to read/write the removed columns:

- `GetSession(ctx, sessionKey)` — queries `execution_id`/`session_id`, returns `*platform.Session`
- `SetExecution(ctx, msgID, executionID, sessionID)` — unconditional UPDATE of the two columns
- `SetMessageExecution(ctx, messageID, executionID, sessionID)` — conditional UPDATE (`WHERE status = 'bee_processed'`)

### 3. platform.Session struct (`internal/platform/interfaces.go`)

Delete the `Session` struct. Its only use is as the return type of `GetSession`.

### 4. Dispatcher cleanup (`internal/dispatcher/dispatcher.go`)

- Delete `MessageStore` interface definition
- Delete `msgStore MessageStore` field from `Dispatcher` struct
- Remove `msgStore MessageStore` parameter from `New()`
- Delete the `SetMessageExecution` call in `executeAsync`

### 5. Test cleanup

**`internal/store/message_store_test.go`** — delete:

- `TestMessageStore_GetSession_NoRows`
- `TestMessageStore_GetSession_AfterExecution`
- `TestMessageStore_GetSession_AfterClear`
- `TestMessageStore_GetSession_FirstMessageNoExecution`
- `TestMessageStore_SetMessageExecution_OnlyWhenBeeProcessed`
- Any test cases that call `SetExecution` on `MessageStore`

**`internal/dispatcher/dispatcher_test.go`** — delete:

- `mockMsgStore` struct and its `SetMessageExecution` method
- Update `New()` calls to remove the `msgStore` argument

## What Is Not Changed

- `session_contexts` table and all `SessionStore` methods — the live session resume mechanism, unaffected
- `tasks.execution_id` column — belongs to a different table, serves a different purpose
- `worker_executions.session_id` column — belongs to a different table, used by `ExecutionStore`
- Migration 3 DDL — historical record, left as-is

## Testing

```
go test ./internal/store/... ./internal/dispatcher/... ./internal/bee/...
```

All tests must pass. Deleted test functions are not replaced — their coverage is irrelevant once the underlying code is gone.
