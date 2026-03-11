# Message Queue & Debounce Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Record every platform message to the DB, prevent concurrent executions per session via a queue, and debounce rapid messages by merging them before execution.

**Architecture:** Incoming messages are recorded to `platform_messages` on arrival. A per-(session_key, worker_id) `sessionQueue` (in the `platform` package) manages a rolling debounce timer; when it fires, merged content enters an execution slot. The `QueueManager` controls all queues. The existing `Pipeline.Handle` path is replaced by `Route` + `Enqueue`; the queue's executor calls a new `Pipeline.HandleRouted` method that skips re-routing.

**Tech Stack:** Go 1.25, SQLite (mattn/go-sqlite3), standard library (`sync`, `time`), existing codebase patterns.

**Spec:** `docs/superpowers/specs/2026-03-11-message-queue-debounce-design.md`

**Implementation notes vs spec:**
- `worker_id` and `merged_into` use `NOT NULL DEFAULT ''` instead of nullable `TEXT` — consistent with the rest of the schema (all other columns use NOT NULL DEFAULT); empty string is the sentinel for "not yet set". The `GetUnfinished` query uses `worker_id != ''`.
- `CREATE TABLE IF NOT EXISTS` is used (not bare `CREATE TABLE`) — consistent with existing migrations.
- The spec's `MessageStore` interface method names are refined: `CreateMessage(*PlatformMessage)` → `Create(id, sk, p, c string)` (flat params, no struct); `UpdateMessageStatus` → `SetStatus` + `UpdateStatusBatch`; `UpdateMessageWorker` → `SetWorkerID`. Behavior is identical.
- `PendingMessage` is defined in `internal/model/` (not `store` or `platform`) to avoid circular imports between those packages.

---

## Chunk 1: Data Layer

### Task 1: Add `platform_messages` table to the DB schema

**Files:**
- Modify: `internal/store/db.go`
- Modify: `internal/store/db_test.go`

- [ ] **Step 1: Write a failing test asserting the table exists**

