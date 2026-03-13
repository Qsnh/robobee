# MCP Task Lifecycle & Messaging Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add three MCP tools (`mark_task_success`, `mark_task_failed`, `send_message`), simplify the dispatcher by removing its message-sending responsibility, and delete the `msgsender` package.

**Architecture:** Workers call `mark_task_success/failed` to set task terminal status, and both workers and bee call `send_message` to deliver replies — bypassing the old dispatcher→msgsender pipeline. The dispatcher retains its per-session execution queue but no longer sends any messages. The MCPServer gains direct access to platform senders and the message store.

**Tech Stack:** Go, SQLite (via `database/sql`), Gin, existing `platform.PlatformSenderAdapter` interface

---

## Chunk 1: Store Layer

### Task 1: Add `TaskStore.UpdateStatus`

**Files:**
- Modify: `internal/store/task_store.go`
- Modify: `internal/store/task_store_test.go`

- [ ] **Step 1: Write failing tests**

Add to `internal/store/task_store_test.go`:

```go
func TestTaskStore_UpdateStatus_SetsCompleted(t *testing.T) {
	ts, cleanup := newTaskStoreForTest(t)
	defer cleanup()

	now := time.Now().UnixMilli()
	id, err := ts.Create(context.Background(), model.Task{
		MessageID: "m1", WorkerID: "w1", Instruction: "go",
		Type: model.TaskTypeImmediate, Status: model.TaskStatusRunning,
		CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	if err := ts.UpdateStatus(context.Background(), id, model.TaskStatusCompleted); err != nil {
		t.Fatalf("UpdateStatus: %v", err)
	}

	got, err := ts.GetByID(context.Background(), id)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if got.Status != model.TaskStatusCompleted {
		t.Errorf("status: want completed, got %q", got.Status)
	}
	// execution_id must be untouched (different from SetExecution)
	if got.ExecutionID != "" {
		t.Errorf("execution_id should be empty, got %q", got.ExecutionID)
	}
}

func TestTaskStore_UpdateStatus_SetsFailed(t *testing.T) {
	ts, cleanup := newTaskStoreForTest(t)
	defer cleanup()

	now := time.Now().UnixMilli()
	id, err := ts.Create(context.Background(), model.Task{
		MessageID: "m1", WorkerID: "w1", Instruction: "go",
		Type: model.TaskTypeImmediate, Status: model.TaskStatusRunning,
		CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	if err := ts.UpdateStatus(context.Background(), id, model.TaskStatusFailed); err != nil {
		t.Fatalf("UpdateStatus: %v", err)
	}

	got, _ := ts.GetByID(context.Background(), id)
	if got.Status != model.TaskStatusFailed {
		t.Errorf("status: want failed, got %q", got.Status)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

```bash
go test ./internal/store/... -run TestTaskStore_UpdateStatus -v
```
Expected: FAIL with `ts.UpdateStatus undefined`

- [ ] **Step 3: Add `UpdateStatus` to `internal/store/task_store.go`**

Add after the `CancelTask` method:

```go
// UpdateStatus sets only the status of a task. Unlike SetExecution, it does
// not touch execution_id. Used by mark_task_success and mark_task_failed MCP tools.
func (s *TaskStore) UpdateStatus(ctx context.Context, taskID, status string) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE tasks SET status = ?, updated_at = ? WHERE id = ?`,
		status, time.Now().UnixMilli(), taskID)
	return err
}
```

- [ ] **Step 4: Run tests to verify they pass**

```bash
go test ./internal/store/... -run TestTaskStore_UpdateStatus -v
```
Expected: PASS (both `_SetsCompleted` and `_SetsFailed`)

- [ ] **Step 5: Run full store tests to check for regressions**

```bash
go test ./internal/store/... -v
```
Expected: all PASS

- [ ] **Step 6: Commit**

```bash
git add internal/store/task_store.go internal/store/task_store_test.go
git commit -m "feat(store): add TaskStore.UpdateStatus for terminal task status"
```

---

### Task 2: Add `MessageStore.StoredMessage` and `GetByID`

**Files:**
- Modify: `internal/store/message_store.go`
- Modify: `internal/store/message_store_test.go`

- [ ] **Step 1: Write failing tests**

Add to `internal/store/message_store_test.go`:

```go
func TestMessageStore_GetByID_ReturnsStoredFields(t *testing.T) {
	s := setupMessageStore(t)
	ctx := context.Background()

	s.Create(ctx, "msg-1", "feishu:chat1:userA", "feishu", "hello", `{"raw":"data"}`, "", 0) //nolint

	got, err := s.GetByID(ctx, "msg-1")
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if got.Platform != "feishu" {
		t.Errorf("Platform: want feishu, got %q", got.Platform)
	}
	if got.SessionKey != "feishu:chat1:userA" {
		t.Errorf("SessionKey: want feishu:chat1:userA, got %q", got.SessionKey)
	}
	if got.Raw != `{"raw":"data"}` {
		t.Errorf("Raw: want %q, got %q", `{"raw":"data"}`, got.Raw)
	}
}

func TestMessageStore_GetByID_NotFound(t *testing.T) {
	s := setupMessageStore(t)
	ctx := context.Background()

	_, err := s.GetByID(ctx, "nonexistent")
	if err == nil {
		t.Error("expected error for missing message, got nil")
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

```bash
go test ./internal/store/... -run TestMessageStore_GetByID -v
```
Expected: FAIL with `s.GetByID undefined`

- [ ] **Step 3: Add `StoredMessage` struct and `GetByID` to `internal/store/message_store.go`**

Add after the `CreateBatch` method (end of file):

```go
// StoredMessage is the subset of platform_messages fields needed by platform senders.
type StoredMessage struct {
	Platform   string
	SessionKey string
	Raw        string
}

