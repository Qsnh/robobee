# Drop platform_sessions: platform_messages as Single Source of Truth

**Date:** 2026-03-11
**Status:** Ready for Implementation

## Problem

`platform_sessions` (and the legacy per-platform tables `feishu_sessions`, `dingtalk_sessions`, `mail_sessions`) duplicate state that already lives in `platform_messages`. Every successful execution writes to both tables. This creates two sources of truth for session state, with no guarantee of consistency.

## Goal

Make `platform_messages` the single source of truth for session state. Remove all separate session tables and the `platform.SessionStore` abstraction. Session state (last execution ID, session ID, worker ID) is derived from the latest message row for a given `session_key`.

## Design

### Schema Changes (`internal/store/db.go`)

Add two columns to `platform_messages`:

```sql
execution_id  TEXT NOT NULL DEFAULT ''
session_id    TEXT NOT NULL DEFAULT ''
```

Add a new status value `'clear'` (no DDL constraint needed; signals that a user reset their session).

**Migration strategy** (no versioning system exists; use idempotent ALTER TABLE):

```go
for _, col := range []string{
    "ALTER TABLE platform_messages ADD COLUMN execution_id TEXT NOT NULL DEFAULT ''",
    "ALTER TABLE platform_messages ADD COLUMN session_id  TEXT NOT NULL DEFAULT ''",
} {
    db.Exec(col) // ignore "duplicate column name" error on re-runs
}
```

**Drop redundant tables:**

```sql
DROP TABLE IF EXISTS platform_sessions;
DROP TABLE IF EXISTS feishu_sessions;
DROP TABLE IF EXISTS dingtalk_sessions;
DROP TABLE IF EXISTS mail_sessions;
```

**Existing data:** Session records in `platform_sessions` are lost on deployment. The next message from any user starts a fresh conversation (same behavior as if the user had issued `clear`). This is an acceptable one-time interruption.

### Interface Changes (`internal/platform/interfaces.go`)

**Remove** `platform.SessionStore` interface entirely.

**Add three methods to `platform.MessageStore`:**

```go
// GetSession returns the session state derived from the latest non-clear message
// for the given sessionKey. Returns nil if no session exists or it was cleared.
GetSession(ctx context.Context, sessionKey string) (*Session, error)

// SetExecution records the execution metadata on the given message row.
SetExecution(ctx context.Context, msgID, executionID, sessionID string) error

// InsertClearSentinel inserts a 'clear' sentinel row for the given session.
InsertClearSentinel(ctx context.Context, id, sessionKey, platform string) error
```

**`platform.Session` struct** is retained in `interfaces.go` as the return type of `GetSession`.

**`platform.Executor` type** gains a `primaryMsgID` parameter:

```go
// Before
type Executor func(sessionKey, workerID, content string, replyTo InboundMessage)

// After
type Executor func(sessionKey, workerID, content string, replyTo InboundMessage, primaryMsgID string)
```

### Pipeline (`internal/platform/pipeline.go`)

Constructor drops `SessionStore`, takes `MessageStore` instead:

```go
func NewPipeline(router MessageRouter, msgStore MessageStore, manager ExecutionManager) *Pipeline
```

**`Handle`** (used only for `clear`): replace `sessions.Delete` with sentinel insert:

```go
p.msgStore.InsertClearSentinel(ctx, uuid.New().String(), msg.SessionKey, msg.Platform)
```

**`HandleRouted`** gains `msgID string` parameter and uses `msgStore` for session reads/writes:

```go
func (p *Pipeline) HandleRouted(ctx context.Context, msg InboundMessage, workerID, msgID string) string

// session read
sess, err := p.msgStore.GetSession(ctx, msg.SessionKey)

// session write (after execution starts)
p.msgStore.SetExecution(ctx, msgID, exec.ID, exec.SessionID)
```

### Queue Layer (`internal/platform/session_queue.go`, `queue_manager.go`, `manager.go`)

**`sessionQueue.runExecutor`** passes `ids[0]` as `primaryMsgID` to the executor:

```go
q.executor(q.sessionKey, q.workerID, content, replyTo, ids[0])
```

**`manager.go` executor closure** accepts and forwards `primaryMsgID`:

```go
executor := func(sessionKey, workerID, content string, replyTo InboundMessage, primaryMsgID string) {
    mergedMsg := replyTo
    mergedMsg.Content = content
    result := m.pipeline.HandleRouted(context.Background(), mergedMsg, workerID, primaryMsgID)
    // ... send result
}
```

### App Wiring (`cmd/server/main.go`)

```go
// Before
sessionStore := store.NewPlatformSessionStore(db)
pipe := platform.NewPipeline(router, sessionStore, mgr)

// After
msgStore := store.NewMessageStore(db)
pipe := platform.NewPipeline(router, msgStore, mgr)
```

### store.MessageStore Implementation (`internal/store/message_store.go`)

**`GetSession`:** Query the latest row for `session_key` ordered by `received_at DESC`. Return nil if status is `'clear'` or `execution_id` is empty (no execution has completed yet for this session).

**`SetExecution`:** `UPDATE platform_messages SET execution_id = ?, session_id = ? WHERE id = ?`

**`InsertClearSentinel`:** Insert a row with `status = 'clear'`, empty content, and the provided ID.

### Files Deleted

- `internal/store/platform_session_store.go`
- `internal/store/feishu_session_store.go`
- `internal/store/dingtalk_session_store.go`
- `internal/store/mail_session_store.go`

### Testing

**`pipeline_test.go`:**
- Replace `stubSessionStore` with a `stubMessageStore` that implements the full `platform.MessageStore` interface including the three new methods.
- Update all `HandleRouted` calls to pass a `msgID` argument.
- Add tests: `clear` command calls `InsertClearSentinel`; `GetSession` returning nil routes to `ExecuteWorker`.

**`message_store_test.go`:**
- Add integration tests for `GetSession`, `SetExecution`, and `InsertClearSentinel` using an in-memory SQLite DB.
- Cover: first message (no prior session), existing session, session after clear.

**`session_queue_test.go` / `queue_manager_test.go`:**
- Update `Executor` stubs to the new signature; assert that `primaryMsgID == ids[0]`.

## Scope

**Files modified:** `internal/store/db.go`, `internal/store/message_store.go`, `internal/platform/interfaces.go`, `internal/platform/pipeline.go`, `internal/platform/session_queue.go`, `internal/platform/queue_manager.go`, `internal/platform/manager.go`, `cmd/server/main.go`, `internal/platform/pipeline_test.go`, `internal/store/message_store_test.go`, `internal/platform/session_queue_test.go`, `internal/platform/queue_manager_test.go`

**Files deleted:** `internal/store/platform_session_store.go`, `internal/store/feishu_session_store.go`, `internal/store/dingtalk_session_store.go`, `internal/store/mail_session_store.go`
