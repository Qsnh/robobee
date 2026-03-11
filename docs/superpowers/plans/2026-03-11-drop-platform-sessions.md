# Drop platform_sessions Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Remove all separate session tables and make `platform_messages` the single source of truth for session state by adding `execution_id`/`session_id` columns and three new store methods.

**Architecture:** Schema migration adds two columns to `platform_messages` and drops the four legacy session tables. `store.MessageStore` gains `GetSession`, `SetExecution`, and `InsertClearSentinel`. `platform.SessionStore` is deleted; `Pipeline` uses `MessageStore` directly. `Executor` gains a `primaryMsgID` parameter so `HandleRouted` knows which message row to update.

**Tech Stack:** Go, SQLite (`github.com/mattn/go-sqlite3`), `database/sql`, `github.com/google/uuid`

**Spec:** `docs/superpowers/specs/2026-03-11-drop-platform-sessions-design.md`

---

## Chunk 1: Schema migration and new MessageStore methods

### File map

- Modify: `internal/store/db.go` — schema DDL (new columns, drop tables, new index)
- Modify: `internal/store/message_store.go` — add `GetSession`, `SetExecution`, `InsertClearSentinel`
- Modify: `internal/store/message_store_test.go` — tests for the three new methods

---

### Task 1: Write failing tests for the three new MessageStore methods

**Files:**
- Modify: `internal/store/message_store_test.go`

- [ ] **Step 1: Add the three test functions to `message_store_test.go`**

Append after the existing tests:

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

func TestMessageStore_GetSession_AfterExecution(t *testing.T) {
	s := setupMessageStore(t)
	ctx := context.Background()

	s.Create(ctx, "msg-1", "feishu:chat1:userA", "feishu", "hello")
	s.SetWorkerID(ctx, "msg-1", "worker-abc")
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
	if sess.WorkerID != "worker-abc" {
		t.Errorf("WorkerID: got %q, want %q", sess.WorkerID, "worker-abc")
	}
	if sess.Platform != "feishu" {
		t.Errorf("Platform: got %q, want %q", sess.Platform, "feishu")
	}
	if sess.Key != "feishu:chat1:userA" {
		t.Errorf("Key: got %q, want %q", sess.Key, "feishu:chat1:userA")
	}
}