In `internal/store/db_test.go`, add (or if file doesn't exist, create it with this test):

```go
func TestInitDB_PlatformMessagesTable(t *testing.T) {
    db, err := InitDB(t.TempDir() + "/test.db")
    if err != nil {
        t.Fatalf("InitDB: %v", err)
    }
    defer db.Close()

    _, err = db.Exec(`INSERT INTO platform_messages (id, session_key, platform, content) VALUES ('x','sk','p','c')`)
    if err != nil {
        t.Fatalf("platform_messages table not created: %v", err)
    }
}
```

- [ ] **Step 2: Run to verify it fails**

```bash
cd /Users/tengteng/work/robobee/core
go test ./internal/store/... -run TestInitDB_PlatformMessagesTable -v
```

Expected: FAIL — `no such table: platform_messages`

- [ ] **Step 3: Add the table DDL to `migrate()`**

In `internal/store/db.go`, append to the `schema` string (just before the closing backtick):

```sql
CREATE TABLE IF NOT EXISTS platform_messages (
    id           TEXT PRIMARY KEY,
    session_key  TEXT NOT NULL,
    platform     TEXT NOT NULL,
    worker_id    TEXT NOT NULL DEFAULT '',
    content      TEXT NOT NULL,
    status       TEXT NOT NULL DEFAULT 'received',
    merged_into  TEXT NOT NULL DEFAULT '',
    received_at  DATETIME NOT NULL DEFAULT (datetime('now')),
    processed_at DATETIME
);
CREATE INDEX IF NOT EXISTS idx_platform_messages_session
    ON platform_messages(session_key, worker_id, status);
```

- [ ] **Step 4: Run tests to verify they pass**

```bash
go test ./internal/store/... -v
```

Expected: all PASS

- [ ] **Step 5: Commit**

```bash
git add internal/store/db.go internal/store/db_test.go
git commit -m "feat: add platform_messages table to DB schema"
```

---

### Task 2: Add `model.PendingMessage`

**Files:**
- Create: `internal/model/pending_message.go`

`PendingMessage` lives in `model` so both `store` and `platform` can import it without cycles.

- [ ] **Step 1: Create the file**

```go
package model

// PendingMessage is a platform message recovered from the DB during startup.
// It represents an unfinished message that needs to be re-queued.
type PendingMessage struct {
	ID         string
	SessionKey string
	WorkerID   string
	Platform   string
	Content    string
}
```

- [ ] **Step 2: Verify it compiles**

```bash
go build ./internal/model/...
```

Expected: no errors

- [ ] **Step 3: Commit**

```bash
git add internal/model/pending_message.go
git commit -m "feat: add model.PendingMessage for startup recovery"
```

---

### Task 3: Implement `MessageStore`

**Files:**
- Create: `internal/store/message_store.go`
- Create: `internal/store/message_store_test.go`

- [ ] **Step 1: Write failing tests**

Create `internal/store/message_store_test.go`:

```go
package store

import (
	"context"
	"testing"
)

func setupMessageStore(t *testing.T) *MessageStore {
	t.Helper()
	db, err := InitDB(t.TempDir() + "/test.db")
	if err != nil {
		t.Fatalf("InitDB: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return NewMessageStore(db)
}

func TestMessageStore_Create(t *testing.T) {
	s := setupMessageStore(t)
	ctx := context.Background()

	if err := s.Create(ctx, "msg-1", "feishu:chat1:userA", "feishu", "hello world"); err != nil {
		t.Fatalf("Create: %v", err)
	}
}

func TestMessageStore_SetWorkerID(t *testing.T) {
	s := setupMessageStore(t)
	ctx := context.Background()

	s.Create(ctx, "msg-1", "feishu:chat1:userA", "feishu", "hello")
	if err := s.SetWorkerID(ctx, "msg-1", "worker-abc"); err != nil {
		t.Fatalf("SetWorkerID: %v", err)
	}
}

func TestMessageStore_SetStatus(t *testing.T) {
	s := setupMessageStore(t)
	ctx := context.Background()

	s.Create(ctx, "msg-1", "feishu:chat1:userA", "feishu", "hello")
	if err := s.SetStatus(ctx, "msg-1", "debouncing"); err != nil {
		t.Fatalf("SetStatus: %v", err)
	}
}

func TestMessageStore_UpdateStatusBatch(t *testing.T) {
	s := setupMessageStore(t)
	ctx := context.Background()

	s.Create(ctx, "msg-1", "feishu:chat1:userA", "feishu", "a")
	s.Create(ctx, "msg-2", "feishu:chat1:userA", "feishu", "b")

	if err := s.UpdateStatusBatch(ctx, []string{"msg-1", "msg-2"}, "debouncing"); err != nil {
		t.Fatalf("UpdateStatusBatch: %v", err)
	}
}

func TestMessageStore_MarkMerged(t *testing.T) {
	s := setupMessageStore(t)
	ctx := context.Background()

	s.Create(ctx, "msg-1", "feishu:chat1:userA", "feishu", "msg1")
	s.Create(ctx, "msg-2", "feishu:chat1:userA", "feishu", "msg2")
	s.Create(ctx, "msg-3", "feishu:chat1:userA", "feishu", "msg3")

	if err := s.MarkMerged(ctx, "msg-1", []string{"msg-2", "msg-3"}); err != nil {
		t.Fatalf("MarkMerged: %v", err)
	}
}

func TestMessageStore_MarkTerminal_Done(t *testing.T) {
	s := setupMessageStore(t)
	ctx := context.Background()

	s.Create(ctx, "msg-1", "feishu:chat1:userA", "feishu", "hello")
	if err := s.MarkTerminal(ctx, []string{"msg-1"}, "done"); err != nil {
		t.Fatalf("MarkTerminal done: %v", err)
	}
}

func TestMessageStore_MarkTerminal_Failed(t *testing.T) {
	s := setupMessageStore(t)
	ctx := context.Background()

	s.Create(ctx, "msg-1", "feishu:chat1:userA", "feishu", "hello")
	if err := s.MarkTerminal(ctx, []string{"msg-1"}, "failed"); err != nil {
		t.Fatalf("MarkTerminal failed: %v", err)
	}
}

func TestMessageStore_GetUnfinished(t *testing.T) {
	s := setupMessageStore(t)
	ctx := context.Background()

	// received — no worker_id, should be excluded
	s.Create(ctx, "msg-received", "feishu:chat1:userA", "feishu", "received")

	// routed — has worker_id, should be returned
	s.Create(ctx, "msg-routed", "feishu:chat1:userA", "feishu", "routed")
	s.SetWorkerID(ctx, "msg-routed", "worker-1")

	// done — terminal, should be excluded
	s.Create(ctx, "msg-done", "feishu:chat1:userA", "feishu", "done")
	s.SetWorkerID(ctx, "msg-done", "worker-1")
	s.MarkTerminal(ctx, []string{"msg-done"}, "done")

	pending, err := s.GetUnfinished(ctx)
	if err != nil {
		t.Fatalf("GetUnfinished: %v", err)
	}
	if len(pending) != 1 {
		t.Errorf("expected 1 pending message, got %d", len(pending))
	}
	if len(pending) > 0 && pending[0].ID != "msg-routed" {
		t.Errorf("expected msg-routed, got %s", pending[0].ID)
	}
}
```

- [ ] **Step 2: Run to verify failure**

```bash
go test ./internal/store/... -run TestMessageStore -v
```

Expected: FAIL — `MessageStore` undefined

- [ ] **Step 3: Implement `MessageStore`**

Create `internal/store/message_store.go`:

```go
package store

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/robobee/core/internal/model"
)

// MessageStore persists platform messages to the platform_messages table.
type MessageStore struct {
	db *sql.DB
}

// NewMessageStore constructs a MessageStore.
func NewMessageStore(db *sql.DB) *MessageStore {
	return &MessageStore{db: db}
}

// Create inserts a new message record with status "received".
func (s *MessageStore) Create(ctx context.Context, id, sessionKey, platform, content string) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO platform_messages (id, session_key, platform, content) VALUES (?, ?, ?, ?)`,
		id, sessionKey, platform, content,
	)
	return err
}

// SetWorkerID sets the worker_id and advances status to "routed".
func (s *MessageStore) SetWorkerID(ctx context.Context, id, workerID string) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE platform_messages SET worker_id = ?, status = 'routed' WHERE id = ?`,
		workerID, id,
	)
	return err
}

// SetStatus updates the status of a single message.
func (s *MessageStore) SetStatus(ctx context.Context, id, status string) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE platform_messages SET status = ? WHERE id = ?`,
		status, id,
	)
	return err
}

// UpdateStatusBatch sets the same status on all provided message IDs.
func (s *MessageStore) UpdateStatusBatch(ctx context.Context, ids []string, status string) error {
	if len(ids) == 0 {
		return nil
	}
	placeholders := strings.Repeat("?,", len(ids))
	placeholders = placeholders[:len(placeholders)-1]
	args := make([]any, 0, len(ids)+1)
	args = append(args, status)
	for _, id := range ids {
		args = append(args, id)
	}
	_, err := s.db.ExecContext(ctx,
		fmt.Sprintf(`UPDATE platform_messages SET status = ? WHERE id IN (%s)`, placeholders),
		args...,
	)
	return err
}

// MarkMerged sets primaryID status to "merged" and records merged_into on all mergedIDs.
func (s *MessageStore) MarkMerged(ctx context.Context, primaryID string, mergedIDs []string) error {
	if _, err := s.db.ExecContext(ctx,
		`UPDATE platform_messages SET status = 'merged' WHERE id = ?`, primaryID,
	); err != nil {
		return err
	}
	for _, id := range mergedIDs {
		if _, err := s.db.ExecContext(ctx,
			`UPDATE platform_messages SET status = 'merged', merged_into = ? WHERE id = ?`,
			primaryID, id,
		); err != nil {
			return err
		}
	}
	return nil
}

