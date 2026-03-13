# Smart Clear Session Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Move clear session logic from a hard-coded intercept chain into the bee agent, with MCP tools for task inspection and session clearing (including process termination).

**Architecture:** Remove the existing clear interception in msgingest/feeder/dispatcher. Add `ListBySessionKey` and `CancelBySessionKey` to TaskStore. Extend the `list_tasks` MCP tool with `session_key` support. Add a new `clear_session` MCP tool that stops running worker processes, cancels tasks, clears dispatcher queues, and resets session contexts. Add `ClearSession` channel-based method to dispatcher. Wire new dependencies through MCP server.

**Tech Stack:** Go, SQLite, MCP (JSON-RPC over SSE)

---

## Chunk 1: Store Layer — New TaskStore Methods

### Task 1: Add `ListBySessionKey` to TaskStore

**Files:**
- Modify: `internal/store/task_store.go:54-71`
- Test: `internal/store/task_store_test.go`

- [ ] **Step 1: Write the failing test for ListBySessionKey**

Add to `internal/store/task_store_test.go`:

```go
func newTaskStoreWithTwoSessions(t *testing.T) (*TaskStore, func()) {
	t.Helper()
	db, err := InitDB(t.TempDir() + "/test.db")
	if err != nil {
		t.Fatalf("InitDB: %v", err)
	}
	db.Exec(`INSERT INTO workers (id,name,work_dir,status,created_at,updated_at) VALUES ('w1','W','/','idle',1,1)`)
	db.Exec(`INSERT INTO platform_messages
		(id, session_key, platform, content, raw, platform_msg_id, received_at, created_at, updated_at)
		VALUES ('m1','session-A','feishu','hi','','',1,1,1)`)
	db.Exec(`INSERT INTO platform_messages
		(id, session_key, platform, content, raw, platform_msg_id, received_at, created_at, updated_at)
		VALUES ('m2','session-B','feishu','bye','','',1,1,1)`)
	return NewTaskStore(db), func() { db.Close() }
}

func TestTaskStore_ListBySessionKey(t *testing.T) {
	ts, cleanup := newTaskStoreWithTwoSessions(t)
	defer cleanup()
	ctx := context.Background()
	now := time.Now().UnixMilli()

	// Create tasks in session-A: one pending, one running
	ts.Create(ctx, model.Task{
		MessageID: "m1", WorkerID: "w1", Instruction: "a",
		Type: model.TaskTypeImmediate, Status: model.TaskStatusPending,
		CreatedAt: now, UpdatedAt: now,
	})
	ts.Create(ctx, model.Task{
		MessageID: "m1", WorkerID: "w1", Instruction: "b",
		Type: model.TaskTypeImmediate, Status: model.TaskStatusRunning,
		CreatedAt: now, UpdatedAt: now,
	})
	// Create task in session-B
	ts.Create(ctx, model.Task{
		MessageID: "m2", WorkerID: "w1", Instruction: "c",
		Type: model.TaskTypeImmediate, Status: model.TaskStatusPending,
		CreatedAt: now, UpdatedAt: now,
	})

	// List all tasks for session-A
	tasks, err := ts.ListBySessionKey(ctx, "session-A", "")
	if err != nil {
		t.Fatalf("ListBySessionKey: %v", err)
	}
	if len(tasks) != 2 {
		t.Errorf("expected 2 tasks for session-A, got %d", len(tasks))
	}

	// List only pending tasks for session-A
	tasks, err = ts.ListBySessionKey(ctx, "session-A", "pending")
	if err != nil {
		t.Fatalf("ListBySessionKey (pending): %v", err)
	}
	if len(tasks) != 1 {
		t.Errorf("expected 1 pending task for session-A, got %d", len(tasks))
	}

	// List with comma-separated status
	tasks, err = ts.ListBySessionKey(ctx, "session-A", "pending,running")
	if err != nil {
		t.Fatalf("ListBySessionKey (pending,running): %v", err)
	}
	if len(tasks) != 2 {
		t.Errorf("expected 2 tasks for session-A with pending,running, got %d", len(tasks))
	}

	// List for session-B
	tasks, err = ts.ListBySessionKey(ctx, "session-B", "")
	if err != nil {
		t.Fatalf("ListBySessionKey session-B: %v", err)
	}
	if len(tasks) != 1 {
		t.Errorf("expected 1 task for session-B, got %d", len(tasks))
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd /Users/tengyongzhi/work/robobee && go test ./internal/store/ -run TestTaskStore_ListBySessionKey -v`
Expected: FAIL — `ts.ListBySessionKey undefined`

- [ ] **Step 3: Implement ListBySessionKey**

Add to `internal/store/task_store.go` after the `ListByMessageID` method (after line 71):

```go
// ListBySessionKey returns tasks whose originating message belongs to the given session.
// status supports comma-separated values (e.g., "pending,running"); empty means all.
func (s *TaskStore) ListBySessionKey(ctx context.Context, sessionKey, status string) ([]model.Task, error) {
	q := `SELECT t.id, t.message_id, t.worker_id, t.instruction, t.type, t.status,
	             t.scheduled_at, t.cron_expr, t.next_run_at, t.reply_session_key, t.execution_id,
	             t.created_at, t.updated_at
	      FROM tasks t
	      JOIN platform_messages pm ON t.message_id = pm.id
	      WHERE pm.session_key = ?`
	args := []any{sessionKey}

	if status != "" {
		statuses := strings.Split(status, ",")
		placeholders := strings.Repeat("?,", len(statuses))
		placeholders = placeholders[:len(placeholders)-1]
		q += " AND t.status IN (" + placeholders + ")"
		for _, st := range statuses {
			args = append(args, strings.TrimSpace(st))
		}
	}

	q += " ORDER BY t.created_at DESC"
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("list tasks by session key: %w", err)
	}
	defer rows.Close()
	return scanTasks(rows)
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd /Users/tengyongzhi/work/robobee && go test ./internal/store/ -run TestTaskStore_ListBySessionKey -v`
Expected: PASS

### Task 1b: Update `ListByMessageID` to support comma-separated status

**Files:**
- Modify: `internal/store/task_store.go:55-71`
- Test: `internal/store/task_store_test.go`