// GetByID fetches the platform, session_key, and raw fields for a single message.
func (s *MessageStore) GetByID(ctx context.Context, id string) (StoredMessage, error) {
	var m StoredMessage
	err := s.db.QueryRowContext(ctx,
		`SELECT platform, session_key, raw FROM platform_messages WHERE id = ?`, id,
	).Scan(&m.Platform, &m.SessionKey, &m.Raw)
	if err != nil {
		return StoredMessage{}, fmt.Errorf("get message %s: %w", id, err)
	}
	return m, nil
}
```

- [ ] **Step 4: Run tests to verify they pass**

```bash
go test ./internal/store/... -run TestMessageStore_GetByID -v
```
Expected: PASS (both tests)

- [ ] **Step 5: Run full store tests to check for regressions**

```bash
go test ./internal/store/... -v
```
Expected: all PASS

- [ ] **Step 6: Commit**

```bash
git add internal/store/message_store.go internal/store/message_store_test.go
git commit -m "feat(store): add MessageStore.StoredMessage and GetByID"
```

---

## Chunk 2: Dispatcher Simplification

### Task 3: Rewrite dispatcher tests (TDD first)

The existing `dispatcher_test.go` depends on `d.Out()` and `msgsender.SenderEvent`. These will be removed. Write the replacement tests first so they drive the implementation.

**Files:**
- Modify: `internal/dispatcher/dispatcher_test.go`

- [ ] **Step 1: Replace `dispatcher_test.go` with new tests**

Replace the entire file content:

```go
package dispatcher_test

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/robobee/core/internal/dispatcher"
	"github.com/robobee/core/internal/model"
	"github.com/robobee/core/internal/platform"
)

// --- Mocks ---

type mockExecManager struct {
	mu                   sync.Mutex
	execResult           model.WorkerExecution
	getResult            model.WorkerExecution
	resumedWithSessionID string
	executedInstructions []string
}

func (m *mockExecManager) ExecuteWorker(_ context.Context, _, instruction string) (model.WorkerExecution, error) {
	m.mu.Lock()
	m.executedInstructions = append(m.executedInstructions, instruction)
	m.mu.Unlock()
	return m.execResult, nil
}
func (m *mockExecManager) ExecuteWorkerWithSession(_ context.Context, _, instruction, sessionID string) (model.WorkerExecution, error) {
	m.mu.Lock()
	m.resumedWithSessionID = sessionID
	m.executedInstructions = append(m.executedInstructions, instruction)
	m.mu.Unlock()
	return m.execResult, nil
}
func (m *mockExecManager) GetExecution(_ string) (model.WorkerExecution, error) {
	return m.getResult, nil
}

type mockTaskStore struct{}

func (s *mockTaskStore) SetExecution(_ context.Context, _, _, _ string) error { return nil }

type mockSessionStore struct {
	mu      sync.Mutex
	data    map[string]string
	cleared []string
}

func newMockSessionStore() *mockSessionStore {
	return &mockSessionStore{data: make(map[string]string)}
}

func (s *mockSessionStore) GetSessionContext(_ context.Context, sessionKey, agentID string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.data[sessionKey+"|"+agentID], nil
}
func (s *mockSessionStore) UpsertSessionContext(_ context.Context, sessionKey, agentID, sessionID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.data[sessionKey+"|"+agentID] = sessionID
	return nil
}
func (s *mockSessionStore) ClearSessionContexts(_ context.Context, sessionKey string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cleared = append(s.cleared, sessionKey)
	return nil
}

func newDispatcher(mgr dispatcher.ExecutionManager, ss dispatcher.SessionStore) (*dispatcher.Dispatcher, chan dispatcher.DispatchTask) {
	in := make(chan dispatcher.DispatchTask, 4)
	d := dispatcher.New(mgr, &mockTaskStore{}, ss, in)
	return d, in
}

func immediateTask(sessionKey, workerID, instruction string) dispatcher.DispatchTask {
	return dispatcher.DispatchTask{
		TaskID:      "task-1",
		WorkerID:    workerID,
		SessionKey:  sessionKey,
		Instruction: instruction,
		ReplyTo:     platform.InboundMessage{Platform: "test", SessionKey: sessionKey},
		TaskType:    "immediate",
		MessageID:   "msg-1",
	}
}

// waitForExecCount waits until mgr.executedInstructions reaches n or timeout.
func waitForExecCount(mgr *mockExecManager, n int, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		mgr.mu.Lock()
		count := len(mgr.executedInstructions)
		mgr.mu.Unlock()
		if count >= n {
			return true
		}
		time.Sleep(10 * time.Millisecond)
	}
	return false
}

// --- Tests ---

func TestDispatcher_ImmediateTask_CallsExecuteWorker(t *testing.T) {
	mgr := &mockExecManager{
		execResult: model.WorkerExecution{ID: "exec-1", SessionID: "sess-1"},
		getResult:  model.WorkerExecution{ID: "exec-1", Status: model.ExecStatusCompleted, Result: "done!"},
	}
	d, in := newDispatcher(mgr, newMockSessionStore())

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go d.Run(ctx)

	in <- immediateTask("s1", "w1", "check weather")

	if !waitForExecCount(mgr, 1, 2*time.Second) {
		t.Fatal("ExecuteWorker was not called within timeout")
	}
}

func TestDispatcher_InstructionInjection(t *testing.T) {
	mgr := &mockExecManager{
		execResult: model.WorkerExecution{ID: "exec-1", SessionID: "sess-1"},
		getResult:  model.WorkerExecution{ID: "exec-1", Status: model.ExecStatusCompleted},
	}
	d, in := newDispatcher(mgr, newMockSessionStore())

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go d.Run(ctx)

	task := dispatcher.DispatchTask{
		TaskID:      "task-abc",
		WorkerID:    "w1",
		SessionKey:  "s1",
		Instruction: "do the thing",
		ReplyTo:     platform.InboundMessage{Platform: "test", SessionKey: "s1"},
		TaskType:    "immediate",
		MessageID:   "msg-xyz",
	}
	in <- task

	if !waitForExecCount(mgr, 1, 2*time.Second) {
		t.Fatal("ExecuteWorker was not called within timeout")
	}

	mgr.mu.Lock()
	instr := mgr.executedInstructions[0]
	mgr.mu.Unlock()

	if !strings.Contains(instr, "task_id=task-abc") {
		t.Errorf("instruction missing task_id injection, got: %q", instr)
	}
	if !strings.Contains(instr, "message_id=msg-xyz") {
		t.Errorf("instruction missing message_id injection, got: %q", instr)
	}
	if !strings.Contains(instr, "do the thing") {
		t.Errorf("instruction missing original text, got: %q", instr)
	}
}