// MarkTerminal sets status to "done" or "failed" and records processed_at.
func (s *MessageStore) MarkTerminal(ctx context.Context, ids []string, status string) error {
	if len(ids) == 0 {
		return nil
	}
	placeholders := strings.Repeat("?,", len(ids))
	placeholders = placeholders[:len(placeholders)-1]
	args := make([]any, 0, len(ids)+1)
	args = append(args, status)
	for _, id := range ids {
		args = append(args, id)
	}
	_, err := s.db.ExecContext(ctx,
		fmt.Sprintf(`UPDATE platform_messages SET status = ?, processed_at = datetime('now') WHERE id IN (%s)`, placeholders),
		args...,
	)
	return err
}

// GetUnfinished returns messages with an active status (routed/debouncing/merged/executing)
// that have a worker_id, ordered by received_at ASC.
func (s *MessageStore) GetUnfinished(ctx context.Context) ([]model.PendingMessage, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, session_key, worker_id, platform, content
		FROM platform_messages
		WHERE status IN ('routed', 'debouncing', 'merged', 'executing')
		  AND worker_id != ''
		ORDER BY received_at ASC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var msgs []model.PendingMessage
	for rows.Next() {
		var m model.PendingMessage
		if err := rows.Scan(&m.ID, &m.SessionKey, &m.WorkerID, &m.Platform, &m.Content); err != nil {
			return nil, err
		}
		msgs = append(msgs, m)
	}
	return msgs, rows.Err()
}
```

- [ ] **Step 4: Run tests to verify they pass**

```bash
go test ./internal/store/... -run TestMessageStore -v
```

Expected: all PASS

- [ ] **Step 5: Commit**

```bash
git add internal/store/message_store.go internal/store/message_store_test.go
git commit -m "feat: add MessageStore for platform message persistence"
```

---

## Chunk 2: Config & Queue Core

### Task 4: Add `MessageQueueConfig` to config

**Files:**
- Modify: `internal/config/config.go`

- [ ] **Step 1: Add `MessageQueueConfig` struct and field**

In `internal/config/config.go`, add the new struct and field:

```go
// Add to Config struct:
MessageQueue MessageQueueConfig `yaml:"message_queue"`

// New struct:
type MessageQueueConfig struct {
    DebounceWindow time.Duration `yaml:"debounce_window"`
}
```

Add a default in `applyDefaults`:

```go
if cfg.MessageQueue.DebounceWindow == 0 {
    cfg.MessageQueue.DebounceWindow = 3 * time.Second
}
```

- [ ] **Step 2: Verify config test still passes**

```bash
go test ./internal/config/... -v
```

Expected: PASS

- [ ] **Step 3: Commit**

```bash
git add internal/config/config.go
git commit -m "feat: add MessageQueueConfig with debounce_window"
```

---

### Task 5: Extend `platform/interfaces.go` with `MessageStore` interface

**Files:**
- Modify: `internal/platform/interfaces.go`

- [ ] **Step 1: Add `MessageStore` interface**

Append to `internal/platform/interfaces.go`:

```go
import "github.com/robobee/core/internal/model"

// MessageStore is the subset of store.MessageStore operations used by the queue.
// The concrete implementation is *store.MessageStore.
type MessageStore interface {
    Create(ctx context.Context, id, sessionKey, platform, content string) error
    SetWorkerID(ctx context.Context, id, workerID string) error
    SetStatus(ctx context.Context, id, status string) error
    UpdateStatusBatch(ctx context.Context, ids []string, status string) error
    MarkMerged(ctx context.Context, primaryID string, mergedIDs []string) error
    MarkTerminal(ctx context.Context, ids []string, status string) error
    GetUnfinished(ctx context.Context) ([]model.PendingMessage, error)
}
```

Note: add `"github.com/robobee/core/internal/model"` to the existing imports in interfaces.go.

- [ ] **Step 2: Verify it compiles**

```bash
go build ./internal/platform/...
```

Expected: no errors

- [ ] **Step 3: Commit**

```bash
git add internal/platform/interfaces.go
git commit -m "feat: add MessageStore interface to platform package"
```

---

### Task 6: Add `Route` and `HandleRouted` to Pipeline

**Files:**
- Modify: `internal/platform/pipeline.go`
- Modify: `internal/platform/pipeline_test.go`

The queue's executor needs to call routing once (in dispatch) then pass the workerID directly to execution, bypassing the routing step inside `Handle`. Add two new methods.

- [ ] **Step 1: Write failing tests**

Append to `internal/platform/pipeline_test.go`:

```go
func TestPipeline_Route_ReturnsWorkerID(t *testing.T) {
	p := newPipeline(&stubRouter{workerID: "w-deploy"}, newStubSessionStore(), &stubManager{})
	id, err := p.Route(context.Background(), "deploy the app")
	if err != nil {
		t.Fatalf("Route: %v", err)
	}
	if id != "w-deploy" {
		t.Errorf("want w-deploy, got %s", id)
	}
}