func TestMessageStore_SetExecution(t *testing.T) {
	s := setupMessageStore(t)
	ctx := context.Background()

	s.Create(ctx, "msg-1", "feishu:chat1:userA", "feishu", "hello")
	if err := s.SetExecution(ctx, "msg-1", "exec-42", "sess-42"); err != nil {
		t.Fatalf("SetExecution: %v", err)
	}
	// Verify via GetSession (the only way to read back execution metadata)
	// Need worker_id set so GetSession returns a result
	s.SetWorkerID(ctx, "msg-1", "worker-abc")
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

func TestMessageStore_GetSession_AfterClear(t *testing.T) {
	s := setupMessageStore(t)
	ctx := context.Background()

	s.Create(ctx, "msg-1", "feishu:chat1:userA", "feishu", "hello")
	s.SetWorkerID(ctx, "msg-1", "worker-abc")
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

func TestMessageStore_GetSession_FirstMessageNoExecution(t *testing.T) {
	s := setupMessageStore(t)
	ctx := context.Background()

	// message exists but SetExecution hasn't been called yet
	s.Create(ctx, "msg-1", "feishu:chat1:userA", "feishu", "hello")
	s.SetWorkerID(ctx, "msg-1", "worker-abc")

	sess, err := s.GetSession(ctx, "feishu:chat1:userA")
	if err != nil {
		t.Fatalf("GetSession: %v", err)
	}
	if sess != nil {
		t.Errorf("expected nil when execution_id is empty, got %+v", sess)
	}
}

func TestMessageStore_InsertClearSentinel_NotRecoverable(t *testing.T) {
	s := setupMessageStore(t)
	ctx := context.Background()

	if err := s.InsertClearSentinel(ctx, "clear-1", "feishu:chat1:userA", "feishu"); err != nil {
		t.Fatalf("InsertClearSentinel: %v", err)
	}

	// Clear sentinel must never appear in GetUnfinished (worker_id='')
	pending, err := s.GetUnfinished(ctx)
	if err != nil {
		t.Fatalf("GetUnfinished: %v", err)
	}
	for _, m := range pending {
		if m.ID == "clear-1" {
			t.Error("clear sentinel should not appear in GetUnfinished")
		}
	}
}
```

- [ ] **Step 2: Run the new tests to confirm they fail**

```bash
cd /Users/tengteng/work/robobee/core && go test ./internal/store/... -run "TestMessageStore_GetSession|TestMessageStore_InsertClear|TestMessageStore_SetExecution" -v
```

Expected: compilation errors (methods don't exist yet).

---

### Task 2: Apply schema migration

**Files:**
- Modify: `internal/store/db.go`

- [ ] **Step 1: Update the `migrate` function in `db.go`**

Replace the current `migrate` function with:

```go
func migrate(db *sql.DB) error {
	schema := `
	DROP TABLE IF EXISTS platform_sessions;
	DROP TABLE IF EXISTS feishu_sessions;
	DROP TABLE IF EXISTS dingtalk_sessions;
	DROP TABLE IF EXISTS mail_sessions;

	CREATE TABLE IF NOT EXISTS workers (
		id TEXT PRIMARY KEY,
		name TEXT NOT NULL,
		description TEXT NOT NULL DEFAULT '',
		prompt TEXT NOT NULL DEFAULT '',
		work_dir TEXT NOT NULL,
		cron_expression TEXT NOT NULL DEFAULT '',
		schedule_enabled INTEGER NOT NULL DEFAULT 0,
		schedule_description TEXT NOT NULL DEFAULT '',
		status TEXT NOT NULL DEFAULT 'idle',
		created_at DATETIME NOT NULL DEFAULT (datetime('now')),
		updated_at DATETIME NOT NULL DEFAULT (datetime('now'))
	);

	CREATE TABLE IF NOT EXISTS worker_executions (
		id TEXT PRIMARY KEY,
		worker_id TEXT NOT NULL,
		session_id TEXT NOT NULL,
		trigger_input TEXT NOT NULL DEFAULT '',
		status TEXT NOT NULL DEFAULT 'pending',
		result TEXT NOT NULL DEFAULT '',
		logs TEXT NOT NULL DEFAULT '',
		ai_process_pid INTEGER NOT NULL DEFAULT 0,
		started_at DATETIME,
		completed_at DATETIME,
		FOREIGN KEY (worker_id) REFERENCES workers(id) ON DELETE CASCADE
	);

	CREATE TABLE IF NOT EXISTS platform_messages (
		id           TEXT PRIMARY KEY,
		session_key  TEXT NOT NULL,
		platform     TEXT NOT NULL,
		worker_id    TEXT NOT NULL DEFAULT '',
		content      TEXT NOT NULL,
		status       TEXT NOT NULL DEFAULT 'received',
		merged_into  TEXT NOT NULL DEFAULT '',
		execution_id TEXT NOT NULL DEFAULT '',
		session_id   TEXT NOT NULL DEFAULT '',
		received_at  DATETIME NOT NULL DEFAULT (datetime('now')),
		processed_at DATETIME
	);

	CREATE INDEX IF NOT EXISTS idx_worker_executions_worker_id ON worker_executions(worker_id);
	CREATE INDEX IF NOT EXISTS idx_worker_executions_session_id ON worker_executions(session_id);
	CREATE INDEX IF NOT EXISTS idx_workers_schedule ON workers(schedule_enabled) WHERE schedule_enabled = 1;
	DROP INDEX IF EXISTS idx_platform_messages_session;
	CREATE INDEX IF NOT EXISTS idx_platform_messages_session
		ON platform_messages(session_key, received_at DESC);
	CREATE INDEX IF NOT EXISTS idx_platform_messages_worker_status
		ON platform_messages(session_key, worker_id, status);
	`
	if _, err := db.Exec(schema); err != nil {
		return err
	}

	// Idempotent column additions for existing databases.
	for _, stmt := range []string{
		"ALTER TABLE platform_messages ADD COLUMN execution_id TEXT NOT NULL DEFAULT ''",
		"ALTER TABLE platform_messages ADD COLUMN session_id  TEXT NOT NULL DEFAULT ''",
	} {
		db.Exec(stmt) // ignore "duplicate column name" on re-runs
	}

	return nil
}
```

- [ ] **Step 2: Verify the schema compiles and tests still build**

```bash
cd /Users/tengteng/work/robobee/core && go build ./internal/store/...
```

Expected: no errors.

---

### Task 3: Implement GetSession, SetExecution, InsertClearSentinel

**Files:**
- Modify: `internal/store/message_store.go`

- [ ] **Step 1: Add the import for `platform` package at the top of `message_store.go`**

The file currently imports `model`. Add `platform` to the imports:

```go
import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/robobee/core/internal/model"
	"github.com/robobee/core/internal/platform"
)
```

- [ ] **Step 2: Append the three methods to `message_store.go`**

```go
// GetSession returns the session state derived from the latest message row for
// the given sessionKey. Returns nil if no session exists, the latest row is a
// 'clear' sentinel, or no execution has been written yet (execution_id is empty).
func (s *MessageStore) GetSession(ctx context.Context, sessionKey string) (*platform.Session, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT status, worker_id, execution_id, session_id, platform
		FROM platform_messages
		WHERE session_key = ?
		ORDER BY received_at DESC, rowid DESC
		LIMIT 1`, sessionKey)

	var status, workerID, executionID, sessionID, plt string
	if err := row.Scan(&status, &workerID, &executionID, &sessionID, &plt); err == sql.ErrNoRows {
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
		WorkerID:        workerID,
		SessionID:       sessionID,
		LastExecutionID: executionID,
	}, nil
}

// SetExecution records execution metadata on the given message row.
func (s *MessageStore) SetExecution(ctx context.Context, msgID, executionID, sessionID string) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE platform_messages SET execution_id = ?, session_id = ? WHERE id = ?`,
		executionID, sessionID, msgID)
	return err
}