The existing `ListByMessageID` uses `AND status = ?` which doesn't support comma-separated values. Since the `list_tasks` MCP tool schema advertises comma-separated status support, both query methods must handle it.

- [ ] **Step 1: Write failing test**

Add to `internal/store/task_store_test.go`:

```go
func TestTaskStore_ListByMessageID_CommaSeparatedStatus(t *testing.T) {
	ts, cleanup := newTaskStoreForTest(t)
	defer cleanup()
	ctx := context.Background()
	now := time.Now().UnixMilli()

	ts.Create(ctx, model.Task{
		MessageID: "m1", WorkerID: "w1", Instruction: "a",
		Type: model.TaskTypeImmediate, Status: model.TaskStatusPending,
		CreatedAt: now, UpdatedAt: now,
	})
	ts.Create(ctx, model.Task{
		MessageID: "m1", WorkerID: "w1", Instruction: "b",
		Type: model.TaskTypeImmediate, Status: model.TaskStatusRunning,
		CreatedAt: now, UpdatedAt: now,
	})
	ts.Create(ctx, model.Task{
		MessageID: "m1", WorkerID: "w1", Instruction: "c",
		Type: model.TaskTypeImmediate, Status: model.TaskStatusCompleted,
		CreatedAt: now, UpdatedAt: now,
	})

	tasks, err := ts.ListByMessageID(ctx, "m1", "pending,running")
	if err != nil {
		t.Fatalf("ListByMessageID: %v", err)
	}
	if len(tasks) != 2 {
		t.Errorf("expected 2 tasks (pending+running), got %d", len(tasks))
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd /Users/tengyongzhi/work/robobee && go test ./internal/store/ -run TestTaskStore_ListByMessageID_CommaSeparatedStatus -v`
Expected: FAIL — returns 0 tasks (literal `"pending,running"` matches nothing)

- [ ] **Step 3: Extract shared status filter helper and update ListByMessageID**

Add a helper function in `internal/store/task_store.go`:

```go
// appendStatusFilter appends an IN clause for comma-separated status values.
// If status is empty, nothing is appended.
func appendStatusFilter(q string, args []any, status string) (string, []any) {
	if status == "" {
		return q, args
	}
	statuses := strings.Split(status, ",")
	placeholders := strings.Repeat("?,", len(statuses))
	placeholders = placeholders[:len(placeholders)-1]
	q += " AND t.status IN (" + placeholders + ")"
	for _, st := range statuses {
		args = append(args, strings.TrimSpace(st))
	}
	return q, args
}
```

Update `ListByMessageID` to use the helper:

```go
func (s *TaskStore) ListByMessageID(ctx context.Context, messageID, status string) ([]model.Task, error) {
	q := `SELECT t.id, t.message_id, t.worker_id, t.instruction, t.type, t.status,
	             t.scheduled_at, t.cron_expr, t.next_run_at, t.reply_session_key, t.execution_id,
	             t.created_at, t.updated_at
	      FROM tasks t WHERE t.message_id = ?`
	args := []any{messageID}
	q, args = appendStatusFilter(q, args, status)
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("list tasks: %w", err)
	}
	defer rows.Close()
	return scanTasks(rows)
}
```

Also update `ListBySessionKey` (from Task 1) to use the same helper:

```go
func (s *TaskStore) ListBySessionKey(ctx context.Context, sessionKey, status string) ([]model.Task, error) {
	q := `SELECT t.id, t.message_id, t.worker_id, t.instruction, t.type, t.status,
	             t.scheduled_at, t.cron_expr, t.next_run_at, t.reply_session_key, t.execution_id,
	             t.created_at, t.updated_at
	      FROM tasks t
	      JOIN platform_messages pm ON t.message_id = pm.id
	      WHERE pm.session_key = ?`
	args := []any{sessionKey}
	q, args = appendStatusFilter(q, args, status)
	q += " ORDER BY t.created_at DESC"
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("list tasks by session key: %w", err)
	}
	defer rows.Close()
	return scanTasks(rows)
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd /Users/tengyongzhi/work/robobee && go test ./internal/store/ -run TestTaskStore_ListByMessageID_CommaSeparatedStatus -v`
Expected: PASS

---

### Task 2: Add `CancelBySessionKey` to TaskStore

**Files:**
- Modify: `internal/store/task_store.go`
- Test: `internal/store/task_store_test.go`

- [ ] **Step 1: Write the failing test for CancelBySessionKey**

Add to `internal/store/task_store_test.go`:

```go
func TestTaskStore_CancelBySessionKey(t *testing.T) {
	ts, cleanup := newTaskStoreWithTwoSessions(t)
	defer cleanup()
	ctx := context.Background()
	now := time.Now().UnixMilli()

	// Create tasks in session-A: pending + running + completed
	ts.Create(ctx, model.Task{
		MessageID: "m1", WorkerID: "w1", Instruction: "a",
		Type: model.TaskTypeImmediate, Status: model.TaskStatusPending,
		CreatedAt: now, UpdatedAt: now,
	})
	ts.Create(ctx, model.Task{
		MessageID: "m1", WorkerID: "w1", Instruction: "b",
		Type: model.TaskTypeImmediate, Status: model.TaskStatusRunning,
		CreatedAt: now, UpdatedAt: now,
	})
	ts.Create(ctx, model.Task{
		MessageID: "m1", WorkerID: "w1", Instruction: "c",
		Type: model.TaskTypeImmediate, Status: model.TaskStatusCompleted,
		CreatedAt: now, UpdatedAt: now,
	})
	// Task in session-B (should not be affected)
	ts.Create(ctx, model.Task{
		MessageID: "m2", WorkerID: "w1", Instruction: "d",
		Type: model.TaskTypeImmediate, Status: model.TaskStatusPending,
		CreatedAt: now, UpdatedAt: now,
	})

	n, err := ts.CancelBySessionKey(ctx, "session-A")
	if err != nil {
		t.Fatalf("CancelBySessionKey: %v", err)
	}
	if n != 2 {
		t.Errorf("expected 2 cancelled (pending+running), got %d", n)
	}

	// Verify: session-A completed task untouched
	tasksA, _ := ts.ListBySessionKey(ctx, "session-A", "completed")
	if len(tasksA) != 1 {
		t.Errorf("completed task should be untouched, got %d", len(tasksA))
	}

	// Verify: session-A cancelled tasks
	cancelledA, _ := ts.ListBySessionKey(ctx, "session-A", "cancelled")
	if len(cancelledA) != 2 {
		t.Errorf("expected 2 cancelled tasks, got %d", len(cancelledA))
	}

	// Verify: session-B unaffected
	tasksB, _ := ts.ListBySessionKey(ctx, "session-B", "pending")
	if len(tasksB) != 1 {
		t.Errorf("session-B task should be unaffected, got %d", len(tasksB))
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd /Users/tengyongzhi/work/robobee && go test ./internal/store/ -run TestTaskStore_CancelBySessionKey -v`
Expected: FAIL — `ts.CancelBySessionKey undefined`