func TestDispatcher_InstructionInjection_SkippedWhenNoTaskID(t *testing.T) {
	mgr := &mockExecManager{
		execResult: model.WorkerExecution{ID: "exec-1"},
		getResult:  model.WorkerExecution{ID: "exec-1", Status: model.ExecStatusCompleted},
	}
	d, in := newDispatcher(mgr, newMockSessionStore())

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go d.Run(ctx)

	// Clear command has no TaskID
	in <- dispatcher.DispatchTask{
		TaskType:   "clear",
		SessionKey: "s1",
		ReplyTo:    platform.InboundMessage{Platform: "test", SessionKey: "s1"},
	}

	// Just verify no panic; clear doesn't call ExecuteWorker
	time.Sleep(50 * time.Millisecond)
	mgr.mu.Lock()
	count := len(mgr.executedInstructions)
	mgr.mu.Unlock()
	if count != 0 {
		t.Errorf("expected 0 executions for clear command, got %d", count)
	}
}

func TestDispatcher_ClearTask_ClearsSessionContexts(t *testing.T) {
	ss := newMockSessionStore()
	d, in := newDispatcher(&mockExecManager{}, ss)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go d.Run(ctx)

	in <- dispatcher.DispatchTask{
		TaskType:   "clear",
		SessionKey: "s1",
		ReplyTo:    platform.InboundMessage{Platform: "test", SessionKey: "s1"},
	}

	// Wait for async clear
	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		ss.mu.Lock()
		cleared := len(ss.cleared)
		ss.mu.Unlock()
		if cleared > 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	ss.mu.Lock()
	defer ss.mu.Unlock()
	if len(ss.cleared) == 0 || ss.cleared[0] != "s1" {
		t.Errorf("expected ClearSessionContexts called with s1, got %v", ss.cleared)
	}
}

func TestDispatcher_ImmediateTask_ResumesWhenSessionExists(t *testing.T) {
	ss := newMockSessionStore()
	ss.data["s1|w1"] = "prior-session-id"

	mgr := &mockExecManager{
		execResult: model.WorkerExecution{ID: "exec-1", SessionID: "prior-session-id"},
		getResult:  model.WorkerExecution{ID: "exec-1", Status: model.ExecStatusCompleted, Result: "resumed!"},
	}
	d, in := newDispatcher(mgr, ss)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go d.Run(ctx)

	in <- immediateTask("s1", "w1", "follow-up")

	if !waitForExecCount(mgr, 1, 2*time.Second) {
		t.Fatal("ExecuteWorkerWithSession was not called within timeout")
	}

	mgr.mu.Lock()
	resumed := mgr.resumedWithSessionID
	mgr.mu.Unlock()

	if resumed != "prior-session-id" {
		t.Errorf("expected ExecuteWorkerWithSession with prior-session-id, got %q", resumed)
	}
}

func TestDispatcher_ImmediateTask_FreshWhenNoSession(t *testing.T) {
	ss := newMockSessionStore()
	mgr := &mockExecManager{
		execResult: model.WorkerExecution{ID: "exec-1", SessionID: "new-session"},
		getResult:  model.WorkerExecution{ID: "exec-1", Status: model.ExecStatusCompleted, Result: "fresh!"},
	}
	d, in := newDispatcher(mgr, ss)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go d.Run(ctx)

	in <- immediateTask("s1", "w1", "first message")

	if !waitForExecCount(mgr, 1, 2*time.Second) {
		t.Fatal("ExecuteWorker was not called within timeout")
	}

	mgr.mu.Lock()
	resumed := mgr.resumedWithSessionID
	mgr.mu.Unlock()

	if resumed != "" {
		t.Errorf("expected fresh start (no resume), but ExecuteWorkerWithSession was called with %q", resumed)
	}
}

func TestDispatcher_ImmediateTask_ResumeFails_FallsBackToFresh(t *testing.T) {
	ss := newMockSessionStore()
	ss.data["s1|w1"] = "broken-session-id"

	mgr := &fallbackExecManager{
		freshResult: model.WorkerExecution{ID: "exec-fresh", SessionID: "new-session"},
		getResult:   model.WorkerExecution{ID: "exec-fresh", Status: model.ExecStatusCompleted, Result: "fallback-ok"},
	}

	in := make(chan dispatcher.DispatchTask, 4)
	d := dispatcher.New(mgr, &mockTaskStore{}, ss, in)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go d.Run(ctx)

	in <- immediateTask("s1", "w1", "message after broken session")

	// Wait for execution to complete — mgr.execCount reaches 1 (fresh execute)
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if atomic.LoadInt64(&mgr.freshCount) >= 1 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if atomic.LoadInt64(&mgr.freshCount) < 1 {
		t.Fatal("fallback ExecuteWorker was never called")
	}

	// Stale session should be cleared
	deadline = time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		ss.mu.Lock()
		cleared := len(ss.cleared)
		ss.mu.Unlock()
		if cleared > 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	ss.mu.Lock()
	defer ss.mu.Unlock()
	if len(ss.cleared) == 0 {
		t.Error("expected ClearSessionContexts called after resume failure")
	}
}

func TestDispatcher_TwoTasks_SameSession_Serialized(t *testing.T) {
	blocker := make(chan struct{})
	mgr := &blockingExecManager{blocker: blocker}
	d, in := newDispatcher(mgr, newMockSessionStore())

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go d.Run(ctx)

	t1 := immediateTask("s1", "w1", "first")
	t1.TaskID = "task-1"
	t2 := immediateTask("s1", "w1", "second")
	t2.TaskID = "task-2"

	in <- t1
	in <- t2

	// Wait for first task to start blocking
	time.Sleep(50 * time.Millisecond)
	if atomic.LoadInt64(&mgr.started) != 1 {
		t.Fatalf("expected 1 execution started, got %d", atomic.LoadInt64(&mgr.started))
	}

	// Unblock first execution
	close(blocker)

	// Wait for both to complete
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if atomic.LoadInt64(&mgr.completed) >= 2 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if atomic.LoadInt64(&mgr.completed) < 2 {
		t.Errorf("expected 2 executions completed, got %d", atomic.LoadInt64(&mgr.completed))
	}
}

// --- Helper managers ---

type blockingExecManager struct {
	blocker   <-chan struct{}
	started   int64
	completed int64
}

func (m *blockingExecManager) ExecuteWorker(_ context.Context, _, _ string) (model.WorkerExecution, error) {
	atomic.AddInt64(&m.started, 1)
	<-m.blocker
	atomic.AddInt64(&m.completed, 1)
	return model.WorkerExecution{ID: "exec-x"}, nil
}
func (m *blockingExecManager) ExecuteWorkerWithSession(_ context.Context, _, _, _ string) (model.WorkerExecution, error) {
	atomic.AddInt64(&m.started, 1)
	<-m.blocker
	atomic.AddInt64(&m.completed, 1)
	return model.WorkerExecution{ID: "exec-x"}, nil
}
func (m *blockingExecManager) GetExecution(_ string) (model.WorkerExecution, error) {
	return model.WorkerExecution{ID: "exec-x", Status: model.ExecStatusCompleted, Result: "ok"}, nil
}

