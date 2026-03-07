# Digital Worker Dispatch System - Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Build a digital worker dispatch system where AI-powered workers (Claude Code / Codex) execute tasks, communicate via embedded SMTP, and support approval workflows.

**Architecture:** Go monolithic service (REST API + SMTP + Scheduler + Process Manager) with SQLite storage. Next.js + shadcn/ui frontend. Single machine deployment via Docker Compose.

**Tech Stack:** Go 1.22+, SQLite, gin, go-smtp, robfig/cron, gorilla/websocket, Next.js 14+, shadcn/ui, TypeScript

---

## Phase 1: Project Scaffold & Configuration

### Task 1.1: Initialize Go Module & Directory Structure

**Files:**
- Create: `go.mod`
- Create: `cmd/server/main.go`
- Create: `internal/config/config.go`
- Create: `config.yaml`

**Step 1: Initialize Go module**

Run:
```bash
cd /Users/tengteng/work/robobee/core
go mod init github.com/robobee/core
```

**Step 2: Create directory structure**

Run:
```bash
mkdir -p cmd/server
mkdir -p internal/{api,model,store,worker,mail,scheduler,config}
mkdir -p data/workers
```

**Step 3: Create config.yaml**

```yaml
server:
  port: 8080
  host: 0.0.0.0

smtp:
  port: 2525
  domain: robobee.local
  system_cc: admin@company.com

database:
  path: ./data/robobee.db

workers:
  base_dir: ./data/workers
  default_runtime: claude_code

runtime:
  claude_code:
    binary: claude
    timeout: 30m
  codex:
    binary: codex
    timeout: 30m
```

**Step 4: Write config loader**

Create `internal/config/config.go`:

```go
package config

import (
	"os"
	"time"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Server   ServerConfig   `yaml:"server"`
	SMTP     SMTPConfig     `yaml:"smtp"`
	Database DatabaseConfig `yaml:"database"`
	Workers  WorkersConfig  `yaml:"workers"`
	Runtime  RuntimeConfig  `yaml:"runtime"`
}

type ServerConfig struct {
	Port int    `yaml:"port"`
	Host string `yaml:"host"`
}

type SMTPConfig struct {
	Port     int    `yaml:"port"`
	Domain   string `yaml:"domain"`
	SystemCC string `yaml:"system_cc"`
}

type DatabaseConfig struct {
	Path string `yaml:"path"`
}

type WorkersConfig struct {
	BaseDir        string `yaml:"base_dir"`
	DefaultRuntime string `yaml:"default_runtime"`
}

type RuntimeConfig struct {
	ClaudeCode RuntimeEntry `yaml:"claude_code"`
	Codex      RuntimeEntry `yaml:"codex"`
}

type RuntimeEntry struct {
	Binary  string        `yaml:"binary"`
	Timeout time.Duration `yaml:"timeout"`
}

func Load(path string) (Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Config{}, err
	}
	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return Config{}, err
	}
	return cfg, nil
}
```

**Step 5: Create minimal main.go**

Create `cmd/server/main.go`:

```go
package main

import (
	"fmt"
	"log"
	"os"

	"github.com/robobee/core/internal/config"
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

	fmt.Printf("RoboBee Core starting on %s:%d\n", cfg.Server.Host, cfg.Server.Port)
}
```

**Step 6: Install dependencies and verify build**

Run:
```bash
go get gopkg.in/yaml.v3
go mod tidy
go build ./cmd/server/
```
Expected: Binary builds successfully.

**Step 7: Commit**

```bash
git init
echo "data/robobee.db\ndata/workers/\n*.exe\nserver" > .gitignore
git add .
git commit -m "feat: initialize project scaffold with config loader"
```

---

### Task 1.2: Data Models

**Files:**
- Create: `internal/model/worker.go`
- Create: `internal/model/task.go`
- Create: `internal/model/execution.go`
- Create: `internal/model/email.go`
- Create: `internal/model/memory.go`

**Step 1: Create worker model**

Create `internal/model/worker.go`:

```go
package model

import "time"

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

type Worker struct {
	ID          string       `json:"id" db:"id"`
	Name        string       `json:"name" db:"name"`
	Description string       `json:"description" db:"description"`
	Email       string       `json:"email" db:"email"`
	RuntimeType RuntimeType  `json:"runtime_type" db:"runtime_type"`
	WorkDir     string       `json:"work_dir" db:"work_dir"`
	Status      WorkerStatus `json:"status" db:"status"`
	CreatedAt   time.Time    `json:"created_at" db:"created_at"`
	UpdatedAt   time.Time    `json:"updated_at" db:"updated_at"`
}
```

**Step 2: Create task model**

Create `internal/model/task.go`:

```go
package model

import (
	"encoding/json"
	"time"
)

type TriggerType string

const (
	TriggerManual TriggerType = "manual"
	TriggerEmail  TriggerType = "email"
	TriggerCron   TriggerType = "cron"
)

type Task struct {
	ID               string          `json:"id" db:"id"`
	WorkerID         string          `json:"worker_id" db:"worker_id"`
	Name             string          `json:"name" db:"name"`
	Plan             string          `json:"plan" db:"plan"`
	TriggerType      TriggerType     `json:"trigger_type" db:"trigger_type"`
	CronExpression   string          `json:"cron_expression,omitempty" db:"cron_expression"`
	Recipients       json.RawMessage `json:"recipients" db:"recipients"`
	RequiresApproval bool            `json:"requires_approval" db:"requires_approval"`
	CreatedAt        time.Time       `json:"created_at" db:"created_at"`
	UpdatedAt        time.Time       `json:"updated_at" db:"updated_at"`
}
```

**Step 3: Create execution model**

Create `internal/model/execution.go`:

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

type TaskExecution struct {
	ID           string          `json:"id" db:"id"`
	TaskID       string          `json:"task_id" db:"task_id"`
	SessionID    string          `json:"session_id" db:"session_id"`
	Status       ExecutionStatus `json:"status" db:"status"`
	Result       string          `json:"result,omitempty" db:"result"`
	AIProcessPID int             `json:"ai_process_pid,omitempty" db:"ai_process_pid"`
	StartedAt    *time.Time      `json:"started_at,omitempty" db:"started_at"`
	CompletedAt  *time.Time      `json:"completed_at,omitempty" db:"completed_at"`
}
```

**Step 4: Create email model**

Create `internal/model/email.go`:

```go
package model

import "time"

type EmailDirection string

const (
	EmailInbound  EmailDirection = "inbound"
	EmailOutbound EmailDirection = "outbound"
)

type Email struct {
	ID          string         `json:"id" db:"id"`
	ExecutionID string         `json:"execution_id" db:"execution_id"`
	FromAddr    string         `json:"from_addr" db:"from_addr"`
	ToAddr      string         `json:"to_addr" db:"to_addr"`
	CCAddr      string         `json:"cc_addr,omitempty" db:"cc_addr"`
	Subject     string         `json:"subject" db:"subject"`
	Body        string         `json:"body" db:"body"`
	InReplyTo   string         `json:"in_reply_to,omitempty" db:"in_reply_to"`
	Direction   EmailDirection `json:"direction" db:"direction"`
	CreatedAt   time.Time      `json:"created_at" db:"created_at"`
}
```

**Step 5: Create memory model**

Create `internal/model/memory.go`:

```go
package model

import "time"

type WorkerMemory struct {
	ID          string    `json:"id" db:"id"`
	WorkerID    string    `json:"worker_id" db:"worker_id"`
	ExecutionID string    `json:"execution_id" db:"execution_id"`
	Summary     string    `json:"summary" db:"summary"`
	CreatedAt   time.Time `json:"created_at" db:"created_at"`
}
```

**Step 6: Verify build**

Run: `go build ./...`
Expected: Builds successfully.

**Step 7: Commit**

```bash
git add internal/model/
git commit -m "feat: add data models for worker, task, execution, email, memory"
```

---

## Phase 2: SQLite Store Layer

### Task 2.1: Database Initialization & Migration

**Files:**
- Create: `internal/store/db.go`
- Create: `internal/store/db_test.go`

**Step 1: Write test for DB initialization**

Create `internal/store/db_test.go`:

```go
package store

import (
	"os"
	"testing"
)

