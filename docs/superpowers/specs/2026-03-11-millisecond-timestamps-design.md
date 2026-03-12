# Millisecond Timestamp Precision

**Date:** 2026-03-11
**Status:** Approved

## Problem

`platform_messages.received_at` uses `datetime('now')` which has second-level precision. When multiple messages arrive within the same second, `GetSession()` ordering (`ORDER BY received_at DESC, rowid DESC`) falls back to `rowid` as a tiebreaker — unstable and incidental. The same issue applies to `processed_at`, `started_at`, and `completed_at`.

## Scope

Four timestamp columns across two tables:

| Table | Column | Current Write Method |
|-------|--------|---------------------|
| `platform_messages` | `received_at` | SQL `DEFAULT (datetime('now'))` |
| `platform_messages` | `processed_at` | SQL `datetime('now')` in `MarkTerminal()` |
| `worker_executions` | `started_at` | Go `time.Now().UTC()` in `create()` |
| `worker_executions` | `completed_at` | Go `time.Now().UTC()` in `UpdateResult()` |

## Design

### Storage Format

All timestamps stored as TEXT in ISO 8601 format with millisecond precision:

```
2026-03-11T10:30:00.123
```

Format string: `"2006-01-02T15:04:05.000"` (Go) / `strftime('%Y-%m-%dT%H:%M:%f', 'now')` (SQLite).

This format is lexicographically sortable, compatible with SQLite's TEXT comparison, and parseable by both `mattn/go-sqlite3` and `modernc.org/sqlite` drivers back into `time.Time`.

### Schema Changes (`internal/store/db.go`)

Update `CREATE TABLE` defaults to use millisecond-precision SQLite functions:

```sql
-- platform_messages
received_at DATETIME NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%f', 'now')),

-- worker_executions (add defaults as fallback)
started_at   DATETIME DEFAULT (strftime('%Y-%m-%dT%H:%M:%f', 'now')),
completed_at DATETIME DEFAULT (strftime('%Y-%m-%dT%H:%M:%f', 'now')),
```

### Go Write-Side Changes

**`internal/store/message_store.go`**

`Create()` — insert `received_at` explicitly:
```go
result, err := s.db.ExecContext(ctx,
    `INSERT OR IGNORE INTO platform_messages
         (id, session_key, platform, content, raw, platform_msg_id, received_at)
     VALUES (?, ?, ?, ?, ?, ?, ?)`,
    id, sessionKey, platform, content, raw, platformMsgID,
    time.Now().UTC().Format("2006-01-02T15:04:05.000"),
)
```

`MarkTerminal()` — pass `processed_at` from Go instead of SQL `datetime('now')`:
```go
// before binding other params, compute:
now := time.Now().UTC().Format("2006-01-02T15:04:05.000")
// use `now` in the UPDATE SET processed_at = ?
```

**`internal/store/execution_store.go`**

`create()` — format `started_at` as millisecond string:
```go
startedAt := time.Now().UTC().Format("2006-01-02T15:04:05.000")
// pass startedAt in INSERT instead of time.Now().UTC()
```

`UpdateResult()` — format `completed_at` as millisecond string:
```go
completedAt := time.Now().UTC().Format("2006-01-02T15:04:05.000")
// pass completedAt in UPDATE instead of time.Now().UTC()
```

### Model / Scan Compatibility

`WorkerExecution.StartedAt` and `CompletedAt` remain `*time.Time`. The SQLite driver parses the millisecond TEXT format back into `time.Time` correctly. No model struct changes required.

### Existing Data

Old rows retain second-level precision. Mixed ordering (old second-precision + new millisecond-precision) is acceptable: same-second old rows fall back to `rowid` as before, which is fine since they predate the fix.

### No Migrations Required

`ALTER COLUMN DEFAULT` is not supported in SQLite. Since:
1. Go code explicitly passes the value (not relying on DEFAULT), the DEFAULT in schema is only a fallback for direct SQL inserts.
2. No data migration is needed for existing rows.

The `CREATE TABLE IF NOT EXISTS` change applies to new databases only. Existing databases continue to work correctly.

## Files to Change

1. `internal/store/db.go` — update `CREATE TABLE` defaults for all four columns
2. `internal/store/message_store.go` — `Create()` and `MarkTerminal()`
3. `internal/store/execution_store.go` — `create()` and `UpdateResult()`

## Testing

- Existing unit tests should continue to pass without modification
- Verify that `GetSession()` ordering is stable for rapid back-to-back inserts
- Verify `WorkerExecution` JSON serialization still includes `started_at`/`completed_at` with sub-second values