- [ ] **Step 3: Implement CancelBySessionKey**

Add to `internal/store/task_store.go` after `CancelByWorkerID` (after line 185):

```go
// CancelBySessionKey cancels all pending/running tasks for a given session.
// Returns the number of tasks cancelled.
func (s *TaskStore) CancelBySessionKey(ctx context.Context, sessionKey string) (int64, error) {
	res, err := s.db.ExecContext(ctx,
		`UPDATE tasks SET status = 'cancelled', updated_at = ?
		 WHERE message_id IN (SELECT id FROM platform_messages WHERE session_key = ?)
		   AND status IN ('pending', 'running')`,
		time.Now().UnixMilli(), sessionKey)
	if err != nil {
		return 0, fmt.Errorf("cancel tasks by session key: %w", err)
	}
	return res.RowsAffected()
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd /Users/tengyongzhi/work/robobee && go test ./internal/store/ -run TestTaskStore_CancelBySessionKey -v`
Expected: PASS

- [ ] **Step 5: Run all store tests**

Run: `cd /Users/tengyongzhi/work/robobee && go test ./internal/store/ -v`
Expected: All PASS

- [ ] **Step 6: Commit**

```bash
git add internal/store/task_store.go internal/store/task_store_test.go
git commit -m "feat(store): add ListBySessionKey and CancelBySessionKey to TaskStore"
```

---

## Chunk 2: Dispatcher — Add ClearSession Channel Method

### Task 3: Add `clearCh` and `ClearSession` to Dispatcher

**Files:**
- Modify: `internal/dispatcher/dispatcher.go:50-105`
- Test: `internal/dispatcher/dispatcher_test.go`

- [ ] **Step 1: Write the failing test for ClearSession**

Add to `internal/dispatcher/dispatcher_test.go`:

```go
func TestDispatcher_ClearSession_ClearsQueueAndSessionContexts(t *testing.T) {
	ss := newMockSessionStore()
	blocker := make(chan struct{})
	mgr := &blockingExecManager{blocker: blocker}

	in := make(chan dispatcher.DispatchTask, 4)
	d := dispatcher.New(mgr, &mockTaskStore{}, ss, in)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go d.Run(ctx)

	// Send a task to create a queue entry
	t1 := immediateTask("s1", "w1", "first")
	t1.TaskID = "task-1"
	in <- t1

	// Wait for first task to start
	time.Sleep(50 * time.Millisecond)

	// Queue a second task (pending in queue)
	t2 := immediateTask("s1", "w1", "second")
	t2.TaskID = "task-2"
	in <- t2
	time.Sleep(20 * time.Millisecond)

	// Call ClearSession — should clear the pending queue entry and session contexts
	d.ClearSession("s1")
	time.Sleep(50 * time.Millisecond)

	// Unblock the first task
	close(blocker)
	time.Sleep(100 * time.Millisecond)

	// Session contexts should have been cleared
	ss.mu.Lock()
	cleared := ss.cleared
	ss.mu.Unlock()
	if len(cleared) == 0 || cleared[0] != "s1" {
		t.Errorf("expected ClearSessionContexts called with s1, got %v", cleared)
	}

	// Second task should NOT have executed (queue was cleared)
	if atomic.LoadInt64(&mgr.completed) > 1 {
		t.Errorf("expected at most 1 execution (second should be cleared from queue), got %d", atomic.LoadInt64(&mgr.completed))
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd /Users/tengyongzhi/work/robobee && go test ./internal/dispatcher/ -run TestDispatcher_ClearSession -v`
Expected: FAIL — `d.ClearSession undefined`

- [ ] **Step 3: Implement ClearSession**

Modify `internal/dispatcher/dispatcher.go`:

1. Add `clearCh` field to `Dispatcher` struct (line 57):

```go
type Dispatcher struct {
	ctx          context.Context
	manager      ExecutionManager
	taskStore    TaskStore
	sessionStore SessionStore
	in           <-chan DispatchTask
	results      chan internalResult
	queues       map[string]*queueState
	clearCh      chan string
}
```

2. Initialize `clearCh` in `New` (line 68):

```go
func New(manager ExecutionManager, taskStore TaskStore, sessionStore SessionStore, in <-chan DispatchTask) *Dispatcher {
	return &Dispatcher{
		manager:      manager,
		taskStore:    taskStore,
		sessionStore: sessionStore,
		in:           in,
		results:      make(chan internalResult, 64),
		queues:       make(map[string]*queueState),
		clearCh:      make(chan string, 8),
	}
}
```

3. Add `clearCh` case to `Run` select loop (after line 84):

```go
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
		case sessionKey := <-d.clearCh:
			d.clearQueues(sessionKey)
		case <-ctx.Done():
			return
		}
	}
}
```

4. Add `ClearSession` and `clearQueues` methods:

```go
// ClearSession removes all queued tasks for the given session and clears session contexts.
// Safe to call from any goroutine — uses a buffered channel to signal the Run loop.
func (d *Dispatcher) ClearSession(sessionKey string) {
	select {
	case d.clearCh <- sessionKey:
	default:
		log.Printf("dispatcher: clearCh full, dropping clear for %s", sessionKey)
	}
}

func (d *Dispatcher) clearQueues(sessionKey string) {
	prefix := sessionKey + "|"
	for key := range d.queues {
		if strings.HasPrefix(key, prefix) {
			delete(d.queues, key)
		}
	}
	if err := d.sessionStore.ClearSessionContexts(d.ctx, sessionKey); err != nil {
		log.Printf("dispatcher: clear session contexts for %s: %v", sessionKey, err)
	}
}
```

