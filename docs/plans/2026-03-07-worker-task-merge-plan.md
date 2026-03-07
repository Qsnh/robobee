# Worker-Task Merge Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Merge the Task entity into Worker so each Worker is a self-contained unit with its own prompt, trigger type, and execution history.

**Architecture:** Delete the Task model/store/handler entirely. Move all Task fields (plan→prompt, trigger_type, cron_expression, recipients, requires_approval) onto Worker. Rename task_executions→worker_executions with worker_id FK. Update all API routes, business logic, scheduler, mail handler, and frontend.

**Tech Stack:** Go 1.25 (Gin, SQLite3, robfig/cron), React 19 (React Router, TanStack Query, Tailwind, shadcn/ui)

---

### Task 1: Update Worker Model

**Files:**
- Modify: `internal/model/worker.go`

**Step 1: Add new fields and trigger types to Worker model**

Replace the entire file with:

```go
package model

import (
	"encoding/json"
	"time"
)

type RuntimeType string

const (
	RuntimeClaudeCode RuntimeType = "claude_code"
	RuntimeCodex      RuntimeType = "codex"
)

type WorkerStatus string

const (
	WorkerStatusIdle    WorkerStatus = "idle"
	WorkerStatusWorking WorkerStatus = "working"
	WorkerStatusError   WorkerStatus = "error"
)

type TriggerType string

const (
	TriggerCron    TriggerType = "cron"
	TriggerMessage TriggerType = "message"
)

type Worker struct {
	ID               string          `json:"id" db:"id"`
	Name             string          `json:"name" db:"name"`
	Description      string          `json:"description" db:"description"`
	Prompt           string          `json:"prompt" db:"prompt"`
	Email            string          `json:"email" db:"email"`
	RuntimeType      RuntimeType     `json:"runtime_type" db:"runtime_type"`
	WorkDir          string          `json:"work_dir" db:"work_dir"`
	TriggerType      TriggerType     `json:"trigger_type" db:"trigger_type"`
	CronExpression   string          `json:"cron_expression,omitempty" db:"cron_expression"`
	Recipients       json.RawMessage `json:"recipients" db:"recipients"`
	RequiresApproval bool            `json:"requires_approval" db:"requires_approval"`
	Status           WorkerStatus    `json:"status" db:"status"`
	CreatedAt        time.Time       `json:"created_at" db:"created_at"`
	UpdatedAt        time.Time       `json:"updated_at" db:"updated_at"`
}
```

**Step 2: Commit**

```bash
git add internal/model/worker.go
git commit -m "refactor: add task fields to Worker model (prompt, trigger_type, cron, recipients, requires_approval)"
```

---

### Task 2: Update Execution Model

**Files:**
- Modify: `internal/model/execution.go`

**Step 1: Rename TaskExecution to WorkerExecution, change TaskID→WorkerID, add TriggerInput**

Replace the entire file with:

```go
package model

import "time"

type ExecutionStatus string

const (
	ExecStatusPending          ExecutionStatus = "pending"
	ExecStatusRunning          ExecutionStatus = "running"
	ExecStatusAwaitingApproval ExecutionStatus = "awaiting_approval"
	ExecStatusApproved         ExecutionStatus = "approved"
	ExecStatusRejected         ExecutionStatus = "rejected"
	ExecStatusCompleted        ExecutionStatus = "completed"
	ExecStatusFailed           ExecutionStatus = "failed"
)

type WorkerExecution struct {
	ID           string          `json:"id" db:"id"`
	WorkerID     string          `json:"worker_id" db:"worker_id"`
	SessionID    string          `json:"session_id" db:"session_id"`
	TriggerInput string          `json:"trigger_input,omitempty" db:"trigger_input"`
	Status       ExecutionStatus `json:"status" db:"status"`
	Result       string          `json:"result,omitempty" db:"result"`
	AIProcessPID int             `json:"ai_process_pid,omitempty" db:"ai_process_pid"`
	StartedAt    *time.Time      `json:"started_at,omitempty" db:"started_at"`
	CompletedAt  *time.Time      `json:"completed_at,omitempty" db:"completed_at"`
}
```

**Step 2: Commit**

```bash
git add internal/model/execution.go
git commit -m "refactor: rename TaskExecution to WorkerExecution, add trigger_input field"
```

---

### Task 3: Delete Task Model

**Files:**
- Delete: `internal/model/task.go`

**Step 1: Delete the file**

```bash
rm internal/model/task.go
```

**Step 2: Commit**

```bash
git add internal/model/task.go
git commit -m "refactor: remove Task model"
```

---

### Task 4: Update Database Schema

**Files:**
- Modify: `internal/store/db.go`

**Step 1: Replace schema — drop tasks table, update workers table, rename task_executions**

Replace the entire file with:

```go
package store

import (
	"database/sql"

	_ "github.com/mattn/go-sqlite3"
)

func InitDB(dbPath string) (*sql.DB, error) {
	db, err := sql.Open("sqlite3", dbPath+"?_journal_mode=WAL&_foreign_keys=on")
	if err != nil {
		return nil, err
	}

	if err := migrate(db); err != nil {
		db.Close()
		return nil, err
	}

	return db, nil
}

func migrate(db *sql.DB) error {
	// Drop old tables to rebuild schema (dev only, no production data)
	drops := []string{
		"DROP TABLE IF EXISTS worker_memories",
		"DROP TABLE IF EXISTS emails",
		"DROP TABLE IF EXISTS task_executions",
		"DROP TABLE IF EXISTS tasks",
		"DROP TABLE IF EXISTS worker_executions",
		"DROP TABLE IF EXISTS workers",
	}
	for _, drop := range drops {
		if _, err := db.Exec(drop); err != nil {
			return err
		}
	}

	schema := `
	CREATE TABLE IF NOT EXISTS workers (
		id TEXT PRIMARY KEY,
		name TEXT NOT NULL,
		description TEXT NOT NULL DEFAULT '',
		prompt TEXT NOT NULL DEFAULT '',
		email TEXT NOT NULL UNIQUE,
		runtime_type TEXT NOT NULL DEFAULT 'claude_code',
		work_dir TEXT NOT NULL,
		trigger_type TEXT NOT NULL DEFAULT 'message',
		cron_expression TEXT NOT NULL DEFAULT '',
		recipients TEXT NOT NULL DEFAULT '[]',
		requires_approval INTEGER NOT NULL DEFAULT 0,
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
		ai_process_pid INTEGER NOT NULL DEFAULT 0,
		started_at DATETIME,
		completed_at DATETIME,
		FOREIGN KEY (worker_id) REFERENCES workers(id) ON DELETE CASCADE
	);

	CREATE TABLE IF NOT EXISTS emails (
		id TEXT PRIMARY KEY,
		execution_id TEXT NOT NULL,
		from_addr TEXT NOT NULL,
		to_addr TEXT NOT NULL,
		cc_addr TEXT NOT NULL DEFAULT '',
		subject TEXT NOT NULL,
		body TEXT NOT NULL,
		in_reply_to TEXT NOT NULL DEFAULT '',
		direction TEXT NOT NULL,
		created_at DATETIME NOT NULL DEFAULT (datetime('now')),
		FOREIGN KEY (execution_id) REFERENCES worker_executions(id) ON DELETE CASCADE
	);

	CREATE TABLE IF NOT EXISTS worker_memories (
		id TEXT PRIMARY KEY,
		worker_id TEXT NOT NULL,
		execution_id TEXT NOT NULL,
		summary TEXT NOT NULL,
		created_at DATETIME NOT NULL DEFAULT (datetime('now')),
		FOREIGN KEY (worker_id) REFERENCES workers(id) ON DELETE CASCADE,
		FOREIGN KEY (execution_id) REFERENCES worker_executions(id) ON DELETE CASCADE
	);
	`
	_, err := db.Exec(schema)
	return err
}
```

**Important:** Delete the old database file before running: `rm -f data/robobee.db`

**Step 2: Commit**

```bash
git add internal/store/db.go
git commit -m "refactor: rebuild schema — merge tasks into workers, rename task_executions to worker_executions"
```

---

### Task 5: Update Worker Store

**Files:**
- Modify: `internal/store/worker_store.go`

**Step 1: Update all SQL queries to include new fields (prompt, trigger_type, cron_expression, recipients, requires_approval)**

Replace the entire file with:

```go
package store

import (
	"database/sql"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/robobee/core/internal/model"
)

type WorkerStore struct {
	db *sql.DB
}

func NewWorkerStore(db *sql.DB) *WorkerStore {
	return &WorkerStore{db: db}
}

func (s *WorkerStore) Create(w model.Worker) (model.Worker, error) {
	w.ID = uuid.New().String()
	w.Status = model.WorkerStatusIdle
	w.CreatedAt = time.Now().UTC()
	w.UpdatedAt = w.CreatedAt

	_, err := s.db.Exec(
		`INSERT INTO workers (id, name, description, prompt, email, runtime_type, work_dir, trigger_type, cron_expression, recipients, requires_approval, status, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		w.ID, w.Name, w.Description, w.Prompt, w.Email, w.RuntimeType, w.WorkDir,
		w.TriggerType, w.CronExpression, string(w.Recipients), w.RequiresApproval,
		w.Status, w.CreatedAt, w.UpdatedAt,
	)
	if err != nil {
		return model.Worker{}, fmt.Errorf("insert worker: %w", err)
	}
	return w, nil
}