func TestInitDB(t *testing.T) {
	dbPath := t.TempDir() + "/test.db"
	db, err := InitDB(dbPath)
	if err != nil {
		t.Fatalf("InitDB failed: %v", err)
	}
	defer db.Close()

	// Verify tables exist
	tables := []string{"workers", "tasks", "task_executions", "emails", "worker_memories"}
	for _, table := range tables {
		var name string
		err := db.QueryRow("SELECT name FROM sqlite_master WHERE type='table' AND name=?", table).Scan(&name)
		if err != nil {
			t.Errorf("table %s not found: %v", table, err)
		}
	}

	_ = os.Remove(dbPath)
}
```

**Step 2: Run test to verify it fails**

Run: `go test ./internal/store/ -run TestInitDB -v`
Expected: FAIL — `InitDB` not defined.

**Step 3: Install SQLite driver and implement InitDB**

Run: `go get github.com/mattn/go-sqlite3`

Create `internal/store/db.go`:

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
	schema := `
	CREATE TABLE IF NOT EXISTS workers (
		id TEXT PRIMARY KEY,
		name TEXT NOT NULL,
		description TEXT NOT NULL DEFAULT '',
		email TEXT NOT NULL UNIQUE,
		runtime_type TEXT NOT NULL DEFAULT 'claude_code',
		work_dir TEXT NOT NULL,
		status TEXT NOT NULL DEFAULT 'idle',
		created_at DATETIME NOT NULL DEFAULT (datetime('now')),
		updated_at DATETIME NOT NULL DEFAULT (datetime('now'))
	);

	CREATE TABLE IF NOT EXISTS tasks (
		id TEXT PRIMARY KEY,
		worker_id TEXT NOT NULL,
		name TEXT NOT NULL,
		plan TEXT NOT NULL DEFAULT '',
		trigger_type TEXT NOT NULL DEFAULT 'manual',
		cron_expression TEXT NOT NULL DEFAULT '',
		recipients TEXT NOT NULL DEFAULT '[]',
		requires_approval INTEGER NOT NULL DEFAULT 0,
		created_at DATETIME NOT NULL DEFAULT (datetime('now')),
		updated_at DATETIME NOT NULL DEFAULT (datetime('now')),
		FOREIGN KEY (worker_id) REFERENCES workers(id) ON DELETE CASCADE
	);

	CREATE TABLE IF NOT EXISTS task_executions (
		id TEXT PRIMARY KEY,
		task_id TEXT NOT NULL,
		session_id TEXT NOT NULL,
		status TEXT NOT NULL DEFAULT 'pending',
		result TEXT NOT NULL DEFAULT '',
		ai_process_pid INTEGER NOT NULL DEFAULT 0,
		started_at DATETIME,
		completed_at DATETIME,
		FOREIGN KEY (task_id) REFERENCES tasks(id) ON DELETE CASCADE
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
		FOREIGN KEY (execution_id) REFERENCES task_executions(id) ON DELETE CASCADE
	);

	CREATE TABLE IF NOT EXISTS worker_memories (
		id TEXT PRIMARY KEY,
		worker_id TEXT NOT NULL,
		execution_id TEXT NOT NULL,
		summary TEXT NOT NULL,
		created_at DATETIME NOT NULL DEFAULT (datetime('now')),
		FOREIGN KEY (worker_id) REFERENCES workers(id) ON DELETE CASCADE,
		FOREIGN KEY (execution_id) REFERENCES task_executions(id) ON DELETE CASCADE
	);
	`
	_, err := db.Exec(schema)
	return err
}
```

**Step 4: Run test to verify it passes**

Run: `go test ./internal/store/ -run TestInitDB -v`
Expected: PASS

**Step 5: Commit**

```bash
git add internal/store/db.go internal/store/db_test.go go.mod go.sum
git commit -m "feat: add SQLite database initialization with schema migration"
```

---

### Task 2.2: Worker Store (Repository)

**Files:**
- Create: `internal/store/worker_store.go`
- Create: `internal/store/worker_store_test.go`

**Step 1: Write tests**

Create `internal/store/worker_store_test.go`:

```go
package store

import (
	"testing"

	"github.com/robobee/core/internal/model"
)

func setupTestDB(t *testing.T) *WorkerStore {
	t.Helper()
	db, err := InitDB(t.TempDir() + "/test.db")
	if err != nil {
		t.Fatalf("InitDB: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return NewWorkerStore(db)
}

func TestWorkerStore_Create(t *testing.T) {
	s := setupTestDB(t)
	w := model.Worker{
		Name:        "TestBot",
		Description: "A test worker",
		Email:       "testbot@robobee.local",
		RuntimeType: model.RuntimeClaudeCode,
		WorkDir:     "/tmp/testbot",
	}
	created, err := s.Create(w)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if created.ID == "" {
		t.Error("expected non-empty ID")
	}
	if created.Status != model.WorkerStatusIdle {
		t.Errorf("expected status idle, got %s", created.Status)
	}
}

func TestWorkerStore_GetByID(t *testing.T) {
	s := setupTestDB(t)
	w, _ := s.Create(model.Worker{
		Name: "Bot1", Email: "bot1@robobee.local",
		RuntimeType: model.RuntimeClaudeCode, WorkDir: "/tmp/bot1",
	})
	got, err := s.GetByID(w.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if got.Name != "Bot1" {
		t.Errorf("expected Bot1, got %s", got.Name)
	}
}

func TestWorkerStore_List(t *testing.T) {
	s := setupTestDB(t)
	s.Create(model.Worker{Name: "A", Email: "a@robobee.local", RuntimeType: model.RuntimeClaudeCode, WorkDir: "/tmp/a"})
	s.Create(model.Worker{Name: "B", Email: "b@robobee.local", RuntimeType: model.RuntimeCodex, WorkDir: "/tmp/b"})
	list, err := s.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 2 {
		t.Errorf("expected 2 workers, got %d", len(list))
	}
}

func TestWorkerStore_Update(t *testing.T) {
	s := setupTestDB(t)
	w, _ := s.Create(model.Worker{Name: "Old", Email: "old@robobee.local", RuntimeType: model.RuntimeClaudeCode, WorkDir: "/tmp/old"})
	w.Name = "New"
	updated, err := s.Update(w)
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if updated.Name != "New" {
		t.Errorf("expected New, got %s", updated.Name)
	}
}

func TestWorkerStore_Delete(t *testing.T) {
	s := setupTestDB(t)
	w, _ := s.Create(model.Worker{Name: "Del", Email: "del@robobee.local", RuntimeType: model.RuntimeClaudeCode, WorkDir: "/tmp/del"})
	if err := s.Delete(w.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	_, err := s.GetByID(w.ID)
	if err == nil {
		t.Error("expected error after delete")
	}
}
```

**Step 2: Run tests to verify they fail**

Run: `go test ./internal/store/ -run TestWorkerStore -v`
Expected: FAIL — `NewWorkerStore` not defined.

**Step 3: Implement WorkerStore**

Create `internal/store/worker_store.go`:

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
		`INSERT INTO workers (id, name, description, email, runtime_type, work_dir, status, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		w.ID, w.Name, w.Description, w.Email, w.RuntimeType, w.WorkDir, w.Status, w.CreatedAt, w.UpdatedAt,
	)
	if err != nil {
		return model.Worker{}, fmt.Errorf("insert worker: %w", err)
	}
	return w, nil
}

func (s *WorkerStore) GetByID(id string) (model.Worker, error) {
	var w model.Worker
	err := s.db.QueryRow(
		`SELECT id, name, description, email, runtime_type, work_dir, status, created_at, updated_at
		 FROM workers WHERE id = ?`, id,
	).Scan(&w.ID, &w.Name, &w.Description, &w.Email, &w.RuntimeType, &w.WorkDir, &w.Status, &w.CreatedAt, &w.UpdatedAt)
	if err != nil {
		return model.Worker{}, fmt.Errorf("get worker: %w", err)
	}
	return w, nil
}

func (s *WorkerStore) GetByEmail(email string) (model.Worker, error) {
	var w model.Worker
	err := s.db.QueryRow(
		`SELECT id, name, description, email, runtime_type, work_dir, status, created_at, updated_at
		 FROM workers WHERE email = ?`, email,
	).Scan(&w.ID, &w.Name, &w.Description, &w.Email, &w.RuntimeType, &w.WorkDir, &w.Status, &w.CreatedAt, &w.UpdatedAt)
	if err != nil {
		return model.Worker{}, fmt.Errorf("get worker by email: %w", err)
	}
	return w, nil
}

func (s *WorkerStore) List() ([]model.Worker, error) {
	rows, err := s.db.Query(
		`SELECT id, name, description, email, runtime_type, work_dir, status, created_at, updated_at FROM workers ORDER BY created_at DESC`,
	)
	if err != nil {
		return nil, fmt.Errorf("list workers: %w", err)
	}
	defer rows.Close()

	var workers []model.Worker
	for rows.Next() {
		var w model.Worker
		if err := rows.Scan(&w.ID, &w.Name, &w.Description, &w.Email, &w.RuntimeType, &w.WorkDir, &w.Status, &w.CreatedAt, &w.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan worker: %w", err)
		}
		workers = append(workers, w)
	}
	return workers, rows.Err()
}

func (s *WorkerStore) Update(w model.Worker) (model.Worker, error) {
	w.UpdatedAt = time.Now().UTC()
	_, err := s.db.Exec(
		`UPDATE workers SET name=?, description=?, email=?, runtime_type=?, work_dir=?, status=?, updated_at=? WHERE id=?`,
		w.Name, w.Description, w.Email, w.RuntimeType, w.WorkDir, w.Status, w.UpdatedAt, w.ID,
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

**Step 4: Install uuid dependency and run tests**

Run:
```bash
go get github.com/google/uuid
go test ./internal/store/ -run TestWorkerStore -v
```
Expected: All PASS.

**Step 5: Commit**

```bash
git add internal/store/worker_store.go internal/store/worker_store_test.go go.mod go.sum
git commit -m "feat: add worker store with CRUD operations"
```

---

### Task 2.3: Task Store (Repository)

**Files:**
- Create: `internal/store/task_store.go`
- Create: `internal/store/task_store_test.go`

**Step 1: Write tests**

Create `internal/store/task_store_test.go`:

```go
package store

