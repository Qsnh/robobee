# platform_messages Schema Cleanup Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Remove the `worker_id` column from `platform_messages`, add `created_at`/`updated_at` audit columns, and delete the dead code that used `worker_id`.

**Architecture:** Edit migrations 3 and 8 in-place (no production DB exists), then update Go code to match: delete dead methods (`SetWorkerID`, `GetUnfinished`), update all INSERT/UPDATE SQL to include the new columns, clean up the `Session` struct and model file, and fix tests.

**Tech Stack:** Go, SQLite (`go-sqlite3`), standard `database/sql`

**Spec:** `docs/superpowers/specs/2026-03-12-platform-messages-schema-cleanup-design.md`

---

## Chunk 1: Schema + Model Cleanup

### Task 1: Update migrations in `db.go`

**Files:**
- Modify: `internal/store/db.go`

- [ ] **Step 1: Edit migration version 3 — update `CREATE TABLE`**

In `internal/store/db.go`, replace migration version 3's SQL block. The current SQL has `worker_id TEXT NOT NULL DEFAULT ''` and no `created_at`/`updated_at`. Replace it with:

```go
{
    version: 3,
    name:    "20260311000003_create_table_platform_messages",
    sql: `CREATE TABLE IF NOT EXISTS platform_messages (
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
)`,
},
```

- [ ] **Step 2: Edit migration version 8 — replace index creation with drop**

Find migration version 8 (currently creates `idx_platform_messages_worker_status`). Replace its SQL:

```go
{
    version: 8,
    name:    "20260311000008_create_index_platform_messages_worker_status",
    sql:     `DROP INDEX IF EXISTS idx_platform_messages_worker_status`,
},
```

- [ ] **Step 3: Verify the DB initializes cleanly**

```bash
cd /Users/tengteng/work/robobee/core
go build ./internal/store/...
```

Expected: no compile errors.

- [ ] **Step 4: Commit**

```bash
git add internal/store/db.go
git commit -m "feat(db): remove worker_id, add created_at/updated_at to platform_messages"
```

---

### Task 2: Remove `WorkerID` from `platform.Session`

**Files:**
- Modify: `internal/platform/interfaces.go`

- [ ] **Step 1: Remove `WorkerID` from the `Session` struct**

In `internal/platform/interfaces.go`, update the `Session` struct from:

```go
type Session struct {
    Key             string
    Platform        string
    WorkerID        string
    SessionID       string
    LastExecutionID string
}
```

To:

```go
type Session struct {
    Key             string
    Platform        string
    SessionID       string
    LastExecutionID string
}
```

- [ ] **Step 2: Build to catch all compile errors caused by this removal**

```bash
go build ./...
```

Expected: compile errors in `internal/store/message_store.go` (references `WorkerID` in `GetSession`). These are fixed in Task 4.

- [ ] **Step 3: Commit**

```bash
git add internal/platform/interfaces.go
git commit -m "feat(platform): remove WorkerID from Session struct"
```

---

### Task 3: Delete `model/pending_message.go`

**Files:**
- Delete: `internal/model/pending_message.go`

- [ ] **Step 1: Delete the file**

```bash
rm /Users/tengteng/work/robobee/core/internal/model/pending_message.go
```

- [ ] **Step 2: Build to verify no other callers**

```bash
go build ./...
```

Expected: compile errors only in `internal/store/message_store.go` (references `model.PendingMessage` in `GetUnfinished`). These are resolved in Task 4.

- [ ] **Step 3: Commit**

```bash
git add -u internal/model/pending_message.go
git commit -m "feat(model): delete PendingMessage — no callers outside dead GetUnfinished"
```

---

## Chunk 2: message_store.go Rewrite

### Task 4: Update `message_store.go`

**Files:**
- Modify: `internal/store/message_store.go`

This task makes all changes to `message_store.go` at once (delete dead methods, fix `GetSession`, update all INSERT/UPDATE SQL). Do changes in this order.

- [ ] **Step 1: Delete `SetWorkerID` method**

Remove the entire method (lines 60–67):

```go
// SetWorkerID sets the worker_id and advances status to "routed".
func (s *MessageStore) SetWorkerID(ctx context.Context, id, workerID string) error {
    _, err := s.db.ExecContext(ctx,
        `UPDATE platform_messages SET worker_id = ?, status = 'routed' WHERE id = ?`,
        workerID, id,
    )
    return err
}
```

- [ ] **Step 2: Delete `GetUnfinished` method**

Remove the entire method (lines 134–158):

```go
// GetUnfinished returns messages with an active status that have a worker_id assigned,
// ordered by received_at ASC. Used for startup recovery.
func (s *MessageStore) GetUnfinished(ctx context.Context) ([]model.PendingMessage, error) {
    ...
}
```