const workerColumns = `id, name, description, prompt, email, runtime_type, work_dir, trigger_type, cron_expression, recipients, requires_approval, status, created_at, updated_at`

func scanWorker(scanner interface{ Scan(...any) error }) (model.Worker, error) {
	var w model.Worker
	var recipients string
	err := scanner.Scan(
		&w.ID, &w.Name, &w.Description, &w.Prompt, &w.Email, &w.RuntimeType,
		&w.WorkDir, &w.TriggerType, &w.CronExpression, &recipients,
		&w.RequiresApproval, &w.Status, &w.CreatedAt, &w.UpdatedAt,
	)
	if err != nil {
		return model.Worker{}, err
	}
	w.Recipients = []byte(recipients)
	return w, nil
}

func (s *WorkerStore) GetByID(id string) (model.Worker, error) {
	row := s.db.QueryRow(`SELECT `+workerColumns+` FROM workers WHERE id = ?`, id)
	w, err := scanWorker(row)
	if err != nil {
		return model.Worker{}, fmt.Errorf("get worker: %w", err)
	}
	return w, nil
}

func (s *WorkerStore) GetByEmail(email string) (model.Worker, error) {
	row := s.db.QueryRow(`SELECT `+workerColumns+` FROM workers WHERE email = ?`, email)
	w, err := scanWorker(row)
	if err != nil {
		return model.Worker{}, fmt.Errorf("get worker by email: %w", err)
	}
	return w, nil
}

func (s *WorkerStore) List() ([]model.Worker, error) {
	rows, err := s.db.Query(`SELECT ` + workerColumns + ` FROM workers ORDER BY created_at DESC`)
	if err != nil {
		return nil, fmt.Errorf("list workers: %w", err)
	}
	defer rows.Close()

	var workers []model.Worker
	for rows.Next() {
		w, err := scanWorker(rows)
		if err != nil {
			return nil, fmt.Errorf("scan worker: %w", err)
		}
		workers = append(workers, w)
	}
	return workers, rows.Err()
}

func (s *WorkerStore) ListCronWorkers() ([]model.Worker, error) {
	rows, err := s.db.Query(
		`SELECT `+workerColumns+` FROM workers WHERE trigger_type = 'cron' AND cron_expression != ''`,
	)
	if err != nil {
		return nil, fmt.Errorf("list cron workers: %w", err)
	}
	defer rows.Close()

	var workers []model.Worker
	for rows.Next() {
		w, err := scanWorker(rows)
		if err != nil {
			return nil, fmt.Errorf("scan worker: %w", err)
		}
		workers = append(workers, w)
	}
	return workers, rows.Err()
}

func (s *WorkerStore) Update(w model.Worker) (model.Worker, error) {
	w.UpdatedAt = time.Now().UTC()
	_, err := s.db.Exec(
		`UPDATE workers SET name=?, description=?, prompt=?, email=?, runtime_type=?, work_dir=?,
		 trigger_type=?, cron_expression=?, recipients=?, requires_approval=?, status=?, updated_at=?
		 WHERE id=?`,
		w.Name, w.Description, w.Prompt, w.Email, w.RuntimeType, w.WorkDir,
		w.TriggerType, w.CronExpression, string(w.Recipients), w.RequiresApproval,
		w.Status, w.UpdatedAt, w.ID,
	)
	if err != nil {
		return model.Worker{}, fmt.Errorf("update worker: %w", err)
	}
	return w, nil
}

func (s *WorkerStore) UpdateStatus(id string, status model.WorkerStatus) error {
	_, err := s.db.Exec(`UPDATE workers SET status=?, updated_at=? WHERE id=?`, status, time.Now().UTC(), id)
	return err
}

func (s *WorkerStore) Delete(id string) error {
	_, err := s.db.Exec(`DELETE FROM workers WHERE id=?`, id)
	return err
}
```

**Step 2: Commit**

```bash
git add internal/store/worker_store.go
git commit -m "refactor: update WorkerStore with merged task fields and ListCronWorkers"
```

---

### Task 6: Update Execution Store

**Files:**
- Modify: `internal/store/execution_store.go`

**Step 1: Update to use worker_executions table, WorkerExecution model, worker_id FK, and trigger_input**

Replace the entire file with:

```go
package store

import (
	"database/sql"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/robobee/core/internal/model"
)

type ExecutionStore struct {
	db *sql.DB
}

func NewExecutionStore(db *sql.DB) *ExecutionStore {
	return &ExecutionStore{db: db}
}

func (s *ExecutionStore) Create(workerID, triggerInput string) (model.WorkerExecution, error) {
	now := time.Now().UTC()
	exec := model.WorkerExecution{
		ID:           uuid.New().String(),
		WorkerID:     workerID,
		SessionID:    uuid.New().String(),
		TriggerInput: triggerInput,
		Status:       model.ExecStatusPending,
		StartedAt:    &now,
	}

	_, err := s.db.Exec(
		`INSERT INTO worker_executions (id, worker_id, session_id, trigger_input, status, result, ai_process_pid, started_at)
		 VALUES (?, ?, ?, ?, ?, '', 0, ?)`,
		exec.ID, exec.WorkerID, exec.SessionID, exec.TriggerInput, exec.Status, exec.StartedAt,
	)
	if err != nil {
		return model.WorkerExecution{}, fmt.Errorf("insert execution: %w", err)
	}
	return exec, nil
}

const execColumns = `id, worker_id, session_id, trigger_input, status, result, ai_process_pid, started_at, completed_at`

func scanExecution(scanner interface{ Scan(...any) error }) (model.WorkerExecution, error) {
	var e model.WorkerExecution
	err := scanner.Scan(&e.ID, &e.WorkerID, &e.SessionID, &e.TriggerInput, &e.Status, &e.Result, &e.AIProcessPID, &e.StartedAt, &e.CompletedAt)
	return e, err
}

func (s *ExecutionStore) GetByID(id string) (model.WorkerExecution, error) {
	row := s.db.QueryRow(`SELECT `+execColumns+` FROM worker_executions WHERE id = ?`, id)
	e, err := scanExecution(row)
	if err != nil {
		return model.WorkerExecution{}, fmt.Errorf("get execution: %w", err)
	}
	return e, nil
}

func (s *ExecutionStore) GetBySessionID(sessionID string) (model.WorkerExecution, error) {
	row := s.db.QueryRow(`SELECT `+execColumns+` FROM worker_executions WHERE session_id = ?`, sessionID)
	e, err := scanExecution(row)
	if err != nil {
		return model.WorkerExecution{}, fmt.Errorf("get execution by session: %w", err)
	}
	return e, nil
}

func (s *ExecutionStore) List() ([]model.WorkerExecution, error) {
	rows, err := s.db.Query(`SELECT ` + execColumns + ` FROM worker_executions ORDER BY started_at DESC`)
	if err != nil {
		return nil, fmt.Errorf("list executions: %w", err)
	}
	defer rows.Close()

	var execs []model.WorkerExecution
	for rows.Next() {
		e, err := scanExecution(rows)
		if err != nil {
			return nil, fmt.Errorf("scan execution: %w", err)
		}
		execs = append(execs, e)
	}
	return execs, rows.Err()
}

func (s *ExecutionStore) ListByWorkerID(workerID string) ([]model.WorkerExecution, error) {
	rows, err := s.db.Query(`SELECT `+execColumns+` FROM worker_executions WHERE worker_id = ? ORDER BY started_at DESC`, workerID)
	if err != nil {
		return nil, fmt.Errorf("list executions by worker: %w", err)
	}
	defer rows.Close()

	var execs []model.WorkerExecution
	for rows.Next() {
		e, err := scanExecution(rows)
		if err != nil {
			return nil, fmt.Errorf("scan execution: %w", err)
		}
		execs = append(execs, e)
	}
	return execs, rows.Err()
}

func (s *ExecutionStore) UpdateStatus(id string, status model.ExecutionStatus) error {
	_, err := s.db.Exec(`UPDATE worker_executions SET status=? WHERE id=?`, status, id)
	return err
}

func (s *ExecutionStore) UpdateResult(id string, result string, status model.ExecutionStatus) error {
	now := time.Now().UTC()
	_, err := s.db.Exec(`UPDATE worker_executions SET result=?, status=?, completed_at=? WHERE id=?`, result, status, now, id)
	return err
}