type fallbackExecManager struct {
	freshResult model.WorkerExecution
	getResult   model.WorkerExecution
	freshCount  int64
}

func (m *fallbackExecManager) ExecuteWorker(_ context.Context, _, _ string) (model.WorkerExecution, error) {
	atomic.AddInt64(&m.freshCount, 1)
	return m.freshResult, nil
}
func (m *fallbackExecManager) ExecuteWorkerWithSession(_ context.Context, _, _, _ string) (model.WorkerExecution, error) {
	return model.WorkerExecution{}, fmt.Errorf("session broken")
}
func (m *fallbackExecManager) GetExecution(_ string) (model.WorkerExecution, error) {
	return m.getResult, nil
}
```

- [ ] **Step 2: Verify the new tests fail (still reference old dispatcher)**

```bash
go test ./internal/dispatcher/... -v 2>&1 | head -30
```
Expected: compile error (import `msgsender` removed, `d.Out()` removed — this confirms we're TDD'ing the change)

---

### Task 4: Simplify `dispatcher.go`

**Files:**
- Modify: `internal/dispatcher/dispatcher.go`

- [ ] **Step 1: Replace `dispatcher.go` with the simplified version**

Replace the entire file:

```go
package dispatcher

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/robobee/core/internal/model"
	"github.com/robobee/core/internal/platform"
)

const (
	pollInterval = 2 * time.Second
	pollTimeout  = 30 * time.Minute
)

// ExecutionManager manages worker executions.
type ExecutionManager interface {
	ExecuteWorker(ctx context.Context, workerID, input string) (model.WorkerExecution, error)
	ExecuteWorkerWithSession(ctx context.Context, workerID, input, sessionID string) (model.WorkerExecution, error)
	GetExecution(id string) (model.WorkerExecution, error)
}

// TaskStore is the subset of store.TaskStore used by the Dispatcher.
type TaskStore interface {
	SetExecution(ctx context.Context, taskID, executionID, status string) error
}

// SessionStore is the subset of store.SessionStore used by the Dispatcher.
type SessionStore interface {
	GetSessionContext(ctx context.Context, sessionKey, agentID string) (string, error)
	UpsertSessionContext(ctx context.Context, sessionKey, agentID, sessionID string) error
	ClearSessionContexts(ctx context.Context, sessionKey string) error
}

type queueState struct {
	executing    bool
	pendingTasks []DispatchTask
	lastReplyTo  platform.InboundMessage
}

type internalResult struct {
	queueKey string
	task     DispatchTask
}

// Dispatcher serializes worker executions per (SessionKey, WorkerID).
type Dispatcher struct {
	ctx          context.Context
	manager      ExecutionManager
	taskStore    TaskStore
	sessionStore SessionStore
	in           <-chan DispatchTask
	results      chan internalResult
	queues       map[string]*queueState
}

// New constructs a Dispatcher.
func New(manager ExecutionManager, taskStore TaskStore, sessionStore SessionStore, in <-chan DispatchTask) *Dispatcher {
	return &Dispatcher{
		manager:      manager,
		taskStore:    taskStore,
		sessionStore: sessionStore,
		in:           in,
		results:      make(chan internalResult, 64),
		queues:       make(map[string]*queueState),
	}
}

// Run processes tasks until ctx is cancelled. Call in a goroutine.
func (d *Dispatcher) Run(ctx context.Context) {
	d.ctx = ctx
	for {
		select {
		case task, ok := <-d.in:
			if !ok {
				return
			}
			d.handleInbound(task)
		case res := <-d.results:
			d.handleResult(res)
		case <-ctx.Done():
			return
		}
	}
}

func queueKey(sessionKey, workerID string) string {
	return sessionKey + "|" + workerID
}

func (d *Dispatcher) handleInbound(task DispatchTask) {
	if task.TaskType == "clear" {
		if err := d.sessionStore.ClearSessionContexts(d.ctx, task.SessionKey); err != nil {
			log.Printf("dispatcher: clear session contexts for %s: %v", task.SessionKey, err)
		}
		prefix := task.SessionKey + "|"
		for key := range d.queues {
			if strings.HasPrefix(key, prefix) {
				delete(d.queues, key)
			}
		}
		return
	}

	key := queueKey(task.SessionKey, task.WorkerID)
	state, ok := d.queues[key]
	if !ok {
		state = &queueState{}
		d.queues[key] = state
	}

	replyTo := task.ReplyTo
	if task.ReplySessionKey != "" {
		replyTo.SessionKey = task.ReplySessionKey
	}

	if !state.executing {
		state.executing = true
		state.lastReplyTo = replyTo
		go d.executeAsync(d.ctx, key, task, replyTo)
	} else {
		state.pendingTasks = append(state.pendingTasks, task)
		state.lastReplyTo = replyTo
	}
}

// buildInstruction prepends task metadata to the instruction so workers
// can call mark_task_success/failed and send_message via MCP.
func buildInstruction(task DispatchTask) string {
	if task.TaskID == "" {
		return task.Instruction
	}
	return fmt.Sprintf("[系统元数据] task_id=%s message_id=%s\n\n%s",
		task.TaskID, task.MessageID, task.Instruction)
}

