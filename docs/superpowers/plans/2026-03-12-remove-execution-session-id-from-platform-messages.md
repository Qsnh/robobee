# Remove platform_messages.execution_id and session_id — Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Drop `execution_id` and `session_id` columns from `platform_messages` and delete all code that exists solely to read or write them.

**Architecture:** Two SQLite `ALTER TABLE ... DROP COLUMN` migrations remove the columns. Dispatcher code that called `SetMessageExecution` is cleaned up first so the callee methods can be deleted cleanly without breaking compilation. Tests for the deleted methods are removed in the same pass.

**Tech Stack:** Go 1.21+, SQLite 3.35+ (WAL mode)

**Spec:** `docs/superpowers/specs/2026-03-12-remove-execution-session-id-from-platform-messages-design.md`

---

## Chunk 1: DB Migrations + Dispatcher Caller Cleanup

### Task 1: Add DROP COLUMN migrations

**Files:**
- Modify: `internal/store/db.go`

The current highest migration is version 12. Append versions 13 and 14.

- [ ] **Step 1: Add migrations 13 and 14 to the `migrations` slice in `db.go`**

Open `internal/store/db.go`. At the end of the `migrations` slice (after the closing `}` of the version 12 entry, before the final `}`), add:

```go
	{
		version: 13,
		name:    "20260312_drop_platform_messages_execution_id",
		sql:     `ALTER TABLE platform_messages DROP COLUMN execution_id`,
	},
	{
		version: 14,
		name:    "20260312_drop_platform_messages_session_id",
		sql:     `ALTER TABLE platform_messages DROP COLUMN session_id`,
	},
```

- [ ] **Step 2: Verify migrations compile**

```bash
go build ./internal/store/...
```

Expected: no errors.

- [ ] **Step 3: Commit**

```bash
git add internal/store/db.go
git commit -m "feat(store): add migrations 13-14 to drop execution_id and session_id from platform_messages"
```

---

### Task 2: Remove SetMessageExecution from dispatcher

**Files:**
- Modify: `internal/dispatcher/dispatcher.go`

The dispatcher is the only production caller of `SetMessageExecution`. Remove the call and the entire `MessageStore` interface before deleting the method itself — this keeps the code in a compilable state throughout.

- [ ] **Step 1: Delete the `MessageStore` interface**

In `internal/dispatcher/dispatcher.go`, delete these lines (around lines 37–39):

```go
// MessageStore is the subset of store.MessageStore used by the Dispatcher.
type MessageStore interface {
	SetMessageExecution(ctx context.Context, messageID, executionID, sessionID string) error
}
```

- [ ] **Step 2: Delete the `msgStore` field from the `Dispatcher` struct**

In the `Dispatcher` struct, delete:

```go
	msgStore     MessageStore
```

- [ ] **Step 3: Remove `msgStore MessageStore` from `New()`**

Change the `New()` function signature from:

```go
func New(manager ExecutionManager, taskStore TaskStore, msgStore MessageStore, sessionStore SessionStore, in <-chan DispatchTask) *Dispatcher {
	return &Dispatcher{
		manager:      manager,
		taskStore:    taskStore,
		msgStore:     msgStore,
		sessionStore: sessionStore,
```

to:

```go
func New(manager ExecutionManager, taskStore TaskStore, sessionStore SessionStore, in <-chan DispatchTask) *Dispatcher {
	return &Dispatcher{
		manager:      manager,
		taskStore:    taskStore,
		sessionStore: sessionStore,
```

- [ ] **Step 4: Delete the `SetMessageExecution` call in `executeAsync`**

In `executeAsync`, delete:

```go
	if task.TaskType == model.TaskTypeImmediate && task.MessageID != "" {
		d.msgStore.SetMessageExecution(ctx, task.MessageID, exec.ID, exec.SessionID) //nolint:errcheck
	}
```

- [ ] **Step 5: Verify dispatcher package compiles (ignoring test files)**

```bash
go build ./internal/dispatcher/...
```