func (s *ExecutionStore) UpdatePID(id string, pid int) error {
	_, err := s.db.Exec(`UPDATE worker_executions SET ai_process_pid=?, status=? WHERE id=?`, pid, model.ExecStatusRunning, id)
	return err
}
```

**Step 2: Commit**

```bash
git add internal/store/execution_store.go
git commit -m "refactor: update ExecutionStore for worker_executions table with worker_id and trigger_input"
```

---

### Task 7: Delete Task Store

**Files:**
- Delete: `internal/store/task_store.go`

**Step 1: Delete the file**

```bash
rm internal/store/task_store.go
```

**Step 2: Commit**

```bash
git add internal/store/task_store.go
git commit -m "refactor: remove TaskStore"
```

---

### Task 8: Update Manager

**Files:**
- Modify: `internal/worker/manager.go`

**Step 1: Remove TaskStore dependency, rename ExecuteTask→ExecuteWorker, update monitorExecution**

Replace the entire file with:

```go
package worker

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/robobee/core/internal/config"
	"github.com/robobee/core/internal/model"
	"github.com/robobee/core/internal/store"
)

type Manager struct {
	cfg            config.Config
	workerStore    *store.WorkerStore
	executionStore *store.ExecutionStore
	emailStore     *store.EmailStore
	memoryStore    *store.MemoryStore

	activeRuntimes map[string]Runtime     // execution_id -> runtime
	logSubscribers map[string][]chan Output // execution_id -> subscribers
	mu             sync.RWMutex
}

func NewManager(
	cfg config.Config,
	ws *store.WorkerStore,
	es *store.ExecutionStore,
	emailS *store.EmailStore,
	ms *store.MemoryStore,
) *Manager {
	return &Manager{
		cfg:            cfg,
		workerStore:    ws,
		executionStore: es,
		emailStore:     emailS,
		memoryStore:    ms,
		activeRuntimes: make(map[string]Runtime),
		logSubscribers: make(map[string][]chan Output),
	}
}

func (m *Manager) CreateWorker(
	name, description, prompt string,
	runtimeType model.RuntimeType,
	triggerType model.TriggerType,
	cronExpression string,
	recipients []string,
	requiresApproval bool,
) (model.Worker, error) {
	email := fmt.Sprintf("%s@%s", name, m.cfg.SMTP.Domain)
	workDir := filepath.Join(m.cfg.Workers.BaseDir, name)

	if err := os.MkdirAll(workDir, 0755); err != nil {
		return model.Worker{}, fmt.Errorf("create work dir: %w", err)
	}

	// Initialize CLAUDE.md for the worker
	claudeMD := filepath.Join(workDir, "CLAUDE.md")
	initialContent := fmt.Sprintf("# %s\n\n**Role:** %s\n\n## Work Memories\n\n", name, description)
	if err := os.WriteFile(claudeMD, []byte(initialContent), 0644); err != nil {
		return model.Worker{}, fmt.Errorf("create CLAUDE.md: %w", err)
	}

	recipientsJSON, _ := json.Marshal(recipients)

	return m.workerStore.Create(model.Worker{
		Name:             name,
		Description:      description,
		Prompt:           prompt,
		Email:            email,
		RuntimeType:      runtimeType,
		WorkDir:          workDir,
		TriggerType:      triggerType,
		CronExpression:   cronExpression,
		Recipients:       recipientsJSON,
		RequiresApproval: requiresApproval,
	})
}

func (m *Manager) ExecuteWorker(ctx context.Context, workerID, triggerInput string) (model.WorkerExecution, error) {
	worker, err := m.workerStore.GetByID(workerID)
	if err != nil {
		return model.WorkerExecution{}, fmt.Errorf("get worker: %w", err)
	}

	exec, err := m.executionStore.Create(workerID, triggerInput)
	if err != nil {
		return model.WorkerExecution{}, fmt.Errorf("create execution: %w", err)
	}

	// Update worker status
	if err := m.workerStore.UpdateStatus(worker.ID, model.WorkerStatusWorking); err != nil {
		log.Printf("failed to update worker status: %v", err)
	}

	// Create runtime based on worker type
	var rt Runtime
	var timeout time.Duration
	switch worker.RuntimeType {
	case model.RuntimeClaudeCode:
		rt = NewClaudeRuntime(m.cfg.Runtime.ClaudeCode.Binary)
		timeout = m.cfg.Runtime.ClaudeCode.Timeout
	case model.RuntimeCodex:
		rt = NewCodexRuntime(m.cfg.Runtime.Codex.Binary)
		timeout = m.cfg.Runtime.Codex.Timeout
	default:
		return model.WorkerExecution{}, fmt.Errorf("unknown runtime: %s", worker.RuntimeType)
	}

	// Decouple from caller context; apply configured timeout if set
	execCtx := ctx
	if timeout > 0 {
		var cancel context.CancelFunc
		execCtx, cancel = context.WithTimeout(ctx, timeout)
		_ = cancel
	}

	// Build the prompt: base prompt + trigger input for message-triggered workers
	prompt := worker.Prompt
	if triggerInput != "" && triggerInput != "scheduled" {
		prompt = fmt.Sprintf("%s\n\n---\nMessage:\n%s", worker.Prompt, triggerInput)
	}

	// Start execution in background
	outputCh, err := rt.Execute(execCtx, worker.WorkDir, prompt)
	if err != nil {
		m.executionStore.UpdateResult(exec.ID, err.Error(), model.ExecStatusFailed)
		m.workerStore.UpdateStatus(worker.ID, model.WorkerStatusError)
		return exec, fmt.Errorf("start runtime: %w", err)
	}

	// Store active runtime
	m.mu.Lock()
	m.activeRuntimes[exec.ID] = rt
	m.mu.Unlock()

	// Update PID
	if cr, ok := rt.(*ClaudeRuntime); ok {
		m.executionStore.UpdatePID(exec.ID, cr.PID())
	} else if cx, ok := rt.(*CodexRuntime); ok {
		m.executionStore.UpdatePID(exec.ID, cx.PID())
	}

	// Monitor output in background
	go m.monitorExecution(exec, worker, outputCh)

	return exec, nil
}

func (m *Manager) monitorExecution(exec model.WorkerExecution, worker model.Worker, outputCh <-chan Output) {
	var result string

	for out := range outputCh {
		// Broadcast to WebSocket subscribers
		m.mu.RLock()
		subs := m.logSubscribers[exec.ID]
		m.mu.RUnlock()

		for _, sub := range subs {
			select {
			case sub <- out:
			default:
			}
		}

		switch out.Type {
		case OutputStdout:
			result += out.Content + "\n"
		case OutputDone:
			if worker.RequiresApproval {
				m.executionStore.UpdateResult(exec.ID, result, model.ExecStatusAwaitingApproval)
			} else {
				m.executionStore.UpdateResult(exec.ID, result, model.ExecStatusCompleted)
			}
			m.workerStore.UpdateStatus(worker.ID, model.WorkerStatusIdle)
		case OutputError:
			m.executionStore.UpdateResult(exec.ID, result+"\nERROR: "+out.Content, model.ExecStatusFailed)
			m.workerStore.UpdateStatus(worker.ID, model.WorkerStatusError)
		}
	}

	// Cleanup
	m.mu.Lock()
	delete(m.activeRuntimes, exec.ID)
	for _, sub := range m.logSubscribers[exec.ID] {
		close(sub)
	}
	delete(m.logSubscribers, exec.ID)
	m.mu.Unlock()
}

func (m *Manager) SubscribeLogs(executionID string) <-chan Output {
	m.mu.Lock()
	defer m.mu.Unlock()

	ch := make(chan Output, 100)
	m.logSubscribers[executionID] = append(m.logSubscribers[executionID], ch)
	return ch
}

func (m *Manager) StopExecution(executionID string) error {
	m.mu.RLock()
	rt, ok := m.activeRuntimes[executionID]
	m.mu.RUnlock()

	if !ok {
		return fmt.Errorf("no active runtime for execution %s", executionID)
	}
	return rt.Stop()
}
```

**Step 2: Commit**

```bash
git add internal/worker/manager.go
git commit -m "refactor: update Manager — remove TaskStore, ExecuteTask→ExecuteWorker, read prompt from Worker"
```

---

### Task 9: Update Scheduler

**Files:**
- Modify: `internal/scheduler/cron.go`

**Step 1: Replace TaskStore with WorkerStore, load cron Workers instead of Tasks**

Replace the entire file with:

```go
package scheduler

import (
	"context"
	"log"

	"github.com/robfig/cron/v3"
	"github.com/robobee/core/internal/store"
	"github.com/robobee/core/internal/worker"
)

type Scheduler struct {
	cron        *cron.Cron
	workerStore *store.WorkerStore
	manager     *worker.Manager
	entryMap    map[string]cron.EntryID // worker_id -> entry_id
}

func New(ws *store.WorkerStore, mgr *worker.Manager) *Scheduler {
	return &Scheduler{
		cron:        cron.New(),
		workerStore: ws,
		manager:     mgr,
		entryMap:    make(map[string]cron.EntryID),
	}
}