func (d *Dispatcher) executeAsync(ctx context.Context, key string, task DispatchTask, replyTo platform.InboundMessage) {
	var exec model.WorkerExecution
	var err error

	instruction := buildInstruction(task)

	if task.TaskType == model.TaskTypeImmediate {
		sessionID, sessErr := d.sessionStore.GetSessionContext(ctx, task.SessionKey, task.WorkerID)
		if sessErr != nil {
			log.Printf("dispatcher: get session context error: %v", sessErr)
		}
		if sessionID != "" {
			log.Printf("dispatcher: resuming session=%s for task %s", sessionID, task.TaskID)
			exec, err = d.manager.ExecuteWorkerWithSession(ctx, task.WorkerID, instruction, sessionID)
			if err != nil {
				log.Printf("dispatcher: resume error (falling back to fresh): %v", err)
				if clearErr := d.sessionStore.ClearSessionContexts(ctx, task.SessionKey); clearErr != nil {
					log.Printf("dispatcher: clear stale session contexts for %s: %v", task.SessionKey, clearErr)
				}
				exec, err = d.manager.ExecuteWorker(ctx, task.WorkerID, instruction)
			}
			goto execStarted
		}
	}

	log.Printf("dispatcher: executing worker %s for task %s", task.WorkerID, task.TaskID)
	exec, err = d.manager.ExecuteWorker(ctx, task.WorkerID, instruction)

execStarted:
	if err != nil {
		log.Printf("dispatcher: execute error: %v", err)
		d.results <- internalResult{queueKey: key, task: task}
		return
	}

	if task.TaskID != "" {
		d.taskStore.SetExecution(ctx, task.TaskID, exec.ID, model.TaskStatusRunning) //nolint:errcheck
	}
	d.waitForResult(ctx, exec.ID, task.TaskID, task.SessionKey, task.WorkerID)
	d.results <- internalResult{queueKey: key, task: task}
}

func (d *Dispatcher) waitForResult(ctx context.Context, executionID, taskID, sessionKey, workerID string) {
	deadline := time.Now().Add(pollTimeout)
	lastStatus := ""
	for time.Now().Before(deadline) {
		exec, err := d.manager.GetExecution(executionID)
		if err != nil {
			log.Printf("dispatcher: poll error execID=%s: %v", executionID, err)
			return
		}
		if string(exec.Status) != lastStatus {
			log.Printf("dispatcher: polling execID=%s status=%s", executionID, exec.Status)
			lastStatus = string(exec.Status)
		}
		switch exec.Status {
		case model.ExecStatusCompleted:
			// Persist session_id for future resume (only on success).
			// Terminal task status is set by the worker via mark_task_success.
			if sessionKey != "" && workerID != "" {
				if err := d.sessionStore.UpsertSessionContext(ctx, sessionKey, workerID, exec.SessionID); err != nil {
					log.Printf("dispatcher: upsert session context: %v", err)
				}
			}
			return
		case model.ExecStatusFailed:
			// Terminal task status is set by the worker via mark_task_failed.
			return
		}
		select {
		case <-time.After(pollInterval):
		case <-ctx.Done():
			return
		}
	}
}

func (d *Dispatcher) handleResult(res internalResult) {
	state, ok := d.queues[res.queueKey]
	if !ok {
		return
	}

	if len(state.pendingTasks) > 0 {
		next := state.pendingTasks[0]
		state.pendingTasks = state.pendingTasks[1:]
		nextReplyTo := next.ReplyTo
		if next.ReplySessionKey != "" {
			nextReplyTo.SessionKey = next.ReplySessionKey
		}
		state.lastReplyTo = nextReplyTo
		go d.executeAsync(d.ctx, res.queueKey, next, nextReplyTo)
	} else {
		state.executing = false
		delete(d.queues, res.queueKey)
	}
}
```

- [ ] **Step 2: Run dispatcher tests**

```bash
go test ./internal/dispatcher/... -v
```
Expected: all PASS

- [ ] **Step 3: Run all tests to check for compilation errors**

```bash
go build ./...
```
Expected: compile errors in `cmd/server/app.go` referencing `msgsender` and `disp.Out()` — expected, will be fixed in Chunk 4

- [ ] **Step 4: Commit**

```bash
git add internal/dispatcher/dispatcher.go internal/dispatcher/dispatcher_test.go
git commit -m "refactor(dispatcher): remove message sending, add task metadata injection"
```

---

## Chunk 3: MCP Tools and Server Wiring

### Task 5: Add three new MCP tools

**Files:**
- Modify: `internal/mcp/tools.go`
- Modify: `internal/mcp/tools_test.go`

- [ ] **Step 1: Write failing tests for the new tools**

The test file needs these additional imports (add to the existing import block in `tools_test.go`):

```go
"context"
"database/sql"
"sync"