5. Remove the old `TaskType == "clear"` branch from `handleInbound` (lines 95-106). The new `handleInbound` starts directly at `key := queueKey(...)`:

```go
func (d *Dispatcher) handleInbound(task DispatchTask) {
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
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd /Users/tengyongzhi/work/robobee && go test ./internal/dispatcher/ -run TestDispatcher_ClearSession -v`
Expected: PASS

- [ ] **Step 5: Update existing clear tests**

The old `TestDispatcher_ClearTask_ClearsSessionContexts` and `TestDispatcher_InstructionInjection_SkippedWhenNoTaskID` tests send `TaskType: "clear"` via the dispatch channel. Update them to use `ClearSession` instead.

Replace `TestDispatcher_ClearTask_ClearsSessionContexts` with:

```go
func TestDispatcher_ClearSession_ClearsSessionContexts(t *testing.T) {
	ss := newMockSessionStore()
	d, _ := newDispatcher(&mockExecManager{}, ss)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go d.Run(ctx)

	d.ClearSession("s1")

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
```

Remove `TestDispatcher_InstructionInjection_SkippedWhenNoTaskID` entirely (it tested that clear tasks don't call ExecuteWorker — that flow no longer exists).

- [ ] **Step 6: Run all dispatcher tests**

Run: `cd /Users/tengyongzhi/work/robobee && go test ./internal/dispatcher/ -v`
Expected: All PASS

- [ ] **Step 7: Commit**

```bash
git add internal/dispatcher/dispatcher.go internal/dispatcher/dispatcher_test.go
git commit -m "feat(dispatcher): add ClearSession channel method, remove old clear branch"
```

---

## Chunk 3: MCP Server — Extend list_tasks and Add clear_session

### Task 4: Add new dependencies to MCP Server

**Files:**
- Modify: `internal/mcp/server.go:50-78`

- [ ] **Step 1: Add interfaces and fields to MCP server**

Add the interfaces above `MCPServer` struct in `internal/mcp/server.go`:

```go
// ExecutionStopper can kill a running worker process by execution ID.
type ExecutionStopper interface {
	StopExecution(executionID string) error
}

// SessionClearer clears dispatcher queues and session contexts for a session.
type SessionClearer interface {
	ClearSession(sessionKey string)
}
```

Add fields to `MCPServer` struct:

```go
type MCPServer struct {
	workerStore    *store.WorkerStore
	manager        *worker.Manager
	taskStore      *store.TaskStore
	messageStore   *store.MessageStore
	senders        map[string]platform.PlatformSenderAdapter
	execStopper    ExecutionStopper
	sessionClearer SessionClearer

	mu       sync.Mutex
	sessions map[string]chan rpcResponse
}
```

Update `NewServer` signature:

```go
func NewServer(
	ws *store.WorkerStore,
	mgr *worker.Manager,
	ts *store.TaskStore,
	ms *store.MessageStore,
	senders map[string]platform.PlatformSenderAdapter,
	execStopper ExecutionStopper,
	sessionClearer SessionClearer,
) *MCPServer {
	return &MCPServer{
		workerStore:    ws,
		manager:        mgr,
		taskStore:      ts,
		messageStore:   ms,
		senders:        senders,
		execStopper:    execStopper,
		sessionClearer: sessionClearer,
		sessions:       make(map[string]chan rpcResponse),
	}
}
```

- [ ] **Step 2: Verify compilation fails (callers need updating)**

Run: `cd /Users/tengyongzhi/work/robobee && go build ./...`
Expected: FAIL — callers pass wrong number of args to `NewServer`

- [ ] **Step 3: Fix callers — update app.go**

In `cmd/server/app.go`, update the `NewServer` call at line 78:

```go
mcpSrv := mcp.NewServer(s.workerStore, mgr, s.taskStore, s.msgStore, sendersByPlatform, mgr, disp)
```

But `disp` isn't created yet at line 78. Reorder: move the `mcpSrv` creation after `buildPipeline`. The relevant section of `buildApp` (lines 72-81) becomes:

```go
	dispatchCh := make(chan dispatcher.DispatchTask, 128)

	sendersByPlatform := make(map[string]platform.PlatformSenderAdapter)

	feeder, sched := buildBee(cfg.Bee, s, dispatchCh)
	ingest, disp := buildPipeline(cfg.MessageQueue, s, mgr, dispatchCh)

	mcpSrv := mcp.NewServer(s.workerStore, mgr, s.taskStore, s.msgStore, sendersByPlatform, mgr, disp)
```

- [ ] **Step 4: Fix callers — update test helpers**

In `internal/mcp/tools_test.go`, update both `setupMCPServerWithMessaging` and `setupMCPServerWithSender` to pass `nil` for the new args (they don't test clear_session yet):

`setupMCPServerWithMessaging`:
```go
return mcp.NewServer(ws, mgr, ts, ms, senders, nil, nil)
```

`setupMCPServerWithSender`:
```go
return mcp.NewServer(ws, mgr, ts, ms, senders, nil, nil), db
```

- [ ] **Step 5: Run build to verify compilation**

Run: `cd /Users/tengyongzhi/work/robobee && go build ./...`
Expected: PASS

- [ ] **Step 6: Run all tests to verify nothing broke**

Run: `cd /Users/tengyongzhi/work/robobee && go test ./... 2>&1 | tail -30`
Expected: All PASS

- [ ] **Step 7: Commit**

```bash
git add internal/mcp/server.go internal/mcp/tools_test.go cmd/server/app.go
git commit -m "feat(mcp): add ExecutionStopper and SessionClearer dependencies to MCP server"
```

### Task 5: Extend `list_tasks` with `session_key` support

**Files:**
- Modify: `internal/mcp/tools.go:106-116, 382-401`
- Test: `internal/mcp/tools_test.go`

- [ ] **Step 1: Write failing tests for extended list_tasks**

Add to `internal/mcp/tools_test.go`:

```go
func TestCallTool_ListTasks_BySessionKey(t *testing.T) {
	s, db := setupMCPServerWithSender(t, "feishu", &mockSender{})
	ctx := context.Background()
	ms := store.NewMessageStore(db)
	ms.Create(ctx, "msg-sk1", "session-X", "feishu", "hi", `{}`, "", 0) //nolint

	workerResult, _ := s.CallTool("create_worker", mustMarshal(t, map[string]any{"name": "W"}))
	w := workerResult.(model.Worker)

	s.CallTool("create_task", mustMarshal(t, map[string]any{
		"message_id": "msg-sk1", "worker_id": w.ID,
		"instruction": "task1", "type": "immediate",
	}))

	result, err := s.CallTool("list_tasks", mustMarshal(t, map[string]any{
		"session_key": "session-X",
	}))
	if err != nil {
		t.Fatalf("list_tasks by session_key: %v", err)
	}
	tasks := result.([]model.Task)
	if len(tasks) != 1 {
		t.Errorf("expected 1 task, got %d", len(tasks))
	}
}

func TestCallTool_ListTasks_BothParams_Error(t *testing.T) {
	s := setupMCPServerWithMessaging(t)
	_, err := s.CallTool("list_tasks", mustMarshal(t, map[string]any{
		"message_id":  "msg-1",
		"session_key": "session-X",
	}))
	if err == nil {
		t.Error("expected error when both message_id and session_key provided")
	}
}

func TestCallTool_ListTasks_NoParams_Error(t *testing.T) {
	s := setupMCPServerWithMessaging(t)
	_, err := s.CallTool("list_tasks", mustMarshal(t, map[string]any{}))
	if err == nil {
		t.Error("expected error when neither message_id nor session_key provided")
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd /Users/tengyongzhi/work/robobee && go test ./internal/mcp/ -run TestCallTool_ListTasks -v`
Expected: FAIL

- [ ] **Step 3: Update list_tasks schema and handler**

In `internal/mcp/tools.go`, update the `list_tasks` schema (lines 106-116):

```go
		{
			Name:        "list_tasks",
			Description: "List tasks filtered by message_id or session_key (mutually exclusive), optionally filtered by status (supports comma-separated values like 'pending,running')",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"message_id":  map[string]string{"type": "string", "description": "Filter by message ID"},
					"session_key": map[string]string{"type": "string", "description": "Filter by session key (mutually exclusive with message_id)"},
					"status":      map[string]string{"type": "string", "description": "Optional status filter, supports comma-separated values e.g. 'pending,running'"},
				},
			},
		},
```

Update the `toolListTasks` handler (lines 382-401):

```go
func (s *MCPServer) toolListTasks(args json.RawMessage) (any, error) {
	var params struct {
		MessageID  string `json:"message_id"`
		SessionKey string `json:"session_key"`
		Status     string `json:"status"`
	}
	if err := json.Unmarshal(args, &params); err != nil {
		return nil, fmt.Errorf("invalid args: %w", err)
	}
	if params.MessageID != "" && params.SessionKey != "" {
		return nil, fmt.Errorf("message_id and session_key are mutually exclusive")
	}
	if params.MessageID == "" && params.SessionKey == "" {
		return nil, fmt.Errorf("either message_id or session_key is required")
	}

	var tasks []model.Task
	var err error
	if params.SessionKey != "" {
		tasks, err = s.taskStore.ListBySessionKey(context.Background(), params.SessionKey, params.Status)
	} else {
		tasks, err = s.taskStore.ListByMessageID(context.Background(), params.MessageID, params.Status)
	}
	if err != nil {
		return nil, fmt.Errorf("list tasks: %w", err)
	}
	if tasks == nil {
		tasks = []model.Task{}
	}
	return tasks, nil
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd /Users/tengyongzhi/work/robobee && go test ./internal/mcp/ -run TestCallTool_ListTasks -v`
Expected: All PASS

- [ ] **Step 5: Commit**

```bash
git add internal/mcp/tools.go internal/mcp/tools_test.go
git commit -m "feat(mcp): extend list_tasks with session_key filter and comma-separated status"
```

### Task 6: Add `clear_session` MCP tool

**Files:**
- Modify: `internal/mcp/tools.go`
- Test: `internal/mcp/tools_test.go`

- [ ] **Step 1: Write failing tests for clear_session**

Add to `internal/mcp/tools_test.go`:

```go
// --- Mock implementations for clear_session ---

type mockExecStopper struct {
	mu      sync.Mutex
	stopped []string
}

func (m *mockExecStopper) StopExecution(executionID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.stopped = append(m.stopped, executionID)
	return nil
}

type mockSessionClearer struct {
	mu      sync.Mutex
	cleared []string
}

func (m *mockSessionClearer) ClearSession(sessionKey string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.cleared = append(m.cleared, sessionKey)
}

func setupMCPServerWithClear(t *testing.T) (*mcp.MCPServer, *sql.DB, *mockExecStopper, *mockSessionClearer) {
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
	stopper := &mockExecStopper{}
	clearer := &mockSessionClearer{}
	return mcp.NewServer(ws, mgr, ts, ms, senders, stopper, clearer), db, stopper, clearer
}

func TestCallTool_ClearSession_NoActiveTasks(t *testing.T) {
	s, _, _, clearer := setupMCPServerWithClear(t)

	result, err := s.CallTool("clear_session", mustMarshal(t, map[string]any{
		"session_key": "session-X",
	}))
	if err != nil {
		t.Fatalf("clear_session: %v", err)
	}
	m := result.(map[string]any)
	if m["cleared"] != true {
		t.Errorf("expected cleared=true, got %v", m["cleared"])
	}

	clearer.mu.Lock()
	defer clearer.mu.Unlock()
	if len(clearer.cleared) != 1 || clearer.cleared[0] != "session-X" {
		t.Errorf("expected ClearSession called with session-X, got %v", clearer.cleared)
	}
}

func TestCallTool_ClearSession_CancelsAndStopsTasks(t *testing.T) {
	s, db, stopper, clearer := setupMCPServerWithClear(t)
	ctx := context.Background()

	ms := store.NewMessageStore(db)
	ms.Create(ctx, "msg-c1", "session-Y", "feishu", "hi", `{}`, "", 0) //nolint

	workerResult, _ := s.CallTool("create_worker", mustMarshal(t, map[string]any{"name": "W"}))
	w := workerResult.(model.Worker)

	// Create a running task with execution_id
	ts := store.NewTaskStore(db)
	id, _ := ts.Create(ctx, model.Task{
		MessageID: "msg-c1", WorkerID: w.ID, Instruction: "long task",
		Type: model.TaskTypeImmediate, Status: model.TaskStatusRunning,
		CreatedAt: 1, UpdatedAt: 1,
	})
	ts.SetExecution(ctx, id, "exec-running-1", model.TaskStatusRunning)

	// Create a pending task
	ts.Create(ctx, model.Task{
		MessageID: "msg-c1", WorkerID: w.ID, Instruction: "queued task",
		Type: model.TaskTypeImmediate, Status: model.TaskStatusPending,
		CreatedAt: 1, UpdatedAt: 1,
	})

	result, err := s.CallTool("clear_session", mustMarshal(t, map[string]any{
		"session_key": "session-Y",
	}))
	if err != nil {
		t.Fatalf("clear_session: %v", err)
	}
	m := result.(map[string]any)
	cancelled, ok := m["cancelled_tasks"].(int64)
	if !ok || cancelled < 1 {
		t.Errorf("expected cancelled_tasks >= 1, got %v", m["cancelled_tasks"])
	}

	// StopExecution should have been called for the running task
	stopper.mu.Lock()
	defer stopper.mu.Unlock()
	if len(stopper.stopped) != 1 || stopper.stopped[0] != "exec-running-1" {
		t.Errorf("expected StopExecution(exec-running-1), got %v", stopper.stopped)
	}

	// ClearSession should have been called
	clearer.mu.Lock()
	defer clearer.mu.Unlock()
	if len(clearer.cleared) != 1 || clearer.cleared[0] != "session-Y" {
		t.Errorf("expected ClearSession(session-Y), got %v", clearer.cleared)
	}
}

func TestCallTool_ClearSession_MissingSessionKey(t *testing.T) {
	s, _, _, _ := setupMCPServerWithClear(t)
	_, err := s.CallTool("clear_session", mustMarshal(t, map[string]any{}))
	if err == nil {
		t.Error("expected error for missing session_key")
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd /Users/tengyongzhi/work/robobee && go test ./internal/mcp/ -run TestCallTool_ClearSession -v`
Expected: FAIL — `unknown tool: clear_session`

- [ ] **Step 3: Add clear_session schema and handler**

Add the schema to `toolSchemas()` in `internal/mcp/tools.go` (after the `send_message` schema, before the closing `}`):

```go
		{
			Name:        "clear_session",
			Description: "Cancel all active tasks (terminating running worker processes), clear dispatcher queues, and reset all session contexts for the given session. Use this to fully reset a conversation session.",
			InputSchema: map[string]any{
				"type":     "object",
				"required": []string{"session_key"},
				"properties": map[string]any{
					"session_key": map[string]string{"type": "string", "description": "The session key to clear"},
				},
			},
		},
```

Add the case in `callTool` switch (after `send_message`):

```go
	case "clear_session":
		return s.toolClearSession(args)
```

Add the handler:

```go
func (s *MCPServer) toolClearSession(args json.RawMessage) (any, error) {
	var params struct {
		SessionKey string `json:"session_key"`
	}
	if err := json.Unmarshal(args, &params); err != nil {
		return nil, fmt.Errorf("invalid args: %w", err)
	}
	if params.SessionKey == "" {
		return nil, fmt.Errorf("session_key is required")
	}

	ctx := context.Background()

	// Step 1: Collect running tasks with execution IDs (before cancelling)
	runningTasks, err := s.taskStore.ListBySessionKey(ctx, params.SessionKey, "running")
	if err != nil {
		return nil, fmt.Errorf("list running tasks: %w", err)
	}

	// Step 2: Stop running worker processes
	for _, t := range runningTasks {
		if t.ExecutionID != "" {
			if err := s.execStopper.StopExecution(t.ExecutionID); err != nil {
				log.Printf("clear_session: stop execution %s: %v", t.ExecutionID, err)
			}
		}
	}

	// Step 3: Cancel all pending/running tasks in DB
	cancelled, err := s.taskStore.CancelBySessionKey(ctx, params.SessionKey)
	if err != nil {
		return nil, fmt.Errorf("cancel tasks: %w", err)
	}

	// Step 4: Clear dispatcher queues + session contexts
	if s.sessionClearer != nil {
		s.sessionClearer.ClearSession(params.SessionKey)
	}

	return map[string]any{
		"cancelled_tasks": cancelled,
		"cleared":         true,
	}, nil
}
```

**Important:** Add `"log"` to the import list at the top of `tools.go`. The current imports do not include it and compilation will fail without it.

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd /Users/tengyongzhi/work/robobee && go test ./internal/mcp/ -run TestCallTool_ClearSession -v`
Expected: All PASS

- [ ] **Step 5: Update the schema count test**

In `TestToolSchemas_Count_AfterNewTools`, update the expected count from 11 to 12:

```go
func TestToolSchemas_Count_AfterNewTools(t *testing.T) {
	schemas := mcp.ToolSchemas()
	if len(schemas) != 12 {
		t.Errorf("expected 12 tool schemas, got %d", len(schemas))
	}
}
```

- [ ] **Step 6: Run all MCP tests**

Run: `cd /Users/tengyongzhi/work/robobee && go test ./internal/mcp/ -v`
Expected: All PASS

- [ ] **Step 7: Commit**

```bash
git add internal/mcp/tools.go internal/mcp/tools_test.go
git commit -m "feat(mcp): add clear_session tool with process termination"
```

---

## Chunk 4: Remove Old Clear Chain and Update Bee Prompt

### Task 7: Remove clear interception from msgingest gateway

**Files:**
- Modify: `internal/msgingest/gateway.go:17-23, 102-106, 253-259`
- Test: `internal/msgingest/gateway_test.go`

- [ ] **Step 1: Remove CommandClear and command detection**

In `internal/msgingest/gateway.go`:

1. Remove the `CommandType` type, `CommandNone`, and `CommandClear` constants entirely (lines 17-23). They are now dead code.

3. Remove the command detection block in `Dispatch` (lines 101-106). The code after dedup should flow directly into debounce:

```go
	// Accumulate into debounce state.
	state, ok := g.sessions[msg.SessionKey]
```

4. Remove the `detectCommand` function entirely (lines 253-259).

5. Remove the `handleCommand` method entirely (lines 142-182) — it is now dead code.

6. The `IngestedMessage` struct becomes:

```go
type IngestedMessage struct {
	MsgID      string
	SessionKey string
	Platform   string
	Content    string
	ReplyTo    platform.InboundMessage
}
```

- [ ] **Step 2: Update gateway tests**

In `internal/msgingest/gateway_test.go`:

- `TestGateway_Command_EmitsOnOut`: Change to verify "clear" now goes through debounce as a normal message:

```go
func TestGateway_ClearMessage_DebounceAsNormal(t *testing.T) {
	st := newMock()
	g := msgingest.New(st, 100*time.Millisecond)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go g.Run(ctx)

	g.Dispatch(inbound("s1", "clear", "cmd-1"))

	select {
	case msg := <-g.Out():
		if msg.Content != "clear" {
			t.Fatalf("expected content 'clear', got %q", msg.Content)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("timeout waiting for debounced message")
	}
}
```

- `TestGateway_Command_InterruptsDebounce`: Change to verify "hello" + "clear" are now merged:

```go
func TestGateway_ClearMessage_MergedWithDebounce(t *testing.T) {
	st := newMock()
	g := msgingest.New(st, 200*time.Millisecond)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go g.Run(ctx)

	g.Dispatch(inbound("s1", "hello", "m1"))
	g.Dispatch(inbound("s1", "clear", "cmd-1"))

	select {
	case msg := <-g.Out():
		if msg.Content != "hello\n\n---\n\nclear" {
			t.Fatalf("expected merged content, got %q", msg.Content)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("timeout waiting for debounced message")
	}

	// No extra messages
	select {
	case extra := <-g.Out():
		t.Fatalf("unexpected extra message: %+v", extra)
	case <-time.After(300 * time.Millisecond):
	}
}
```

- `TestGateway_BatchWrite_Error_CommandPath`: Remove this test entirely (there is no command path anymore).

- `TestGateway_Debounce_EmitsSingleMergedMessage`: Remove the `msg.Command` check (the `Command` field no longer exists).

- Any other test referencing `msgingest.CommandClear`, `msgingest.CommandNone`, or `msg.Command` must be updated to remove those references. Grep for these symbols and fix all occurrences.

- [ ] **Step 3: Run gateway tests**

Run: `cd /Users/tengyongzhi/work/robobee && go test ./internal/msgingest/ -v`
Expected: All PASS

- [ ] **Step 4: Commit**

```bash
git add internal/msgingest/gateway.go internal/msgingest/gateway_test.go
git commit -m "refactor(msgingest): remove CommandClear detection, clear flows as normal message"
```

### Task 8: Remove clear interception from feeder

**Files:**
- Modify: `internal/bee/feeder.go:24-43, 94-122`
- Test: `internal/bee/feeder_test.go`

- [ ] **Step 1: Remove clear logic from Feeder**

In `internal/bee/feeder.go`:

1. Remove `clearCh` field from `Feeder` struct and `clearCh` param from `NewFeeder`:

```go
type Feeder struct {
	msgStore     *store.MessageStore
	taskStore    *store.TaskStore
	sessionStore *store.SessionStore
	runner       BeeRunner
	cfg          config.BeeConfig
}

func NewFeeder(ms *store.MessageStore, ts *store.TaskStore, ss *store.SessionStore, runner BeeRunner, cfg config.BeeConfig) *Feeder {
	return &Feeder{
		msgStore:     ms,
		taskStore:    ts,
		sessionStore: ss,
		runner:       runner,
		cfg:          cfg,
	}
}
```

2. Remove the `dispatcher` and `platform` imports (no longer needed — check if `platform` is used elsewhere in the file; it is not).

3. In `tick()`, remove the clear message separation (lines 94-122). Replace with direct use of `msgs`:

```go
func (f *Feeder) tick(ctx context.Context) {
	count, _ := f.msgStore.CountReceived(ctx)
	if count > f.cfg.Feeder.QueueWarnThreshold {
		log.Printf("feeder: WARNING: %d unprocessed messages in queue (threshold: %d)", count, f.cfg.Feeder.QueueWarnThreshold)
	}

	msgs, err := f.msgStore.ClaimBatch(ctx, f.cfg.Feeder.BatchSize)
	if err != nil {
		log.Printf("feeder: claim batch: %v", err)
		return
	}
	if len(msgs) == 0 {
		return
	}

	if err := WriteCLAUDEMD(f.cfg.WorkDir, f.cfg.Persona); err != nil {
		log.Printf("feeder: write CLAUDE.md: %v", err)
		f.rollback(ctx, msgs)
		return
	}

	groups := make(map[string][]store.ClaimedMessage)
	for _, m := range msgs {
		groups[m.SessionKey] = append(groups[m.SessionKey], m)
	}

	var wg sync.WaitGroup
	for sessionKey, group := range groups {
		wg.Add(1)
		go func(sessionKey string, group []store.ClaimedMessage) {
			defer wg.Done()
			f.processBeeGroup(ctx, sessionKey, group)
		}(sessionKey, group)
	}
	wg.Wait()
}
```

4. Remove `detectClear` function (line 202-204).

- [ ] **Step 2: Update feeder tests**

In `internal/bee/feeder_test.go`, update `newFeeder` to match new signature:

```go
func newFeeder(ms *store.MessageStore, ts *store.TaskStore, ss *store.SessionStore, runner bee.BeeRunner) *bee.Feeder {
	cfg := config.BeeConfig{}
	cfg.Feeder.Interval = 50 * time.Millisecond
	cfg.Feeder.BatchSize = 10
	cfg.Feeder.Timeout = 5 * time.Second
	cfg.Feeder.QueueWarnThreshold = 100
	cfg.WorkDir = "/tmp"
	return bee.NewFeeder(ms, ts, ss, runner, cfg)
}
```

Remove the `dispatcher` import from feeder_test.go.

- [ ] **Step 3: Update app.go wiring**

In `cmd/server/app.go`, update `buildBee` to remove `dispatchCh` param:

```go
func buildBee(cfg config.BeeConfig, s appStores, dispatchCh chan dispatcher.DispatchTask) (*bee.Feeder, *taskscheduler.Scheduler) {
	beeProcess := bee.NewBeeProcess(cfg)
	feeder := bee.NewFeeder(s.msgStore, s.taskStore, s.sessionStore, beeProcess, cfg)
	sched := taskscheduler.New(s.taskStore, dispatchCh, cfg.Feeder.Interval)
	return feeder, sched
}
```

Note: `dispatchCh` is still needed by `taskscheduler.New`, so the `buildBee` signature keeps it. Only the `NewFeeder` call loses it.

- [ ] **Step 4: Run all tests**

Run: `cd /Users/tengyongzhi/work/robobee && go test ./... 2>&1 | tail -30`
Expected: All PASS

- [ ] **Step 5: Commit**

```bash
git add internal/bee/feeder.go internal/bee/feeder_test.go cmd/server/app.go
git commit -m "refactor(bee): remove clear interception from feeder, clear goes through bee"
```

### Task 9: Clean up dead code — DispatchTask docs, InsertClearSentinel

**Files:**
- Modify: `internal/dispatcher/task.go`

- [ ] **Step 1: Update DispatchTask comment**

In `internal/dispatcher/task.go`, update the `TaskType` comment to remove `"clear"`:

```go
type DispatchTask struct {
	TaskID          string
	WorkerID        string
	SessionKey      string
	Instruction     string
	ReplyTo         platform.InboundMessage
	TaskType        string                 // "immediate"|"countdown"|"scheduled"
	MessageID       string
	ReplySessionKey string
}
```

Also remove `InsertClearSentinel` from `internal/store/message_store.go` (lines 105-118) and its test `TestMessageStore_InsertClearSentinel_UnaffectedByDedupSchema` from `internal/store/message_store_test.go` — this method is dead code (never called from production code).

- [ ] **Step 2: Run all tests**

Run: `cd /Users/tengyongzhi/work/robobee && go test ./... 2>&1 | tail -30`
Expected: All PASS

- [ ] **Step 3: Commit**

```bash
git add internal/dispatcher/task.go internal/store/message_store.go internal/store/message_store_test.go
git commit -m "refactor: remove dead clear-related code (DispatchTask docs, InsertClearSentinel)"
```

### Task 10: Update bee system prompt for clear handling

**Files:**
- Modify: `internal/bee/bee_process.go` (or wherever persona/prompt is configured)

Note: The bee's behavior is controlled by the `persona` string written to `CLAUDE.md`. The prompt configuration is in `config.BeeConfig.Persona`. This is set by the user in their config file, not in code. However, we should document the expected prompt additions.

- [ ] **Step 1: Check where persona is configured**

Look at the config to determine where to add the clear handling instructions. The persona is in the config file, so we need to update the default persona or document the addition.

Since the persona comes from user configuration (`cfg.Bee.Persona`), the clear handling instructions should be appended programmatically in `WriteCLAUDEMD` or in the prompt built by `buildPrompt`.

The best approach is to append the clear instructions to the `buildPrompt` output. However, looking at the code, `buildPrompt` generates per-message prompts, not system instructions. The system instructions are in `CLAUDE.md` via `WriteCLAUDEMD`.

Add the clear instructions as a constant appended to the persona in `WriteCLAUDEMD`:

In `internal/bee/bee_process.go`, add a constant and modify `WriteCLAUDEMD`:

```go
const clearInstructions = `

## 清除上下文处理

当用户发送的消息表示想要清除/重置对话（例如"clear"、"清除"、"重置上下文"等）时：

1. 首先调用 list_tasks，传入 session_key 和 status "pending,running" 检查是否有活跃任务。

2. 如果没有活跃任务：
   - 调用 clear_session，传入 session_key
   - 调用 send_message 确认："已清除会话上下文。"

3. 如果有活跃任务：
   - 调用 send_message 告知用户："当前有 N 个任务正在处理中，清除上下文将终止这些任务。是否确认清除？"
   - 等待用户下一条消息。

4. 如果用户确认（再次发送 "clear" 或类似确认消息）：
   - 调用 clear_session（将自动取消所有任务、终止运行中的 worker 进程、清除所有会话上下文）
   - 调用 send_message 确认："已终止所有任务并清除会话上下文。"
`

func WriteCLAUDEMD(workDir, persona string) error {
	if err := os.MkdirAll(workDir, 0o755); err != nil {
		return fmt.Errorf("mkdir bee workdir: %w", err)
	}
	path := filepath.Join(workDir, "CLAUDE.md")
	content := persona + clearInstructions
	return os.WriteFile(path, []byte(content), 0o644)
}
```

- [ ] **Step 2: Run all tests**

Run: `cd /Users/tengyongzhi/work/robobee && go test ./... 2>&1 | tail -30`
Expected: All PASS

- [ ] **Step 3: Commit**

```bash
git add internal/bee/bee_process.go
git commit -m "feat(bee): add clear session handling instructions to bee prompt"
```

---

## Chunk 5: Final Verification

### Task 11: Full test suite and build verification

- [ ] **Step 1: Run full test suite**

Run: `cd /Users/tengyongzhi/work/robobee && go test ./... -v 2>&1 | tail -50`
Expected: All PASS

- [ ] **Step 2: Run go vet**

Run: `cd /Users/tengyongzhi/work/robobee && go vet ./...`
Expected: No issues

- [ ] **Step 3: Build the binary**

Run: `cd /Users/tengyongzhi/work/robobee && go build ./cmd/server/`
Expected: Builds successfully

- [ ] **Step 4: Review git log**

Run: `cd /Users/tengyongzhi/work/robobee && git log --oneline -10`
Expected: Clean commit history with all changes