func (s *Scheduler) Start() error {
	workers, err := s.workerStore.ListCronWorkers()
	if err != nil {
		return err
	}

	for _, w := range workers {
		if err := s.AddWorker(w.ID, w.CronExpression); err != nil {
			log.Printf("failed to schedule worker %s: %v", w.ID, err)
		}
	}

	s.cron.Start()
	log.Printf("Scheduler started with %d cron workers", len(workers))
	return nil
}

func (s *Scheduler) AddWorker(workerID, cronExpr string) error {
	s.RemoveWorker(workerID)

	id, err := s.cron.AddFunc(cronExpr, func() {
		log.Printf("Cron triggered worker %s", workerID)
		if _, err := s.manager.ExecuteWorker(context.Background(), workerID, "scheduled"); err != nil {
			log.Printf("failed to execute cron worker %s: %v", workerID, err)
		}
	})
	if err != nil {
		return err
	}

	s.entryMap[workerID] = id
	return nil
}

func (s *Scheduler) RemoveWorker(workerID string) {
	if entryID, ok := s.entryMap[workerID]; ok {
		s.cron.Remove(entryID)
		delete(s.entryMap, workerID)
	}
}

func (s *Scheduler) Stop() {
	s.cron.Stop()
}
```

**Step 2: Commit**

```bash
git add internal/scheduler/cron.go
git commit -m "refactor: update Scheduler to load cron Workers instead of Tasks"
```

---

### Task 10: Update API Router and Handlers

**Files:**
- Modify: `internal/api/router.go`
- Modify: `internal/api/worker_handler.go`
- Modify: `internal/api/execution_handler.go`
- Delete: `internal/api/task_handler.go`

**Step 1: Update router.go — remove TaskStore, remove task routes, add worker executions route**

Replace `internal/api/router.go` with:

```go
package api

import (
	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"
	"github.com/robobee/core/internal/store"
	"github.com/robobee/core/internal/worker"
)

type Server struct {
	router         *gin.Engine
	workerStore    *store.WorkerStore
	executionStore *store.ExecutionStore
	emailStore     *store.EmailStore
	memoryStore    *store.MemoryStore
	manager        *worker.Manager
}

func NewServer(
	ws *store.WorkerStore,
	es *store.ExecutionStore,
	emailS *store.EmailStore,
	ms *store.MemoryStore,
	mgr *worker.Manager,
) *Server {
	router := gin.Default()
	router.Use(cors.New(cors.Config{
		AllowOrigins:     []string{"*"},
		AllowMethods:     []string{"GET", "POST", "PUT", "DELETE", "OPTIONS"},
		AllowHeaders:     []string{"Origin", "Content-Type", "Authorization"},
		ExposeHeaders:    []string{"Content-Length"},
		AllowCredentials: false,
	}))

	s := &Server{
		router:         router,
		workerStore:    ws,
		executionStore: es,
		emailStore:     emailS,
		memoryStore:    ms,
		manager:        mgr,
	}
	s.setupRoutes()
	return s
}

func (s *Server) setupRoutes() {
	api := s.router.Group("/api")
	{
		// Workers
		api.POST("/workers", s.createWorker)
		api.GET("/workers", s.listWorkers)
		api.GET("/workers/:id", s.getWorker)
		api.PUT("/workers/:id", s.updateWorker)
		api.DELETE("/workers/:id", s.deleteWorker)

		// Worker message trigger
		api.POST("/workers/:id/message", s.sendMessage)

		// Worker executions
		api.GET("/workers/:id/executions", s.listWorkerExecutions)

		// Executions
		api.GET("/executions", s.listExecutions)
		api.GET("/executions/:id", s.getExecution)
		api.POST("/executions/:id/approve", s.approveExecution)
		api.POST("/executions/:id/reject", s.rejectExecution)

		// Emails
		api.GET("/executions/:id/emails", s.listEmails)

		// WebSocket logs
		api.GET("/executions/:id/logs", s.streamLogs)
	}
}

func (s *Server) Run(addr string) error {
	return s.router.Run(addr)
}
```

**Step 2: Update worker_handler.go — merge task creation fields into createWorker, update sendMessage**

Replace `internal/api/worker_handler.go` with:

```go
package api

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/robobee/core/internal/model"
)

type createWorkerRequest struct {
	Name             string            `json:"name" binding:"required"`
	Description      string            `json:"description"`
	Prompt           string            `json:"prompt" binding:"required"`
	RuntimeType      model.RuntimeType `json:"runtime_type"`
	TriggerType      model.TriggerType `json:"trigger_type" binding:"required"`
	CronExpression   string            `json:"cron_expression"`
	Recipients       []string          `json:"recipients"`
	RequiresApproval bool              `json:"requires_approval"`
}

func (s *Server) createWorker(c *gin.Context) {
	var req createWorkerRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if req.RuntimeType == "" {
		req.RuntimeType = model.RuntimeClaudeCode
	}

	if req.TriggerType == model.TriggerCron && req.CronExpression == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "cron_expression is required for cron trigger type"})
		return
	}

	if req.Recipients == nil {
		req.Recipients = []string{}
	}

	w, err := s.manager.CreateWorker(
		req.Name, req.Description, req.Prompt,
		req.RuntimeType, req.TriggerType,
		req.CronExpression, req.Recipients, req.RequiresApproval,
	)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusCreated, w)
}

func (s *Server) listWorkers(c *gin.Context) {
	workers, err := s.workerStore.List()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, workers)
}

func (s *Server) getWorker(c *gin.Context) {
	w, err := s.workerStore.GetByID(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "worker not found"})
		return
	}
	c.JSON(http.StatusOK, w)
}

func (s *Server) updateWorker(c *gin.Context) {
	w, err := s.workerStore.GetByID(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "worker not found"})
		return
	}

	var req struct {
		Name             string            `json:"name"`
		Description      string            `json:"description"`
		Prompt           string            `json:"prompt"`
		RuntimeType      model.RuntimeType `json:"runtime_type"`
		TriggerType      model.TriggerType `json:"trigger_type"`
		CronExpression   string            `json:"cron_expression"`
		Recipients       []string          `json:"recipients"`
		RequiresApproval *bool             `json:"requires_approval"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if req.Name != "" {
		w.Name = req.Name
	}
	if req.Description != "" {
		w.Description = req.Description
	}
	if req.Prompt != "" {
		w.Prompt = req.Prompt
	}
	if req.RuntimeType != "" {
		w.RuntimeType = req.RuntimeType
	}
	if req.TriggerType != "" {
		w.TriggerType = req.TriggerType
	}
	if req.CronExpression != "" {
		w.CronExpression = req.CronExpression
	}
	if req.Recipients != nil {
		recipients, _ := json.Marshal(req.Recipients)
		w.Recipients = recipients
	}
	if req.RequiresApproval != nil {
		w.RequiresApproval = *req.RequiresApproval
	}

	updated, err := s.workerStore.Update(w)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, updated)
}

func (s *Server) deleteWorker(c *gin.Context) {
	if err := s.workerStore.Delete(c.Param("id")); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "deleted"})
}

func (s *Server) sendMessage(c *gin.Context) {
	workerID := c.Param("id")

	worker, err := s.workerStore.GetByID(workerID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "worker not found"})
		return
	}

	if worker.TriggerType != model.TriggerMessage {
		c.JSON(http.StatusBadRequest, gin.H{"error": "this worker is not message-triggered"})
		return
	}

	var req struct {
		Message string `json:"message" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	exec, err := s.manager.ExecuteWorker(context.Background(), workerID, req.Message)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusAccepted, exec)
}
```

**Step 3: Update execution_handler.go — remove executeTask, add listWorkerExecutions, update types**

Replace `internal/api/execution_handler.go` with:

```go
package api

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	"github.com/robobee/core/internal/model"
)

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool { return true },
}

func (s *Server) listWorkerExecutions(c *gin.Context) {
	workerID := c.Param("id")
	execs, err := s.executionStore.ListByWorkerID(workerID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, execs)
}

func (s *Server) listExecutions(c *gin.Context) {
	execs, err := s.executionStore.List()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, execs)
}

func (s *Server) getExecution(c *gin.Context) {
	exec, err := s.executionStore.GetByID(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "execution not found"})
		return
	}
	c.JSON(http.StatusOK, exec)
}

func (s *Server) approveExecution(c *gin.Context) {
	execID := c.Param("id")
	if err := s.executionStore.UpdateStatus(execID, model.ExecStatusApproved); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "approved"})
}

func (s *Server) rejectExecution(c *gin.Context) {
	execID := c.Param("id")

	var req struct {
		Feedback string `json:"feedback"`
	}
	c.ShouldBindJSON(&req)

	if err := s.executionStore.UpdateStatus(execID, model.ExecStatusRejected); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "rejected", "feedback": req.Feedback})
}