- [ ] **Step 3: Update `GetSession` — remove `worker_id` from SELECT and Scan**

Replace the current `GetSession` implementation:

```go
// GetSession returns the session state derived from the latest message row for
// the given sessionKey. Returns nil if no session exists, the latest row is a
// 'clear' sentinel, or no execution has been written yet (execution_id is empty).
func (s *MessageStore) GetSession(ctx context.Context, sessionKey string) (*platform.Session, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT status, execution_id, session_id, platform
		FROM platform_messages
		WHERE session_key = ?
		  AND (execution_id != '' OR status = 'clear')
		ORDER BY received_at DESC, rowid DESC
		LIMIT 1`, sessionKey)

	var status, executionID, sessionID, plt string
	if err := row.Scan(&status, &executionID, &sessionID, &plt); err == sql.ErrNoRows {
		return nil, nil
	} else if err != nil {
		return nil, err
	}
	if status == "clear" || executionID == "" {
		return nil, nil
	}
	return &platform.Session{
		Key:             sessionKey,
		Platform:        plt,
		SessionID:       sessionID,
		LastExecutionID: executionID,
	}, nil
}
```

- [ ] **Step 4: Update `Create` — add `created_at`, `updated_at`**

Replace the `ExecContext` call inside `Create`:

```go
func (s *MessageStore) Create(ctx context.Context, id, sessionKey, platform, content, raw, platformMsgID string, messageTime int64) (bool, error) {
	if messageTime == 0 {
		messageTime = time.Now().UnixMilli()
	}
	now := time.Now().UnixMilli()
	result, err := s.db.ExecContext(ctx,
		`INSERT OR IGNORE INTO platform_messages (id, session_key, platform, content, raw, platform_msg_id, received_at, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		id, sessionKey, platform, content, raw, platformMsgID, messageTime, now, now,
	)
	...
```

- [ ] **Step 5: Update `SetStatus` — add `updated_at`**

```go
func (s *MessageStore) SetStatus(ctx context.Context, id, status string) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE platform_messages SET status = ?, updated_at = ? WHERE id = ?`,
		status, time.Now().UnixMilli(), id,
	)
	return err
}
```

- [ ] **Step 6: Update `UpdateStatusBatch` — add `updated_at`**

```go
func (s *MessageStore) UpdateStatusBatch(ctx context.Context, ids []string, status string) error {
	if len(ids) == 0 {
		return nil
	}
	placeholders := strings.Repeat("?,", len(ids))
	placeholders = placeholders[:len(placeholders)-1]
	args := make([]any, 0, len(ids)+2)
	args = append(args, status, time.Now().UnixMilli())
	for _, id := range ids {
		args = append(args, id)
	}
	_, err := s.db.ExecContext(ctx,
		fmt.Sprintf(`UPDATE platform_messages SET status = ?, updated_at = ? WHERE id IN (%s)`, placeholders),
		args...,
	)
	return err
}
```

- [ ] **Step 7: Update `MarkMerged` — add `updated_at` to both UPDATE statements**

```go
func (s *MessageStore) MarkMerged(ctx context.Context, primaryID string, mergedIDs []string) error {
	now := time.Now().UnixMilli()
	if _, err := s.db.ExecContext(ctx,
		`UPDATE platform_messages SET status = 'merged', updated_at = ? WHERE id = ?`, now, primaryID,
	); err != nil {
		return err
	}
	for _, id := range mergedIDs {
		if _, err := s.db.ExecContext(ctx,
			`UPDATE platform_messages SET status = 'merged', merged_into = ?, updated_at = ? WHERE id = ?`,
			primaryID, now, id,
		); err != nil {
			return err
		}
	}
	return nil
}
```

- [ ] **Step 8: Update `MarkTerminal` — add `updated_at`**

```go
func (s *MessageStore) MarkTerminal(ctx context.Context, ids []string, status string) error {
	if len(ids) == 0 {
		return nil
	}
	placeholders := strings.Repeat("?,", len(ids))
	placeholders = placeholders[:len(placeholders)-1]
	now := time.Now().UnixMilli()
	args := make([]any, 0, len(ids)+3)
	args = append(args, status, now, now) // status=?, processed_at=?, updated_at=?
	for _, id := range ids {
		args = append(args, id)
	}
	_, err := s.db.ExecContext(ctx,
		fmt.Sprintf(`UPDATE platform_messages SET status = ?, processed_at = ?, updated_at = ? WHERE id IN (%s)`, placeholders),
		args...,
	)
	return err
}
```

- [ ] **Step 9: Update `SetExecution` — add `updated_at`**

```go
func (s *MessageStore) SetExecution(ctx context.Context, msgID, executionID, sessionID string) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE platform_messages SET execution_id = ?, session_id = ?, updated_at = ? WHERE id = ?`,
		executionID, sessionID, time.Now().UnixMilli(), msgID)
	return err
}
```