func TestPipeline_Route_Error(t *testing.T) {
	p := newPipeline(&stubRouter{err: errors.New("none")}, newStubSessionStore(), &stubManager{})
	_, err := p.Route(context.Background(), "anything")
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestPipeline_HandleRouted_NewSession(t *testing.T) {
	mgr := &stubManager{exec: model.WorkerExecution{
		ID:        "exec-1",
		SessionID: "sess-1",
		Status:    model.ExecStatusCompleted,
		Result:    "deployed",
	}}
	p := newPipeline(&stubRouter{workerID: "w1"}, newStubSessionStore(), mgr)
	result := p.HandleRouted(context.Background(), msg("deploy"), "w-deploy")
	if result != "deployed" {
		t.Errorf("want deployed, got %s", result)
	}
}

func TestPipeline_HandleRouted_ExistingSession(t *testing.T) {
	store := newStubSessionStore()
	store.sessions["test:session1"] = &Session{
		Key:             "test:session1",
		Platform:        "test",
		WorkerID:        "w1",
		LastExecutionID: "prev-exec",
	}
	mgr := &stubManager{exec: model.WorkerExecution{
		ID:        "exec-2",
		SessionID: "sess-1",
		Status:    model.ExecStatusCompleted,
		Result:    "continued",
	}}
	p := newPipeline(&stubRouter{workerID: "w1"}, store, mgr)
	result := p.HandleRouted(context.Background(), msg("continue"), "w1")
	if result != "continued" {
		t.Errorf("want continued, got %s", result)
	}
}
```

- [ ] **Step 2: Run to verify failure**

```bash
go test ./internal/platform/... -run "TestPipeline_Route|TestPipeline_HandleRouted" -v
```

Expected: FAIL — `Route` and `HandleRouted` not defined

- [ ] **Step 3: Implement `Route` and `HandleRouted` in pipeline.go**

Append to `internal/platform/pipeline.go`:

```go
// Route resolves the best worker ID for the given message content.
// Call this before HandleRouted to pre-route in the dispatch layer.
func (p *Pipeline) Route(ctx context.Context, content string) (string, error) {
	return p.router.Route(ctx, content)
}

// HandleRouted processes an already-routed message, skipping the routing step.
// workerID must be the result of a prior Route call for this content.
func (p *Pipeline) HandleRouted(ctx context.Context, msg InboundMessage, workerID string) string {
	sess, err := p.sessions.Get(msg.SessionKey)
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

	if err := p.sessions.Upsert(Session{
		Key:             msg.SessionKey,
		Platform:        msg.Platform,
		WorkerID:        workerID,
		SessionID:       exec.SessionID,
		LastExecutionID: exec.ID,
	}); err != nil {
		log.Printf("platform: upsert session error: %v", err)
	}

	return p.waitForResult(exec.ID)
}
```

- [ ] **Step 4: Run tests**

```bash
go test ./internal/platform/... -v
```

Expected: all PASS

- [ ] **Step 5: Commit**

```bash
git add internal/platform/pipeline.go internal/platform/pipeline_test.go
git commit -m "feat: add Route and HandleRouted to Pipeline for queue integration"
```

---

### Task 7: Implement `sessionQueue`

**Files:**
- Create: `internal/platform/session_queue.go`
- Create: `internal/platform/session_queue_test.go`

- [ ] **Step 1: Write failing tests**

Create `internal/platform/session_queue_test.go`:

```go
package platform

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/robobee/core/internal/model"
)

// stubMessageStore is a no-op MessageStore for queue tests.
type stubMessageStore struct {
	mu      sync.Mutex
	updates []string
}

func (s *stubMessageStore) Create(_ context.Context, _, _, _, _ string) error { return nil }
func (s *stubMessageStore) SetWorkerID(_ context.Context, _, _ string) error  { return nil }
func (s *stubMessageStore) SetStatus(_ context.Context, _, _ string) error    { return nil }
func (s *stubMessageStore) UpdateStatusBatch(_ context.Context, ids []string, status string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, id := range ids {
		s.updates = append(s.updates, status+":"+id)
	}
	return nil
}
func (s *stubMessageStore) MarkMerged(_ context.Context, _ string, _ []string) error { return nil }
func (s *stubMessageStore) MarkTerminal(_ context.Context, _ []string, _ string) error {
	return nil
}
func (s *stubMessageStore) GetUnfinished(_ context.Context) ([]model.PendingMessage, error) {
	return nil, nil
}