Expected: compile errors from `dispatcher_test.go` and `cmd/server/main.go` (they still pass the old `msgStore` argument). That is expected — fix them in Tasks 3 and 4.

---

### Task 3: Update dispatcher_test.go

**Files:**
- Modify: `internal/dispatcher/dispatcher_test.go`

- [ ] **Step 1: Delete `mockMsgStore`**

Delete these three lines (around lines 39–41):

```go
type mockMsgStore struct{}

func (s *mockMsgStore) SetMessageExecution(_ context.Context, _, _, _ string) error { return nil }
```

- [ ] **Step 2: Update `newDispatcher` helper**

Change the `newDispatcher` function from:

```go
func newDispatcher(mgr dispatcher.ExecutionManager, ss dispatcher.SessionStore) (*dispatcher.Dispatcher, chan dispatcher.DispatchTask) {
	in := make(chan dispatcher.DispatchTask, 4)
	d := dispatcher.New(mgr, &mockTaskStore{}, &mockMsgStore{}, ss, in)
	return d, in
}
```

to:

```go
func newDispatcher(mgr dispatcher.ExecutionManager, ss dispatcher.SessionStore) (*dispatcher.Dispatcher, chan dispatcher.DispatchTask) {
	in := make(chan dispatcher.DispatchTask, 4)
	d := dispatcher.New(mgr, &mockTaskStore{}, ss, in)
	return d, in
}
```

- [ ] **Step 3: Verify dispatcher tests compile and pass**

```bash
go test ./internal/dispatcher/... -v
```

Expected: all tests pass.

---

### Task 4: Update cmd/server/main.go

**Files:**
- Modify: `cmd/server/main.go`

- [ ] **Step 1: Remove `msgStore` from `dispatcher.New` call**

In `cmd/server/main.go` around line 91, change:

```go
disp := dispatcher.New(mgr, taskStore, msgStore, sessionStore, dispatchCh)
```

to:

```go
disp := dispatcher.New(mgr, taskStore, sessionStore, dispatchCh)
```

The `msgStore` local variable itself stays — it is still used by `bee.NewFeeder` (line 80) and `msgingest.New` (line 90).

- [ ] **Step 2: Verify the whole project compiles**

```bash
go build ./...
```

Expected: no errors.

- [ ] **Step 3: Commit**

```bash
git add internal/dispatcher/dispatcher.go internal/dispatcher/dispatcher_test.go cmd/server/main.go
git commit -m "feat(dispatcher): remove MessageStore interface and SetMessageExecution wiring"
```

---

## Chunk 2: MessageStore Dead Code + Test Cleanup

### Task 5: Delete dead MessageStore methods

**Files:**
- Modify: `internal/store/message_store.go`

- [ ] **Step 1: Delete `GetSession`**

Delete the entire `GetSession` method (lines 127–154):

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

- [ ] **Step 2: Delete `SetExecution`**

Delete the `SetExecution` method (lines 156–162):

```go
// SetExecution records execution metadata on the given message row.
func (s *MessageStore) SetExecution(ctx context.Context, msgID, executionID, sessionID string) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE platform_messages SET execution_id = ?, session_id = ?, updated_at = ? WHERE id = ?`,
		executionID, sessionID, time.Now().UnixMilli(), msgID)
	return err
}
```

- [ ] **Step 3: Delete `SetMessageExecution`**

Delete the `SetMessageExecution` method (lines 164–174):

```go
// SetMessageExecution writes execution_id and session_id back to a platform_messages row,
// but only when status = 'bee_processed'. This is a no-op if the Feeder rolled the row back.
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

- [ ] **Step 4: Remove unused `platform` import**

Now that `GetSession` is gone, the `"github.com/robobee/core/internal/platform"` import in `message_store.go` is unused. Delete that import line.

- [ ] **Step 5: Verify store package compiles (production files only)**

```bash
go build ./internal/store/...
```

Expected: may still see errors from `message_store_test.go` — that is fine and will be fixed in Task 7.