- [ ] **Step 10: Update `SetMessageExecution` — add `updated_at`**

```go
func (s *MessageStore) SetMessageExecution(ctx context.Context, messageID, executionID, sessionID string) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE platform_messages
         SET execution_id = ?, session_id = ?, updated_at = ?
         WHERE id = ? AND status = 'bee_processed'`,
		executionID, sessionID, time.Now().UnixMilli(), messageID,
	)
	return err
}
```

- [ ] **Step 11: Update `InsertClearSentinel` — add `created_at`, `updated_at`**

```go
func (s *MessageStore) InsertClearSentinel(ctx context.Context, id, sessionKey, plt string) error {
	now := time.Now().UnixMilli()
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO platform_messages (id, session_key, platform, content, status, received_at, created_at, updated_at)
		 VALUES (?, ?, ?, '', 'clear', ?, ?, ?)`,
		id, sessionKey, plt, now, now, now)
	return err
}
```

- [ ] **Step 12: Update `CreateBatch` — add `created_at`, `updated_at` per row**

`worker_id` was already absent from this INSERT. Net change: 9 → 11 columns per row.

```go
func (s *MessageStore) CreateBatch(ctx context.Context, msgs []BatchMsg) (int64, error) {
	if len(msgs) == 0 {
		return 0, nil
	}

	now := time.Now().UnixMilli()
	placeholders := strings.Repeat("(?,?,?,?,?,?,?,?,?,?,?),", len(msgs))
	placeholders = placeholders[:len(placeholders)-1]

	args := make([]any, 0, len(msgs)*11)
	for _, m := range msgs {
		mt := m.MessageTime
		if mt == 0 {
			mt = now
		}
		args = append(args, m.ID, m.SessionKey, m.Platform, m.Content, m.Raw,
			m.PlatformMsgID, mt, m.Status, m.MergedInto, now, now)
	}

	result, err := s.db.ExecContext(ctx,
		fmt.Sprintf(`INSERT OR IGNORE INTO platform_messages
			(id, session_key, platform, content, raw, platform_msg_id, received_at, status, merged_into, created_at, updated_at)
			VALUES %s`, placeholders),
		args...,
	)
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}
```

- [ ] **Step 13: Update `ClaimBatch` — add `updated_at` to the batch UPDATE**

Find the args construction block inside `ClaimBatch` and replace:

```go
	args := make([]any, 0, len(ids)+2)
	args = append(args, "feeding", time.Now().UnixMilli())
	for _, id := range ids {
		args = append(args, id)
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE platform_messages SET status = ?, updated_at = ? WHERE id IN (`+placeholders+`)`, args...); err != nil {
		return nil, fmt.Errorf("update feeding: %w", err)
	}