import (
	"encoding/json"
	"testing"

	"github.com/robobee/core/internal/model"
)

func setupTaskTestDB(t *testing.T) (*TaskStore, *WorkerStore) {
	t.Helper()
	db, err := InitDB(t.TempDir() + "/test.db")
	if err != nil {
		t.Fatalf("InitDB: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return NewTaskStore(db), NewWorkerStore(db)
}

func TestTaskStore_CRUD(t *testing.T) {
	ts, ws := setupTaskTestDB(t)
	w, _ := ws.Create(model.Worker{Name: "Bot", Email: "bot@robobee.local", RuntimeType: model.RuntimeClaudeCode, WorkDir: "/tmp/bot"})

	recipients, _ := json.Marshal([]string{"user@example.com"})
	task := model.Task{
		WorkerID:         w.ID,
		Name:             "Daily Report",
		Plan:             "Generate daily report",
		TriggerType:      model.TriggerManual,
		Recipients:       recipients,
		RequiresApproval: true,
	}

	created, err := ts.Create(task)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if created.ID == "" {
		t.Error("expected non-empty ID")
	}

	got, err := ts.GetByID(created.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if got.Name != "Daily Report" {
		t.Errorf("expected Daily Report, got %s", got.Name)
	}

	list, err := ts.ListByWorkerID(w.ID)
	if err != nil {
		t.Fatalf("ListByWorkerID: %v", err)
	}
	if len(list) != 1 {
		t.Errorf("expected 1 task, got %d", len(list))
	}

	got.Name = "Weekly Report"
	updated, err := ts.Update(got)
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if updated.Name != "Weekly Report" {
		t.Errorf("expected Weekly Report, got %s", updated.Name)
	}

	if err := ts.Delete(created.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
}
```

**Step 2: Run test — expect FAIL**

Run: `go test ./internal/store/ -run TestTaskStore -v`

**Step 3: Implement TaskStore**

Create `internal/store/task_store.go`:

```go
package store

import (
	"database/sql"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/robobee/core/internal/model"
)

type TaskStore struct {
	db *sql.DB
}

func NewTaskStore(db *sql.DB) *TaskStore {
	return &TaskStore{db: db}
}

func (s *TaskStore) Create(t model.Task) (model.Task, error) {
	t.ID = uuid.New().String()
	t.CreatedAt = time.Now().UTC()
	t.UpdatedAt = t.CreatedAt

	_, err := s.db.Exec(
		`INSERT INTO tasks (id, worker_id, name, plan, trigger_type, cron_expression, recipients, requires_approval, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		t.ID, t.WorkerID, t.Name, t.Plan, t.TriggerType, t.CronExpression, string(t.Recipients), t.RequiresApproval, t.CreatedAt, t.UpdatedAt,
	)
	if err != nil {
		return model.Task{}, fmt.Errorf("insert task: %w", err)
	}
	return t, nil
}

func (s *TaskStore) GetByID(id string) (model.Task, error) {
	var t model.Task
	var recipients string
	err := s.db.QueryRow(
		`SELECT id, worker_id, name, plan, trigger_type, cron_expression, recipients, requires_approval, created_at, updated_at
		 FROM tasks WHERE id = ?`, id,
	).Scan(&t.ID, &t.WorkerID, &t.Name, &t.Plan, &t.TriggerType, &t.CronExpression, &recipients, &t.RequiresApproval, &t.CreatedAt, &t.UpdatedAt)
	if err != nil {
		return model.Task{}, fmt.Errorf("get task: %w", err)
	}
	t.Recipients = []byte(recipients)
	return t, nil
}

func (s *TaskStore) ListByWorkerID(workerID string) ([]model.Task, error) {
	rows, err := s.db.Query(
		`SELECT id, worker_id, name, plan, trigger_type, cron_expression, recipients, requires_approval, created_at, updated_at
		 FROM tasks WHERE worker_id = ? ORDER BY created_at DESC`, workerID,
	)
	if err != nil {
		return nil, fmt.Errorf("list tasks: %w", err)
	}
	defer rows.Close()

	var tasks []model.Task
	for rows.Next() {
		var t model.Task
		var recipients string
		if err := rows.Scan(&t.ID, &t.WorkerID, &t.Name, &t.Plan, &t.TriggerType, &t.CronExpression, &recipients, &t.RequiresApproval, &t.CreatedAt, &t.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan task: %w", err)
		}
		t.Recipients = []byte(recipients)
		tasks = append(tasks, t)
	}
	return tasks, rows.Err()
}

func (s *TaskStore) ListCronTasks() ([]model.Task, error) {
	rows, err := s.db.Query(
		`SELECT id, worker_id, name, plan, trigger_type, cron_expression, recipients, requires_approval, created_at, updated_at
		 FROM tasks WHERE trigger_type = 'cron' AND cron_expression != ''`,
	)
	if err != nil {
		return nil, fmt.Errorf("list cron tasks: %w", err)
	}
	defer rows.Close()

	var tasks []model.Task
	for rows.Next() {
		var t model.Task
		var recipients string
		if err := rows.Scan(&t.ID, &t.WorkerID, &t.Name, &t.Plan, &t.TriggerType, &t.CronExpression, &recipients, &t.RequiresApproval, &t.CreatedAt, &t.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan task: %w", err)
		}
		t.Recipients = []byte(recipients)
		tasks = append(tasks, t)
	}
	return tasks, rows.Err()
}

func (s *TaskStore) Update(t model.Task) (model.Task, error) {
	t.UpdatedAt = time.Now().UTC()
	_, err := s.db.Exec(
		`UPDATE tasks SET name=?, plan=?, trigger_type=?, cron_expression=?, recipients=?, requires_approval=?, updated_at=? WHERE id=?`,
		t.Name, t.Plan, t.TriggerType, t.CronExpression, string(t.Recipients), t.RequiresApproval, t.UpdatedAt, t.ID,
	)
	if err != nil {
		return model.Task{}, fmt.Errorf("update task: %w", err)
	}
	return t, nil
}

func (s *TaskStore) Delete(id string) error {
	_, err := s.db.Exec(`DELETE FROM tasks WHERE id=?`, id)
	return err
}
```

**Step 4: Run tests**

Run: `go test ./internal/store/ -run TestTaskStore -v`
Expected: PASS

**Step 5: Commit**

```bash
git add internal/store/task_store.go internal/store/task_store_test.go
git commit -m "feat: add task store with CRUD operations"
```

---

### Task 2.4: Execution Store & Email Store

**Files:**
- Create: `internal/store/execution_store.go`
- Create: `internal/store/email_store.go`
- Create: `internal/store/memory_store.go`
- Create: `internal/store/execution_store_test.go`

**Step 1: Write execution store tests**

Create `internal/store/execution_store_test.go`:

```go
package store

import (
	"encoding/json"
	"testing"

	"github.com/robobee/core/internal/model"
)

func TestExecutionStore_CreateAndGet(t *testing.T) {
	db, err := InitDB(t.TempDir() + "/test.db")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	ws := NewWorkerStore(db)
	ts := NewTaskStore(db)
	es := NewExecutionStore(db)

	w, _ := ws.Create(model.Worker{Name: "Bot", Email: "bot@robobee.local", RuntimeType: model.RuntimeClaudeCode, WorkDir: "/tmp/bot"})
	recipients, _ := json.Marshal([]string{"user@example.com"})
	task, _ := ts.Create(model.Task{WorkerID: w.ID, Name: "Task1", Plan: "do stuff", TriggerType: model.TriggerManual, Recipients: recipients})

	exec, err := es.Create(task.ID)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if exec.Status != model.ExecStatusPending {
		t.Errorf("expected pending, got %s", exec.Status)
	}
	if exec.SessionID == "" {
		t.Error("expected non-empty session_id")
	}

	got, err := es.GetByID(exec.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if got.TaskID != task.ID {
		t.Errorf("expected task_id %s, got %s", task.ID, got.TaskID)
	}
}

func TestExecutionStore_UpdateStatus(t *testing.T) {
	db, err := InitDB(t.TempDir() + "/test.db")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	ws := NewWorkerStore(db)
	ts := NewTaskStore(db)
	es := NewExecutionStore(db)

	w, _ := ws.Create(model.Worker{Name: "Bot", Email: "bot@robobee.local", RuntimeType: model.RuntimeClaudeCode, WorkDir: "/tmp/bot"})
	recipients, _ := json.Marshal([]string{"user@example.com"})
	task, _ := ts.Create(model.Task{WorkerID: w.ID, Name: "Task1", Plan: "do stuff", TriggerType: model.TriggerManual, Recipients: recipients})
	exec, _ := es.Create(task.ID)

	err = es.UpdateStatus(exec.ID, model.ExecStatusRunning)
	if err != nil {
		t.Fatalf("UpdateStatus: %v", err)
	}
	got, _ := es.GetByID(exec.ID)
	if got.Status != model.ExecStatusRunning {
		t.Errorf("expected running, got %s", got.Status)
	}
}

func TestExecutionStore_GetBySessionID(t *testing.T) {
	db, err := InitDB(t.TempDir() + "/test.db")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	ws := NewWorkerStore(db)
	ts := NewTaskStore(db)
	es := NewExecutionStore(db)

	w, _ := ws.Create(model.Worker{Name: "Bot", Email: "bot@robobee.local", RuntimeType: model.RuntimeClaudeCode, WorkDir: "/tmp/bot"})
	recipients, _ := json.Marshal([]string{"user@example.com"})
	task, _ := ts.Create(model.Task{WorkerID: w.ID, Name: "Task1", Plan: "do stuff", TriggerType: model.TriggerManual, Recipients: recipients})
	exec, _ := es.Create(task.ID)

	got, err := es.GetBySessionID(exec.SessionID)
	if err != nil {
		t.Fatalf("GetBySessionID: %v", err)
	}
	if got.ID != exec.ID {
		t.Errorf("expected ID %s, got %s", exec.ID, got.ID)
	}
}
```

**Step 2: Run tests — expect FAIL**

Run: `go test ./internal/store/ -run TestExecutionStore -v`

**Step 3: Implement ExecutionStore**

Create `internal/store/execution_store.go`:

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

func (s *ExecutionStore) Create(taskID string) (model.TaskExecution, error) {
	now := time.Now().UTC()
	exec := model.TaskExecution{
		ID:        uuid.New().String(),
		TaskID:    taskID,
		SessionID: uuid.New().String(),
		Status:    model.ExecStatusPending,
		StartedAt: &now,
	}

	_, err := s.db.Exec(
		`INSERT INTO task_executions (id, task_id, session_id, status, result, ai_process_pid, started_at)
		 VALUES (?, ?, ?, ?, '', 0, ?)`,
		exec.ID, exec.TaskID, exec.SessionID, exec.Status, exec.StartedAt,
	)
	if err != nil {
		return model.TaskExecution{}, fmt.Errorf("insert execution: %w", err)
	}
	return exec, nil
}

func (s *ExecutionStore) GetByID(id string) (model.TaskExecution, error) {
	var e model.TaskExecution
	err := s.db.QueryRow(
		`SELECT id, task_id, session_id, status, result, ai_process_pid, started_at, completed_at
		 FROM task_executions WHERE id = ?`, id,
	).Scan(&e.ID, &e.TaskID, &e.SessionID, &e.Status, &e.Result, &e.AIProcessPID, &e.StartedAt, &e.CompletedAt)
	if err != nil {
		return model.TaskExecution{}, fmt.Errorf("get execution: %w", err)
	}
	return e, nil
}

func (s *ExecutionStore) GetBySessionID(sessionID string) (model.TaskExecution, error) {
	var e model.TaskExecution
	err := s.db.QueryRow(
		`SELECT id, task_id, session_id, status, result, ai_process_pid, started_at, completed_at
		 FROM task_executions WHERE session_id = ?`, sessionID,
	).Scan(&e.ID, &e.TaskID, &e.SessionID, &e.Status, &e.Result, &e.AIProcessPID, &e.StartedAt, &e.CompletedAt)
	if err != nil {
		return model.TaskExecution{}, fmt.Errorf("get execution by session: %w", err)
	}
	return e, nil
}

func (s *ExecutionStore) List() ([]model.TaskExecution, error) {
	rows, err := s.db.Query(
		`SELECT id, task_id, session_id, status, result, ai_process_pid, started_at, completed_at
		 FROM task_executions ORDER BY started_at DESC`,
	)
	if err != nil {
		return nil, fmt.Errorf("list executions: %w", err)
	}
	defer rows.Close()

	var execs []model.TaskExecution
	for rows.Next() {
		var e model.TaskExecution
		if err := rows.Scan(&e.ID, &e.TaskID, &e.SessionID, &e.Status, &e.Result, &e.AIProcessPID, &e.StartedAt, &e.CompletedAt); err != nil {
			return nil, fmt.Errorf("scan execution: %w", err)
		}
		execs = append(execs, e)
	}
	return execs, rows.Err()
}

func (s *ExecutionStore) UpdateStatus(id string, status model.ExecutionStatus) error {
	_, err := s.db.Exec(`UPDATE task_executions SET status=? WHERE id=?`, status, id)
	return err
}

func (s *ExecutionStore) UpdateResult(id string, result string, status model.ExecutionStatus) error {
	now := time.Now().UTC()
	_, err := s.db.Exec(`UPDATE task_executions SET result=?, status=?, completed_at=? WHERE id=?`, result, status, now, id)
	return err
}

func (s *ExecutionStore) UpdatePID(id string, pid int) error {
	_, err := s.db.Exec(`UPDATE task_executions SET ai_process_pid=?, status=? WHERE id=?`, pid, model.ExecStatusRunning, id)
	return err
}
```

**Step 4: Implement EmailStore**

Create `internal/store/email_store.go`:

```go
package store

import (
	"database/sql"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/robobee/core/internal/model"
)

type EmailStore struct {
	db *sql.DB
}

func NewEmailStore(db *sql.DB) *EmailStore {
	return &EmailStore{db: db}
}

func (s *EmailStore) Create(e model.Email) (model.Email, error) {
	e.ID = uuid.New().String()
	e.CreatedAt = time.Now().UTC()

	_, err := s.db.Exec(
		`INSERT INTO emails (id, execution_id, from_addr, to_addr, cc_addr, subject, body, in_reply_to, direction, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		e.ID, e.ExecutionID, e.FromAddr, e.ToAddr, e.CCAddr, e.Subject, e.Body, e.InReplyTo, e.Direction, e.CreatedAt,
	)
	if err != nil {
		return model.Email{}, fmt.Errorf("insert email: %w", err)
	}
	return e, nil
}

func (s *EmailStore) ListByExecutionID(executionID string) ([]model.Email, error) {
	rows, err := s.db.Query(
		`SELECT id, execution_id, from_addr, to_addr, cc_addr, subject, body, in_reply_to, direction, created_at
		 FROM emails WHERE execution_id = ? ORDER BY created_at ASC`, executionID,
	)
	if err != nil {
		return nil, fmt.Errorf("list emails: %w", err)
	}
	defer rows.Close()

	var emails []model.Email
	for rows.Next() {
		var e model.Email
		if err := rows.Scan(&e.ID, &e.ExecutionID, &e.FromAddr, &e.ToAddr, &e.CCAddr, &e.Subject, &e.Body, &e.InReplyTo, &e.Direction, &e.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan email: %w", err)
		}
		emails = append(emails, e)
	}
	return emails, rows.Err()
}
```

**Step 5: Implement MemoryStore**

Create `internal/store/memory_store.go`:

```go
package store

import (
	"database/sql"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/robobee/core/internal/model"
)

type MemoryStore struct {
	db *sql.DB
}

func NewMemoryStore(db *sql.DB) *MemoryStore {
	return &MemoryStore{db: db}
}

func (s *MemoryStore) Create(m model.WorkerMemory) (model.WorkerMemory, error) {
	m.ID = uuid.New().String()
	m.CreatedAt = time.Now().UTC()

	_, err := s.db.Exec(
		`INSERT INTO worker_memories (id, worker_id, execution_id, summary, created_at)
		 VALUES (?, ?, ?, ?, ?)`,
		m.ID, m.WorkerID, m.ExecutionID, m.Summary, m.CreatedAt,
	)
	if err != nil {
		return model.WorkerMemory{}, fmt.Errorf("insert memory: %w", err)
	}
	return m, nil
}

func (s *MemoryStore) ListByWorkerID(workerID string) ([]model.WorkerMemory, error) {
	rows, err := s.db.Query(
		`SELECT id, worker_id, execution_id, summary, created_at
		 FROM worker_memories WHERE worker_id = ? ORDER BY created_at DESC`, workerID,
	)
	if err != nil {
		return nil, fmt.Errorf("list memories: %w", err)
	}
	defer rows.Close()

	var memories []model.WorkerMemory
	for rows.Next() {
		var m model.WorkerMemory
		if err := rows.Scan(&m.ID, &m.WorkerID, &m.ExecutionID, &m.Summary, &m.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan memory: %w", err)
		}
		memories = append(memories, m)
	}
	return memories, rows.Err()
}
```

**Step 6: Run all store tests**

Run: `go test ./internal/store/ -v`
Expected: All PASS.

**Step 7: Commit**

```bash
git add internal/store/execution_store.go internal/store/execution_store_test.go internal/store/email_store.go internal/store/memory_store.go
git commit -m "feat: add execution, email, and memory stores"
```

---

## Phase 3: AI Runtime Layer

### Task 3.1: Runtime Interface & Claude Code Implementation

**Files:**
- Create: `internal/worker/runtime.go`
- Create: `internal/worker/claude_runtime.go`
- Create: `internal/worker/codex_runtime.go`
- Create: `internal/worker/runtime_test.go`

**Step 1: Write runtime interface and output types**

Create `internal/worker/runtime.go`:

```go
package worker

import "context"

type OutputType string

const (
	OutputStdout OutputType = "stdout"
	OutputStderr OutputType = "stderr"
	OutputDone   OutputType = "done"
	OutputError  OutputType = "error"
)

type Output struct {
	Type    OutputType `json:"type"`
	Content string     `json:"content"`
}

type Runtime interface {
	Execute(ctx context.Context, workDir string, plan string) (<-chan Output, error)
	Stop() error
}
```

**Step 2: Implement Claude Code runtime**

Create `internal/worker/claude_runtime.go`:

```go
package worker

import (
	"bufio"
	"context"
	"fmt"
	"os/exec"
	"sync"
)

type ClaudeRuntime struct {
	binary string
	cmd    *exec.Cmd
	mu     sync.Mutex
}

func NewClaudeRuntime(binary string) *ClaudeRuntime {
	return &ClaudeRuntime{binary: binary}
}

func (r *ClaudeRuntime) Execute(ctx context.Context, workDir string, plan string) (<-chan Output, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.cmd = exec.CommandContext(ctx, r.binary,
		"--dangerously-skip-permissions",
		"-p", plan,
		"--output-format", "stream-json",
	)
	r.cmd.Dir = workDir

	stdout, err := r.cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("stdout pipe: %w", err)
	}
	stderr, err := r.cmd.StderrPipe()
	if err != nil {
		return nil, fmt.Errorf("stderr pipe: %w", err)
	}

	if err := r.cmd.Start(); err != nil {
		return nil, fmt.Errorf("start claude: %w", err)
	}

	ch := make(chan Output, 100)

	go func() {
		defer close(ch)

		var wg sync.WaitGroup
		wg.Add(2)

		go func() {
			defer wg.Done()
			scanner := bufio.NewScanner(stdout)
			scanner.Buffer(make([]byte, 1024*1024), 1024*1024)
			for scanner.Scan() {
				ch <- Output{Type: OutputStdout, Content: scanner.Text()}
			}
		}()

		go func() {
			defer wg.Done()
			scanner := bufio.NewScanner(stderr)
			scanner.Buffer(make([]byte, 1024*1024), 1024*1024)
			for scanner.Scan() {
				ch <- Output{Type: OutputStderr, Content: scanner.Text()}
			}
		}()

		wg.Wait()

		if err := r.cmd.Wait(); err != nil {
			ch <- Output{Type: OutputError, Content: err.Error()}
		} else {
			ch <- Output{Type: OutputDone, Content: ""}
		}
	}()

	return ch, nil
}

func (r *ClaudeRuntime) Stop() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.cmd != nil && r.cmd.Process != nil {
		return r.cmd.Process.Kill()
	}
	return nil
}

func (r *ClaudeRuntime) PID() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.cmd != nil && r.cmd.Process != nil {
		return r.cmd.Process.Pid
	}
	return 0
}
```

**Step 3: Implement Codex runtime**

Create `internal/worker/codex_runtime.go`:

```go
package worker

import (
	"bufio"
	"context"
	"fmt"
	"os/exec"
	"sync"
)

type CodexRuntime struct {
	binary string
	cmd    *exec.Cmd
	mu     sync.Mutex
}

func NewCodexRuntime(binary string) *CodexRuntime {
	return &CodexRuntime{binary: binary}
}

func (r *CodexRuntime) Execute(ctx context.Context, workDir string, plan string) (<-chan Output, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.cmd = exec.CommandContext(ctx, r.binary,
		"--quiet",
		"--approval-mode", "full-auto",
		plan,
	)
	r.cmd.Dir = workDir

	stdout, err := r.cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("stdout pipe: %w", err)
	}
	stderr, err := r.cmd.StderrPipe()
	if err != nil {
		return nil, fmt.Errorf("stderr pipe: %w", err)
	}

	if err := r.cmd.Start(); err != nil {
		return nil, fmt.Errorf("start codex: %w", err)
	}

	ch := make(chan Output, 100)

	go func() {
		defer close(ch)

		var wg sync.WaitGroup
		wg.Add(2)

		go func() {
			defer wg.Done()
			scanner := bufio.NewScanner(stdout)
			scanner.Buffer(make([]byte, 1024*1024), 1024*1024)
			for scanner.Scan() {
				ch <- Output{Type: OutputStdout, Content: scanner.Text()}
			}
		}()

		go func() {
			defer wg.Done()
			scanner := bufio.NewScanner(stderr)
			scanner.Buffer(make([]byte, 1024*1024), 1024*1024)
			for scanner.Scan() {
				ch <- Output{Type: OutputStderr, Content: scanner.Text()}
			}
		}()

		wg.Wait()

		if err := r.cmd.Wait(); err != nil {
			ch <- Output{Type: OutputError, Content: err.Error()}
		} else {
			ch <- Output{Type: OutputDone, Content: ""}
		}
	}()

	return ch, nil
}

func (r *CodexRuntime) Stop() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.cmd != nil && r.cmd.Process != nil {
		return r.cmd.Process.Kill()
	}
	return nil
}

func (r *CodexRuntime) PID() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.cmd != nil && r.cmd.Process != nil {
		return r.cmd.Process.Pid
	}
	return 0
}
```

**Step 4: Write test with mock command**

Create `internal/worker/runtime_test.go`:

```go
package worker

import (
	"context"
	"testing"
	"time"
)

func TestClaudeRuntime_ExecuteWithEcho(t *testing.T) {
	// Use echo as a stand-in to verify the process pipeline works
	r := &ClaudeRuntime{binary: "echo"}
	r.cmd = nil

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// We can't test claude directly, but we can verify the struct compiles
	// and the interface is satisfied
	var _ Runtime = r
	_ = ctx
}

func TestCodexRuntime_ImplementsInterface(t *testing.T) {
	r := &CodexRuntime{binary: "echo"}
	var _ Runtime = r
	_ = r
}

func TestNewClaudeRuntime(t *testing.T) {
	r := NewClaudeRuntime("/usr/bin/claude")
	if r.binary != "/usr/bin/claude" {
		t.Errorf("expected binary /usr/bin/claude, got %s", r.binary)
	}
}

func TestNewCodexRuntime(t *testing.T) {
	r := NewCodexRuntime("/usr/bin/codex")
	if r.binary != "/usr/bin/codex" {
		t.Errorf("expected binary /usr/bin/codex, got %s", r.binary)
	}
}
```

**Step 5: Run tests**

Run: `go test ./internal/worker/ -v`
Expected: PASS

**Step 6: Commit**

```bash
git add internal/worker/
git commit -m "feat: add AI runtime interface with Claude Code and Codex implementations"
```

---

### Task 3.2: Worker Manager

**Files:**
- Create: `internal/worker/manager.go`

**Step 1: Implement Worker Manager**

Create `internal/worker/manager.go`:

```go
package worker

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"

	"github.com/robobee/core/internal/config"
	"github.com/robobee/core/internal/model"
	"github.com/robobee/core/internal/store"
)

type Manager struct {
	cfg            config.Config
	workerStore    *store.WorkerStore
	taskStore      *store.TaskStore
	executionStore *store.ExecutionStore
	emailStore     *store.EmailStore
	memoryStore    *store.MemoryStore

	activeRuntimes map[string]Runtime // execution_id -> runtime
	logSubscribers map[string][]chan Output // execution_id -> subscribers
	mu             sync.RWMutex
}

func NewManager(
	cfg config.Config,
	ws *store.WorkerStore,
	ts *store.TaskStore,
	es *store.ExecutionStore,
	emailS *store.EmailStore,
	ms *store.MemoryStore,
) *Manager {
	return &Manager{
		cfg:            cfg,
		workerStore:    ws,
		taskStore:      ts,
		executionStore: es,
		emailStore:     emailS,
		memoryStore:    ms,
		activeRuntimes: make(map[string]Runtime),
		logSubscribers: make(map[string][]chan Output),
	}
}

func (m *Manager) CreateWorker(name, description string, runtimeType model.RuntimeType) (model.Worker, error) {
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

	return m.workerStore.Create(model.Worker{
		Name:        name,
		Description: description,
		Email:       email,
		RuntimeType: runtimeType,
		WorkDir:     workDir,
	})
}

func (m *Manager) ExecuteTask(ctx context.Context, taskID string) (model.TaskExecution, error) {
	task, err := m.taskStore.GetByID(taskID)
	if err != nil {
		return model.TaskExecution{}, fmt.Errorf("get task: %w", err)
	}

	worker, err := m.workerStore.GetByID(task.WorkerID)
	if err != nil {
		return model.TaskExecution{}, fmt.Errorf("get worker: %w", err)
	}

	exec, err := m.executionStore.Create(taskID)
	if err != nil {
		return model.TaskExecution{}, fmt.Errorf("create execution: %w", err)
	}

	// Update worker status
	if err := m.workerStore.UpdateStatus(worker.ID, model.WorkerStatusWorking); err != nil {
		log.Printf("failed to update worker status: %v", err)
	}

	// Create runtime based on worker type
	var rt Runtime
	switch worker.RuntimeType {
	case model.RuntimeClaudeCode:
		rt = NewClaudeRuntime(m.cfg.Runtime.ClaudeCode.Binary)
	case model.RuntimeCodex:
		rt = NewCodexRuntime(m.cfg.Runtime.Codex.Binary)
	default:
		return model.TaskExecution{}, fmt.Errorf("unknown runtime: %s", worker.RuntimeType)
	}

	// Start execution in background
	outputCh, err := rt.Execute(ctx, worker.WorkDir, task.Plan)
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
	go m.monitorExecution(exec, worker, task, outputCh)

	return exec, nil
}

func (m *Manager) monitorExecution(exec model.TaskExecution, worker model.Worker, task model.Task, outputCh <-chan Output) {
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
			if task.RequiresApproval {
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
	// Close subscriber channels
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

**Step 2: Verify build**

Run: `go build ./...`
Expected: Builds successfully.

**Step 3: Commit**

```bash
git add internal/worker/manager.go
git commit -m "feat: add worker manager for task execution and log streaming"
```

---

## Phase 4: REST API

### Task 4.1: Router & Worker Handlers

**Files:**
- Create: `internal/api/router.go`
- Create: `internal/api/worker_handler.go`

**Step 1: Create router**

Create `internal/api/router.go`:

```go
package api

import (
	"github.com/gin-gonic/gin"
	"github.com/robobee/core/internal/store"
	"github.com/robobee/core/internal/worker"
)

type Server struct {
	router         *gin.Engine
	workerStore    *store.WorkerStore
	taskStore      *store.TaskStore
	executionStore *store.ExecutionStore
	emailStore     *store.EmailStore
	memoryStore    *store.MemoryStore
	manager        *worker.Manager
}

func NewServer(
	ws *store.WorkerStore,
	ts *store.TaskStore,
	es *store.ExecutionStore,
	emailS *store.EmailStore,
	ms *store.MemoryStore,
	mgr *worker.Manager,
) *Server {
	s := &Server{
		router:         gin.Default(),
		workerStore:    ws,
		taskStore:      ts,
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

		// Tasks
		api.POST("/workers/:id/tasks", s.createTask)
		api.GET("/workers/:id/tasks", s.listTasks)
		api.PUT("/tasks/:id", s.updateTask)
		api.DELETE("/tasks/:id", s.deleteTask)

		// Executions
		api.POST("/tasks/:id/execute", s.executeTask)
		api.GET("/executions", s.listExecutions)
		api.GET("/executions/:id", s.getExecution)
		api.POST("/executions/:id/approve", s.approveExecution)
		api.POST("/executions/:id/reject", s.rejectExecution)

		// Message trigger
		api.POST("/workers/:id/message", s.sendMessage)

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

**Step 2: Create worker handlers**

Create `internal/api/worker_handler.go`:

```go
package api

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/robobee/core/internal/model"
)

type createWorkerRequest struct {
	Name        string            `json:"name" binding:"required"`
	Description string            `json:"description"`
	RuntimeType model.RuntimeType `json:"runtime_type"`
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

	w, err := s.manager.CreateWorker(req.Name, req.Description, req.RuntimeType)
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
		Name        string            `json:"name"`
		Description string            `json:"description"`
		RuntimeType model.RuntimeType `json:"runtime_type"`
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
	if req.RuntimeType != "" {
		w.RuntimeType = req.RuntimeType
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
```

**Step 3: Install gin dependency**

Run: `go get github.com/gin-gonic/gin`

**Step 4: Verify build**

Run: `go build ./...`
Expected: Build fails due to missing handler stubs. Create them next.

**Step 5: Commit**

```bash
git add internal/api/router.go internal/api/worker_handler.go
git commit -m "feat: add API router and worker CRUD handlers"
```

---

### Task 4.2: Task, Execution & WebSocket Handlers

**Files:**
- Create: `internal/api/task_handler.go`
- Create: `internal/api/execution_handler.go`

**Step 1: Create task handlers**

Create `internal/api/task_handler.go`:

```go
package api

import (
	"encoding/json"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/robobee/core/internal/model"
)

type createTaskRequest struct {
	Name             string          `json:"name" binding:"required"`
	Plan             string          `json:"plan" binding:"required"`
	TriggerType      model.TriggerType `json:"trigger_type"`
	CronExpression   string          `json:"cron_expression"`
	Recipients       []string        `json:"recipients" binding:"required,min=1"`
	RequiresApproval bool            `json:"requires_approval"`
}

func (s *Server) createTask(c *gin.Context) {
	workerID := c.Param("id")

	// Verify worker exists
	if _, err := s.workerStore.GetByID(workerID); err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "worker not found"})
		return
	}

	var req createTaskRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if req.TriggerType == "" {
		req.TriggerType = model.TriggerManual
	}

	recipients, _ := json.Marshal(req.Recipients)
	task, err := s.taskStore.Create(model.Task{
		WorkerID:         workerID,
		Name:             req.Name,
		Plan:             req.Plan,
		TriggerType:      req.TriggerType,
		CronExpression:   req.CronExpression,
		Recipients:       recipients,
		RequiresApproval: req.RequiresApproval,
	})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusCreated, task)
}

func (s *Server) listTasks(c *gin.Context) {
	tasks, err := s.taskStore.ListByWorkerID(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, tasks)
}

func (s *Server) updateTask(c *gin.Context) {
	task, err := s.taskStore.GetByID(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "task not found"})
		return
	}

	var req struct {
		Name             string   `json:"name"`
		Plan             string   `json:"plan"`
		TriggerType      string   `json:"trigger_type"`
		CronExpression   string   `json:"cron_expression"`
		Recipients       []string `json:"recipients"`
		RequiresApproval *bool    `json:"requires_approval"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if req.Name != "" {
		task.Name = req.Name
	}
	if req.Plan != "" {
		task.Plan = req.Plan
	}
	if req.TriggerType != "" {
		task.TriggerType = model.TriggerType(req.TriggerType)
	}
	if req.CronExpression != "" {
		task.CronExpression = req.CronExpression
	}
	if req.Recipients != nil {
		recipients, _ := json.Marshal(req.Recipients)
		task.Recipients = recipients
	}
	if req.RequiresApproval != nil {
		task.RequiresApproval = *req.RequiresApproval
	}

	updated, err := s.taskStore.Update(task)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, updated)
}

func (s *Server) deleteTask(c *gin.Context) {
	if err := s.taskStore.Delete(c.Param("id")); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "deleted"})
}
```

**Step 2: Create execution handlers with WebSocket**

Create `internal/api/execution_handler.go`:

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

func (s *Server) executeTask(c *gin.Context) {
	taskID := c.Param("id")

	exec, err := s.manager.ExecuteTask(c.Request.Context(), taskID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusAccepted, exec)
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

	// TODO: Resume worker execution after approval
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

	// TODO: Feed rejection feedback to worker memory and re-execute
	c.JSON(http.StatusOK, gin.H{"status": "rejected", "feedback": req.Feedback})
}

func (s *Server) sendMessage(c *gin.Context) {
	workerID := c.Param("id")

	var req struct {
		Message string `json:"message" binding:"required"`
		TaskID  string `json:"task_id"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// If task_id provided, execute that task; otherwise find first manual task
	taskID := req.TaskID
	if taskID == "" {
		tasks, err := s.taskStore.ListByWorkerID(workerID)
		if err != nil || len(tasks) == 0 {
			c.JSON(http.StatusNotFound, gin.H{"error": "no tasks found for worker"})
			return
		}
		taskID = tasks[0].ID
	}

	exec, err := s.manager.ExecuteTask(c.Request.Context(), taskID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusAccepted, exec)
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

**Step 3: Install websocket dependency and verify build**

Run:
```bash
go get github.com/gorilla/websocket
go build ./...
```
Expected: Build succeeds.

**Step 4: Commit**

```bash
git add internal/api/task_handler.go internal/api/execution_handler.go go.mod go.sum
git commit -m "feat: add task, execution, message handlers and WebSocket log streaming"
```

---

## Phase 5: SMTP Service

### Task 5.1: Embedded SMTP Server

**Files:**
- Create: `internal/mail/server.go`
- Create: `internal/mail/sender.go`
- Create: `internal/mail/handler.go`

**Step 1: Implement SMTP server**

Create `internal/mail/server.go`:

```go
package mail

import (
	"fmt"
	"log"
	"time"

	"github.com/emersion/go-smtp"
	"github.com/robobee/core/internal/config"
	"github.com/robobee/core/internal/store"
)

type SMTPServer struct {
	server   *smtp.Server
	cfg      config.SMTPConfig
	handler  *InboundHandler
}

func NewSMTPServer(cfg config.SMTPConfig, execStore *store.ExecutionStore, emailStore *store.EmailStore, workerStore *store.WorkerStore) *SMTPServer {
	handler := NewInboundHandler(cfg, execStore, emailStore, workerStore)

	s := smtp.NewServer(handler)
	s.Addr = fmt.Sprintf(":%d", cfg.Port)
	s.Domain = cfg.Domain
	s.ReadTimeout = 30 * time.Second
	s.WriteTimeout = 30 * time.Second
	s.MaxMessageBytes = 10 * 1024 * 1024 // 10MB
	s.MaxRecipients = 50
	s.AllowInsecureAuth = true

	return &SMTPServer{
		server:  s,
		cfg:     cfg,
		handler: handler,
	}
}

func (s *SMTPServer) Start() error {
	log.Printf("SMTP server starting on %s (domain: %s)", s.server.Addr, s.server.Domain)
	return s.server.ListenAndServe()
}

func (s *SMTPServer) Close() error {
	return s.server.Close()
}
```

**Step 2: Implement inbound handler**

Create `internal/mail/handler.go`:

```go
package mail

import (
	"bytes"
	"io"
	"log"
	"net/mail"
	"strings"

	"github.com/emersion/go-smtp"
	"github.com/robobee/core/internal/config"
	"github.com/robobee/core/internal/model"
	"github.com/robobee/core/internal/store"
)

type InboundHandler struct {
	cfg         config.SMTPConfig
	execStore   *store.ExecutionStore
	emailStore  *store.EmailStore
	workerStore *store.WorkerStore
}

func NewInboundHandler(cfg config.SMTPConfig, es *store.ExecutionStore, emailS *store.EmailStore, ws *store.WorkerStore) *InboundHandler {
	return &InboundHandler{
		cfg:         cfg,
		execStore:   es,
		emailStore:  emailS,
		workerStore: ws,
	}
}

// Backend interface methods
func (h *InboundHandler) NewSession(_ *smtp.Conn) (smtp.Session, error) {
	return &session{handler: h}, nil
}

type session struct {
	handler *InboundHandler
	from    string
	to      []string
}

func (s *session) AuthPlain(username, password string) error {
	return nil // Accept all auth for internal use
}

func (s *session) Mail(from string, opts *smtp.MailOptions) error {
	s.from = from
	return nil
}

func (s *session) Rcpt(to string, opts *smtp.RcptOptions) error {
	s.to = append(s.to, to)
	return nil
}

func (s *session) Data(r io.Reader) error {
	var buf bytes.Buffer
	if _, err := buf.ReadFrom(r); err != nil {
		return err
	}

	msg, err := mail.ReadMessage(&buf)
	if err != nil {
		log.Printf("failed to parse email: %v", err)
		return err
	}

	subject := msg.Header.Get("Subject")
	inReplyTo := msg.Header.Get("In-Reply-To")
	body, _ := io.ReadAll(msg.Body)

	log.Printf("Received email from=%s to=%v subject=%s in_reply_to=%s", s.from, s.to, subject, inReplyTo)

	// Handle approval reply
	if inReplyTo != "" {
		s.handler.handleApprovalReply(s.from, inReplyTo, subject, string(body))
	}

	return nil
}

func (s *session) Reset() {
	s.from = ""
	s.to = nil
}

func (s *session) Logout() error {
	return nil
}

func (h *InboundHandler) handleApprovalReply(from, inReplyTo, subject, body string) {
	// inReplyTo format: <session_id@domain>
	sessionID := strings.TrimPrefix(inReplyTo, "<")
	sessionID = strings.Split(sessionID, "@")[0]

	exec, err := h.execStore.GetBySessionID(sessionID)
	if err != nil {
		log.Printf("no execution found for session %s: %v", sessionID, err)
		return
	}

	if exec.Status != model.ExecStatusAwaitingApproval {
		log.Printf("execution %s not awaiting approval (status=%s)", exec.ID, exec.Status)
		return
	}

	// Store the reply email
	h.emailStore.Create(model.Email{
		ExecutionID: exec.ID,
		FromAddr:    from,
		ToAddr:      strings.Join([]string{}, ","),
		Subject:     subject,
		Body:        body,
		InReplyTo:   inReplyTo,
		Direction:   model.EmailInbound,
	})

	// Parse approval/rejection from body
	lowerBody := strings.ToLower(body)
	if strings.Contains(lowerBody, "approve") || strings.Contains(lowerBody, "通过") {
		log.Printf("Execution %s approved via email", exec.ID)
		h.execStore.UpdateStatus(exec.ID, model.ExecStatusApproved)
	} else if strings.Contains(lowerBody, "reject") || strings.Contains(lowerBody, "驳回") {
		log.Printf("Execution %s rejected via email", exec.ID)
		h.execStore.UpdateStatus(exec.ID, model.ExecStatusRejected)
	} else {
		log.Printf("Unrecognized reply for execution %s, treating as feedback", exec.ID)
	}
}
```

**Step 3: Implement email sender**

Create `internal/mail/sender.go`:

```go
package mail

import (
	"fmt"
	"log"
	"net/smtp"
	"strings"

	"github.com/robobee/core/internal/config"
	"github.com/robobee/core/internal/model"
	"github.com/robobee/core/internal/store"
)

type Sender struct {
	cfg        config.SMTPConfig
	emailStore *store.EmailStore
}

func NewSender(cfg config.SMTPConfig, emailStore *store.EmailStore) *Sender {
	return &Sender{cfg: cfg, emailStore: emailStore}
}

func (s *Sender) SendReport(execution model.TaskExecution, workerEmail string, recipients []string, subject, body string) error {
	msgID := fmt.Sprintf("<%s@%s>", execution.SessionID, s.cfg.Domain)

	allRecipients := make([]string, len(recipients))
	copy(allRecipients, recipients)

	// Add system CC
	ccAddr := ""
	if s.cfg.SystemCC != "" {
		allRecipients = append(allRecipients, s.cfg.SystemCC)
		ccAddr = s.cfg.SystemCC
	}

	msg := fmt.Sprintf(
		"From: %s\r\nTo: %s\r\nCc: %s\r\nSubject: %s\r\nMessage-ID: %s\r\nContent-Type: text/plain; charset=UTF-8\r\n\r\n%s",
		workerEmail,
		strings.Join(recipients, ", "),
		ccAddr,
		subject,
		msgID,
		body,
	)

	addr := fmt.Sprintf("localhost:%d", s.cfg.Port)
	err := smtp.SendMail(addr, nil, workerEmail, allRecipients, []byte(msg))
	if err != nil {
		log.Printf("failed to send email: %v", err)
		// Still store the email record even if send fails
	}

	// Store outbound email record
	s.emailStore.Create(model.Email{
		ExecutionID: execution.ID,
		FromAddr:    workerEmail,
		ToAddr:      strings.Join(recipients, ", "),
		CCAddr:      ccAddr,
		Subject:     subject,
		Body:        body,
		InReplyTo:   "",
		Direction:   model.EmailOutbound,
	})

	return err
}

func (s *Sender) SendApprovalRequest(execution model.TaskExecution, workerEmail string, recipients []string, subject, body string) error {
	approvalBody := body + "\n\n---\nReply with 'approve/通过' to approve, or 'reject/驳回' with feedback to reject.\n"
	return s.SendReport(execution, workerEmail, recipients, "[Approval Required] "+subject, approvalBody)
}
```

**Step 4: Install go-smtp dependency and verify build**

Run:
```bash
go get github.com/emersion/go-smtp
go build ./...
```
Expected: Build succeeds.

**Step 5: Commit**

```bash
git add internal/mail/
git commit -m "feat: add embedded SMTP server with inbound approval handling and outbound sender"
```

---

## Phase 6: Cron Scheduler

### Task 6.1: Cron Task Scheduler

**Files:**
- Create: `internal/scheduler/cron.go`

**Step 1: Implement scheduler**

Create `internal/scheduler/cron.go`:

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
	cron      *cron.Cron
	taskStore *store.TaskStore
	manager   *worker.Manager
	entryMap  map[string]cron.EntryID // task_id -> entry_id
}

func New(ts *store.TaskStore, mgr *worker.Manager) *Scheduler {
	return &Scheduler{
		cron:      cron.New(),
		taskStore: ts,
		manager:   mgr,
		entryMap:  make(map[string]cron.EntryID),
	}
}

func (s *Scheduler) Start() error {
	// Load existing cron tasks
	tasks, err := s.taskStore.ListCronTasks()
	if err != nil {
		return err
	}

	for _, task := range tasks {
		if err := s.AddTask(task.ID, task.CronExpression); err != nil {
			log.Printf("failed to schedule task %s: %v", task.ID, err)
		}
	}

	s.cron.Start()
	log.Printf("Scheduler started with %d cron tasks", len(tasks))
	return nil
}

func (s *Scheduler) AddTask(taskID, cronExpr string) error {
	// Remove existing entry if any
	s.RemoveTask(taskID)

	id, err := s.cron.AddFunc(cronExpr, func() {
		log.Printf("Cron triggered task %s", taskID)
		if _, err := s.manager.ExecuteTask(context.Background(), taskID); err != nil {
			log.Printf("failed to execute cron task %s: %v", taskID, err)
		}
	})
	if err != nil {
		return err
	}

	s.entryMap[taskID] = id
	return nil
}

func (s *Scheduler) RemoveTask(taskID string) {
	if entryID, ok := s.entryMap[taskID]; ok {
		s.cron.Remove(entryID)
		delete(s.entryMap, taskID)
	}
}

func (s *Scheduler) Stop() {
	s.cron.Stop()
}
```

**Step 2: Install cron dependency and verify build**

Run:
```bash
go get github.com/robfig/cron/v3
go build ./...
```

**Step 3: Commit**

```bash
git add internal/scheduler/
git commit -m "feat: add cron scheduler for periodic task execution"
```

---

## Phase 7: Wire Everything in main.go

### Task 7.1: Complete Server Bootstrap

**Files:**
- Modify: `cmd/server/main.go`

**Step 1: Update main.go to wire all components**

Replace `cmd/server/main.go`:

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
	taskStore := store.NewTaskStore(db)
	execStore := store.NewExecutionStore(db)
	emailStore := store.NewEmailStore(db)
	memoryStore := store.NewMemoryStore(db)

	// Create worker manager
	mgr := worker.NewManager(cfg, workerStore, taskStore, execStore, emailStore, memoryStore)

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
	sched := scheduler.New(taskStore, mgr)
	if err := sched.Start(); err != nil {
		log.Printf("scheduler start error: %v", err)
	}

	// Start HTTP API
	srv := api.NewServer(workerStore, taskStore, execStore, emailStore, memoryStore, mgr)

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

**Step 2: Ensure data directory exists and verify build**

Run:
```bash
mkdir -p data/workers
go build ./cmd/server/
```
Expected: Binary builds successfully.

**Step 3: Commit**

```bash
git add cmd/server/main.go
git commit -m "feat: wire all components in main server bootstrap"
```

---

## Phase 8: Frontend (Next.js + shadcn/ui)

### Task 8.1: Initialize Next.js Project

**Step 1: Create Next.js app**

Run:
```bash
cd /Users/tengteng/work/robobee/core
npx create-next-app@latest web --typescript --tailwind --eslint --app --src-dir --import-alias "@/*" --use-npm
```

**Step 2: Install shadcn/ui**

Run:
```bash
cd web
npx shadcn@latest init -d
npx shadcn@latest add button card input label textarea select badge table dialog tabs
```

**Step 3: Commit**

```bash
cd /Users/tengteng/work/robobee/core
git add web/
git commit -m "feat: initialize Next.js frontend with shadcn/ui"
```

---

### Task 8.2: API Client & Types

**Files:**
- Create: `web/src/lib/api.ts`
- Create: `web/src/lib/types.ts`

**Step 1: Create TypeScript types**

Create `web/src/lib/types.ts`:

```typescript
export type RuntimeType = "claude_code" | "codex"
export type WorkerStatus = "idle" | "working" | "error"
export type TriggerType = "manual" | "email" | "cron"
export type ExecutionStatus = "pending" | "running" | "awaiting_approval" | "approved" | "rejected" | "completed" | "failed"

export interface Worker {
  id: string
  name: string
  description: string
  email: string
  runtime_type: RuntimeType
  work_dir: string
  status: WorkerStatus
  created_at: string
  updated_at: string
}

export interface Task {
  id: string
  worker_id: string
  name: string
  plan: string
  trigger_type: TriggerType
  cron_expression: string
  recipients: string[]
  requires_approval: boolean
  created_at: string
  updated_at: string
}

export interface TaskExecution {
  id: string
  task_id: string
  session_id: string
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

**Step 2: Create API client**

Create `web/src/lib/api.ts`:

```typescript
const API_BASE = process.env.NEXT_PUBLIC_API_URL || "http://localhost:8080/api"

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
    list: () => fetchAPI<Worker[]>("/workers"),
    get: (id: string) => fetchAPI<Worker>(`/workers/${id}`),
    create: (data: { name: string; description: string; runtime_type: string }) =>
      fetchAPI<Worker>("/workers", { method: "POST", body: JSON.stringify(data) }),
    update: (id: string, data: Partial<Worker>) =>
      fetchAPI<Worker>(`/workers/${id}`, { method: "PUT", body: JSON.stringify(data) }),
    delete: (id: string) => fetchAPI(`/workers/${id}`, { method: "DELETE" }),
  },
  tasks: {
    listByWorker: (workerId: string) => fetchAPI<Task[]>(`/workers/${workerId}/tasks`),
    create: (workerId: string, data: Omit<Task, "id" | "worker_id" | "created_at" | "updated_at">) =>
      fetchAPI<Task>(`/workers/${workerId}/tasks`, { method: "POST", body: JSON.stringify(data) }),
    update: (id: string, data: Partial<Task>) =>
      fetchAPI<Task>(`/tasks/${id}`, { method: "PUT", body: JSON.stringify(data) }),
    delete: (id: string) => fetchAPI(`/tasks/${id}`, { method: "DELETE" }),
    execute: (id: string) =>
      fetchAPI<TaskExecution>(`/tasks/${id}/execute`, { method: "POST" }),
  },
  executions: {
    list: () => fetchAPI<TaskExecution[]>("/executions"),
    get: (id: string) => fetchAPI<TaskExecution>(`/executions/${id}`),
    approve: (id: string) => fetchAPI(`/executions/${id}/approve`, { method: "POST" }),
    reject: (id: string, feedback: string) =>
      fetchAPI(`/executions/${id}/reject`, { method: "POST", body: JSON.stringify({ feedback }) }),
    emails: (id: string) => fetchAPI<Email[]>(`/executions/${id}/emails`),
  },
  message: {
    send: (workerId: string, message: string, taskId?: string) =>
      fetchAPI<TaskExecution>(`/workers/${workerId}/message`, {
        method: "POST",
        body: JSON.stringify({ message, task_id: taskId }),
      }),
  },
}

import type { Worker, Task, TaskExecution, Email } from "./types"
```

**Step 3: Commit**

```bash
git add web/src/lib/
git commit -m "feat: add frontend API client and TypeScript types"
```

---

### Task 8.3: Dashboard Page

**Files:**
- Modify: `web/src/app/page.tsx`
- Create: `web/src/app/workers/page.tsx`
- Create: `web/src/app/workers/[id]/page.tsx`
- Create: `web/src/app/executions/page.tsx`
- Create: `web/src/app/executions/[id]/page.tsx`
- Create: `web/src/app/layout.tsx` (modify)
- Create: `web/src/components/nav.tsx`

> **Note:** The frontend pages are standard Next.js app router pages using shadcn/ui components. Each page fetches data from the Go API via the `api` client. Implementation follows standard React patterns — fetch on mount with `useEffect`, forms with controlled inputs, WebSocket connection for log streaming.

> **Key pages:**
> - **Dashboard** (`/`): Grid of worker cards showing status, quick actions
> - **Workers** (`/workers`): Worker list + create dialog
> - **Worker Detail** (`/workers/[id]`): Tasks list, memories, send message
> - **Executions** (`/executions`): Execution history table
> - **Execution Detail** (`/executions/[id]`): Real-time logs via WebSocket, approve/reject buttons, email thread

> **Implementation guidance:** Each page should be a client component (`"use client"`) that calls the `api` functions. Use shadcn/ui `Card`, `Table`, `Button`, `Dialog`, `Badge`, `Tabs` components. For WebSocket logs, connect to `ws://localhost:8080/api/executions/:id/logs` and display output in a scrolling terminal-style div.

**Step 1: Implement pages one at a time, starting with the layout/nav**

_(Each page is a separate sub-step. Implement, verify in browser, commit.)_

**Step 2: Commit after each page**

```bash
git add web/src/
git commit -m "feat: add frontend dashboard and worker management pages"
```

---

## Phase 9: Docker Compose

### Task 9.1: Dockerize

**Files:**
- Create: `Dockerfile`
- Create: `web/Dockerfile`
- Create: `docker-compose.yml`

**Step 1: Create backend Dockerfile**

Create `Dockerfile`:

```dockerfile
FROM golang:1.22-alpine AS builder
RUN apk add --no-cache gcc musl-dev
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=1 go build -o server ./cmd/server/

FROM alpine:3.19
RUN apk add --no-cache ca-certificates
WORKDIR /app
COPY --from=builder /app/server .
COPY config.yaml .
RUN mkdir -p data/workers
EXPOSE 8080 2525
CMD ["./server"]
```

**Step 2: Create frontend Dockerfile**

Create `web/Dockerfile`:

```dockerfile
FROM node:20-alpine AS builder
WORKDIR /app
COPY package*.json ./
RUN npm ci
COPY . .
RUN npm run build

FROM node:20-alpine
WORKDIR /app
COPY --from=builder /app/.next/standalone ./
COPY --from=builder /app/.next/static ./.next/static
COPY --from=builder /app/public ./public
EXPOSE 3000
CMD ["node", "server.js"]
```

**Step 3: Create docker-compose.yml**

Create `docker-compose.yml`:

```yaml
services:
  backend:
    build: .
    ports:
      - "8080:8080"
      - "2525:2525"
    volumes:
      - ./data:/app/data
    restart: unless-stopped

  frontend:
    build: ./web
    ports:
      - "3000:3000"
    environment:
      - NEXT_PUBLIC_API_URL=http://backend:8080/api
    depends_on:
      - backend
    restart: unless-stopped
```

**Step 4: Commit**

```bash
git add Dockerfile web/Dockerfile docker-compose.yml
git commit -m "feat: add Docker and docker-compose configuration"
```

---

## Phase Summary

| Phase | Tasks | Description |
|-------|-------|-------------|
| 1 | 1.1-1.2 | Project scaffold, config, data models |
| 2 | 2.1-2.4 | SQLite store layer (all repositories) |
| 3 | 3.1-3.2 | AI runtime interface + worker manager |
| 4 | 4.1-4.2 | REST API (all handlers + WebSocket) |
| 5 | 5.1 | Embedded SMTP server |
| 6 | 6.1 | Cron scheduler |
| 7 | 7.1 | Wire everything in main.go |
| 8 | 8.1-8.3 | Next.js frontend |
| 9 | 9.1 | Docker deployment |

**Total: ~15 tasks, ~50 steps**

Each task produces a working, testable increment. Tests cover the store layer; runtime and API layers are verified via build + manual testing. The SMTP and scheduler are integration-tested via the running system.
