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

All timestamps stored as TEXT in ISO 8601 format with millisecond precision and UTC `Z` suffix:

```
2026-03-11T10:30:00.123Z
```

Format string: `"2006-01-02T15:04:05.000Z"` (Go) / `strftime('%Y-%m-%dT%H:%M:%fZ', 'now')` (SQLite).

The `Z` suffix is required: `mattn/go-sqlite3` only auto-parses TEXT into `time.Time` for formats that include a timezone marker. Without `Z`, scanning into `*time.Time` will fail silently or error. With `Z`, the format matches the driver's recognized RFC3339-with-fractional-seconds pattern. The format remains lexicographically sortable (UTC-only, fixed-width milliseconds).

### Schema Changes (`internal/store/db.go`)

Update `CREATE TABLE` defaults to use millisecond-precision SQLite functions:

```sql
-- platform_messages
received_at DATETIME NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),

-- worker_executions (defensive fallback for direct SQL inserts)
started_at   DATETIME DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
completed_at DATETIME DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
```

Note: Go code always supplies explicit values for these columns; the `DEFAULT` is a defensive fallback for direct SQL inserts and tests. The `CREATE TABLE IF NOT EXISTS` change applies only to new databases — existing databases continue to work correctly, with old rows retaining second-level precision.

### Go Write-Side Changes

**`internal/store/message_store.go`**

`Create()` — add `received_at` to INSERT, binding a Go-formatted millisecond string:
```go
result, err := s.db.ExecContext(ctx,
    `INSERT OR IGNORE INTO platform_messages
         (id, session_key, platform, content, raw, platform_msg_id, received_at)
     VALUES (?, ?, ?, ?, ?, ?, ?)`,
    id, sessionKey, platform, content, raw, platformMsgID,
    time.Now().UTC().Format("2006-01-02T15:04:05.000Z"),
)
```

`MarkTerminal()` — replace `datetime('now')` with a Go-bound parameter. The args slice gains one extra element for `processed_at` inserted after `status`:
```go
now := time.Now().UTC().Format("2006-01-02T15:04:05.000Z")
args := make([]any, 0, len(ids)+2)
args = append(args, status, now)        // status=?, processed_at=?
for _, id := range ids {
    args = append(args, id)             // id IN (?, ?, ...)
}
// query: SET status = ?, processed_at = ? WHERE id IN (...)
```

`InsertClearSentinel()` — no change needed. It omits `received_at`, so it picks up the new `DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))` automatically.

**`internal/store/execution_store.go`**

`create()` — format `started_at` as a millisecond string for the INSERT, while keeping `exec.StartedAt` pointing to the original `time.Time` for the returned struct:
```go
now := time.Now().UTC()
startedAtStr := now.Format("2006-01-02T15:04:05.000Z")
exec := model.WorkerExecution{
    // ... other fields ...
    StartedAt: &now,   // struct field stays *time.Time
}
_, err := s.db.Exec(
    `INSERT INTO worker_executions (..., started_at) VALUES (..., ?)`,
    // ... other args ..., startedAtStr,   // DB gets formatted string
)
```

`UpdateResult()` — format `completed_at` as millisecond string:
```go
now := time.Now().UTC()
completedAt := now.Format("2006-01-02T15:04:05.000Z")
_, err := s.db.Exec(
    `UPDATE worker_executions SET result=?, status=?, completed_at=? WHERE id=?`,
    result, status, completedAt, id,
)
```

### Model / Scan Compatibility

`WorkerExecution.StartedAt` and `CompletedAt` remain `*time.Time`. When `scanExecution()` scans these columns, `mattn/go-sqlite3` recognizes the `2026-03-11T10:30:00.123Z` format as RFC3339-with-fractional-seconds and converts it back to `time.Time` correctly. No model struct or API changes required.

### Existing Data

Old rows retain second-level precision. Mixed ordering is acceptable: same-second old rows fall back to `rowid` as before, which is fine since they predate the fix.

## Files to Change

1. `internal/store/db.go` — update `CREATE TABLE` defaults for `received_at`, `started_at`, `completed_at`
2. `internal/store/message_store.go` — `Create()` and `MarkTerminal()`
3. `internal/store/execution_store.go` — `create()` and `UpdateResult()`

## Testing

- Existing unit tests should continue to pass without modification
- Verify that `GetSession()` ordering is stable for rapid back-to-back inserts
- Verify `WorkerExecution` JSON serialization still includes `started_at`/`completed_at` with sub-second values