```

- [ ] **Step 14: Remove unused `model` import if now empty**

Check if `model` is still imported after deleting the `GetUnfinished` method (which used `model.PendingMessage`). If the import is now unused, remove it.

```bash
go build ./internal/store/...
```

Expected: clean build.

- [ ] **Step 15: Commit**

```bash
git add internal/store/message_store.go
git commit -m "feat(store): remove worker_id usage, add created_at/updated_at to all message_store writes"
```

---

## Chunk 3: Test Cleanup

### Task 5: Update `message_store_test.go`

**Files:**
- Modify: `internal/store/message_store_test.go`

- [ ] **Step 1: Delete `TestMessageStore_SetWorkerID`**

Remove the entire function (lines 136–144).

- [ ] **Step 2: Delete `TestMessageStore_GetUnfinished`**

Remove the entire function (lines 201–233 approx — from the function signature to the closing `}`).

- [ ] **Step 3: Delete `TestMessageStore_InsertClearSentinel_NotRecoverable`**

Remove the entire function (lines 339–357). Its purpose (verifying the clear sentinel doesn't appear in `GetUnfinished`) is moot since `GetUnfinished` no longer exists. `InsertClearSentinel` is still tested by `TestMessageStore_GetSession_AfterClear`.

- [ ] **Step 4: Update `TestMessageStore_GetSession_AfterExecution`**

Remove the `SetWorkerID` call and the `WorkerID` assertion. The test should look like:

```go
func TestMessageStore_GetSession_AfterExecution(t *testing.T) {
	s := setupMessageStore(t)
	ctx := context.Background()

	s.Create(ctx, "msg-1", "feishu:chat1:userA", "feishu", "hello", "", "", 0) //nolint
	if err := s.SetExecution(ctx, "msg-1", "exec-1", "sess-1"); err != nil {
		t.Fatalf("SetExecution: %v", err)
	}
	s.MarkTerminal(ctx, []string{"msg-1"}, "done")

	sess, err := s.GetSession(ctx, "feishu:chat1:userA")
	if err != nil {
		t.Fatalf("GetSession: %v", err)
	}
	if sess == nil {
		t.Fatal("expected session, got nil")
	}
	if sess.LastExecutionID != "exec-1" {
		t.Errorf("LastExecutionID: got %q, want %q", sess.LastExecutionID, "exec-1")
	}
	if sess.SessionID != "sess-1" {
		t.Errorf("SessionID: got %q, want %q", sess.SessionID, "sess-1")
	}
	if sess.Platform != "feishu" {
		t.Errorf("Platform: got %q, want %q", sess.Platform, "feishu")
	}
	if sess.Key != "feishu:chat1:userA" {
		t.Errorf("Key: got %q, want %q", sess.Key, "feishu:chat1:userA")
	}
}
```

- [ ] **Step 5: Update `TestMessageStore_SetExecution`**

Remove the `SetWorkerID` call and update the comment:

```go
func TestMessageStore_SetExecution(t *testing.T) {
	s := setupMessageStore(t)
	ctx := context.Background()

	s.Create(ctx, "msg-1", "feishu:chat1:userA", "feishu", "hello", "", "", 0) //nolint
	if err := s.SetExecution(ctx, "msg-1", "exec-42", "sess-42"); err != nil {
		t.Fatalf("SetExecution: %v", err)
	}
	// Verify via GetSession — execution_id != '' is sufficient for GetSession to return a result
	sess, err := s.GetSession(ctx, "feishu:chat1:userA")
	if err != nil {
		t.Fatalf("GetSession: %v", err)
	}
	if sess == nil {
		t.Fatal("expected session, got nil")
	}
	if sess.LastExecutionID != "exec-42" {
		t.Errorf("LastExecutionID: got %q, want exec-42", sess.LastExecutionID)
	}
}
```

- [ ] **Step 6: Update `TestMessageStore_GetSession_AfterClear`**

Remove the `SetWorkerID` call:

```go
func TestMessageStore_GetSession_AfterClear(t *testing.T) {
	s := setupMessageStore(t)
	ctx := context.Background()

	s.Create(ctx, "msg-1", "feishu:chat1:userA", "feishu", "hello", "", "", 0) //nolint
	s.SetExecution(ctx, "msg-1", "exec-1", "sess-1")
	s.MarkTerminal(ctx, []string{"msg-1"}, "done")

	if err := s.InsertClearSentinel(ctx, "clear-1", "feishu:chat1:userA", "feishu"); err != nil {
		t.Fatalf("InsertClearSentinel: %v", err)
	}

	sess, err := s.GetSession(ctx, "feishu:chat1:userA")
	if err != nil {
		t.Fatalf("GetSession after clear: %v", err)
	}
	if sess != nil {
		t.Errorf("expected nil after clear, got %+v", sess)
	}
}
```

- [ ] **Step 7: Update `TestMessageStore_GetSession_FirstMessageNoExecution`**

Remove the `SetWorkerID` call:

```go
func TestMessageStore_GetSession_FirstMessageNoExecution(t *testing.T) {
	s := setupMessageStore(t)
	ctx := context.Background()

	// message exists but SetExecution hasn't been called yet
	s.Create(ctx, "msg-1", "feishu:chat1:userA", "feishu", "hello", "", "", 0) //nolint

	sess, err := s.GetSession(ctx, "feishu:chat1:userA")
	if err != nil {
		t.Fatalf("GetSession: %v", err)
	}
	if sess != nil {
		t.Errorf("expected nil when execution_id is empty, got %+v", sess)
	}
}
```

- [ ] **Step 8: Update `TestMessageStore_SetMessageExecution_OnlyWhenBeeProcessed`**

The raw `db.Exec` INSERT must include `created_at` and `updated_at` (both `NOT NULL`):

```go
db.Exec(`INSERT INTO platform_messages
    (id, session_key, platform, content, raw, platform_msg_id, status, received_at, created_at, updated_at)
    VALUES ('m1', 'sk', 'feishu', 'hi', '', '', 'bee_processed', 1, 1, 1)`)
```

- [ ] **Step 9: Run all store tests**

```bash
go test ./internal/store/... -v 2>&1 | tail -30
```

Expected: all tests PASS, no compile errors.

- [ ] **Step 10: Run full test suite**

```bash
go test ./... 2>&1 | tail -20
```

Expected: all packages pass.

- [ ] **Step 11: Commit**

```bash
git add internal/store/message_store_test.go
git commit -m "test(store): remove SetWorkerID/GetUnfinished tests, fix remaining message_store tests"
```