func (s *Server) listEmails(c *gin.Context) {
	emails, err := s.emailStore.ListByExecutionID(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, emails)
}

func (s *Server) streamLogs(c *gin.Context) {
	execID := c.Param("id")

	conn, err := upgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		return
	}
	defer conn.Close()

	ch := s.manager.SubscribeLogs(execID)
	for out := range ch {
		if err := conn.WriteJSON(out); err != nil {
			break
		}
	}
}
```

**Step 4: Delete task_handler.go**

```bash
rm internal/api/task_handler.go
```

**Step 5: Commit**

```bash
git add internal/api/router.go internal/api/worker_handler.go internal/api/execution_handler.go internal/api/task_handler.go
git commit -m "refactor: update API — remove task routes, merge into worker endpoints"
```

---

### Task 11: Update main.go Entry Point

**Files:**
- Modify: `cmd/server/main.go`

**Step 1: Remove TaskStore initialization, update Manager and Server constructors, update Scheduler**

Replace the entire file with:

```go
package main

import (
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/robobee/core/internal/api"
	"github.com/robobee/core/internal/config"
	"github.com/robobee/core/internal/mail"
	"github.com/robobee/core/internal/scheduler"
	"github.com/robobee/core/internal/store"
	"github.com/robobee/core/internal/worker"
)

func main() {
	cfgPath := "config.yaml"
	if len(os.Args) > 1 {
		cfgPath = os.Args[1]
	}

	cfg, err := config.Load(cfgPath)
	if err != nil {
		log.Fatalf("failed to load config: %v", err)
	}

	// Initialize database
	db, err := store.InitDB(cfg.Database.Path)
	if err != nil {
		log.Fatalf("failed to init database: %v", err)
	}
	defer db.Close()

	// Create stores
	workerStore := store.NewWorkerStore(db)
	execStore := store.NewExecutionStore(db)
	emailStore := store.NewEmailStore(db)
	memoryStore := store.NewMemoryStore(db)

	// Create worker manager
	mgr := worker.NewManager(cfg, workerStore, execStore, emailStore, memoryStore)

	// Create email sender
	_ = mail.NewSender(cfg.SMTP, emailStore)

	// Start SMTP server
	smtpSrv := mail.NewSMTPServer(cfg.SMTP, execStore, emailStore, workerStore)
	go func() {
		if err := smtpSrv.Start(); err != nil {
			log.Printf("SMTP server error: %v", err)
		}
	}()

	// Start cron scheduler
	sched := scheduler.New(workerStore, mgr)
	if err := sched.Start(); err != nil {
		log.Printf("scheduler start error: %v", err)
	}

	// Start HTTP API
	srv := api.NewServer(workerStore, execStore, emailStore, memoryStore, mgr)

	// Graceful shutdown
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-quit
		log.Println("Shutting down...")
		smtpSrv.Close()
		sched.Stop()
		db.Close()
		os.Exit(0)
	}()

	addr := fmt.Sprintf("%s:%d", cfg.Server.Host, cfg.Server.Port)
	log.Printf("RoboBee Core starting on %s", addr)
	if err := srv.Run(addr); err != nil {
		log.Fatalf("server error: %v", err)
	}
}
```

**Step 2: Commit**

```bash
git add cmd/server/main.go
git commit -m "refactor: update main.go — remove TaskStore, update constructor signatures"
```

---

### Task 12: Verify Backend Compiles

**Step 1: Delete old database and build**

```bash
rm -f data/robobee.db
cd /Users/tengteng/work/robobee/core && go build ./...
```

Expected: Build succeeds with no errors.

**Step 2: If build fails, fix any remaining references to Task/TaskExecution/TaskStore**

Search for remaining references:

```bash
grep -r "TaskStore\|TaskExecution\|model\.Task\b\|task_id\|taskStore\|taskID\|ExecuteTask\|TriggerManual\|TriggerEmail" internal/ cmd/ --include="*.go"
```

Fix any found references. Common spots: `internal/store/memory_store.go`, `internal/store/email_store.go`.

**Step 3: Commit any fixes**

```bash
git add -A && git commit -m "fix: resolve remaining Task references after merge"
```

---

### Task 13: Update Frontend Types

**Files:**
- Modify: `web/src/lib/types.ts`

**Step 1: Remove Task interface, update Worker and execution types**

Replace the entire file with:

```typescript
export type RuntimeType = "claude_code" | "codex"
export type WorkerStatus = "idle" | "working" | "error"
export type TriggerType = "cron" | "message"
export type ExecutionStatus = "pending" | "running" | "awaiting_approval" | "approved" | "rejected" | "completed" | "failed"

export interface Worker {
  id: string
  name: string
  description: string
  prompt: string
  email: string
  runtime_type: RuntimeType
  work_dir: string
  trigger_type: TriggerType
  cron_expression: string
  recipients: string[]
  requires_approval: boolean
  status: WorkerStatus
  created_at: string
  updated_at: string
}

export interface WorkerExecution {
  id: string
  worker_id: string
  session_id: string
  trigger_input: string
  status: ExecutionStatus
  result: string
  ai_process_pid: number
  started_at: string | null
  completed_at: string | null
}

export interface Email {
  id: string
  execution_id: string
  from_addr: string
  to_addr: string
  cc_addr: string
  subject: string
  body: string
  in_reply_to: string
  direction: "inbound" | "outbound"
  created_at: string
}
```

**Step 2: Commit**

```bash
git add web/src/lib/types.ts
git commit -m "refactor: update frontend types — remove Task, add Worker fields, rename WorkerExecution"
```

---

### Task 14: Update Frontend API Client

**Files:**
- Modify: `web/src/lib/api.ts`

**Step 1: Remove tasks API, update workers.create, add workers.executions, update execution types**

Replace the entire file with:

```typescript
import type { Worker, WorkerExecution, Email } from "./types"

const API_BASE = import.meta.env.VITE_API_URL || "http://localhost:8080/api"

async function fetchAPI<T>(path: string, options?: RequestInit): Promise<T> {
  const res = await fetch(`${API_BASE}${path}`, {
    headers: { "Content-Type": "application/json" },
    ...options,
  })
  if (!res.ok) {
    const err = await res.json().catch(() => ({ error: res.statusText }))
    throw new Error(err.error || res.statusText)
  }
  return res.json()
}

export const api = {
  workers: {
    list: async () => {
      const workers = await fetchAPI<Worker[] | null>("/workers")
      return Array.isArray(workers) ? workers : []
    },
    get: (id: string) => fetchAPI<Worker>(`/workers/${id}`),
    create: (data: {
      name: string
      description: string
      prompt: string
      runtime_type: string
      trigger_type: string
      cron_expression?: string
      recipients?: string[]
      requires_approval?: boolean
    }) => fetchAPI<Worker>("/workers", { method: "POST", body: JSON.stringify(data) }),
    update: (id: string, data: Partial<Worker>) =>
      fetchAPI<Worker>(`/workers/${id}`, { method: "PUT", body: JSON.stringify(data) }),
    delete: (id: string) => fetchAPI(`/workers/${id}`, { method: "DELETE" }),
    executions: async (id: string) => {
      const execs = await fetchAPI<WorkerExecution[] | null>(`/workers/${id}/executions`)
      return Array.isArray(execs) ? execs : []
    },
  },
  executions: {
    list: async () => {
      const executions = await fetchAPI<WorkerExecution[] | null>("/executions")
      return Array.isArray(executions) ? executions : []
    },
    get: (id: string) => fetchAPI<WorkerExecution>(`/executions/${id}`),
    approve: (id: string) => fetchAPI(`/executions/${id}/approve`, { method: "POST" }),
    reject: (id: string, feedback: string) =>
      fetchAPI(`/executions/${id}/reject`, { method: "POST", body: JSON.stringify({ feedback }) }),
    emails: async (id: string) => {
      const emails = await fetchAPI<Email[] | null>(`/executions/${id}/emails`)
      return Array.isArray(emails) ? emails : []
    },
  },
  message: {
    send: (workerId: string, message: string) =>
      fetchAPI<WorkerExecution>(`/workers/${workerId}/message`, {
        method: "POST",
        body: JSON.stringify({ message }),
      }),
  },
}
```

**Step 2: Commit**

```bash
git add web/src/lib/api.ts
git commit -m "refactor: update API client — remove tasks endpoints, add worker executions"
```

---

### Task 15: Update Frontend Hooks

**Files:**
- Modify: `web/src/hooks/use-workers.ts`
- Modify: `web/src/hooks/use-executions.ts`
- Delete: `web/src/hooks/use-tasks.ts`

**Step 1: Update use-workers.ts with new create signature and add useWorkerExecutions**

Replace `web/src/hooks/use-workers.ts` with:

```typescript
import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query"
import { api } from "@/lib/api"

export function useWorkers() {
  return useQuery({
    queryKey: ["workers"],
    queryFn: api.workers.list,
  })
}