---

### Task 6: Delete platform.Session struct

**Files:**
- Modify: `internal/platform/interfaces.go`

- [ ] **Step 1: Delete the `Session` struct**

In `internal/platform/interfaces.go`, delete:

```go
// Session holds the persistent state for one conversation.
type Session struct {
	Key             string
	Platform        string
	SessionID       string
	LastExecutionID string
}
```

- [ ] **Step 2: Verify interfaces.go compiles**

```bash
go build ./internal/platform/...
```

Expected: no errors.

---

### Task 7: Delete dead tests from message_store_test.go

**Files:**
- Modify: `internal/store/message_store_test.go`

Delete the following six complete test functions. Each spans from its `func Test...` line to the closing `}` line.

- [ ] **Step 1: Delete `TestMessageStore_GetSession_NoRows` (lines ~191–202)**

```go
func TestMessageStore_GetSession_NoRows(t *testing.T) {
	s := setupMessageStore(t)
	ctx := context.Background()

	sess, err := s.GetSession(ctx, "feishu:chat1:userA")
	if err != nil {
		t.Fatalf("GetSession: %v", err)
	}
	if sess != nil {
		t.Errorf("expected nil session for unknown key, got %+v", sess)
	}
}
```

- [ ] **Step 2: Delete `TestMessageStore_GetSession_AfterExecution` (lines ~204–233)**

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

- [ ] **Step 3: Delete `TestMessageStore_SetExecution` (lines ~235–254)**

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

- [ ] **Step 4: Delete `TestMessageStore_GetSession_AfterClear` (lines ~256–275)**

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

- [ ] **Step 5: Delete `TestMessageStore_GetSession_FirstMessageNoExecution` (lines ~277–291)**

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

- [ ] **Step 6: Delete `TestMessageStore_SetMessageExecution_OnlyWhenBeeProcessed` (lines ~429–462)**

```go
func TestMessageStore_SetMessageExecution_OnlyWhenBeeProcessed(t *testing.T) {
	db, err := InitDB(t.TempDir() + "/test.db")
	if err != nil {
		t.Fatalf("InitDB: %v", err)
	}
	defer db.Close()

	ms := NewMessageStore(db)
	ctx := context.Background()

	db.Exec(`INSERT INTO platform_messages
        (id, session_key, platform, content, raw, platform_msg_id, status, received_at, created_at, updated_at)
        VALUES ('m1', 'sk', 'feishu', 'hi', '', '', 'bee_processed', 1, 1, 1)`)

	err = ms.SetMessageExecution(ctx, "m1", "exec-1", "sess-1")
	if err != nil {
		t.Fatalf("SetMessageExecution: %v", err)
	}

	var execID string
	db.QueryRow(`SELECT execution_id FROM platform_messages WHERE id='m1'`).Scan(&execID)
	if execID != "exec-1" {
		t.Errorf("want exec-1, got %q", execID)
	}

	// Reset to 'received': subsequent call should be a no-op (conditional update)
	db.Exec(`UPDATE platform_messages SET status='received' WHERE id='m1'`)
	_ = ms.SetMessageExecution(ctx, "m1", "exec-2", "sess-2")

	db.QueryRow(`SELECT execution_id FROM platform_messages WHERE id='m1'`).Scan(&execID)
	if execID != "exec-1" {
		t.Errorf("conditional update should be no-op for non-bee_processed row; got %q", execID)
	}
}
```

- [ ] **Step 7: Run all store tests**

```bash
go test ./internal/store/... -v
```

Expected: all remaining tests pass, no compile errors.

- [ ] **Step 8: Run full test suite**

```bash
go test ./internal/store/... ./internal/dispatcher/... ./internal/bee/...
```

Expected: all tests pass.

- [ ] **Step 9: Commit**

```bash
git add internal/store/message_store.go internal/platform/interfaces.go internal/store/message_store_test.go
git commit -m "feat(store): remove GetSession, SetExecution, SetMessageExecution and platform.Session"
```