// InsertClearSentinel inserts a 'clear' sentinel row to mark the session as reset.
func (s *MessageStore) InsertClearSentinel(ctx context.Context, id, sessionKey, plt string) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO platform_messages (id, session_key, platform, content, status)
		 VALUES (?, ?, ?, '', 'clear')`,
		id, sessionKey, plt)
	return err
}
```

- [ ] **Step 3: Run the new tests and confirm they pass**

```bash
cd /Users/tengteng/work/robobee/core && go test ./internal/store/... -run "TestMessageStore_GetSession|TestMessageStore_InsertClear|TestMessageStore_SetExecution" -v
```

Expected: all 6 new tests PASS.

- [ ] **Step 4: Run the full store test suite**

```bash
cd /Users/tengteng/work/robobee/core && go test ./internal/store/... -v
```

Expected: all tests PASS.

- [ ] **Step 5: Commit**

```bash
cd /Users/tengteng/work/robobee/core && git add internal/store/db.go internal/store/message_store.go internal/store/message_store_test.go
git commit -m "feat: add execution_id/session_id columns and GetSession/SetExecution/InsertClearSentinel to MessageStore"
```

---

## Chunk 2: Interface changes and Pipeline refactor

### File map

- Modify: `internal/platform/interfaces.go` — remove `SessionStore`, add 3 methods to `MessageStore`, update `Executor`, keep `Session` struct
- Modify: `internal/platform/pipeline.go` — replace `sessions` field with `msgStore`, simplify `Handle`, update `HandleRouted`
- Modify: `internal/platform/pipeline_test.go` — replace `stubSessionStore` with `stubPipelineStore`, update all call sites

---

### Task 4: Update platform interfaces

**Files:**
- Modify: `internal/platform/interfaces.go`

- [ ] **Step 1: Remove `SessionStore` and update `MessageStore` and `Executor` in `interfaces.go`**

The file currently defines `SessionStore` (lines 51–56) and `MessageStore` (lines 58–68) and `Executor` in `session_queue.go`. Make the following changes to `interfaces.go`:

1. Delete the entire `SessionStore` interface block (lines 51–56).
2. Add the three new methods to the `MessageStore` interface after `GetUnfinished`:

```go
// GetSession returns the session state derived from the latest message row.
// Returns nil if no prior session exists or it was cleared.
GetSession(ctx context.Context, sessionKey string) (*Session, error)

// SetExecution records execution metadata on the given message row.
SetExecution(ctx context.Context, msgID, executionID, sessionID string) error

// InsertClearSentinel inserts a 'clear' sentinel row for the given session.
InsertClearSentinel(ctx context.Context, id, sessionKey, platform string) error
```