export function useWorker(id: string) {
  return useQuery({
    queryKey: ["workers", id],
    queryFn: () => api.workers.get(id),
  })
}

export function useCreateWorker() {
  const queryClient = useQueryClient()
  return useMutation({
    mutationFn: (data: {
      name: string
      description: string
      prompt: string
      runtime_type: string
      trigger_type: string
      cron_expression?: string
      recipients?: string[]
      requires_approval?: boolean
    }) => api.workers.create(data),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["workers"] })
    },
  })
}

export function useDeleteWorker() {
  const queryClient = useQueryClient()
  return useMutation({
    mutationFn: (id: string) => api.workers.delete(id),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["workers"] })
    },
  })
}

export function useWorkerExecutions(workerId: string) {
  return useQuery({
    queryKey: ["workers", workerId, "executions"],
    queryFn: () => api.workers.executions(workerId),
  })
}
```

**Step 2: Update use-executions.ts — remove taskId from sendMessage, update types**

Replace `web/src/hooks/use-executions.ts` with:

```typescript
import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query"
import { api } from "@/lib/api"

export function useExecutions() {
  return useQuery({
    queryKey: ["executions"],
    queryFn: api.executions.list,
  })
}

export function useExecution(id: string) {
  return useQuery({
    queryKey: ["executions", id],
    queryFn: () => api.executions.get(id),
  })
}

export function useExecutionEmails(id: string) {
  return useQuery({
    queryKey: ["executions", id, "emails"],
    queryFn: () => api.executions.emails(id),
  })
}

export function useApproveExecution() {
  const queryClient = useQueryClient()
  return useMutation({
    mutationFn: (id: string) => api.executions.approve(id),
    onSuccess: (_data, id) => {
      queryClient.invalidateQueries({ queryKey: ["executions", id] })
    },
  })
}

export function useRejectExecution() {
  const queryClient = useQueryClient()
  return useMutation({
    mutationFn: ({ id, feedback }: { id: string; feedback: string }) =>
      api.executions.reject(id, feedback),
    onSuccess: (_data, { id }) => {
      queryClient.invalidateQueries({ queryKey: ["executions", id] })
    },
  })
}

export function useSendMessage() {
  return useMutation({
    mutationFn: ({ workerId, message }: { workerId: string; message: string }) =>
      api.message.send(workerId, message),
  })
}
```

**Step 3: Delete use-tasks.ts**

```bash
rm web/src/hooks/use-tasks.ts
```

**Step 4: Commit**

```bash
git add web/src/hooks/use-workers.ts web/src/hooks/use-executions.ts web/src/hooks/use-tasks.ts
git commit -m "refactor: update frontend hooks — remove use-tasks, add useWorkerExecutions"
```

---

### Task 16: Update Workers Page (Create Form)

**Files:**
- Modify: `web/src/pages/workers.tsx`

**Step 1: Update create worker form to include prompt, trigger_type, cron_expression, recipients, requires_approval**

Replace the entire file with:

```tsx
import { useState } from "react"
import { Link } from "react-router-dom"
import { useWorkers, useCreateWorker, useDeleteWorker } from "@/hooks/use-workers"
import { Button } from "@/components/ui/button"
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card"
import { Badge } from "@/components/ui/badge"
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
  DialogTrigger,
} from "@/components/ui/dialog"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import { Textarea } from "@/components/ui/textarea"

const statusColor: Record<string, string> = {
  idle: "bg-green-100 text-green-800",
  working: "bg-blue-100 text-blue-800",
  error: "bg-red-100 text-red-800",
}

export function Workers() {
  const { data: workers = [], error: fetchError } = useWorkers()
  const createWorker = useCreateWorker()
  const deleteWorker = useDeleteWorker()
  const [open, setOpen] = useState(false)
  const [name, setName] = useState("")
  const [description, setDescription] = useState("")
  const [prompt, setPrompt] = useState("")
  const [runtimeType, setRuntimeType] = useState("claude_code")
  const [triggerType, setTriggerType] = useState("message")
  const [cronExpression, setCronExpression] = useState("")
  const [recipients, setRecipients] = useState("")
  const [requiresApproval, setRequiresApproval] = useState(false)

  const error = fetchError?.message || createWorker.error?.message || deleteWorker.error?.message || ""

  const handleCreate = async () => {
    const recipientList = recipients.split(",").map((r) => r.trim()).filter(Boolean)
    await createWorker.mutateAsync({
      name,
      description,
      prompt,
      runtime_type: runtimeType,
      trigger_type: triggerType,
      cron_expression: triggerType === "cron" ? cronExpression : undefined,
      recipients: recipientList.length > 0 ? recipientList : undefined,
      requires_approval: requiresApproval || undefined,
    })
    setOpen(false)
    setName("")
    setDescription("")
    setPrompt("")
    setCronExpression("")
    setRecipients("")
    setRequiresApproval(false)
  }

  const handleDelete = async (id: string) => {
    if (!confirm("Delete this worker?")) return
    await deleteWorker.mutateAsync(id)
  }

  return (
    <div>
      <div className="flex items-center justify-between mb-6">
        <h1 className="text-2xl font-bold">Workers</h1>
        <Dialog open={open} onOpenChange={setOpen}>
          <DialogTrigger render={<Button />}>
            Create Worker
          </DialogTrigger>
          <DialogContent>
            <DialogHeader>
              <DialogTitle>Create Worker</DialogTitle>
            </DialogHeader>
            <div className="space-y-4 max-h-[70vh] overflow-y-auto">
              <div>
                <Label htmlFor="name">Name</Label>
                <Input
                  id="name"
                  value={name}
                  onChange={(e) => setName(e.target.value)}
                  placeholder="e.g. report-bot"
                />
              </div>
              <div>
                <Label htmlFor="desc">Description</Label>
                <Textarea
                  id="desc"
                  value={description}
                  onChange={(e) => setDescription(e.target.value)}
                  placeholder="What does this worker do?"
                />
              </div>
              <div>
                <Label htmlFor="prompt">Prompt</Label>
                <Textarea
                  id="prompt"
                  value={prompt}
                  onChange={(e) => setPrompt(e.target.value)}
                  placeholder="The instruction this worker will execute..."
                  rows={4}
                />
              </div>
              <div>
                <Label htmlFor="runtime">Runtime</Label>
                <select
                  id="runtime"
                  value={runtimeType}
                  onChange={(e) => setRuntimeType(e.target.value)}
                  className="w-full rounded-md border px-3 py-2 text-sm"
                >
                  <option value="claude_code">Claude Code</option>
                  <option value="codex">Codex</option>
                </select>
              </div>
              <div>
                <Label htmlFor="trigger">Trigger Type</Label>
                <select
                  id="trigger"
                  value={triggerType}
                  onChange={(e) => setTriggerType(e.target.value)}
                  className="w-full rounded-md border px-3 py-2 text-sm"
                >
                  <option value="message">Message</option>
                  <option value="cron">Cron</option>
                </select>
              </div>
              {triggerType === "cron" && (
                <div>
                  <Label htmlFor="cron">Cron Expression</Label>
                  <Input
                    id="cron"
                    value={cronExpression}
                    onChange={(e) => setCronExpression(e.target.value)}
                    placeholder="0 9 * * *"
                  />
                </div>
              )}
              <div>
                <Label htmlFor="recipients">Recipients (comma-separated emails)</Label>
                <Input
                  id="recipients"
                  value={recipients}
                  onChange={(e) => setRecipients(e.target.value)}
                  placeholder="user@example.com, admin@example.com"
                />
              </div>
              <div className="flex items-center gap-2">
                <input
                  type="checkbox"
                  id="approval"
                  checked={requiresApproval}
                  onChange={(e) => setRequiresApproval(e.target.checked)}
                />
                <Label htmlFor="approval">Requires Approval</Label>
              </div>
              <Button onClick={handleCreate} className="w-full">
                Create
              </Button>
            </div>
          </DialogContent>
        </Dialog>
      </div>

      {error && <p className="text-red-500 mb-4">{error}</p>}

      <div className="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-3 gap-4">
        {workers.map((w) => (
          <Card key={w.id}>
            <CardHeader className="pb-2">
              <div className="flex items-center justify-between">
                <Link to={`/workers/${w.id}`}>
                  <CardTitle className="text-lg hover:underline">
                    {w.name}
                  </CardTitle>
                </Link>
                <Badge className={statusColor[w.status] || ""}>
                  {w.status}
                </Badge>
              </div>
            </CardHeader>
            <CardContent>
              <p className="text-sm text-muted-foreground mb-2">
                {w.description || "No description"}
              </p>
              <p className="text-xs text-muted-foreground">{w.email}</p>
              <p className="text-xs text-muted-foreground mt-1">
                {w.trigger_type === "cron" ? `Cron: ${w.cron_expression}` : "Message triggered"}
              </p>
              <div className="flex gap-2 mt-3">
                <Link to={`/workers/${w.id}`}>
                  <Button variant="outline" size="sm">
                    View
                  </Button>
                </Link>
                <Button
                  variant="destructive"
                  size="sm"
                  onClick={() => handleDelete(w.id)}
                >
                  Delete
                </Button>
              </div>
            </CardContent>
          </Card>
        ))}
      </div>
    </div>
  )
}
```

**Step 2: Commit**

```bash
git add web/src/pages/workers.tsx
git commit -m "refactor: update Workers page — unified create form with prompt, trigger, and recipients"
```

---

### Task 17: Update Worker Detail Page

**Files:**
- Modify: `web/src/pages/worker-detail.tsx`

**Step 1: Remove all task management, show worker prompt/config, show executions directly**

Replace the entire file with:

```tsx
import { useState } from "react"
import { useParams, useNavigate, Link } from "react-router-dom"
import { useWorker } from "@/hooks/use-workers"
import { useWorkerExecutions } from "@/hooks/use-workers"
import { useSendMessage } from "@/hooks/use-executions"
import { Button } from "@/components/ui/button"
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card"
import { Badge } from "@/components/ui/badge"
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs"
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
  DialogTrigger,
} from "@/components/ui/dialog"
import { Textarea } from "@/components/ui/textarea"