"github.com/robobee/core/internal/platform"
```

Also: **delete the existing `setupMCPServer` helper** and rename all its call sites to `setupMCPServerWithMessaging`. The old helper calls `mcp.NewServer(ws, mgr, ts)` which will no longer compile after Step 3 updates `NewServer` to require 5 arguments.

Add to `internal/mcp/tools_test.go`:

```go
func setupMCPServerWithMessaging(t *testing.T) *mcp.MCPServer {
	t.Helper()
	db, err := store.InitDB(t.TempDir() + "/test.db")
	if err != nil {
		t.Fatalf("InitDB: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	ws := store.NewWorkerStore(db)
	es := store.NewExecutionStore(db)
	ts := store.NewTaskStore(db)
	ms := store.NewMessageStore(db)
	mgr := worker.NewManager(
		config.WorkersConfig{BaseDir: t.TempDir()},
		config.RuntimeConfig{ClaudeCode: config.RuntimeEntry{Binary: "claude"}},
		ws, es,
	)
	senders := make(map[string]platform.PlatformSenderAdapter)
	return mcp.NewServer(ws, mgr, ts, ms, senders)
}

// --- mark_task_success ---

func TestCallTool_MarkTaskSuccess(t *testing.T) {
	s := setupMCPServerWithMessaging(t)

	// Seed prerequisite rows via the existing create_task tool
	// First need a worker and message
	workerResult, _ := s.CallTool("create_worker", mustMarshal(t, map[string]any{"name": "W"}))
	w := workerResult.(model.Worker)

	// Insert a platform message directly (no MCP tool for this)
	// Use the task store via create_task which needs message_id
	// We insert the message directly via db access — but setupMCPServerWithMessaging
	// doesn't expose the db. Use create_task with a real message_id seeded via SQL.
	// Instead, create a task using message store helper. But we only have the server.
	// Workaround: use create_task — it validates message_id exists via FK.
	// Since SQLite FK enforcement may be off, try creating a task directly:
	taskResult, err := s.CallTool("create_task", mustMarshal(t, map[string]any{
		"message_id":  "msg-fake",
		"worker_id":   w.ID,
		"instruction": "do something",
		"type":        "immediate",
	}))
	if err != nil {
		t.Fatalf("create_task: %v", err)
	}
	taskMap := taskResult.(map[string]string)
	taskID := taskMap["task_id"]

	result, err := s.CallTool("mark_task_success", mustMarshal(t, map[string]any{
		"task_id": taskID,
	}))
	if err != nil {
		t.Fatalf("mark_task_success: %v", err)
	}
	m := result.(map[string]string)
	if m["status"] != "completed" {
		t.Errorf("expected status=completed, got %q", m["status"])
	}
	if m["task_id"] != taskID {
		t.Errorf("expected task_id=%s, got %q", taskID, m["task_id"])
	}
}

func TestCallTool_MarkTaskSuccess_MissingTaskID(t *testing.T) {
	s := setupMCPServerWithMessaging(t)
	_, err := s.CallTool("mark_task_success", mustMarshal(t, map[string]any{}))
	if err == nil {
		t.Error("expected error for missing task_id")
	}
}

// --- mark_task_failed ---

func TestCallTool_MarkTaskFailed(t *testing.T) {
	s := setupMCPServerWithMessaging(t)

	workerResult, _ := s.CallTool("create_worker", mustMarshal(t, map[string]any{"name": "W2"}))
	w := workerResult.(model.Worker)
	taskResult, err := s.CallTool("create_task", mustMarshal(t, map[string]any{
		"message_id":  "msg-fake2",
		"worker_id":   w.ID,
		"instruction": "do something",
		"type":        "immediate",
	}))
	if err != nil {
		t.Fatalf("create_task: %v", err)
	}
	taskMap := taskResult.(map[string]string)
	taskID := taskMap["task_id"]

	result, err := s.CallTool("mark_task_failed", mustMarshal(t, map[string]any{
		"task_id": taskID,
		"reason":  "network timeout",
	}))
	if err != nil {
		t.Fatalf("mark_task_failed: %v", err)
	}
	m := result.(map[string]string)
	if m["status"] != "failed" {
		t.Errorf("expected status=failed, got %q", m["status"])
	}
	if m["reason"] != "network timeout" {
		t.Errorf("expected reason=network timeout, got %q", m["reason"])
	}
}

func TestCallTool_MarkTaskFailed_NoReason(t *testing.T) {
	s := setupMCPServerWithMessaging(t)

	workerResult, _ := s.CallTool("create_worker", mustMarshal(t, map[string]any{"name": "W3"}))
	w := workerResult.(model.Worker)
	taskResult, _ := s.CallTool("create_task", mustMarshal(t, map[string]any{
		"message_id":  "msg-fake3",
		"worker_id":   w.ID,
		"instruction": "x",
		"type":        "immediate",
	}))
	taskMap := taskResult.(map[string]string)

	result, err := s.CallTool("mark_task_failed", mustMarshal(t, map[string]any{
		"task_id": taskMap["task_id"],
	}))
	if err != nil {
		t.Fatalf("mark_task_failed (no reason): %v", err)
	}
	m := result.(map[string]string)
	if m["status"] != "failed" {
		t.Errorf("expected status=failed, got %q", m["status"])
	}
}

// --- send_message ---

type mockSender struct {
	sent []platform.OutboundMessage
	mu   sync.Mutex
}

func (s *mockSender) Send(_ context.Context, msg platform.OutboundMessage) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sent = append(s.sent, msg)
	return nil
}

func setupMCPServerWithSender(t *testing.T, senderID string, sender platform.PlatformSenderAdapter) (*mcp.MCPServer, *sql.DB) {
	t.Helper()
	db, err := store.InitDB(t.TempDir() + "/test.db")
	if err != nil {
		t.Fatalf("InitDB: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	ws := store.NewWorkerStore(db)
	es := store.NewExecutionStore(db)
	ts := store.NewTaskStore(db)
	ms := store.NewMessageStore(db)
	mgr := worker.NewManager(
		config.WorkersConfig{BaseDir: t.TempDir()},
		config.RuntimeConfig{ClaudeCode: config.RuntimeEntry{Binary: "claude"}},
		ws, es,
	)
	senders := map[string]platform.PlatformSenderAdapter{senderID: sender}
	return mcp.NewServer(ws, mgr, ts, ms, senders), db
}

func TestCallTool_SendMessage_CallsSender(t *testing.T) {
	mock := &mockSender{}
	s, db := setupMCPServerWithSender(t, "feishu", mock)
	ctx := context.Background()

	// Insert a platform message directly
	ms := store.NewMessageStore(db)
	ms.Create(ctx, "msg-send-1", "feishu:chat1:userA", "feishu", "hello", `{"event":{"message":{"chat_id":"c1","chat_type":"p2p","message_id":"m1","message_type":"text","content":"{\"text\":\"hi\"}"}}}`, "", 0) //nolint

	result, err := s.CallTool("send_message", mustMarshal(t, map[string]any{
		"message_id": "msg-send-1",
		"content":    "Task done!",
	}))
	if err != nil {
		t.Fatalf("send_message: %v", err)
	}
	m := result.(map[string]string)
	if m["status"] != "sent" {
		t.Errorf("expected status=sent, got %q", m["status"])
	}

	mock.mu.Lock()
	defer mock.mu.Unlock()
	if len(mock.sent) == 0 {
		t.Fatal("expected sender.Send to be called")
	}
	if mock.sent[0].Content != "Task done!" {
		t.Errorf("expected content 'Task done!', got %q", mock.sent[0].Content)
	}
}

func TestCallTool_SendMessage_MissingMessageID(t *testing.T) {
	s := setupMCPServerWithMessaging(t)
	_, err := s.CallTool("send_message", mustMarshal(t, map[string]any{
		"content": "hello",
	}))
	if err == nil {
		t.Error("expected error for missing message_id")
	}
}

func TestCallTool_SendMessage_MissingContent(t *testing.T) {
	s := setupMCPServerWithMessaging(t)
	_, err := s.CallTool("send_message", mustMarshal(t, map[string]any{
		"message_id": "msg-x",
	}))
	if err == nil {
		t.Error("expected error for missing content")
	}
}

func TestCallTool_SendMessage_UnknownPlatform(t *testing.T) {
	// No sender registered for the platform
	s, db := setupMCPServerWithSender(t, "feishu", &mockSender{})
	ctx := context.Background()

	ms := store.NewMessageStore(db)
	ms.Create(ctx, "msg-unk", "dingtalk:c1:u1", "dingtalk", "hi", `{}`, "", 0) //nolint

	_, err := s.CallTool("send_message", mustMarshal(t, map[string]any{
		"message_id": "msg-unk",
		"content":    "hello",
	}))
	if err == nil {
		t.Error("expected error for unregistered platform sender")
	}
}

func TestCallTool_SendMessage_MessageNotFound(t *testing.T) {
	s := setupMCPServerWithMessaging(t)
	_, err := s.CallTool("send_message", mustMarshal(t, map[string]any{
		"message_id": "nonexistent-msg",
		"content":    "hello",
	}))
	if err == nil {
		t.Error("expected error for nonexistent message_id")
	}
}

// --- Schema count ---

func TestToolSchemas_Count_AfterNewTools(t *testing.T) {
	schemas := mcp.ToolSchemas()
	if len(schemas) != 11 {
		t.Errorf("expected 11 tool schemas (8 existing + 3 new), got %d", len(schemas))
	}
}

func TestToolSchemas_IncludesNewTools(t *testing.T) {
	schemas := mcp.ToolSchemas()
	names := make(map[string]bool)
	for _, s := range schemas {
		names[s.Name] = true
	}
	for _, want := range []string{"mark_task_success", "mark_task_failed", "send_message"} {
		if !names[want] {
			t.Errorf("missing tool schema: %s", want)
		}
	}
}
```

Also update the existing schema count test (it currently expects 8):

Find and change `TestToolSchemas_Count` in the existing tests:

```go
// Change existing test from:
func TestToolSchemas_Count(t *testing.T) {
	schemas := mcp.ToolSchemas()
	if len(schemas) != 8 {
		t.Errorf("expected 8 tool schemas, got %d", len(schemas))
	}
}
// To: delete this test (replaced by TestToolSchemas_Count_AfterNewTools above)
```

Delete the old `TestToolSchemas_Count` function from the test file and replace the `setupMCPServer` helper with the new `setupMCPServerWithMessaging` (also update all existing tests that call `setupMCPServer` to call `setupMCPServerWithMessaging`).

- [ ] **Step 2: Run tests to verify they fail**

```bash
go test ./internal/mcp/... -v 2>&1 | head -30
```
Expected: compile errors (new `NewServer` signature not yet updated, new tool handlers not yet added)

- [ ] **Step 3: Update `internal/mcp/server.go`**

Update the `MCPServer` struct and `NewServer`:

```go
// Add these imports if not present:
// "github.com/robobee/core/internal/platform"
// "github.com/robobee/core/internal/store"

type MCPServer struct {
	workerStore  *store.WorkerStore
	manager      *worker.Manager
	taskStore    *store.TaskStore
	messageStore *store.MessageStore
	senders      map[string]platform.PlatformSenderAdapter

	mu       sync.Mutex
	sessions map[string]chan rpcResponse
}

func NewServer(
	ws      *store.WorkerStore,
	mgr     *worker.Manager,
	ts      *store.TaskStore,
	ms      *store.MessageStore,
	senders map[string]platform.PlatformSenderAdapter,
) *MCPServer {
	return &MCPServer{
		workerStore:  ws,
		manager:      mgr,
		taskStore:    ts,
		messageStore: ms,
		senders:      senders,
		sessions:     make(map[string]chan rpcResponse),
	}
}
```

- [ ] **Step 4: Add new tool schemas and handlers to `internal/mcp/tools.go`**

Update the `toolSchemas()` function comment and append the three new schemas:

```go
// ToolSchemas returns the JSON Schema definitions for all MCP tools.
// Exported so tests can verify the count and structure.
func ToolSchemas() []toolSchema {
	return toolSchemas()
}
```

Append to the `toolSchemas()` slice (after the `cancel_task` entry):

```go
		{
			Name:        "mark_task_success",
			Description: "Mark a task as successfully completed",
			InputSchema: map[string]any{
				"type":     "object",
				"required": []string{"task_id"},
				"properties": map[string]any{
					"task_id": map[string]string{"type": "string", "description": "Task ID to mark as completed"},
				},
			},
		},
		{
			Name:        "mark_task_failed",
			Description: "Mark a task as failed",
			InputSchema: map[string]any{
				"type":     "object",
				"required": []string{"task_id"},
				"properties": map[string]any{
					"task_id": map[string]string{"type": "string", "description": "Task ID to mark as failed"},
					"reason":  map[string]string{"type": "string", "description": "Optional failure reason"},
				},
			},
		},
		{
			Name:        "send_message",
			Description: "Send a message to the user on the originating platform. Use message_id from the task metadata to identify the reply target.",
			InputSchema: map[string]any{
				"type":     "object",
				"required": []string{"message_id", "content"},
				"properties": map[string]any{
					"message_id": map[string]string{"type": "string", "description": "ID of the originating platform message (resolves platform and reply context)"},
					"content":    map[string]string{"type": "string", "description": "Message content to send"},
				},
			},
		},
```

Add to the `callTool` switch:

```go
	case "mark_task_success":
		return s.toolMarkTaskSuccess(args)
	case "mark_task_failed":
		return s.toolMarkTaskFailed(args)
	case "send_message":
		return s.toolSendMessage(args)
```

Add the three handler methods (add to `tools.go`):

```go
func (s *MCPServer) toolMarkTaskSuccess(args json.RawMessage) (any, error) {
	var params struct {
		TaskID string `json:"task_id"`
	}
	if err := json.Unmarshal(args, &params); err != nil {
		return nil, fmt.Errorf("invalid args: %w", err)
	}
	if params.TaskID == "" {
		return nil, fmt.Errorf("task_id is required")
	}
	if err := s.taskStore.UpdateStatus(context.Background(), params.TaskID, model.TaskStatusCompleted); err != nil {
		return nil, fmt.Errorf("mark task success: %w", err)
	}
	return map[string]string{"task_id": params.TaskID, "status": "completed"}, nil
}

func (s *MCPServer) toolMarkTaskFailed(args json.RawMessage) (any, error) {
	var params struct {
		TaskID string `json:"task_id"`
		Reason string `json:"reason"`
	}
	if err := json.Unmarshal(args, &params); err != nil {
		return nil, fmt.Errorf("invalid args: %w", err)
	}
	if params.TaskID == "" {
		return nil, fmt.Errorf("task_id is required")
	}
	if err := s.taskStore.UpdateStatus(context.Background(), params.TaskID, model.TaskStatusFailed); err != nil {
		return nil, fmt.Errorf("mark task failed: %w", err)
	}
	return map[string]string{"task_id": params.TaskID, "status": "failed", "reason": params.Reason}, nil
}

func (s *MCPServer) toolSendMessage(args json.RawMessage) (any, error) {
	var params struct {
		MessageID string `json:"message_id"`
		Content   string `json:"content"`
	}
	if err := json.Unmarshal(args, &params); err != nil {
		return nil, fmt.Errorf("invalid args: %w", err)
	}
	if params.MessageID == "" {
		return nil, fmt.Errorf("message_id is required")
	}
	if params.Content == "" {
		return nil, fmt.Errorf("content is required")
	}

	stored, err := s.messageStore.GetByID(context.Background(), params.MessageID)
	if err != nil {
		return nil, fmt.Errorf("get message: %w", err)
	}

	sender, ok := s.senders[stored.Platform]
	if !ok {
		return nil, fmt.Errorf("no sender registered for platform %q", stored.Platform)
	}

	outbound := platform.OutboundMessage{
		ReplyTo: platform.InboundMessage{
			Platform:   stored.Platform,
			SessionKey: stored.SessionKey,
			Raw:        stored.Raw,
		},
		Content: params.Content,
	}
	if err := sender.Send(context.Background(), outbound); err != nil {
		return nil, fmt.Errorf("send message: %w", err)
	}
	return map[string]string{"status": "sent"}, nil
}
```

- [ ] **Step 5: Add missing imports to `tools.go`**

Ensure `tools.go` imports include `"github.com/robobee/core/internal/platform"` (for `platform.OutboundMessage`).

- [ ] **Step 6: Run MCP tests**

```bash
go test ./internal/mcp/... -v
```
Expected: all PASS

- [ ] **Step 7: Commit**

```bash
git add internal/mcp/server.go internal/mcp/tools.go internal/mcp/tools_test.go
git commit -m "feat(mcp): add mark_task_success, mark_task_failed, send_message tools"
```

---

## Chunk 4: App Wiring and msgsender Deletion

### Task 6: Update `app.go` and delete `msgsender`

**Files:**
- Modify: `cmd/server/app.go`
- Delete: `internal/msgsender/gateway.go`
- Delete: `internal/msgsender/gateway_test.go`

- [ ] **Step 1: Delete the msgsender package**

```bash
rm internal/msgsender/gateway.go internal/msgsender/gateway_test.go
```

- [ ] **Step 2: Verify deletion causes expected compile errors**

```bash
go build ./... 2>&1
```
Expected: errors in `cmd/server/app.go` referencing `msgsender` — confirms scope of changes

- [ ] **Step 3: Update `cmd/server/app.go`**

Replace `buildApp` and `buildPipeline` with the updated versions:

**`buildApp` changes:**
1. Create `sendersByPlatform` before `mcpSrv`
2. Pass `s.msgStore` and `sendersByPlatform` to `mcp.NewServer`
3. Remove `sender` from pipeline and `sender.Run(ctx)` from runners

```go
func buildApp(cfg config.Config) (*App, error) {
	if cfg.MCP.APIKey == "" {
		return nil, fmt.Errorf("mcp.api_key must be set — bee requires MCP to create tasks")
	}

	db, s, err := buildStores(cfg.Database)
	if err != nil {
		return nil, err
	}

	mgr := buildWorkerManager(cfg.Workers, cfg.Runtime, s)

	dispatchCh := make(chan dispatcher.DispatchTask, 128)

	// Create sender map before MCPServer — maps are reference types,
	// so MCPServer holds the same map and sees entries added below.
	sendersByPlatform := make(map[string]platform.PlatformSenderAdapter)

	mcpSrv := mcp.NewServer(s.workerStore, mgr, s.taskStore, s.msgStore, sendersByPlatform)

	feeder, sched := buildBee(cfg.Bee, s, dispatchCh)
	ingest, disp := buildPipeline(cfg.MessageQueue, s, mgr, dispatchCh)
	platforms := buildPlatforms(cfg.Feishu, cfg.DingTalk)

	// Populate sender map before goroutines start
	for _, p := range platforms {
		sendersByPlatform[p.ID()] = p.Sender()
	}

	// Synchronous startup recovery — must run before goroutines start
	feeder.RecoverFeeding(context.Background())
	sched.RecoverRunning(context.Background())

	runners := []func(ctx context.Context){
		func(ctx context.Context) { ingest.Run(ctx) },
		func(ctx context.Context) { feeder.Run(ctx) },
		func(ctx context.Context) { sched.Run(ctx) },
		func(ctx context.Context) { disp.Run(ctx) },
	}
	for _, p := range platforms {
		recv := p.Receiver()
		runners = append(runners, func(ctx context.Context) {
			if err := recv.Start(ctx, ingest.Dispatch); err != nil {
				log.Printf("platform receiver error: %v", err)
			}
		})
	}

	srv := buildAPIServer(cfg.MCP, s, mgr, mcpSrv)
	addr := fmt.Sprintf("%s:%d", cfg.Server.Host, cfg.Server.Port)

	return &App{db: db, server: srv, runners: runners, addr: addr}, nil
}
```

**`buildPipeline` changes** — remove msgsender:

```go
func buildPipeline(
	cfg config.MessageQueueConfig,
	s appStores,
	mgr *worker.Manager,
	dispatchCh chan dispatcher.DispatchTask,
) (*msgingest.Gateway, *dispatcher.Dispatcher) {
	ingest := msgingest.New(s.msgStore, cfg.DebounceWindow)
	disp := dispatcher.New(mgr, s.taskStore, s.sessionStore, dispatchCh)
	return ingest, disp
}
```

Also remove the `msgsender` import from `app.go`.

- [ ] **Step 4: Build to verify no compile errors**

```bash
go build ./...
```
Expected: success (no errors)

- [ ] **Step 5: Run all tests**

```bash
go test ./...
```
Expected: all PASS. The `msgsender` package no longer appears in output.

- [ ] **Step 6: Commit**

```bash
git add cmd/server/app.go
git rm internal/msgsender/gateway.go internal/msgsender/gateway_test.go
git commit -m "feat: wire MCP messaging tools, remove msgsender package"
```

---

## Final Verification

- [ ] **Run full test suite**

```bash
go test ./... -v 2>&1 | grep -E "^(ok|FAIL|---)"
```
Expected: all packages `ok`, no `FAIL`

- [ ] **Build binary**

```bash
go build ./cmd/server/...
```
Expected: success

- [ ] **Confirm msgsender is gone**

```bash
grep -r "msgsender" . --include="*.go"
```
Expected: no matches