func TestSessionQueue_SingleMessage_ExecutesAfterDebounce(t *testing.T) {
	executed := make(chan string, 1)
	executor := func(_, _, content string, _ InboundMessage) { executed <- content }

	q := newSessionQueue("sk1", "w1", 50*time.Millisecond, executor, &stubMessageStore{})
	q.enqueue("msg-1", "hello", InboundMessage{SessionKey: "sk1", Content: "hello"})

	select {
	case got := <-executed:
		if got != "hello" {
			t.Errorf("want hello, got %s", got)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("executor not called within timeout")
	}
}

func TestSessionQueue_TwoRapidMessages_MergedIntoOne(t *testing.T) {
	executed := make(chan string, 1)
	executor := func(_, _, content string, _ InboundMessage) { executed <- content }

	q := newSessionQueue("sk1", "w1", 100*time.Millisecond, executor, &stubMessageStore{})
	q.enqueue("msg-1", "first", InboundMessage{SessionKey: "sk1", Content: "first"})
	time.Sleep(20 * time.Millisecond)
	q.enqueue("msg-2", "second", InboundMessage{SessionKey: "sk1", Content: "second"})

	select {
	case got := <-executed:
		if !strings.Contains(got, "first") || !strings.Contains(got, "second") {
			t.Errorf("want both messages merged, got %q", got)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("executor not called within timeout")
	}

	// Only one execution should fire.
	select {
	case extra := <-executed:
		t.Errorf("unexpected second execution: %s", extra)
	case <-time.After(200 * time.Millisecond):
		// good
	}
}

func TestSessionQueue_RollingDebounce_TimerResetsOnNewMessage(t *testing.T) {
	executedAt := make(chan time.Time, 1)
	debounce := 80 * time.Millisecond
	executor := func(_, _, _ string, _ InboundMessage) { executedAt <- time.Now() }

	q := newSessionQueue("sk1", "w1", debounce, executor, &stubMessageStore{})
	start := time.Now()
	q.enqueue("msg-1", "a", InboundMessage{SessionKey: "sk1"})
	time.Sleep(50 * time.Millisecond) // before debounce fires
	q.enqueue("msg-2", "b", InboundMessage{SessionKey: "sk1"})
	// timer resets; fires ~80ms after msg-2

	select {
	case fired := <-executedAt:
		elapsed := fired.Sub(start)
		// At least 50ms (msg-2 delay) + 80ms (debounce) = 130ms
		if elapsed < 120*time.Millisecond {
			t.Errorf("timer not reset: elapsed=%v (too short)", elapsed)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("executor not called")
	}
}

func TestSessionQueue_MessageDuringExecution_QueuedAndExecutedAfter(t *testing.T) {
	blockExec := make(chan struct{})
	execOrder := make(chan string, 10)
	executor := func(_, _, content string, _ InboundMessage) {
		execOrder <- content
		<-blockExec
	}

	q := newSessionQueue("sk1", "w1", 20*time.Millisecond, executor, &stubMessageStore{})
	q.enqueue("msg-1", "first", InboundMessage{SessionKey: "sk1", Content: "first"})

	select {
	case <-execOrder:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("first execution never started")
	}

	q.enqueue("msg-2", "second", InboundMessage{SessionKey: "sk1", Content: "second"})
	time.Sleep(50 * time.Millisecond) // let debounce fire into pending

	close(blockExec) // release first execution

	select {
	case got := <-execOrder:
		if !strings.Contains(got, "second") {
			t.Errorf("want second message, got %q", got)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("second execution never started")
	}
}

func TestSessionQueue_Cancel_ClearsPendingWork(t *testing.T) {
	executed := make(chan string, 1)
	executor := func(_, _, content string, _ InboundMessage) { executed <- content }

	q := newSessionQueue("sk1", "w1", 200*time.Millisecond, executor, &stubMessageStore{})
	q.enqueue("msg-1", "hello", InboundMessage{SessionKey: "sk1"})
	q.cancel()

	select {
	case got := <-executed:
		t.Errorf("executor should not run after cancel, got %q", got)
	case <-time.After(400 * time.Millisecond):
		// good
	}
}

func TestSessionQueue_IsIdle_AfterExecution(t *testing.T) {
	done := make(chan struct{})
	executor := func(_, _, _ string, _ InboundMessage) { close(done) }

	q := newSessionQueue("sk1", "w1", 20*time.Millisecond, executor, &stubMessageStore{})
	q.enqueue("msg-1", "hi", InboundMessage{SessionKey: "sk1"})

	<-done
	time.Sleep(20 * time.Millisecond) // allow onDone to complete
	if !q.isIdle() {
		t.Error("queue should be idle after execution with no pending work")
	}
}
```

- [ ] **Step 2: Run to confirm failure**

```bash
go test ./internal/platform/... -run TestSessionQueue -v
```

Expected: FAIL — `newSessionQueue` not defined

- [ ] **Step 3: Implement `sessionQueue`**

Create `internal/platform/session_queue.go`:

```go
package platform

import (
	"context"
	"sync"
	"time"
)

const mergedSeparator = "\n\n---\n\n"

// Executor is called when a debounce window closes and the execution slot is free.
// sessionKey and workerID identify the queue; content is the merged message text;
// replyTo is the last InboundMessage received, used for platform reply routing.
type Executor func(sessionKey, workerID, content string, replyTo InboundMessage)

// sessionQueue manages debounce and execution serialization for one session_key+worker_id pair.
type sessionQueue struct {
	sessionKey string
	workerID   string

	debounceTimer   *time.Timer
	debounceIDs     []string
	debounceContent string
	lastInbound     InboundMessage

	pendingContent string
	pendingIDs     []string

	isExecuting bool
	mu          sync.Mutex

	executor Executor
	store    MessageStore
	debounce time.Duration
}

func newSessionQueue(sessionKey, workerID string, debounce time.Duration, executor Executor, store MessageStore) *sessionQueue {
	return &sessionQueue{
		sessionKey: sessionKey,
		workerID:   workerID,
		executor:   executor,
		store:      store,
		debounce:   debounce,
	}
}

// enqueue adds a message to the debounce buffer and resets the rolling timer.
func (q *sessionQueue) enqueue(msgID, content string, inbound InboundMessage) {
	q.mu.Lock()
	defer q.mu.Unlock()

	if q.debounceContent == "" {
		q.debounceContent = content
	} else {
		q.debounceContent = q.debounceContent + mergedSeparator + content
	}
	q.debounceIDs = append(q.debounceIDs, msgID)
	q.lastInbound = inbound

	q.store.UpdateStatusBatch(context.Background(), q.debounceIDs, "debouncing") //nolint:errcheck

	if q.debounceTimer != nil {
		q.debounceTimer.Stop()
	}
	q.debounceTimer = time.AfterFunc(q.debounce, q.onDebounce)
}

// onDebounce fires when the rolling debounce window closes.
func (q *sessionQueue) onDebounce() {
	q.mu.Lock()

	if q.debounceContent == "" {
		q.mu.Unlock()
		return
	}

	mergedContent := q.debounceContent
	ids := q.debounceIDs
	replyTo := q.lastInbound
	q.debounceContent = ""
	q.debounceIDs = nil
	q.debounceTimer = nil

	primaryID := ids[0]
	mergedIDs := ids[1:]
	q.store.MarkMerged(context.Background(), primaryID, mergedIDs) //nolint:errcheck

	if !q.isExecuting {
		q.isExecuting = true
		q.store.UpdateStatusBatch(context.Background(), []string{primaryID}, "executing") //nolint:errcheck
		q.mu.Unlock()
		go q.runExecutor([]string{primaryID}, mergedContent, replyTo)
	} else {
		if q.pendingContent == "" {
			q.pendingContent = mergedContent
		} else {
			q.pendingContent = q.pendingContent + mergedSeparator + mergedContent
		}
		q.pendingIDs = append(q.pendingIDs, ids...)
		q.mu.Unlock()
	}
}

// runExecutor calls the executor and handles completion.
func (q *sessionQueue) runExecutor(ids []string, content string, replyTo InboundMessage) {
	q.executor(q.sessionKey, q.workerID, content, replyTo)
	q.onDone(ids)
}

// onDone is called when an execution finishes (success or failure).
func (q *sessionQueue) onDone(ids []string) {
	q.mu.Lock()
	q.store.MarkTerminal(context.Background(), ids, "done") //nolint:errcheck

	if q.pendingContent != "" {
		nextContent := q.pendingContent
		nextIDs := q.pendingIDs
		nextReplyTo := q.lastInbound
		q.pendingContent = ""
		q.pendingIDs = nil
		q.store.UpdateStatusBatch(context.Background(), nextIDs, "executing") //nolint:errcheck
		q.mu.Unlock()
		go q.runExecutor(nextIDs, nextContent, nextReplyTo)
	} else {
		q.isExecuting = false
		q.mu.Unlock()
	}
}

// isIdle reports whether the queue has no pending or active work.
func (q *sessionQueue) isIdle() bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	return !q.isExecuting && q.debounceContent == "" && q.pendingContent == ""
}

// cancel stops the debounce timer and clears all pending work, marking messages failed.
func (q *sessionQueue) cancel() {
	q.mu.Lock()
	defer q.mu.Unlock()

	if q.debounceTimer != nil {
		q.debounceTimer.Stop()
		q.debounceTimer = nil
	}

	var allIDs []string
	allIDs = append(allIDs, q.debounceIDs...)
	allIDs = append(allIDs, q.pendingIDs...)

	q.debounceContent = ""
	q.debounceIDs = nil
	q.pendingContent = ""
	q.pendingIDs = nil

	if len(allIDs) > 0 {
		q.store.MarkTerminal(context.Background(), allIDs, "failed") //nolint:errcheck
	}
}
```

- [ ] **Step 4: Run tests**

```bash
go test ./internal/platform/... -run TestSessionQueue -v
```

Expected: all PASS

- [ ] **Step 5: Commit**

```bash
git add internal/platform/session_queue.go internal/platform/session_queue_test.go
git commit -m "feat: implement sessionQueue with rolling debounce and pending slot"
```

---

### Task 8: Implement `QueueManager`

**Files:**
- Create: `internal/platform/queue_manager.go`
- Create: `internal/platform/queue_manager_test.go`

- [ ] **Step 1: Write failing tests**

Create `internal/platform/queue_manager_test.go`:

```go
package platform

import (
	"context"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/robobee/core/internal/model"
)

func TestQueueManager_Enqueue_CallsExecutor(t *testing.T) {
	var called atomic.Int32
	executor := func(_, _, _ string, _ InboundMessage) { called.Add(1) }

	qm := NewQueueManager(&stubMessageStore{}, executor, 20*time.Millisecond)
	qm.Enqueue(InboundMessage{SessionKey: "sk1", Platform: "test"}, "w1", "msg-1")

	time.Sleep(200 * time.Millisecond)
	if called.Load() != 1 {
		t.Errorf("expected 1 execution, got %d", called.Load())
	}
}

func TestQueueManager_DifferentSessions_RunConcurrently(t *testing.T) {
	var mu sync.Mutex
	started := map[string]bool{}
	ready := make(chan struct{}, 1)
	executor := func(sessionKey, _, _ string, _ InboundMessage) {
		mu.Lock()
		started[sessionKey] = true
		count := len(started)
		mu.Unlock()
		if count >= 2 {
			select {
			case ready <- struct{}{}:
			default:
			}
		}
		time.Sleep(100 * time.Millisecond)
	}

	qm := NewQueueManager(&stubMessageStore{}, executor, 20*time.Millisecond)
	qm.Enqueue(InboundMessage{SessionKey: "sk1", Platform: "test"}, "w1", "msg-1")
	qm.Enqueue(InboundMessage{SessionKey: "sk2", Platform: "test"}, "w2", "msg-2")

	select {
	case <-ready:
		// both ran concurrently — good
	case <-time.After(500 * time.Millisecond):
		t.Error("expected both sessions to run concurrently")
	}
}

func TestQueueManager_CancelSession_StopsPendingWork(t *testing.T) {
	executed := make(chan string, 1)
	executor := func(_, _, content string, _ InboundMessage) { executed <- content }

	qm := NewQueueManager(&stubMessageStore{}, executor, 200*time.Millisecond)
	qm.Enqueue(InboundMessage{SessionKey: "sk1", Platform: "test"}, "w1", "msg-1")
	qm.CancelSession("sk1")

	select {
	case got := <-executed:
		t.Errorf("executor should not run after cancel, got %q", got)
	case <-time.After(400 * time.Millisecond):
		// good
	}
}

func TestQueueManager_IdleCleanup_RemovesQueue(t *testing.T) {
	done := make(chan struct{})
	executor := func(_, _, _ string, _ InboundMessage) { close(done) }

	qm := NewQueueManager(&stubMessageStore{}, executor, 20*time.Millisecond)
	qm.Enqueue(InboundMessage{SessionKey: "sk1", Platform: "test"}, "w1", "msg-1")

	<-done
	time.Sleep(50 * time.Millisecond)

	qm.mu.RLock()
	_, exists := qm.queues[queueKey("sk1", "w1")]
	qm.mu.RUnlock()

	if exists {
		t.Error("idle queue should have been removed from manager map")
	}
}

func TestQueueManager_RecoverFromDB_ExecutesPendingMessages(t *testing.T) {
	recStore := &recoverableStore{
		stubMessageStore: &stubMessageStore{},
		pending: []model.PendingMessage{
			{ID: "msg-r1", SessionKey: "sk1", WorkerID: "w1", Platform: "feishu", Content: "recovered"},
		},
	}

	executed := make(chan string, 1)
	executor := func(_, _, content string, _ InboundMessage) { executed <- content }

	qm := NewQueueManager(recStore, executor, 20*time.Millisecond)
	qm.RecoverFromDB()

	select {
	case got := <-executed:
		if !strings.Contains(got, "recovered") {
			t.Errorf("want recovered content, got %q", got)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("recovered message was never executed")
	}
}

// recoverableStore overrides GetUnfinished to return preset pending messages.
type recoverableStore struct {
	*stubMessageStore
	pending []model.PendingMessage
}

func (r *recoverableStore) GetUnfinished(_ context.Context) ([]model.PendingMessage, error) {
	return r.pending, nil
}
```

- [ ] **Step 2: Run to confirm failure**

```bash
go test ./internal/platform/... -run TestQueueManager -v
```

Expected: FAIL — `NewQueueManager` not defined

- [ ] **Step 3: Implement `QueueManager`**

Create `internal/platform/queue_manager.go`:

```go
package platform

import (
	"context"
	"log"
	"strings"
	"sync"
	"time"
)

// QueueManager manages per-(session_key, worker_id) sessionQueues.
type QueueManager struct {
	queues   map[string]*sessionQueue
	mu       sync.RWMutex
	msgStore MessageStore
	executor Executor
	debounce time.Duration
}

// NewQueueManager constructs a QueueManager.
func NewQueueManager(msgStore MessageStore, executor Executor, debounce time.Duration) *QueueManager {
	return &QueueManager{
		queues:   make(map[string]*sessionQueue),
		msgStore: msgStore,
		executor: executor,
		debounce: debounce,
	}
}

func queueKey(sessionKey, workerID string) string {
	return sessionKey + "|" + workerID
}

// Enqueue adds a message to the appropriate sessionQueue.
// The message must already be recorded in the DB before calling Enqueue.
func (m *QueueManager) Enqueue(msg InboundMessage, workerID, msgID string) {
	key := queueKey(msg.SessionKey, workerID)

	m.mu.Lock()
	sq, ok := m.queues[key]
	if !ok {
		sq = newSessionQueue(msg.SessionKey, workerID, m.debounce, m.wrappedExecutor(key), m.msgStore)
		m.queues[key] = sq
	}
	m.mu.Unlock()

	sq.enqueue(msgID, msg.Content, msg)
}

// wrappedExecutor wraps the user executor to perform idle cleanup after each execution.
func (m *QueueManager) wrappedExecutor(key string) Executor {
	return func(sessionKey, workerID, content string, replyTo InboundMessage) {
		m.executor(sessionKey, workerID, content, replyTo)
		m.mu.Lock()
		if sq, ok := m.queues[key]; ok && sq.isIdle() {
			delete(m.queues, key)
		}
		m.mu.Unlock()
	}
}

// CancelSession stops and clears all queues for the given session key.
func (m *QueueManager) CancelSession(sessionKey string) {
	prefix := sessionKey + "|"
	m.mu.Lock()
	defer m.mu.Unlock()
	for key, sq := range m.queues {
		if key == sessionKey || strings.HasPrefix(key, prefix) {
			sq.cancel()
			delete(m.queues, key)
		}
	}
}

// RecoverFromDB restores and immediately executes pending queues from the DB.
// Call once at startup before handling new messages.
func (m *QueueManager) RecoverFromDB() {
	msgs, err := m.msgStore.GetUnfinished(context.Background())
	if err != nil {
		log.Printf("queue: RecoverFromDB failed: %v", err)
		return
	}
	if len(msgs) == 0 {
		return
	}

	type group struct {
		content string
		ids     []string
		replyTo InboundMessage
	}
	groups := make(map[string]*group)

	for _, msg := range msgs {
		key := queueKey(msg.SessionKey, msg.WorkerID)
		g, ok := groups[key]
		if !ok {
			g = &group{
				replyTo: InboundMessage{
					Platform:   msg.Platform,
					SessionKey: msg.SessionKey,
				},
			}
			groups[key] = g
		}
		if g.content == "" {
			g.content = msg.Content
		} else {
			g.content = g.content + mergedSeparator + msg.Content
		}
		g.ids = append(g.ids, msg.ID)
	}

	for key, g := range groups {
		parts := strings.SplitN(key, "|", 2)
		if len(parts) != 2 {
			continue
		}
		sessionKey, workerID := parts[0], parts[1]

		sq := newSessionQueue(sessionKey, workerID, m.debounce, m.wrappedExecutor(key), m.msgStore)
		sq.isExecuting = true

		m.mu.Lock()
		m.queues[key] = sq
		m.mu.Unlock()

		m.msgStore.UpdateStatusBatch(context.Background(), g.ids, "executing") //nolint:errcheck

		ids := g.ids
		content := g.content
		replyTo := g.replyTo
		go sq.runExecutor(ids, content, replyTo)
	}

	log.Printf("queue: recovered %d session(s) from DB", len(groups))
}
```

- [ ] **Step 4: Run tests**

```bash
go test ./internal/platform/... -run TestQueueManager -v
```

Expected: all PASS

- [ ] **Step 5: Run all platform tests**

```bash
go test ./internal/platform/... -v
```

Expected: all PASS

- [ ] **Step 6: Commit**

```bash
git add internal/platform/queue_manager.go internal/platform/queue_manager_test.go
git commit -m "feat: implement QueueManager for per-session debounce and concurrency control"
```

---

## Chunk 3: Integration

### Task 9: Wire queue into `PlatformManager`

**Files:**
- Modify: `internal/platform/manager.go`

Replace the async goroutine dispatch pattern with queue-based dispatch.

- [ ] **Step 1: Rewrite `manager.go`**

Replace the entire contents of `internal/platform/manager.go`:

```go
package platform

import (
	"context"
	"log"
	"strings"
	"time"

	"github.com/google/uuid"
)

// PlatformManager registers platforms and drives them concurrently.
type PlatformManager struct {
	pipeline  *Pipeline
	platforms []Platform
	queueMgr  *QueueManager
	msgStore  MessageStore
	debounce  time.Duration
}

// NewManager constructs a PlatformManager.
func NewManager(pipeline *Pipeline, msgStore MessageStore, debounce time.Duration) *PlatformManager {
	return &PlatformManager{
		pipeline: pipeline,
		msgStore: msgStore,
		debounce: debounce,
	}
}

// Register adds a platform to the manager.
func (m *PlatformManager) Register(p Platform) {
	m.platforms = append(m.platforms, p)
}

// StartAll launches each platform's receiver in its own goroutine and blocks until ctx is cancelled.
func (m *PlatformManager) StartAll(ctx context.Context) {
	// Build sender lookup by platform ID for use in executor.
	senderByPlatform := make(map[string]PlatformSenderAdapter, len(m.platforms))
	for _, p := range m.platforms {
		senderByPlatform[p.ID()] = p.Sender()
	}

	// executor: called by queue when debounce fires and slot is free.
	executor := func(sessionKey, workerID, content string, replyTo InboundMessage) {
		mergedMsg := replyTo
		mergedMsg.Content = content

		result := m.pipeline.HandleRouted(context.Background(), mergedMsg, workerID)

		platformID := strings.SplitN(sessionKey, ":", 2)[0]
		if sender, ok := senderByPlatform[platformID]; ok {
			if err := sender.Send(context.Background(), OutboundMessage{
				SessionKey: sessionKey,
				Content:    result,
				ReplyTo:    replyTo,
			}); err != nil {
				log.Printf("platform[%s]: send error: %v", platformID, err)
			}
		}
	}

	m.queueMgr = NewQueueManager(m.msgStore, executor, m.debounce)
	m.queueMgr.RecoverFromDB()

	for _, p := range m.platforms {
		p := p
		go func() {
			sender := p.Sender()
			dispatch := func(msg InboundMessage) {
				log.Printf("platform[%s]: dispatch received sessionKey=%s contentLen=%d", p.ID(), msg.SessionKey, len(msg.Content))

				if IsClearCommand(msg.Content) {
					// Cancel any queued work for this session before clearing.
					m.queueMgr.CancelSession(msg.SessionKey)
					result := m.pipeline.Handle(context.Background(), msg)
					if err := sender.Send(context.Background(), OutboundMessage{
						SessionKey: msg.SessionKey,
						Content:    result,
						ReplyTo:    msg,
					}); err != nil {
						log.Printf("platform[%s]: send error: %v", p.ID(), err)
					}
					return
				}

				// Send ACK immediately.
				if err := sender.Send(context.Background(), OutboundMessage{
					SessionKey: msg.SessionKey,
					Content:    AckMessage,
					ReplyTo:    msg,
				}); err != nil {
					log.Printf("platform[%s]: ack error: %v", p.ID(), err)
				} else {
					log.Printf("platform[%s]: ack sent sessionKey=%s", p.ID(), msg.SessionKey)
				}

				// Record message to DB (best-effort; failure does not block processing).
				msgID := uuid.New().String()
				if err := m.msgStore.Create(context.Background(), msgID, msg.SessionKey, msg.Platform, msg.Content); err != nil {
					log.Printf("platform[%s]: record message error: %v", p.ID(), err)
				}

				// Route to find worker ID.
				workerID, err := m.pipeline.Route(context.Background(), msg.Content)
				if err != nil {
					log.Printf("platform[%s]: route error: %v", p.ID(), err)
					m.msgStore.MarkTerminal(context.Background(), []string{msgID}, "failed") //nolint:errcheck
					if err2 := sender.Send(context.Background(), OutboundMessage{
						SessionKey: msg.SessionKey,
						Content:    noWorkerMsg,
						ReplyTo:    msg,
					}); err2 != nil {
						log.Printf("platform[%s]: send error: %v", p.ID(), err2)
					}
					return
				}
				m.msgStore.SetWorkerID(context.Background(), msgID, workerID) //nolint:errcheck
				log.Printf("platform[%s]: routed sessionKey=%s workerID=%s msgID=%s", p.ID(), msg.SessionKey, workerID, msgID)

				m.queueMgr.Enqueue(msg, workerID, msgID)
			}

			log.Printf("platform[%s]: starting receiver", p.ID())
			if err := p.Receiver().Start(ctx, dispatch); err != nil && ctx.Err() == nil {
				log.Printf("platform[%s]: receiver error: %v", p.ID(), err)
			}
		}()
	}
	<-ctx.Done()
}
```

- [ ] **Step 2: Verify it compiles**

```bash
go build ./internal/platform/...
```

Expected: no errors

- [ ] **Step 3: Commit**

```bash
git add internal/platform/manager.go
git commit -m "feat: replace async goroutine dispatch with queue-based debounce in PlatformManager"
```

---

### Task 10: Wire everything in `main.go`

**Files:**
- Modify: `cmd/server/main.go`

Update `main.go` to create `MessageStore` and pass it to `NewManager`.

- [ ] **Step 1: Update the store and manager wiring in `main.go`**

In `cmd/server/main.go`, after creating `execStore`, add:

```go
msgStore := store.NewMessageStore(db)
```

Change the `NewManager` call from:

```go
platManager := platform.NewManager(pipe)
```

to:

```go
platManager := platform.NewManager(pipe, msgStore, cfg.MessageQueue.DebounceWindow)
```

- [ ] **Step 2: Verify the full build**

```bash
go build ./...
```

Expected: no errors

- [ ] **Step 3: Run all tests**

```bash
go test ./... 2>&1 | tail -30
```

Expected: all PASS

- [ ] **Step 4: Check for a config file and add debounce_window if present**

```bash
ls /Users/tengteng/work/robobee/core/*.yaml 2>/dev/null
```

If a `config.yaml` or `config.example.yaml` exists, add under the top-level keys:

```yaml
message_queue:
  debounce_window: 3s
```

- [ ] **Step 5: Final commit**

```bash
git add cmd/server/main.go
git add -p  # review and stage any config file changes
git commit -m "feat: wire MessageStore and debounce config into PlatformManager"
```

---

## Verification Checklist

After all tasks complete, verify:

- [ ] `go build ./...` succeeds
- [ ] `go test ./...` passes
- [ ] `go vet ./...` reports no issues
- [ ] Two rapid messages to the same session produce one merged execution (check logs: single executor call with merged content)
- [ ] A message sent during an active execution is held and processed after completion
- [ ] `clear` command cancels pending debounce and responds immediately
- [ ] Server restart: messages in unfinished state are re-executed on startup (check logs for "queue: recovered N session(s)")