const statusColor: Record<string, string> = {
  idle: "bg-green-100 text-green-800",
  working: "bg-blue-100 text-blue-800",
  error: "bg-red-100 text-red-800",
}

const execStatusColor: Record<string, string> = {
  pending: "bg-gray-100 text-gray-800",
  running: "bg-blue-100 text-blue-800",
  awaiting_approval: "bg-yellow-100 text-yellow-800",
  approved: "bg-green-100 text-green-800",
  rejected: "bg-red-100 text-red-800",
  completed: "bg-green-100 text-green-800",
  failed: "bg-red-100 text-red-800",
}

export function WorkerDetail() {
  const { id } = useParams<{ id: string }>()
  const navigate = useNavigate()
  const { data: worker, error: workerError } = useWorker(id!)
  const { data: executions = [] } = useWorkerExecutions(id!)
  const sendMessage = useSendMessage()

  const [msgDialogOpen, setMsgDialogOpen] = useState(false)
  const [message, setMessage] = useState("")
  const [error, setError] = useState("")

  const handleSendMessage = async () => {
    try {
      const exec = await sendMessage.mutateAsync({ workerId: id!, message })
      setMsgDialogOpen(false)
      setMessage("")
      navigate(`/executions/${exec.id}`)
    } catch (e: unknown) {
      setError(e instanceof Error ? e.message : "Failed to send message")
    }
  }

  if (!worker) return <p>Loading...</p>

  return (
    <div>
      <div className="flex items-center justify-between mb-6">
        <div>
          <h1 className="text-2xl font-bold">{worker.name}</h1>
          <p className="text-muted-foreground">{worker.email}</p>
        </div>
        <div className="flex gap-2 items-center">
          <Badge className={statusColor[worker.status] || ""}>{worker.status}</Badge>
          {worker.trigger_type === "message" && (
            <Dialog open={msgDialogOpen} onOpenChange={setMsgDialogOpen}>
              <DialogTrigger render={<Button />}>
                Send Message
              </DialogTrigger>
              <DialogContent>
                <DialogHeader>
                  <DialogTitle>Send Message to {worker.name}</DialogTitle>
                </DialogHeader>
                <div className="space-y-4">
                  <Textarea
                    value={message}
                    onChange={(e) => setMessage(e.target.value)}
                    placeholder="Enter your message..."
                    rows={4}
                  />
                  <Button onClick={handleSendMessage} className="w-full">
                    Send
                  </Button>
                </div>
              </DialogContent>
            </Dialog>
          )}
        </div>
      </div>

      {(error || workerError) && (
        <p className="text-red-500 mb-4">{error || workerError?.message}</p>
      )}

      <Tabs defaultValue="executions">
        <TabsList>
          <TabsTrigger value="executions">Executions</TabsTrigger>
          <TabsTrigger value="info">Info</TabsTrigger>
        </TabsList>

        <TabsContent value="executions" className="mt-4">
          {executions.length === 0 && (
            <p className="text-muted-foreground">No executions yet.</p>
          )}
          <div className="space-y-3">
            {executions.map((e) => (
              <Card key={e.id}>
                <CardContent className="flex items-center justify-between py-4">
                  <div>
                    <Link
                      to={`/executions/${e.id}`}
                      className="font-mono text-sm hover:underline"
                    >
                      {e.id.slice(0, 8)}...
                    </Link>
                    <p className="text-xs text-muted-foreground mt-1">
                      {e.started_at ? new Date(e.started_at).toLocaleString() : "-"}
                      {e.trigger_input && ` | ${e.trigger_input.slice(0, 50)}${e.trigger_input.length > 50 ? "..." : ""}`}
                    </p>
                  </div>
                  <Badge className={execStatusColor[e.status] || ""}>
                    {e.status}
                  </Badge>
                </CardContent>
              </Card>
            ))}
          </div>
        </TabsContent>

        <TabsContent value="info" className="mt-4">
          <Card>
            <CardHeader>
              <CardTitle>Worker Info</CardTitle>
            </CardHeader>
            <CardContent className="space-y-2">
              <p><strong>ID:</strong> <span className="font-mono text-sm">{worker.id}</span></p>
              <p><strong>Runtime:</strong> {worker.runtime_type}</p>
              <p><strong>Trigger:</strong> {worker.trigger_type}
                {worker.trigger_type === "cron" && ` (${worker.cron_expression})`}
              </p>
              <p><strong>Requires Approval:</strong> {worker.requires_approval ? "Yes" : "No"}</p>
              <p><strong>Work Dir:</strong> {worker.work_dir}</p>
              <p><strong>Created:</strong> {new Date(worker.created_at).toLocaleString()}</p>
              <div>
                <strong>Prompt:</strong>
                <pre className="mt-1 whitespace-pre-wrap text-sm bg-muted p-3 rounded-md">
                  {worker.prompt}
                </pre>
              </div>
            </CardContent>
          </Card>
        </TabsContent>
      </Tabs>
    </div>
  )
}
```

**Step 2: Commit**

```bash
git add web/src/pages/worker-detail.tsx
git commit -m "refactor: update WorkerDetail — remove task management, show executions and worker config"
```

---

### Task 18: Update Executions Page and Execution Detail

**Files:**
- Modify: `web/src/pages/executions.tsx`
- Modify: `web/src/pages/execution-detail.tsx`

**Step 1: Update executions.tsx — change task_id column to worker_id**

Replace the entire file with:

```tsx
import { Link } from "react-router-dom"
import { useExecutions } from "@/hooks/use-executions"
import { Badge } from "@/components/ui/badge"
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table"

const statusColor: Record<string, string> = {
  pending: "bg-gray-100 text-gray-800",
  running: "bg-blue-100 text-blue-800",
  awaiting_approval: "bg-yellow-100 text-yellow-800",
  approved: "bg-green-100 text-green-800",
  rejected: "bg-red-100 text-red-800",
  completed: "bg-green-100 text-green-800",
  failed: "bg-red-100 text-red-800",
}