3. Keep the `Session` struct as-is (it's now returned by `GetSession`).

The updated `interfaces.go` should look like:

```go
package platform

import (
	"context"

	"github.com/robobee/core/internal/model"
)

// InboundMessage carries a parsed message from any platform.
type InboundMessage struct {
	Platform   string // "feishu" | "dingtalk" | "mail"
	SenderID   string
	SessionKey string // platform-prefixed session key, e.g. "feishu:chatID:userID"
	Content    string
	Raw        any // original platform event, used by the sender for reply metadata
}

// OutboundMessage carries a reply to send back on a platform.
type OutboundMessage struct {
	SessionKey string
	Content    string
	ReplyTo    InboundMessage
}

// PlatformReceiverAdapter receives inbound messages and dispatches them.
type PlatformReceiverAdapter interface {
	Start(ctx context.Context, dispatch func(InboundMessage)) error
}

// PlatformSenderAdapter sends outbound messages on a platform.
type PlatformSenderAdapter interface {
	Send(ctx context.Context, msg OutboundMessage) error
}

// Platform bundles a receiver and sender for a single messaging platform.
type Platform interface {
	ID() string
	Receiver() PlatformReceiverAdapter
	Sender() PlatformSenderAdapter
}

// Session holds the persistent state for one conversation.
type Session struct {
	Key             string
	Platform        string
	WorkerID        string
	SessionID       string
	LastExecutionID string
}

// MessageStore is the subset of store.MessageStore operations used by the queue and pipeline.
// The concrete implementation is *store.MessageStore.
type MessageStore interface {
	Create(ctx context.Context, id, sessionKey, platform, content string) error
	SetWorkerID(ctx context.Context, id, workerID string) error
	SetStatus(ctx context.Context, id, status string) error
	UpdateStatusBatch(ctx context.Context, ids []string, status string) error
	MarkMerged(ctx context.Context, primaryID string, mergedIDs []string) error
	MarkTerminal(ctx context.Context, ids []string, status string) error
	GetUnfinished(ctx context.Context) ([]model.PendingMessage, error)
	GetSession(ctx context.Context, sessionKey string) (*Session, error)
	SetExecution(ctx context.Context, msgID, executionID, sessionID string) error
	InsertClearSentinel(ctx context.Context, id, sessionKey, platform string) error
}
```

- [ ] **Step 2: Update `Executor` type in `session_queue.go` to add `primaryMsgID`**

In `internal/platform/session_queue.go`, change line 12:

```go
// Before
type Executor func(sessionKey, workerID, content string, replyTo InboundMessage)

// After
type Executor func(sessionKey, workerID, content string, replyTo InboundMessage, primaryMsgID string)
```

- [ ] **Step 3: Verify the package compiles (will fail until Pipeline is updated — that's expected)**

```bash
cd /Users/tengteng/work/robobee/core && go build ./internal/platform/... 2>&1 | head -20
```

Expected: compilation errors referencing `SessionStore` and `HandleRouted` — these are fixed in the next task.

---

### Task 5: Refactor Pipeline

**Files:**
- Modify: `internal/platform/pipeline.go`

- [ ] **Step 1: Replace `pipeline.go` with the updated version**

```go
package platform

import (
	"context"
	"log"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/robobee/core/internal/model"
)

const (
	pollInterval = 2 * time.Second
	pollTimeout  = 30 * time.Minute
	ackMessage   = "⏳ 正在处理，请稍候…"
	errorMessage = "❌ 处理失败，请稍后重试"
	noWorkerMsg  = "❌ 没有找到合适的 Worker，请换个描述试试"
	clearMessage = "✅ 上下文已重置"
	timeoutMsg   = "⏰ 任务超时，请稍后通过 Web 界面查看结果"
)

// AckMessage is the immediate acknowledgement sent before async processing.
const AckMessage = ackMessage

// MessageRouter routes a message to the best worker ID.
type MessageRouter interface {
	Route(ctx context.Context, message string) (string, error)
}

// ExecutionManager manages worker executions.
type ExecutionManager interface {
	ExecuteWorker(ctx context.Context, workerID, input string) (model.WorkerExecution, error)
	ReplyExecution(ctx context.Context, execID, input string) (model.WorkerExecution, error)
	GetExecution(execID string) (model.WorkerExecution, error)
}

// Pipeline processes inbound messages through routing, execution, and result polling.
type Pipeline struct {
	router   MessageRouter
	msgStore MessageStore
	manager  ExecutionManager
}

// NewPipeline constructs a Pipeline.
func NewPipeline(router MessageRouter, msgStore MessageStore, manager ExecutionManager) *Pipeline {
	return &Pipeline{router: router, msgStore: msgStore, manager: manager}
}

// IsClearCommand reports whether content is a "clear" command.
func IsClearCommand(content string) bool {
	return strings.EqualFold(strings.TrimSpace(content), "clear")
}

// Handle processes a clear command and returns the reply text.
// Only clear commands are routed here; all other messages go through HandleRouted.
func (p *Pipeline) Handle(ctx context.Context, msg InboundMessage) string {
	if err := p.msgStore.InsertClearSentinel(ctx, uuid.New().String(), msg.SessionKey, msg.Platform); err != nil {
		log.Printf("platform: clear session error: %v", err)
		return errorMessage
	}
	return clearMessage
}

// Route resolves the best worker ID for the given message content.
func (p *Pipeline) Route(ctx context.Context, content string) (string, error) {
	return p.router.Route(ctx, content)
}

// HandleRouted processes an already-routed message.
// workerID must be the result of a prior Route call for this content.
// msgID is the platform_messages row ID for this message (used to record execution metadata).
func (p *Pipeline) HandleRouted(ctx context.Context, msg InboundMessage, workerID, msgID string) string {
	sess, err := p.msgStore.GetSession(ctx, msg.SessionKey)
	if err != nil {
		log.Printf("platform: get session error: %v", err)
		return errorMessage
	}

	var exec model.WorkerExecution
	if sess != nil && sess.LastExecutionID != "" {
		log.Printf("platform: replying to execution execID=%s", sess.LastExecutionID)
		exec, err = p.manager.ReplyExecution(ctx, sess.LastExecutionID, msg.Content)
	} else {
		log.Printf("platform: executing worker workerID=%s", workerID)
		exec, err = p.manager.ExecuteWorker(ctx, workerID, msg.Content)
	}
	if err != nil {
		log.Printf("platform: execute error: %v", err)
		return errorMessage
	}
	log.Printf("platform: execution started execID=%s sessionID=%s", exec.ID, exec.SessionID)

	if err := p.msgStore.SetExecution(ctx, msgID, exec.ID, exec.SessionID); err != nil {
		log.Printf("platform: set execution error: %v", err)
	}

	return p.waitForResult(exec.ID)
}

func (p *Pipeline) waitForResult(executionID string) string {
	deadline := time.Now().Add(pollTimeout)
	lastStatus := ""
	for time.Now().Before(deadline) {
		exec, err := p.manager.GetExecution(executionID)
		if err != nil {
			log.Printf("platform: poll execution error: %v", err)
			return errorMessage
		}
		if string(exec.Status) != lastStatus {
			log.Printf("platform: polling execID=%s status=%s", executionID, exec.Status)
			lastStatus = string(exec.Status)
		}
		switch exec.Status {
		case model.ExecStatusCompleted:
			if exec.Result != "" {
				return exec.Result
			}
			return "✅ 任务已完成"
		case model.ExecStatusFailed:
			return "❌ 任务执行失败: " + exec.Result
		}
		time.Sleep(pollInterval)
	}
	return timeoutMsg
}
```

- [ ] **Step 2: Verify pipeline compiles**

```bash
cd /Users/tengteng/work/robobee/core && go build ./internal/platform/... 2>&1 | head -20
```

Expected: errors in `pipeline_test.go` only (test file references old API). All non-test files should compile cleanly.

---

### Task 6: Update pipeline tests

**Files:**
- Modify: `internal/platform/pipeline_test.go`

- [ ] **Step 1: Replace `pipeline_test.go` with the updated version**

The key changes:
- Replace `stubSessionStore` with a `stubPipelineStore` that implements the full `MessageStore` interface including the three new methods.
- Update `newPipeline` helper — takes `MessageStore` instead of `SessionStore`.
- All `HandleRouted` calls gain a `msgID` argument.
- Tests that check session state now operate via `stubPipelineStore.sessions`.

```go
package platform

import (
	"context"
	"errors"
	"testing"

	"github.com/robobee/core/internal/model"
)

// --- Stubs ---

type stubRouter struct {
	workerID string
	err      error
}

func (r *stubRouter) Route(_ context.Context, _ string) (string, error) {
	return r.workerID, r.err
}

// stubPipelineStore implements platform.MessageStore for pipeline tests.
type stubPipelineStore struct {
	sessions       map[string]*Session
	clearSentinels []string
	executions     map[string][2]string // msgID -> [executionID, sessionID]
	clearErr       error
}

func newStubPipelineStore() *stubPipelineStore {
	return &stubPipelineStore{
		sessions:   make(map[string]*Session),
		executions: make(map[string][2]string),
	}
}

func (s *stubPipelineStore) Create(_ context.Context, _, _, _, _ string) error  { return nil }
func (s *stubPipelineStore) SetWorkerID(_ context.Context, _, _ string) error   { return nil }
func (s *stubPipelineStore) SetStatus(_ context.Context, _, _ string) error     { return nil }
func (s *stubPipelineStore) UpdateStatusBatch(_ context.Context, _ []string, _ string) error {
	return nil
}
func (s *stubPipelineStore) MarkMerged(_ context.Context, _ string, _ []string) error { return nil }
func (s *stubPipelineStore) MarkTerminal(_ context.Context, _ []string, _ string) error {
	return nil
}
func (s *stubPipelineStore) GetUnfinished(_ context.Context) ([]model.PendingMessage, error) {
	return nil, nil
}
func (s *stubPipelineStore) GetSession(_ context.Context, key string) (*Session, error) {
	sess, ok := s.sessions[key]
	if !ok {
		return nil, nil
	}
	return sess, nil
}
func (s *stubPipelineStore) SetExecution(_ context.Context, msgID, execID, sessID string) error {
	s.executions[msgID] = [2]string{execID, sessID}
	return nil
}
func (s *stubPipelineStore) InsertClearSentinel(_ context.Context, id, key, _ string) error {
	if s.clearErr != nil {
		return s.clearErr
	}
	s.clearSentinels = append(s.clearSentinels, id)
	delete(s.sessions, key)
	return nil
}

type stubManager struct {
	exec model.WorkerExecution
	err  error
}

func (m *stubManager) ExecuteWorker(_ context.Context, _, _ string) (model.WorkerExecution, error) {
	return m.exec, m.err
}
func (m *stubManager) ReplyExecution(_ context.Context, _, _ string) (model.WorkerExecution, error) {
	return m.exec, m.err
}
func (m *stubManager) GetExecution(_ string) (model.WorkerExecution, error) {
	return m.exec, m.err
}

// --- Helpers ---

func newPipeline(router MessageRouter, store MessageStore, mgr ExecutionManager) *Pipeline {
	return NewPipeline(router, store, mgr)
}

func msg(content string) InboundMessage {
	return InboundMessage{
		Platform:   "test",
		SessionKey: "test:session1",
		Content:    content,
	}
}

// --- Tests ---

func TestPipeline_ClearCommand(t *testing.T) {
	store := newStubPipelineStore()
	p := newPipeline(&stubRouter{workerID: "w1"}, store, &stubManager{})
	result := p.Handle(context.Background(), msg("clear"))

	if result != clearMessage {
		t.Errorf("want %q, got %q", clearMessage, result)
	}
	if len(store.clearSentinels) == 0 {
		t.Error("InsertClearSentinel should have been called")
	}
}

func TestPipeline_ClearCommand_SentinelError(t *testing.T) {
	store := newStubPipelineStore()
	store.clearErr = errors.New("db error")
	p := newPipeline(&stubRouter{workerID: "w1"}, store, &stubManager{})
	result := p.Handle(context.Background(), msg("clear"))

	if result != errorMessage {
		t.Errorf("want %q, got %q", errorMessage, result)
	}
}

func TestPipeline_HandleRouted_NewSession(t *testing.T) {
	mgr := &stubManager{exec: model.WorkerExecution{
		ID:        "exec-1",
		SessionID: "sess-1",
		Status:    model.ExecStatusCompleted,
		Result:    "deployed",
	}}
	store := newStubPipelineStore()
	p := newPipeline(&stubRouter{workerID: "w1"}, store, mgr)
	result := p.HandleRouted(context.Background(), msg("deploy"), "w-deploy", "msg-1")

	if result != "deployed" {
		t.Errorf("want deployed, got %s", result)
	}
	if got, ok := store.executions["msg-1"]; !ok || got[0] != "exec-1" {
		t.Errorf("SetExecution not called correctly: %v", store.executions)
	}
}

func TestPipeline_HandleRouted_ExistingSession(t *testing.T) {
	store := newStubPipelineStore()
	store.sessions["test:session1"] = &Session{
		Key:             "test:session1",
		Platform:        "test",
		WorkerID:        "w1",
		LastExecutionID: "prev-exec",
	}
	mgr := &stubManager{exec: model.WorkerExecution{
		ID:        "exec-2",
		SessionID: "sess-2",
		Status:    model.ExecStatusCompleted,
		Result:    "continued",
	}}
	p := newPipeline(&stubRouter{workerID: "w1"}, store, mgr)
	result := p.HandleRouted(context.Background(), msg("continue"), "w1", "msg-2")

	if result != "continued" {
		t.Errorf("want continued, got %s", result)
	}
}

func TestPipeline_Route_ReturnsWorkerID(t *testing.T) {
	p := newPipeline(&stubRouter{workerID: "w-deploy"}, newStubPipelineStore(), &stubManager{})
	id, err := p.Route(context.Background(), "deploy the app")
	if err != nil {
		t.Fatalf("Route: %v", err)
	}
	if id != "w-deploy" {
		t.Errorf("want w-deploy, got %s", id)
	}
}

func TestPipeline_Route_Error(t *testing.T) {
	p := newPipeline(&stubRouter{err: errors.New("none")}, newStubPipelineStore(), &stubManager{})
	_, err := p.Route(context.Background(), "anything")
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestPipeline_HandleRouted_FailedExecution(t *testing.T) {
	mgr := &stubManager{exec: model.WorkerExecution{
		Status: model.ExecStatusFailed,
		Result: "build error",
	}}
	p := newPipeline(&stubRouter{workerID: "w1"}, newStubPipelineStore(), mgr)
	result := p.HandleRouted(context.Background(), msg("build"), "w1", "msg-1")

	want := "❌ 任务执行失败: build error"
	if result != want {
		t.Errorf("want %q, got %q", want, result)
	}
}

func TestPipeline_HandleRouted_CompletedWithEmptyResult(t *testing.T) {
	mgr := &stubManager{exec: model.WorkerExecution{
		Status: model.ExecStatusCompleted,
		Result: "",
	}}
	p := newPipeline(&stubRouter{workerID: "w1"}, newStubPipelineStore(), mgr)
	result := p.HandleRouted(context.Background(), msg("run task"), "w1", "msg-1")

	if result != "✅ 任务已完成" {
		t.Errorf("want '✅ 任务已完成', got %q", result)
	}
}

func TestPipeline_HandleRouted_ExecuteWorkerError(t *testing.T) {
	mgr := &stubManager{err: errors.New("worker unavailable")}
	p := newPipeline(&stubRouter{workerID: "w1"}, newStubPipelineStore(), mgr)
	result := p.HandleRouted(context.Background(), msg("run"), "w1", "msg-1")

	if result != errorMessage {
		t.Errorf("want %q, got %q", errorMessage, result)
	}
}

func TestPipeline_GroupChat_TwoUsersGetIndependentSessions(t *testing.T) {
	store := newStubPipelineStore()
	store.sessions["feishu:chat123:userA"] = &Session{
		Key: "feishu:chat123:userA", LastExecutionID: "exec-a",
	}
	mgr := &stubManager{exec: model.WorkerExecution{
		ID: "exec-new", Status: model.ExecStatusCompleted, Result: "done",
	}}
	p := newPipeline(&stubRouter{workerID: "w1"}, store, mgr)

	msgUserA := InboundMessage{Platform: "feishu", SenderID: "userA", SessionKey: "feishu:chat123:userA", Content: "continue"}
	msgUserB := InboundMessage{Platform: "feishu", SenderID: "userB", SessionKey: "feishu:chat123:userB", Content: "start new"}

	p.HandleRouted(context.Background(), msgUserA, "w1", "msg-a")
	p.HandleRouted(context.Background(), msgUserB, "w1", "msg-b")

	if store.executions["msg-a"][0] != "exec-new" {
		t.Errorf("userA execution not recorded")
	}
	if store.executions["msg-b"][0] != "exec-new" {
		t.Errorf("userB execution not recorded")
	}
}
```

- [ ] **Step 2: Run pipeline tests**

```bash
cd /Users/tengteng/work/robobee/core && go test ./internal/platform/... -run TestPipeline -v
```

Expected: all pipeline tests PASS.

- [ ] **Step 3: Commit**

```bash
cd /Users/tengteng/work/robobee/core && git add internal/platform/interfaces.go internal/platform/pipeline.go internal/platform/pipeline_test.go internal/platform/session_queue.go
git commit -m "feat: remove SessionStore, migrate Pipeline to use MessageStore for session state"
```

---

## Chunk 3: Queue layer, wiring, and cleanup

### File map

- Modify: `internal/platform/session_queue.go` — pass `ids[0]` to executor in `runExecutor`
- Modify: `internal/platform/session_queue_test.go` — update `Executor` stubs to new signature
- Modify: `internal/platform/queue_manager.go` — update executor wrapper signature
- Modify: `internal/platform/queue_manager_test.go` — update executor stubs
- Modify: `internal/platform/manager.go` — update executor closure signature, pass `primaryMsgID` to `HandleRouted`
- Modify: `cmd/server/main.go` — remove `sessionStore`, wire `msgStore` into `NewPipeline`
- Delete: `internal/store/platform_session_store.go`
- Delete: `internal/store/feishu_session_store.go`
- Delete: `internal/store/dingtalk_session_store.go`
- Delete: `internal/store/mail_session_store.go`

---

### Task 7: Update sessionQueue to pass primaryMsgID to executor

**Files:**
- Modify: `internal/platform/session_queue.go`

- [ ] **Step 1: Update `runExecutor` to pass `ids[0]` as `primaryMsgID`**

In `session_queue.go`, change `runExecutor` (currently line 100–103):

```go
// Before
func (q *sessionQueue) runExecutor(ids []string, content string, replyTo InboundMessage) {
	q.executor(q.sessionKey, q.workerID, content, replyTo)
	q.onDone(ids)
}

// After
func (q *sessionQueue) runExecutor(ids []string, content string, replyTo InboundMessage) {
	q.executor(q.sessionKey, q.workerID, content, replyTo, ids[0])
	q.onDone(ids)
}
```

- [ ] **Step 2: Update session_queue_test.go to use new Executor signature**

All executor stubs in `session_queue_test.go` have the old 4-arg signature. Update each to accept the 5th `primaryMsgID string` argument. Search for `func(_, _, content string, _ InboundMessage)` and add `_ string`:

```go
// Pattern — all occurrences:
// Before: func(_, _, content string, _ InboundMessage) { ... }
// After:  func(_, _, content string, _ InboundMessage, _ string) { ... }
```

Also add a test asserting `primaryMsgID` is `ids[0]`:

```go
func TestSessionQueue_ExecutorReceivesPrimaryMsgID(t *testing.T) {
	var capturedPrimaryID string
	executor := func(_, _, _ string, _ InboundMessage, primaryMsgID string) {
		capturedPrimaryID = primaryMsgID
	}
	store := &stubMessageStore{}
	q := newSessionQueue("sk", "w1", 10*time.Millisecond, executor, store)
	q.enqueue("msg-first", "hello", InboundMessage{})
	q.enqueue("msg-second", "world", InboundMessage{})
	time.Sleep(50 * time.Millisecond)

	if capturedPrimaryID != "msg-first" {
		t.Errorf("primaryMsgID: got %q, want %q", capturedPrimaryID, "msg-first")
	}
}
```

Also add the three new methods to `stubMessageStore` in this file (the type defined in `session_queue_test.go`):

```go
func (s *stubMessageStore) GetSession(_ context.Context, _ string) (*Session, error) {
	return nil, nil
}
func (s *stubMessageStore) SetExecution(_ context.Context, _, _, _ string) error { return nil }
func (s *stubMessageStore) InsertClearSentinel(_ context.Context, _, _, _ string) error {
	return nil
}
```

- [ ] **Step 3: Run session_queue tests**

```bash
cd /Users/tengteng/work/robobee/core && go test ./internal/platform/... -run TestSessionQueue -v
```

Expected: all PASS.

---

### Task 8: Update QueueManager executor wrapper

**Files:**
- Modify: `internal/platform/queue_manager.go`
- Modify: `internal/platform/queue_manager_test.go`

- [ ] **Step 1: Update `wrappedExecutor` in `queue_manager.go` to pass through `primaryMsgID`**

In `queue_manager.go`, change `wrappedExecutor` (currently lines 56–70):

```go
// Before
func (m *QueueManager) wrappedExecutor(key string) Executor {
	return func(sessionKey, workerID, content string, replyTo InboundMessage) {
		m.executor(sessionKey, workerID, content, replyTo)
		// ...
	}
}

// After
func (m *QueueManager) wrappedExecutor(key string) Executor {
	return func(sessionKey, workerID, content string, replyTo InboundMessage, primaryMsgID string) {
		m.executor(sessionKey, workerID, content, replyTo, primaryMsgID)
		go func() {
			time.Sleep(time.Millisecond)
			m.mu.Lock()
			if sq, ok := m.queues[key]; ok && sq.isIdle() {
				delete(m.queues, key)
			}
			m.mu.Unlock()
		}()
	}
}
```

- [ ] **Step 2: Update executor stubs in `queue_manager_test.go`**

Find all `Executor` stubs in `queue_manager_test.go` and add the `primaryMsgID string` parameter:

```go
// Before: func(_, _, _ string, _ InboundMessage) { ... }
// After:  func(_, _, _ string, _ InboundMessage, _ string) { ... }
```

- [ ] **Step 3: Run queue_manager tests**

```bash
cd /Users/tengteng/work/robobee/core && go test ./internal/platform/... -run TestQueueManager -v
```

Expected: all PASS.

---

### Task 9: Update manager.go and cmd/server/main.go

**Files:**
- Modify: `internal/platform/manager.go`
- Modify: `cmd/server/main.go`

- [ ] **Step 1: Update executor closure in `manager.go`**

In `manager.go`, update the `executor` closure (currently lines 44–60):

```go
// Before
executor := func(sessionKey, workerID, content string, replyTo InboundMessage) {
	mergedMsg := replyTo
	mergedMsg.Content = content
	result := m.pipeline.HandleRouted(context.Background(), mergedMsg, workerID)
	// ...
}

// After
executor := func(sessionKey, workerID, content string, replyTo InboundMessage, primaryMsgID string) {
	mergedMsg := replyTo
	mergedMsg.Content = content
	result := m.pipeline.HandleRouted(context.Background(), mergedMsg, workerID, primaryMsgID)
	// ...
}
```

- [ ] **Step 2: Update `cmd/server/main.go` wiring**

`msgStore` is already declared on line 45 of `main.go` (it is passed to `NewManager`). Only two changes are needed:

1. Delete the line: `sessionStore := store.NewPlatformSessionStore(db)`
2. Change: `pipe := platform.NewPipeline(router, sessionStore, mgr)` → `pipe := platform.NewPipeline(router, msgStore, mgr)`

Do **not** add a second `msgStore :=` declaration. `store.NewMessageStore(db)` must be called exactly once.

- [ ] **Step 3: Build the full project**

```bash
cd /Users/tengteng/work/robobee/core && go build ./...
```

Expected: no errors.

---

### Task 10: Delete legacy store files

**Files:**
- Delete: `internal/store/platform_session_store.go`
- Delete: `internal/store/feishu_session_store.go`
- Delete: `internal/store/dingtalk_session_store.go`
- Delete: `internal/store/mail_session_store.go`

- [ ] **Step 1: Delete the four files**

```bash
rm /Users/tengteng/work/robobee/core/internal/store/platform_session_store.go
rm /Users/tengteng/work/robobee/core/internal/store/feishu_session_store.go
rm /Users/tengteng/work/robobee/core/internal/store/dingtalk_session_store.go
rm /Users/tengteng/work/robobee/core/internal/store/mail_session_store.go
```

- [ ] **Step 2: Build and test everything**

```bash
cd /Users/tengteng/work/robobee/core && go build ./... && go test ./...
```

Expected: clean build and all tests PASS.

- [ ] **Step 3: Commit everything**

```bash
cd /Users/tengteng/work/robobee/core && git add -A
git commit -m "feat: complete platform_sessions removal — messages are now sole session store"
```