export function Executions() {
  const { data: executions = [], error } = useExecutions()

  return (
    <div>
      <h1 className="text-2xl font-bold mb-6">Executions</h1>
      {error && <p className="text-red-500 mb-4">{error.message}</p>}

      {executions.length === 0 && !error && (
        <p className="text-muted-foreground">No executions yet.</p>
      )}

      {executions.length > 0 && (
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>ID</TableHead>
              <TableHead>Worker</TableHead>
              <TableHead>Status</TableHead>
              <TableHead>Started</TableHead>
              <TableHead>Completed</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {executions.map((e) => (
              <TableRow key={e.id}>
                <TableCell>
                  <Link
                    to={`/executions/${e.id}`}
                    className="font-mono text-sm hover:underline"
                  >
                    {e.id.slice(0, 8)}...
                  </Link>
                </TableCell>
                <TableCell>
                  <Link
                    to={`/workers/${e.worker_id}`}
                    className="font-mono text-sm hover:underline"
                  >
                    {e.worker_id.slice(0, 8)}...
                  </Link>
                </TableCell>
                <TableCell>
                  <Badge className={statusColor[e.status] || ""}>
                    {e.status}
                  </Badge>
                </TableCell>
                <TableCell className="text-sm">
                  {e.started_at ? new Date(e.started_at).toLocaleString() : "-"}
                </TableCell>
                <TableCell className="text-sm">
                  {e.completed_at
                    ? new Date(e.completed_at).toLocaleString()
                    : "-"}
                </TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      )}
    </div>
  )
}
```

**Step 2: Update execution-detail.tsx — change task_id references to worker_id**

Replace the entire file with:

```tsx
import { useEffect, useRef, useState } from "react"
import { useParams, Link } from "react-router-dom"
import { useExecution, useExecutionEmails, useApproveExecution, useRejectExecution } from "@/hooks/use-executions"
import { Button } from "@/components/ui/button"
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card"
import { Badge } from "@/components/ui/badge"
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs"
import { Textarea } from "@/components/ui/textarea"

const statusColor: Record<string, string> = {
  pending: "bg-gray-100 text-gray-800",
  running: "bg-blue-100 text-blue-800",
  awaiting_approval: "bg-yellow-100 text-yellow-800",
  approved: "bg-green-100 text-green-800",
  rejected: "bg-red-100 text-red-800",
  completed: "bg-green-100 text-green-800",
  failed: "bg-red-100 text-red-800",
}

interface LogEntry {
  type: string
  content: string
}

export function ExecutionDetail() {
  const { id } = useParams<{ id: string }>()
  const { data: execution, error: fetchError } = useExecution(id!)
  const { data: emails = [] } = useExecutionEmails(id!)
  const approveExecution = useApproveExecution()
  const rejectExecution = useRejectExecution()

  const [logs, setLogs] = useState<LogEntry[]>([])
  const [feedback, setFeedback] = useState("")
  const [error, setError] = useState("")
  const logsEndRef = useRef<HTMLDivElement>(null)

  useEffect(() => {
    const wsBase = import.meta.env.VITE_API_URL || "http://localhost:8080/api"
    const wsUrl = wsBase.replace(/^http/, "ws") + `/executions/${id}/logs`
    const ws = new WebSocket(wsUrl)

    ws.onmessage = (event) => {
      const data = JSON.parse(event.data)
      setLogs((prev) => [...prev, data])
    }

    ws.onerror = () => {}

    return () => ws.close()
  }, [id])

  useEffect(() => {
    logsEndRef.current?.scrollIntoView({ behavior: "smooth" })
  }, [logs])

  const handleApprove = async () => {
    try {
      await approveExecution.mutateAsync(id!)
    } catch (e: unknown) {
      setError(e instanceof Error ? e.message : "Failed to approve")
    }
  }

  const handleReject = async () => {
    try {
      await rejectExecution.mutateAsync({ id: id!, feedback })
    } catch (e: unknown) {
      setError(e instanceof Error ? e.message : "Failed to reject")
    }
  }

  if (!execution) return <p>Loading...</p>

  return (
    <div>
      <div className="flex items-center justify-between mb-6">
        <div>
          <h1 className="text-2xl font-bold">Execution Detail</h1>
          <p className="text-sm text-muted-foreground font-mono">{execution.id}</p>
        </div>
        <Badge className={statusColor[execution.status] || ""}>
          {execution.status}
        </Badge>
      </div>

      {(error || fetchError) && (
        <p className="text-red-500 mb-4">{error || fetchError?.message}</p>
      )}

      {execution.status === "awaiting_approval" && (
        <Card className="mb-6 border-yellow-300">
          <CardHeader>
            <CardTitle>Approval Required</CardTitle>
          </CardHeader>
          <CardContent className="space-y-3">
            <Textarea
              placeholder="Feedback (optional for rejection)"
              value={feedback}
              onChange={(e) => setFeedback(e.target.value)}
            />
            <div className="flex gap-2">
              <Button onClick={handleApprove}>Approve</Button>
              <Button variant="destructive" onClick={handleReject}>
                Reject
              </Button>
            </div>
          </CardContent>
        </Card>
      )}

      <Tabs defaultValue="logs">
        <TabsList>
          <TabsTrigger value="logs">Logs</TabsTrigger>
          <TabsTrigger value="result">Result</TabsTrigger>
          <TabsTrigger value="emails">Emails</TabsTrigger>
          <TabsTrigger value="info">Info</TabsTrigger>
        </TabsList>

        <TabsContent value="logs" className="mt-4">
          <div className="bg-black text-green-400 font-mono text-sm p-4 rounded-lg max-h-[500px] overflow-y-auto">
            {logs.length === 0 && (
              <p className="text-gray-500">
                {execution.status === "running"
                  ? "Waiting for output..."
                  : "No live logs available."}
              </p>
            )}
            {logs.map((log, i) => (
              <div
                key={i}
                className={
                  log.type === "stderr"
                    ? "text-red-400"
                    : log.type === "error"
                    ? "text-red-500 font-bold"
                    : ""
                }
              >
                {log.content}
              </div>
            ))}
            <div ref={logsEndRef} />
          </div>
        </TabsContent>

        <TabsContent value="result" className="mt-4">
          <Card>
            <CardContent className="pt-6">
              <pre className="whitespace-pre-wrap text-sm">
                {execution.result || "No result yet."}
              </pre>
            </CardContent>
          </Card>
        </TabsContent>

        <TabsContent value="emails" className="mt-4">
          {emails.length === 0 && (
            <p className="text-muted-foreground">No emails for this execution.</p>
          )}
          <div className="space-y-3">
            {emails.map((e) => (
              <Card key={e.id}>
                <CardHeader className="pb-2">
                  <div className="flex items-center justify-between">
                    <CardTitle className="text-sm">{e.subject}</CardTitle>
                    <Badge variant="outline">{e.direction}</Badge>
                  </div>
                </CardHeader>
                <CardContent>
                  <p className="text-xs text-muted-foreground mb-2">
                    From: {e.from_addr} | To: {e.to_addr}
                    {e.cc_addr && ` | CC: ${e.cc_addr}`}
                  </p>
                  <pre className="whitespace-pre-wrap text-sm">{e.body}</pre>
                </CardContent>
              </Card>
            ))}
          </div>
        </TabsContent>

        <TabsContent value="info" className="mt-4">
          <Card>
            <CardContent className="pt-6 space-y-2">
              <p><strong>Worker:</strong>{" "}
                <Link to={`/workers/${execution.worker_id}`} className="font-mono text-sm hover:underline">
                  {execution.worker_id.slice(0, 8)}...
                </Link>
              </p>
              <p><strong>Session ID:</strong> <span className="font-mono text-sm">{execution.session_id}</span></p>
              {execution.trigger_input && (
                <div>
                  <strong>Trigger Input:</strong>
                  <pre className="mt-1 whitespace-pre-wrap text-sm bg-muted p-2 rounded-md">
                    {execution.trigger_input}
                  </pre>
                </div>
              )}
              <p><strong>PID:</strong> {execution.ai_process_pid || "N/A"}</p>
              <p><strong>Started:</strong> {execution.started_at ? new Date(execution.started_at).toLocaleString() : "-"}</p>
              <p><strong>Completed:</strong> {execution.completed_at ? new Date(execution.completed_at).toLocaleString() : "-"}</p>
            </CardContent>
          </Card>
        </TabsContent>
      </Tabs>
    </div>
  )
}
```

**Step 3: Commit**

```bash
git add web/src/pages/executions.tsx web/src/pages/execution-detail.tsx
git commit -m "refactor: update execution pages — worker_id instead of task_id, show trigger_input"
```

---

### Task 19: Update Dashboard

**Files:**
- Modify: `web/src/pages/dashboard.tsx`

**Step 1: Add trigger_type display to worker cards**

In `web/src/pages/dashboard.tsx`, add the trigger type info to the card. Find and update the card content area to include trigger info:

```tsx
<p className="text-xs text-muted-foreground mt-1">
  Runtime: {w.runtime_type} | {w.trigger_type === "cron" ? `Cron: ${w.cron_expression}` : "Message"}
</p>
```

Replace the existing line:
```tsx
<p className="text-xs text-muted-foreground mt-1">
  Runtime: {w.runtime_type}
</p>
```

**Step 2: Commit**

```bash
git add web/src/pages/dashboard.tsx
git commit -m "refactor: update dashboard — show trigger type on worker cards"
```

---

### Task 20: Verify Frontend Compiles

**Step 1: Run TypeScript check and build**

```bash
cd /Users/tengteng/work/robobee/core/web && npx tsc --noEmit
```

Expected: No type errors.

**Step 2: If errors, fix remaining Task/TaskExecution references**

Search for remaining references:

```bash
grep -r "Task\b\|task_id\|useTasks\|useCreateTask\|useDeleteTask\|useExecuteTask\|TaskExecution" web/src/ --include="*.ts" --include="*.tsx"
```

Fix any found references.

**Step 3: Commit any fixes**

```bash
git add -A && git commit -m "fix: resolve remaining Task references in frontend"
```

---

### Task 21: Final Integration Verification

**Step 1: Delete old database and rebuild backend**

```bash
rm -f data/robobee.db
cd /Users/tengteng/work/robobee/core && go build ./...
```

Expected: Build succeeds.

**Step 2: Build frontend**

```bash
cd /Users/tengteng/work/robobee/core/web && npm run build
```

Expected: Build succeeds.

**Step 3: Commit any remaining fixes and tag**

```bash
git add -A && git commit -m "feat: complete worker-task merge — workers are self-contained units"
```
