# Bee Central Brain Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace the single-worker AI router (`msgrouter`) with bee — a short-lifecycle Claude process that processes message batches, creates typed tasks via MCP tools, and delegates to a Go-native task scheduler.

**Architecture:** Platform messages land in the DB via the existing Ingester. A Feeder goroutine batches them and spawns a short-lived bee process (Claude + MCP tools) that creates task records. A TaskScheduler goroutine atomically claims due tasks and dispatches them to a refactored Dispatcher that executes workers and writes results back.

**Tech Stack:** Go 1.25, SQLite (mattn/go-sqlite3), `robfig/cron/v3` (new), Gin, `exec.CommandContext` for Claude process management.

**Spec:** `docs/superpowers/specs/2026-03-12-bee-central-brain-design.md`

---

## Chunk 1: Data Model & Task Store

### Task 1: Add robfig/cron dependency

**Files:**
- Modify: `go.mod`, `go.sum`

- [ ] **Step 1: Add dependency**

```bash
cd /path/to/project && go get github.com/robfig/cron/v3
```

Expected: `go.mod` and `go.sum` updated with `github.com/robfig/cron/v3`.

- [ ] **Step 2: Verify build**

```bash
go build ./...
```

Expected: no errors.

- [ ] **Step 3: Commit**

```bash
git add go.mod go.sum
git commit -m "chore: add robfig/cron/v3 dependency"
```

---

### Task 2: Add tasks migration to store/db.go

**Files:**
- Modify: `internal/store/db.go`

- [ ] **Step 1: Write the failing test**

Add to `internal/store/db_test.go`:

```go
func TestInitDB_TasksTable(t *testing.T) {
	db, err := InitDB(t.TempDir() + "/test.db")
	if err != nil {
		t.Fatalf("InitDB: %v", err)
	}
	defer db.Close()

	_, err = db.Exec(`INSERT INTO tasks
		(id, message_id, worker_id, instruction, type, created_at, updated_at)
		VALUES ('t1','m1','w1','do it','immediate',1,1)`)
	if err != nil {
		t.Fatalf("tasks table not created: %v", err)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

```bash
go test ./internal/store/ -run TestInitDB_TasksTable -v
```

Expected: FAIL — `no such table: tasks`

- [ ] **Step 3: Add tasks migration**

Append to the `migrations` slice in `internal/store/db.go` (use the next version number after the existing last migration):

```go
{
    version: 16,
    name:    "20260312000001_create_table_tasks",
    sql: `CREATE TABLE IF NOT EXISTS tasks (
        id                TEXT PRIMARY KEY,
        message_id        TEXT NOT NULL REFERENCES platform_messages(id),
        worker_id         TEXT NOT NULL REFERENCES workers(id),
        instruction       TEXT NOT NULL,
        type              TEXT NOT NULL CHECK(type IN ('immediate','countdown','scheduled')),
        status            TEXT NOT NULL DEFAULT 'pending'
                              CHECK(status IN ('pending','running','completed','failed','cancelled')),
        scheduled_at      INTEGER,
        cron_expr         TEXT NOT NULL DEFAULT '',
        next_run_at       INTEGER,
        reply_session_key TEXT NOT NULL DEFAULT '',
        execution_id      TEXT NOT NULL DEFAULT '',
        created_at        INTEGER NOT NULL,
        updated_at        INTEGER NOT NULL
    )`,
},
{
    version: 17,
    name:    "20260312000002_idx_tasks_status_type",
    sql:     `CREATE INDEX IF NOT EXISTS idx_tasks_status_type ON tasks(status, type)`,
},
{
    version: 18,
    name:    "20260312000003_idx_tasks_message_id",
    sql:     `CREATE INDEX IF NOT EXISTS idx_tasks_message_id ON tasks(message_id)`,
},
{
    version: 19,
    name:    "20260312000004_idx_tasks_worker_id",
    sql:     `CREATE INDEX IF NOT EXISTS idx_tasks_worker_id ON tasks(worker_id)`,
},
```

> Note: check the current last version number in `db.go` and use `last+1` through `last+4`.

- [ ] **Step 4: Run test to verify it passes**

```bash
go test ./internal/store/ -run TestInitDB_TasksTable -v
```

Expected: PASS

- [ ] **Step 5: Verify existing tests still pass**

```bash
go test ./internal/store/ -v
```

Expected: all PASS

- [ ] **Step 6: Commit**

```bash
git add internal/store/db.go internal/store/db_test.go
git commit -m "feat(store): add tasks table migration"
```

---

### Task 3: Create model/task.go

**Files:**
- Create: `internal/model/task.go`

- [ ] **Step 1: Create the file**

```go
package model

const (
    TaskTypeImmediate  = "immediate"
    TaskTypeCountdown  = "countdown"
    TaskTypeScheduled  = "scheduled"

    TaskStatusPending   = "pending"
    TaskStatusRunning   = "running"
    TaskStatusCompleted = "completed"
    TaskStatusFailed    = "failed"
    TaskStatusCancelled = "cancelled"
)

// Task represents a unit of work created by bee and dispatched to a worker.
type Task struct {
    ID               string
    MessageID        string
    WorkerID         string
    Instruction      string
    Type             string // TaskTypeImmediate | TaskTypeCountdown | TaskTypeScheduled
    Status           string // TaskStatus*
    ScheduledAt      *int64 // ms; countdown: absolute trigger time
    CronExpr         string
    NextRunAt        *int64 // ms; scheduled tasks only
    ReplySessionKey  string // optional override reply session
    ExecutionID      string
    CreatedAt        int64
    UpdatedAt        int64
}

// ClaimedTask is a Task joined with data from its originating platform_messages row,
// needed by the TaskScheduler to build a DispatchTask.
type ClaimedTask struct {
    Task
    MessageSessionKey string
    MessagePlatform   string
}
```

- [ ] **Step 2: Verify build**

```bash
go build ./internal/model/
```

Expected: no errors.

- [ ] **Step 3: Commit**

```bash
git add internal/model/task.go
git commit -m "feat(model): add Task model and ClaimedTask"
```

---

### Task 4: Create store/task_store.go

**Files:**
- Create: `internal/store/task_store.go`
- Create: `internal/store/task_store_test.go`

- [ ] **Step 1: Write the failing tests**

Create `internal/store/task_store_test.go`:

```go
package store

import (
    "context"
    "testing"
    "time"

    "github.com/robobee/core/internal/model"
)

func newTaskStoreForTest(t *testing.T) (*TaskStore, func()) {
    t.Helper()
    db, err := InitDB(t.TempDir() + "/test.db")
    if err != nil {
        t.Fatalf("InitDB: %v", err)
    }
    // Insert prerequisite rows matching the actual schema (raw, platform_msg_id required)
    db.Exec(`INSERT INTO workers (id,name,work_dir,status,created_at,updated_at) VALUES ('w1','W','/',`+"`idle`"+`,1,1)`)
    db.Exec(`INSERT INTO platform_messages
        (id, session_key, platform, content, raw, platform_msg_id, received_at)
        VALUES ('m1','feishu:c:u','feishu','hi','','',1)`)
    return NewTaskStore(db), func() { db.Close() }
}

func TestTaskStore_Create_And_Get(t *testing.T) {
    ts, cleanup := newTaskStoreForTest(t)
    defer cleanup()

    now := time.Now().UnixMilli()
    task := model.Task{
        MessageID:   "m1",
        WorkerID:    "w1",
        Instruction: "do it",
        Type:        model.TaskTypeImmediate,
        Status:      model.TaskStatusPending,
        CreatedAt:   now,
        UpdatedAt:   now,
    }

    id, err := ts.Create(context.Background(), task)
    if err != nil {
        t.Fatalf("Create: %v", err)
    }
    if id == "" {
        t.Fatal("expected non-empty task ID")
    }

    got, err := ts.GetByID(context.Background(), id)
    if err != nil {
        t.Fatalf("GetByID: %v", err)
    }
    if got.Instruction != "do it" {
        t.Errorf("instruction: want %q got %q", "do it", got.Instruction)
    }
    if got.Type != model.TaskTypeImmediate {
        t.Errorf("type: want immediate got %q", got.Type)
    }
}

func TestTaskStore_ClaimDueTasks_ImmediateOnly(t *testing.T) {
    ts, cleanup := newTaskStoreForTest(t)
    defer cleanup()

    now := time.Now().UnixMilli()
    ts.Create(context.Background(), model.Task{
        MessageID: "m1", WorkerID: "w1", Instruction: "go",
        Type: model.TaskTypeImmediate, Status: model.TaskStatusPending,
        CreatedAt: now, UpdatedAt: now,
    })

    tasks, err := ts.ClaimDueTasks(context.Background(), now)
    if err != nil {
        t.Fatalf("ClaimDueTasks: %v", err)
    }
    if len(tasks) != 1 {
        t.Fatalf("expected 1 due task, got %d", len(tasks))
    }
    if tasks[0].Status != model.TaskStatusRunning {
        t.Errorf("claimed task should have status running, got %q", tasks[0].Status)
    }
}

func TestTaskStore_ClaimDueTasks_Idempotent(t *testing.T) {
    ts, cleanup := newTaskStoreForTest(t)
    defer cleanup()

    now := time.Now().UnixMilli()
    ts.Create(context.Background(), model.Task{
        MessageID: "m1", WorkerID: "w1", Instruction: "go",
        Type: model.TaskTypeImmediate, Status: model.TaskStatusPending,
        CreatedAt: now, UpdatedAt: now,
    })

    tasks1, _ := ts.ClaimDueTasks(context.Background(), now)
    tasks2, _ := ts.ClaimDueTasks(context.Background(), now)
    if len(tasks1) != 1 {
        t.Errorf("first claim: want 1, got %d", len(tasks1))
    }
    if len(tasks2) != 0 {
        t.Errorf("second claim should be empty (already running), got %d", len(tasks2))
    }
}

func TestTaskStore_SetExecution(t *testing.T) {
    ts, cleanup := newTaskStoreForTest(t)
    defer cleanup()

    now := time.Now().UnixMilli()
    id, _ := ts.Create(context.Background(), model.Task{
        MessageID: "m1", WorkerID: "w1", Instruction: "go",
        Type: model.TaskTypeImmediate, Status: model.TaskStatusPending,
        CreatedAt: now, UpdatedAt: now,
    })

    err := ts.SetExecution(context.Background(), id, "exec-1", model.TaskStatusCompleted)
    if err != nil {
        t.Fatalf("SetExecution: %v", err)
    }

    got, _ := ts.GetByID(context.Background(), id)
    if got.ExecutionID != "exec-1" {
        t.Errorf("execution_id: want exec-1 got %q", got.ExecutionID)
    }
    if got.Status != model.TaskStatusCompleted {
        t.Errorf("status: want completed got %q", got.Status)
    }
}

func TestTaskStore_DeleteByMessageIDs(t *testing.T) {
    ts, cleanup := newTaskStoreForTest(t)
    defer cleanup()

    now := time.Now().UnixMilli()
    ts.Create(context.Background(), model.Task{
        MessageID: "m1", WorkerID: "w1", Instruction: "go",
        Type: model.TaskTypeImmediate, Status: model.TaskStatusPending,
        CreatedAt: now, UpdatedAt: now,
    })

    err := ts.DeletePendingByMessageIDs(context.Background(), []string{"m1"})
    if err != nil {
        t.Fatalf("DeletePendingByMessageIDs: %v", err)
    }

    // Verify no pending tasks remain
    tasks, _ := ts.ClaimDueTasks(context.Background(), now)
    if len(tasks) != 0 {
        t.Errorf("expected 0 tasks after delete, got %d", len(tasks))
    }
}

func TestTaskStore_ListByMessageID(t *testing.T) {
    ts, cleanup := newTaskStoreForTest(t)
    defer cleanup()

    now := time.Now().UnixMilli()
    ts.Create(context.Background(), model.Task{
        MessageID: "m1", WorkerID: "w1", Instruction: "a",
        Type: model.TaskTypeImmediate, Status: model.TaskStatusPending,
        CreatedAt: now, UpdatedAt: now,
    })
    ts.Create(context.Background(), model.Task{
        MessageID: "m1", WorkerID: "w1", Instruction: "b",
        Type: model.TaskTypeCountdown, Status: model.TaskStatusPending,
        CreatedAt: now, UpdatedAt: now,
    })

    tasks, err := ts.ListByMessageID(context.Background(), "m1", "")
    if err != nil {
        t.Fatalf("ListByMessageID: %v", err)
    }
    if len(tasks) != 2 {
        t.Errorf("expected 2 tasks, got %d", len(tasks))
    }
}

func TestTaskStore_ResetRunningToPending(t *testing.T) {
    ts, cleanup := newTaskStoreForTest(t)
    defer cleanup()

    now := time.Now().UnixMilli()
    id, _ := ts.Create(context.Background(), model.Task{
        MessageID: "m1", WorkerID: "w1", Instruction: "go",
        Type: model.TaskTypeImmediate, Status: model.TaskStatusRunning,
        CreatedAt: now, UpdatedAt: now,
    })

    n, err := ts.ResetRunningToPending(context.Background())
    if err != nil {
        t.Fatalf("ResetRunningToPending: %v", err)
    }
    if n != 1 {
        t.Errorf("expected 1 reset, got %d", n)
    }

    got, _ := ts.GetByID(context.Background(), id)
    if got.Status != model.TaskStatusPending {
        t.Errorf("expected pending, got %q", got.Status)
    }
}
```

- [ ] **Step 2: Run tests to verify they fail**

```bash
go test ./internal/store/ -run "TestTaskStore" -v
```

Expected: FAIL — `undefined: TaskStore` (or similar)

- [ ] **Step 3: Implement TaskStore**

Create `internal/store/task_store.go`:

```go
package store

import (
    "context"
    "database/sql"
    "fmt"
    "strings"
    "time"

    "github.com/google/uuid"
    "github.com/robobee/core/internal/model"
)

// TaskStore handles persistence for bee tasks.
type TaskStore struct {
    db *sql.DB
}

// NewTaskStore creates a TaskStore backed by db.
func NewTaskStore(db *sql.DB) *TaskStore {
    return &TaskStore{db: db}
}

// Create inserts a new task and returns its generated ID.
func (s *TaskStore) Create(ctx context.Context, t model.Task) (string, error) {
    id := uuid.New().String()
    now := time.Now().UnixMilli()
    _, err := s.db.ExecContext(ctx, `
        INSERT INTO tasks
            (id, message_id, worker_id, instruction, type, status,
             scheduled_at, cron_expr, next_run_at, reply_session_key, execution_id,
             created_at, updated_at)
        VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?)`,
        id, t.MessageID, t.WorkerID, t.Instruction, t.Type, t.Status,
        t.ScheduledAt, t.CronExpr, t.NextRunAt, t.ReplySessionKey, "",
        now, now,
    )
    if err != nil {
        return "", fmt.Errorf("create task: %w", err)
    }
    return id, nil
}

// GetByID fetches a single task by ID.
func (s *TaskStore) GetByID(ctx context.Context, id string) (model.Task, error) {
    row := s.db.QueryRowContext(ctx, `
        SELECT id, message_id, worker_id, instruction, type, status,
               scheduled_at, cron_expr, next_run_at, reply_session_key, execution_id,
               created_at, updated_at
        FROM tasks WHERE id = ?`, id)
    return scanTask(row)
}

// ListByMessageID returns tasks for a given message, optionally filtered by status.
func (s *TaskStore) ListByMessageID(ctx context.Context, messageID, status string) ([]model.Task, error) {
    q := `SELECT id, message_id, worker_id, instruction, type, status,
                 scheduled_at, cron_expr, next_run_at, reply_session_key, execution_id,
                 created_at, updated_at
          FROM tasks WHERE message_id = ?`
    args := []any{messageID}
    if status != "" {
        q += " AND status = ?"
        args = append(args, status)
    }
    rows, err := s.db.QueryContext(ctx, q, args...)
    if err != nil {
        return nil, fmt.Errorf("list tasks: %w", err)
    }
    defer rows.Close()
    return scanTasks(rows)
}

// ClaimDueTasks atomically selects all pending tasks that are due at or before nowMS,
// marks them running (immediate/countdown) or advances their next_run_at (scheduled),
// and returns them joined with their source platform_message data.
func (s *TaskStore) ClaimDueTasks(ctx context.Context, nowMS int64) ([]model.ClaimedTask, error) {
    tx, err := s.db.BeginTx(ctx, nil)
    if err != nil {
        return nil, fmt.Errorf("begin tx: %w", err)
    }
    defer tx.Rollback() //nolint:errcheck

    rows, err := tx.QueryContext(ctx, `
        SELECT t.id, t.message_id, t.worker_id, t.instruction, t.type, t.status,
               t.scheduled_at, t.cron_expr, t.next_run_at, t.reply_session_key,
               t.execution_id, t.created_at, t.updated_at,
               pm.session_key, pm.platform
        FROM tasks t
        JOIN platform_messages pm ON pm.id = t.message_id
        WHERE t.status = 'pending'
          AND (
            t.type = 'immediate'
            OR (t.type = 'countdown' AND t.scheduled_at <= ?)
            OR (t.type = 'scheduled' AND (t.next_run_at IS NULL OR t.next_run_at <= ?))
          )`, nowMS, nowMS)
    if err != nil {
        return nil, fmt.Errorf("query due tasks: %w", err)
    }

    var claimed []model.ClaimedTask
    for rows.Next() {
        var ct model.ClaimedTask
        var scheduledAt, nextRunAt sql.NullInt64
        err := rows.Scan(
            &ct.ID, &ct.MessageID, &ct.WorkerID, &ct.Instruction,
            &ct.Type, &ct.Status, &scheduledAt, &ct.CronExpr,
            &nextRunAt, &ct.ReplySessionKey, &ct.ExecutionID,
            &ct.CreatedAt, &ct.UpdatedAt,
            &ct.MessageSessionKey, &ct.MessagePlatform,
        )
        if err != nil {
            rows.Close()
            return nil, fmt.Errorf("scan task: %w", err)
        }
        if scheduledAt.Valid {
            v := scheduledAt.Int64
            ct.ScheduledAt = &v
        }
        if nextRunAt.Valid {
            v := nextRunAt.Int64
            ct.NextRunAt = &v
        }
        claimed = append(claimed, ct)
    }
    rows.Close()
    if err := rows.Err(); err != nil {
        return nil, fmt.Errorf("rows error: %w", err)
    }

    now := time.Now().UnixMilli()
    for _, ct := range claimed {
        if ct.Type == model.TaskTypeScheduled {
            // advance next_run_at; caller will compute actual value via cron
            // we mark it running=false (status stays pending) and update next_run_at
            // NOTE: the actual next_run_at computation happens in the scheduler
            // using robfig/cron; here we just mark the task as dispatched by
            // temporarily setting next_run_at far in the future to prevent double-fire.
            // The scheduler will correct it after computing the real next time.
            _, err = tx.ExecContext(ctx,
                `UPDATE tasks SET next_run_at = ?, updated_at = ? WHERE id = ?`,
                nowMS+24*60*60*1000, now, ct.ID) // +24h sentinel; overwritten by scheduler
        } else {
            _, err = tx.ExecContext(ctx,
                `UPDATE tasks SET status = 'running', updated_at = ? WHERE id = ?`,
                now, ct.ID)
        }
        if err != nil {
            return nil, fmt.Errorf("update task %s: %w", ct.ID, err)
        }
    }

    if err := tx.Commit(); err != nil {
        return nil, fmt.Errorf("commit: %w", err)
    }
    return claimed, nil
}

// SetExecution writes execution_id and status back to a task.
func (s *TaskStore) SetExecution(ctx context.Context, taskID, executionID, status string) error {
    _, err := s.db.ExecContext(ctx,
        `UPDATE tasks SET execution_id = ?, status = ?, updated_at = ? WHERE id = ?`,
        executionID, status, time.Now().UnixMilli(), taskID)
    return err
}

// CancelTask sets a task status to cancelled.
func (s *TaskStore) CancelTask(ctx context.Context, taskID string) error {
    _, err := s.db.ExecContext(ctx,
        `UPDATE tasks SET status = 'cancelled', updated_at = ? WHERE id = ?`,
        time.Now().UnixMilli(), taskID)
    return err
}

// CancelByWorkerID cancels all pending/running tasks for a given worker.
func (s *TaskStore) CancelByWorkerID(ctx context.Context, workerID string) error {
    _, err := s.db.ExecContext(ctx,
        `UPDATE tasks SET status = 'cancelled', updated_at = ?
         WHERE worker_id = ? AND status IN ('pending','running')`,
        time.Now().UnixMilli(), workerID)
    return err
}

// DeletePendingByMessageIDs removes pending tasks belonging to the given message IDs.
// Used by the Feeder to clean up orphaned tasks after a failed bee run.
func (s *TaskStore) DeletePendingByMessageIDs(ctx context.Context, messageIDs []string) error {
    if len(messageIDs) == 0 {
        return nil
    }
    placeholders := strings.Repeat("?,", len(messageIDs))
    placeholders = placeholders[:len(placeholders)-1]
    args := make([]any, len(messageIDs))
    for i, id := range messageIDs {
        args[i] = id
    }
    _, err := s.db.ExecContext(ctx,
        `DELETE FROM tasks WHERE message_id IN (`+placeholders+`) AND status = 'pending'`,
        args...)
    return err
}

// ResetRunningToPending resets all running tasks back to pending.
// For scheduled tasks, also clears next_run_at so the TaskScheduler's cron
// re-computation takes effect on the next poll tick (avoids stale sentinel values
// from a prior crash mid-claim leaving the task locked out for up to 24h).
// Called at startup to recover tasks that were mid-dispatch when the server crashed.
func (s *TaskStore) ResetRunningToPending(ctx context.Context) (int64, error) {
    now := time.Now().UnixMilli()
    // Scheduled tasks: clear next_run_at so scheduler recomputes via cron
    _, err := s.db.ExecContext(ctx,
        `UPDATE tasks SET status = 'pending', next_run_at = NULL, updated_at = ?
         WHERE status = 'running' AND type = 'scheduled'`, now)
    if err != nil {
        return 0, err
    }
    // Immediate / countdown tasks: just reset status
    res, err := s.db.ExecContext(ctx,
        `UPDATE tasks SET status = 'pending', updated_at = ?
         WHERE status = 'running' AND type IN ('immediate','countdown')`, now)
    if err != nil {
        return 0, err
    }
    return res.RowsAffected()
}

// UpdateNextRunAt sets next_run_at for a scheduled task after dispatch.
func (s *TaskStore) UpdateNextRunAt(ctx context.Context, taskID string, nextRunAt int64) error {
    _, err := s.db.ExecContext(ctx,
        `UPDATE tasks SET next_run_at = ?, updated_at = ? WHERE id = ?`,
        nextRunAt, time.Now().UnixMilli(), taskID)
    return err
}

// scanTask scans a single task row.
func scanTask(row *sql.Row) (model.Task, error) {
    var t model.Task
    var scheduledAt, nextRunAt sql.NullInt64
    err := row.Scan(
        &t.ID, &t.MessageID, &t.WorkerID, &t.Instruction,
        &t.Type, &t.Status, &scheduledAt, &t.CronExpr,
        &nextRunAt, &t.ReplySessionKey, &t.ExecutionID,
        &t.CreatedAt, &t.UpdatedAt,
    )
    if err != nil {
        return model.Task{}, fmt.Errorf("scan task: %w", err)
    }
    if scheduledAt.Valid {
        v := scheduledAt.Int64
        t.ScheduledAt = &v
    }
    if nextRunAt.Valid {
        v := nextRunAt.Int64
        t.NextRunAt = &v
    }
    return t, nil
}

func scanTasks(rows *sql.Rows) ([]model.Task, error) {
    var result []model.Task
    for rows.Next() {
        var t model.Task
        var scheduledAt, nextRunAt sql.NullInt64
        err := rows.Scan(
            &t.ID, &t.MessageID, &t.WorkerID, &t.Instruction,
            &t.Type, &t.Status, &scheduledAt, &t.CronExpr,
            &nextRunAt, &t.ReplySessionKey, &t.ExecutionID,
            &t.CreatedAt, &t.UpdatedAt,
        )
        if err != nil {
            return nil, fmt.Errorf("scan task row: %w", err)
        }
        if scheduledAt.Valid {
            v := scheduledAt.Int64
            t.ScheduledAt = &v
        }
        if nextRunAt.Valid {
            v := nextRunAt.Int64
            t.NextRunAt = &v
        }
        result = append(result, t)
    }
    return result, rows.Err()
}
```

- [ ] **Step 4: Run tests to verify they pass**

```bash
go test ./internal/store/ -run "TestTaskStore" -v
```

Expected: all PASS

- [ ] **Step 5: Run full store tests to confirm no regressions**

```bash
go test ./internal/store/ -v
```

Expected: all PASS

- [ ] **Step 6: Commit**

```bash
git add internal/store/task_store.go internal/store/task_store_test.go
git commit -m "feat(store): add TaskStore with task CRUD and atomic claim"
```

---

## Chunk 2: Config Extension

### Task 5: Add BeeConfig to config.go

**Files:**
- Modify: `internal/config/config.go`
- Modify: `config.example.yaml`

- [ ] **Step 1: Write the failing test**

Add to a new file `internal/config/config_bee_test.go`:

```go
package config

import (
    "os"
    "testing"
    "time"
)

func TestBeeConfig_Defaults(t *testing.T) {
    f, _ := os.CreateTemp("", "*.yaml")
    f.WriteString(`
server:
  port: 8080
bee:
  name: "bee"
  work_dir: "/tmp/bee"
  persona: "you are bee"
`)
    f.Close()

    cfg, err := Load(f.Name())
    if err != nil {
        t.Fatalf("Load: %v", err)
    }

    if cfg.Bee.Feeder.Interval != 10*time.Second {
        t.Errorf("default interval: want 10s got %v", cfg.Bee.Feeder.Interval)
    }
    if cfg.Bee.Feeder.BatchSize != 10 {
        t.Errorf("default batch_size: want 10 got %d", cfg.Bee.Feeder.BatchSize)
    }
    if cfg.Bee.Feeder.Timeout != 5*time.Minute {
        t.Errorf("default timeout: want 5m got %v", cfg.Bee.Feeder.Timeout)
    }
    if cfg.Bee.Feeder.QueueWarnThreshold != 100 {
        t.Errorf("default queue_warn_threshold: want 100 got %d", cfg.Bee.Feeder.QueueWarnThreshold)
    }
}
```

- [ ] **Step 2: Run test to verify it fails**

```bash
go test ./internal/config/ -run TestBeeConfig_Defaults -v
```

Expected: FAIL — `cfg.Bee undefined`

- [ ] **Step 3: Add BeeConfig to config.go**

Add the following types and update `Config` struct:

```go
type BeeConfig struct {
    Name    string      `yaml:"name"`
    WorkDir string      `yaml:"work_dir"`
    Persona string      `yaml:"persona"`
    Feeder  FeederConfig `yaml:"feeder"`
}

type FeederConfig struct {
    Interval           time.Duration `yaml:"interval"`
    BatchSize          int           `yaml:"batch_size"`
    Timeout            time.Duration `yaml:"timeout"`
    QueueWarnThreshold int           `yaml:"queue_warn_threshold"`
}
```

Add `Bee BeeConfig \`yaml:"bee"\`` to the `Config` struct.

In `applyDefaults`, add:

```go
if cfg.Bee.Name == "" {
    cfg.Bee.Name = "bee"
}
if cfg.Bee.Feeder.Interval == 0 {
    cfg.Bee.Feeder.Interval = 10 * time.Second
}
if cfg.Bee.Feeder.BatchSize == 0 {
    cfg.Bee.Feeder.BatchSize = 10
}
if cfg.Bee.Feeder.Timeout == 0 {
    cfg.Bee.Feeder.Timeout = 5 * time.Minute
}
if cfg.Bee.Feeder.QueueWarnThreshold == 0 {
    cfg.Bee.Feeder.QueueWarnThreshold = 100
}
```

- [ ] **Step 4: Run test to verify it passes**

```bash
go test ./internal/config/ -run TestBeeConfig_Defaults -v
```

Expected: PASS

- [ ] **Step 5: Update config.example.yaml**

Add to `config.example.yaml`:

```yaml
bee:
  name: "bee"
  work_dir: ""           # defaults to ~/.robobee/bee
  persona: |
    你是 bee，一个专注于任务调度的智能助手。
    你的职责是分析用户消息，将其拆解为具体任务并分配给合适的 worker。
    你只做两件事：管理 worker 和创建任务。
    如果消息内容无法路由到任何 worker，或请求超出上述职责，拒绝提供服务。
  feeder:
    interval: 10s
    batch_size: 10
    timeout: 5m
    queue_warn_threshold: 100
```

Also add work_dir default to `applyDefaults`:

```go
if cfg.Bee.WorkDir == "" {
    home, err := os.UserHomeDir()
    if err != nil {
        return fmt.Errorf("get home dir: %w", err)
    }
    cfg.Bee.WorkDir = filepath.Join(home, ".robobee", "bee")
}
```

- [ ] **Step 6: Run all config tests**

```bash
go test ./internal/config/ -v
```

Expected: all PASS

- [ ] **Step 7: Commit**

```bash
git add internal/config/config.go internal/config/config_bee_test.go config.example.yaml
git commit -m "feat(config): add BeeConfig and FeederConfig with defaults"
```

---

## Chunk 3: MCP Task Tools

### Task 6: Add task tool schemas and handlers to MCP server

**Files:**
- Modify: `internal/mcp/tools.go`
- Modify: `internal/mcp/server.go`
- Modify: `internal/mcp/tools_test.go`

- [ ] **Step 1: Write the failing test**

Add to `internal/mcp/tools_test.go`:

```go
func TestToolSchemas_IncludesTaskTools(t *testing.T) {
    schemas := ToolSchemas()
    names := make(map[string]bool)
    for _, s := range schemas {
        names[s.Name] = true
    }
    for _, want := range []string{"create_task", "list_tasks", "cancel_task"} {
        if !names[want] {
            t.Errorf("missing tool schema: %s", want)
        }
    }
}
```

- [ ] **Step 2: Run test to verify it fails**

```bash
go test ./internal/mcp/ -run TestToolSchemas_IncludesTaskTools -v
```

Expected: FAIL

- [ ] **Step 3: Add task tool schemas**

Append to the slice returned by `toolSchemas()` in `internal/mcp/tools.go`:

```go
{
    Name:        "create_task",
    Description: "Create a task assigning a worker to handle a user instruction from a message",
    InputSchema: map[string]any{
        "type":     "object",
        "required": []string{"message_id", "worker_id", "instruction", "type"},
        "properties": map[string]any{
            "message_id":       map[string]string{"type": "string", "description": "ID of the originating platform message"},
            "worker_id":        map[string]string{"type": "string", "description": "Worker ID to assign"},
            "instruction":      map[string]string{"type": "string", "description": "Specific instruction for the worker"},
            "type":             map[string]any{"type": "string", "enum": []string{"immediate", "countdown", "scheduled"}, "description": "Task type"},
            "scheduled_at":     map[string]string{"type": "integer", "description": "Unix ms; required for countdown, must be >= now+5s"},
            "cron_expr":        map[string]string{"type": "string", "description": "5-field cron expression; required for scheduled"},
            "reply_session_key": map[string]string{"type": "string", "description": "Reply target override session key; required for scheduled"},
        },
    },
},
{
    Name:        "list_tasks",
    Description: "List tasks for a given message, optionally filtered by status",
    InputSchema: map[string]any{
        "type":     "object",
        "required": []string{"message_id"},
        "properties": map[string]any{
            "message_id": map[string]string{"type": "string", "description": "ID of the originating platform message"},
            "status":     map[string]string{"type": "string", "description": "Optional status filter"},
        },
    },
},
{
    Name:        "cancel_task",
    Description: "Cancel a pending or scheduled task",
    InputSchema: map[string]any{
        "type":     "object",
        "required": []string{"task_id"},
        "properties": map[string]any{
            "task_id": map[string]string{"type": "string", "description": "Task ID to cancel"},
        },
    },
},
```

- [ ] **Step 4: Add TaskStore to MCPServer**

In `internal/mcp/server.go`, add `taskStore *store.TaskStore` to the `MCPServer` struct and update `NewServer`:

```go
// NewServer creates an MCPServer.
func NewServer(ws *store.WorkerStore, mgr *worker.Manager, ts *store.TaskStore) *MCPServer {
    return &MCPServer{
        workerStore: ws,
        manager:     mgr,
        taskStore:   ts,
        sessions:    make(map[string]chan rpcResponse),
    }
}
```

Add `taskStore *store.TaskStore` field to the struct definition.

Update `callTool` switch to add the three new cases:

```go
case "create_task":
    return s.toolCreateTask(args)
case "list_tasks":
    return s.toolListTasks(args)
case "cancel_task":
    return s.toolCancelTask(args)
```

- [ ] **Step 5: Implement the three tool handlers in tools.go**

Add these methods to `MCPServer` in `internal/mcp/tools.go`:

```go
func (s *MCPServer) toolCreateTask(args json.RawMessage) (any, error) {
    var params struct {
        MessageID       string `json:"message_id"`
        WorkerID        string `json:"worker_id"`
        Instruction     string `json:"instruction"`
        Type            string `json:"type"`
        ScheduledAt     *int64 `json:"scheduled_at"`
        CronExpr        string `json:"cron_expr"`
        ReplySessionKey string `json:"reply_session_key"`
    }
    if err := json.Unmarshal(args, &params); err != nil {
        return nil, fmt.Errorf("invalid args: %w", err)
    }
    // Validate required fields
    if params.MessageID == "" {
        return nil, fmt.Errorf("message_id is required")
    }
    if params.WorkerID == "" {
        return nil, fmt.Errorf("worker_id is required")
    }
    if params.Instruction == "" {
        return nil, fmt.Errorf("instruction is required")
    }
    switch params.Type {
    case "immediate", "countdown", "scheduled":
    default:
        return nil, fmt.Errorf("type must be immediate, countdown, or scheduled")
    }

    nowMS := time.Now().UnixMilli()

    // Type-specific validation
    switch params.Type {
    case "countdown":
        if params.ScheduledAt == nil {
            return nil, fmt.Errorf("scheduled_at is required for countdown tasks")
        }
        if *params.ScheduledAt < nowMS+5000 {
            return nil, fmt.Errorf("scheduled_at must be at least 5 seconds in the future")
        }
    case "scheduled":
        if params.CronExpr == "" {
            return nil, fmt.Errorf("cron_expr is required for scheduled tasks")
        }
        if params.ReplySessionKey == "" {
            return nil, fmt.Errorf("reply_session_key is required for scheduled tasks")
        }
    }

    // Compute initial next_run_at for scheduled tasks
    var nextRunAt *int64
    if params.Type == "scheduled" {
        sched, err := cron.ParseStandard(params.CronExpr)
        if err != nil {
            // Invalid cron: create task as cancelled
            task := model.Task{
                MessageID:   params.MessageID,
                WorkerID:    params.WorkerID,
                Instruction: params.Instruction,
                Type:        params.Type,
                Status:      model.TaskStatusCancelled,
                CronExpr:    params.CronExpr,
                CreatedAt:   nowMS,
                UpdatedAt:   nowMS,
            }
            id, createErr := s.taskStore.Create(context.Background(), task)
            if createErr != nil {
                return nil, fmt.Errorf("create cancelled task: %w", createErr)
            }
            return map[string]string{"task_id": id, "status": "cancelled", "reason": "invalid cron_expr: " + err.Error()}, nil
        }
        next := sched.Next(time.Now()).UnixMilli()
        nextRunAt = &next
    }

    task := model.Task{
        MessageID:       params.MessageID,
        WorkerID:        params.WorkerID,
        Instruction:     params.Instruction,
        Type:            params.Type,
        Status:          model.TaskStatusPending,
        ScheduledAt:     params.ScheduledAt,
        CronExpr:        params.CronExpr,
        NextRunAt:       nextRunAt,
        ReplySessionKey: params.ReplySessionKey,
        CreatedAt:       nowMS,
        UpdatedAt:       nowMS,
    }

    id, err := s.taskStore.Create(context.Background(), task)
    if err != nil {
        return nil, fmt.Errorf("create task: %w", err)
    }
    return map[string]string{"task_id": id, "status": "pending"}, nil
}

func (s *MCPServer) toolListTasks(args json.RawMessage) (any, error) {
    var params struct {
        MessageID string `json:"message_id"`
        Status    string `json:"status"`
    }
    if err := json.Unmarshal(args, &params); err != nil {
        return nil, fmt.Errorf("invalid args: %w", err)
    }
    if params.MessageID == "" {
        return nil, fmt.Errorf("message_id is required")
    }
    tasks, err := s.taskStore.ListByMessageID(context.Background(), params.MessageID, params.Status)
    if err != nil {
        return nil, fmt.Errorf("list tasks: %w", err)
    }
    if tasks == nil {
        tasks = []model.Task{}
    }
    return tasks, nil
}

func (s *MCPServer) toolCancelTask(args json.RawMessage) (any, error) {
    var params struct {
        TaskID string `json:"task_id"`
    }
    if err := json.Unmarshal(args, &params); err != nil {
        return nil, fmt.Errorf("invalid args: %w", err)
    }
    if params.TaskID == "" {
        return nil, fmt.Errorf("task_id is required")
    }
    if err := s.taskStore.CancelTask(context.Background(), params.TaskID); err != nil {
        return nil, fmt.Errorf("cancel task: %w", err)
    }
    return map[string]string{"task_id": params.TaskID, "status": "cancelled"}, nil
}
```

Add required imports to `tools.go`: `"context"`, `"time"`, `"github.com/robfig/cron/v3"`, `"github.com/robobee/core/internal/model"`.

- [ ] **Step 6: Update tools_test.go to pass TaskStore to NewServer**

In `internal/mcp/tools_test.go`, find the `setupMCPServer` (or equivalent) helper that calls `mcp.NewServer(ws, mgr)` and update it to pass a `*store.TaskStore`:

```go
// In tools_test.go: update the server setup helper
db, err := store.InitDB(t.TempDir() + "/test.db")
if err != nil {
    t.Fatalf("InitDB: %v", err)
}
ts := store.NewTaskStore(db)
srv := mcp.NewServer(ws, mgr, ts)
```

- [ ] **Step 7: Run all MCP tests**

```bash
go test ./internal/mcp/ -v
```

Expected: all PASS

- [ ] **Step 7: Commit**

```bash
git add internal/mcp/tools.go internal/mcp/server.go internal/mcp/tools_test.go
git commit -m "feat(mcp): add create_task, list_tasks, cancel_task tools"
```

---

## Chunk 4: Dispatcher Refactor

### Task 7: Define DispatchTask type and refactor Dispatcher

**Files:**
- Create: `internal/dispatcher/task.go`
- Modify: `internal/dispatcher/dispatcher.go`
- Modify: `internal/dispatcher/dispatcher_test.go`

- [ ] **Step 1: Create dispatcher/task.go**

```go
package dispatcher

import "github.com/robobee/core/internal/platform"

// DispatchTask is the unit of work sent to the Dispatcher by the TaskScheduler
// (for task-based dispatch) or directly by the Feeder (for clear commands).
type DispatchTask struct {
    TaskID          string                  // empty for clear commands
    WorkerID        string
    SessionKey      string                  // original message session_key
    Instruction     string
    ReplyTo         platform.InboundMessage // platform info for result delivery
    TaskType        string                  // "immediate"|"countdown"|"scheduled"|"clear"
    MessageID       string                  // originating platform_messages.id (for session lookup)
    ReplySessionKey string                  // overrides ReplyTo session key if non-empty
}
```

- [ ] **Step 2: Write new dispatcher tests**

Replace the contents of `internal/dispatcher/dispatcher_test.go`:

```go
package dispatcher_test

import (
    "context"
    "testing"
    "time"

    "github.com/robobee/core/internal/dispatcher"
    "github.com/robobee/core/internal/model"
    "github.com/robobee/core/internal/msgsender"
    "github.com/robobee/core/internal/platform"
)

// --- Mocks ---

type mockExecManager struct {
    execResult model.WorkerExecution
    getResult  model.WorkerExecution
}

func (m *mockExecManager) ExecuteWorker(_ context.Context, _, _ string) (model.WorkerExecution, error) {
    return m.execResult, nil
}
func (m *mockExecManager) ReplyExecution(_ context.Context, _, _ string) (model.WorkerExecution, error) {
    return m.execResult, nil
}
func (m *mockExecManager) GetExecution(_ string) (model.WorkerExecution, error) {
    return m.getResult, nil
}

type mockTaskStore struct{}

func (s *mockTaskStore) SetExecution(_ context.Context, _, _, _ string) error { return nil }

type mockMsgStore struct{}

func (s *mockMsgStore) GetSession(_ context.Context, _ string) (*platform.Session, error) {
    return nil, nil // no prior session; tests use fresh execution
}
func (s *mockMsgStore) SetMessageExecution(_ context.Context, _, _, _ string) error { return nil }

func dispatchTask(taskType, sessionKey, workerID, instruction string) dispatcher.DispatchTask {
    return dispatcher.DispatchTask{
        TaskID:      "task-1",
        WorkerID:    workerID,
        SessionKey:  sessionKey,
        Instruction: instruction,
        ReplyTo:     platform.InboundMessage{Platform: "test", SessionKey: sessionKey},
        TaskType:    taskType,
        MessageID:   "msg-1",
    }
}

func collectEvents(out <-chan msgsender.SenderEvent, n int, timeout time.Duration) []msgsender.SenderEvent {
    var events []msgsender.SenderEvent
    deadline := time.After(timeout)
    for len(events) < n {
        select {
        case evt := <-out:
            events = append(events, evt)
        case <-deadline:
            return events
        }
    }
    return events
}

func TestDispatcher_ImmediateTask_EmitsACKThenResult(t *testing.T) {
    in := make(chan dispatcher.DispatchTask, 1)
    mgr := &mockExecManager{
        execResult: model.WorkerExecution{ID: "exec-1", SessionID: "sess-1"},
        getResult:  model.WorkerExecution{ID: "exec-1", Status: model.ExecStatusCompleted, Result: "done!"},
    }
    d := dispatcher.New(mgr, &mockTaskStore{}, &mockMsgStore{}, in)

    ctx, cancel := context.WithCancel(context.Background())
    defer cancel()
    go d.Run(ctx)

    in <- dispatchTask("immediate", "s1", "w1", "check weather")

    events := collectEvents(d.Out(), 2, 2*time.Second)
    if len(events) < 2 {
        t.Fatalf("expected ACK+Result, got %d events", len(events))
    }
    if events[0].Type != msgsender.SenderEventACK {
        t.Errorf("first event should be ACK, got %v", events[0].Type)
    }
    if events[1].Type != msgsender.SenderEventResult {
        t.Errorf("second event should be Result, got %v", events[1].Type)
    }
    if events[1].Content != "done!" {
        t.Errorf("unexpected result content: %q", events[1].Content)
    }
}

func TestDispatcher_ClearTask_EmitsClearResult(t *testing.T) {
    in := make(chan dispatcher.DispatchTask, 1)
    d := dispatcher.New(&mockExecManager{}, &mockTaskStore{}, &mockMsgStore{}, in)

    ctx, cancel := context.WithCancel(context.Background())
    defer cancel()
    go d.Run(ctx)

    in <- dispatcher.DispatchTask{
        TaskType:   "clear",
        SessionKey: "s1",
        ReplyTo:    platform.InboundMessage{Platform: "test", SessionKey: "s1"},
    }

    events := collectEvents(d.Out(), 1, 500*time.Millisecond)
    if len(events) != 1 || events[0].Type != msgsender.SenderEventResult {
        t.Fatalf("expected 1 result for clear, got %v", events)
    }
    if events[0].Content != "✅ 上下文已重置" {
        t.Errorf("unexpected clear content: %q", events[0].Content)
    }
}

func TestDispatcher_TwoTasks_SameSession_Serialized(t *testing.T) {
    in := make(chan dispatcher.DispatchTask, 2)
    blocker := make(chan struct{})
    mgr := &blockingExecManager{blocker: blocker}
    d := dispatcher.New(mgr, &mockTaskStore{}, &mockMsgStore{}, in)

    ctx, cancel := context.WithCancel(context.Background())
    defer cancel()
    go d.Run(ctx)

    t1 := dispatchTask("immediate", "s1", "w1", "first")
    t1.TaskID = "task-1"
    t2 := dispatchTask("immediate", "s1", "w1", "second")
    t2.TaskID = "task-2"

    in <- t1
    in <- t2

    // Both should get ACK
    events := collectEvents(d.Out(), 2, 500*time.Millisecond)
    ackCount := 0
    for _, e := range events {
        if e.Type == msgsender.SenderEventACK {
            ackCount++
        }
    }
    if ackCount != 2 {
        t.Errorf("expected 2 ACKs, got %d", ackCount)
    }

    // Unblock first execution
    close(blocker)

    // Should get 2 results
    results := collectEvents(d.Out(), 2, 2*time.Second)
    resultCount := 0
    for _, e := range results {
        if e.Type == msgsender.SenderEventResult {
            resultCount++
        }
    }
    if resultCount != 2 {
        t.Errorf("expected 2 results, got %d", resultCount)
    }
}

type blockingExecManager struct {
    blocker <-chan struct{}
}

func (m *blockingExecManager) ExecuteWorker(_ context.Context, _, _ string) (model.WorkerExecution, error) {
    <-m.blocker
    return model.WorkerExecution{ID: "exec-x"}, nil
}
func (m *blockingExecManager) ReplyExecution(_ context.Context, _, _ string) (model.WorkerExecution, error) {
    <-m.blocker
    return model.WorkerExecution{ID: "exec-x"}, nil
}
func (m *blockingExecManager) GetExecution(_ string) (model.WorkerExecution, error) {
    return model.WorkerExecution{ID: "exec-x", Status: model.ExecStatusCompleted, Result: "ok"}, nil
}
```

- [ ] **Step 3: Run tests to verify they fail**

```bash
go test ./internal/dispatcher/ -v
```

Expected: FAIL — `dispatcher.New` signature mismatch, `DispatchTask` undefined in old code

- [ ] **Step 4: Rewrite dispatcher.go**

Replace `internal/dispatcher/dispatcher.go` with the new implementation. Key changes:
- Input channel type: `<-chan DispatchTask` instead of `<-chan msgrouter.RoutedMessage`
- Remove `msgrouter` import
- Add `TaskStore` and `MessageStore` interface dependencies
- Remove `recoverFromDB` (startup recovery is now in TaskScheduler)
- Handle `TaskType == "clear"` in `handleInbound`
- For `immediate` tasks with existing session: use `ReplyExecution` (`--resume`)
- On completion: call `taskStore.SetExecution` + `msgStore.SetMessageExecution` (for immediate only)

```go
package dispatcher

import (
    "context"
    "log"
    "strings"
    "time"

    "github.com/robobee/core/internal/model"
    "github.com/robobee/core/internal/msgsender"
    "github.com/robobee/core/internal/platform"
)

const (
    pollInterval = 2 * time.Second
    pollTimeout  = 30 * time.Minute
    clearMsg     = "✅ 上下文已重置"
    errorMsg     = "❌ 处理失败，请稍后重试"
    ackMsg       = "⏳ 正在处理，请稍候…"
    timeoutMsg   = "⏰ 任务超时，请稍后通过 Web 界面查看结果"
)

// ExecutionManager manages worker executions.
type ExecutionManager interface {
    ExecuteWorker(ctx context.Context, workerID, input string) (model.WorkerExecution, error)
    // ReplyExecution resumes an existing Claude session identified by execID.
    // Used for immediate tasks where a prior session exists.
    ReplyExecution(ctx context.Context, execID, input string) (model.WorkerExecution, error)
    GetExecution(id string) (model.WorkerExecution, error)
}

// TaskStore is the subset of store.TaskStore used by the Dispatcher.
type TaskStore interface {
    // SetExecution writes execution_id and status to a task row.
    // Called twice per task: once with status "running" when execution starts,
    // once with "completed" or "failed" when the poll loop terminates.
    // Both writes are sequential in the same goroutine — no race.
    SetExecution(ctx context.Context, taskID, executionID, status string) error
}

// MessageStore is the subset of store.MessageStore used by the Dispatcher.
type MessageStore interface {
    // GetSession returns the last session (LastExecutionID) for the given session key.
    // Used by immediate tasks to decide whether to resume a prior Claude session.
    GetSession(ctx context.Context, sessionKey string) (*platform.Session, error)
    // SetMessageExecution writes execution_id and session_id back to platform_messages
    // WHERE status = 'bee_processed' (no-op if rolled back by Feeder).
    SetMessageExecution(ctx context.Context, messageID, executionID, sessionID string) error
}

type queueState struct {
    executing      bool
    pendingTasks   []DispatchTask
    lastReplyTo    platform.InboundMessage
}

type internalResult struct {
    queueKey string
    task     DispatchTask
    content  string
}

// Dispatcher serializes worker executions per (SessionKey, WorkerID) and emits SenderEvents.
type Dispatcher struct {
    ctx       context.Context
    manager   ExecutionManager
    taskStore TaskStore
    msgStore  MessageStore
    in        <-chan DispatchTask
    results   chan internalResult
    out       chan msgsender.SenderEvent
    queues    map[string]*queueState
}

// New constructs a Dispatcher.
func New(manager ExecutionManager, taskStore TaskStore, msgStore MessageStore, in <-chan DispatchTask) *Dispatcher {
    return &Dispatcher{
        manager:   manager,
        taskStore: taskStore,
        msgStore:  msgStore,
        in:        in,
        results:   make(chan internalResult, 64),
        out:       make(chan msgsender.SenderEvent, 64),
        queues:    make(map[string]*queueState),
    }
}

// Out returns the channel of outgoing SenderEvents.
func (d *Dispatcher) Out() <-chan msgsender.SenderEvent { return d.out }

// Run processes tasks until ctx is cancelled. Call in a goroutine.
func (d *Dispatcher) Run(ctx context.Context) {
    defer close(d.out)
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
    // Handle clear command: evict all session state for this session prefix.
    if task.TaskType == "clear" {
        prefix := task.SessionKey + "|"
        for key := range d.queues {
            if strings.HasPrefix(key, prefix) {
                delete(d.queues, key)
            }
        }
        replyTo := task.ReplyTo
        if task.ReplySessionKey != "" {
            replyTo.SessionKey = task.ReplySessionKey
        }
        select {
        case d.out <- msgsender.SenderEvent{Type: msgsender.SenderEventResult, ReplyTo: replyTo, Content: clearMsg}:
        case <-d.ctx.Done():
        }
        return
    }

    key := queueKey(task.SessionKey, task.WorkerID)
    state, ok := d.queues[key]
    if !ok {
        state = &queueState{}
        d.queues[key] = state
    }

    // Determine effective reply target.
    replyTo := task.ReplyTo
    if task.ReplySessionKey != "" {
        replyTo.SessionKey = task.ReplySessionKey
    }

    // ACK immediately.
    select {
    case d.out <- msgsender.SenderEvent{Type: msgsender.SenderEventACK, ReplyTo: replyTo, Content: ackMsg}:
    case <-d.ctx.Done():
        return
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

func (d *Dispatcher) executeAsync(ctx context.Context, key string, task DispatchTask, replyTo platform.InboundMessage) {
    var exec model.WorkerExecution
    var err error

    // For immediate tasks, attempt --resume if a prior session exists.
    // GetSession looks up LastExecutionID stored by the previous execution on this session key.
    if task.TaskType == model.TaskTypeImmediate {
        sess, sessErr := d.msgStore.GetSession(ctx, task.SessionKey)
        if sessErr != nil {
            log.Printf("dispatcher: get session error: %v", sessErr)
        }
        if sess != nil && sess.LastExecutionID != "" {
            log.Printf("dispatcher: resuming execID=%s for task %s", sess.LastExecutionID, task.TaskID)
            exec, err = d.manager.ReplyExecution(ctx, sess.LastExecutionID, task.Instruction)
            if err != nil {
                log.Printf("dispatcher: resume error (falling back to fresh): %v", err)
                exec, err = d.manager.ExecuteWorker(ctx, task.WorkerID, task.Instruction)
            }
            goto execStarted
        }
    }

    log.Printf("dispatcher: executing worker %s for task %s", task.WorkerID, task.TaskID)
    exec, err = d.manager.ExecuteWorker(ctx, task.WorkerID, task.Instruction)

execStarted:
    if err != nil {
        log.Printf("dispatcher: execute error: %v", err)
        select {
        case d.results <- internalResult{queueKey: key, task: task, content: errorMsg}:
        case <-ctx.Done():
        }
        return
    }

    // Write execution back to task and message records.
    if task.TaskID != "" {
        d.taskStore.SetExecution(ctx, task.TaskID, exec.ID, model.TaskStatusRunning) //nolint:errcheck
    }
    if task.TaskType == model.TaskTypeImmediate && task.MessageID != "" {
        d.msgStore.SetMessageExecution(ctx, task.MessageID, exec.ID, exec.SessionID) //nolint:errcheck
    }

    result := d.waitForResult(ctx, exec.ID, task.TaskID)
    select {
    case d.results <- internalResult{queueKey: key, task: task, content: result}:
    case <-ctx.Done():
    }
}

func (d *Dispatcher) waitForResult(ctx context.Context, executionID, taskID string) string {
    deadline := time.Now().Add(pollTimeout)
    lastStatus := ""
    for time.Now().Before(deadline) {
        exec, err := d.manager.GetExecution(executionID)
        if err != nil {
            log.Printf("dispatcher: poll error execID=%s: %v", executionID, err)
            return errorMsg
        }
        if string(exec.Status) != lastStatus {
            log.Printf("dispatcher: polling execID=%s status=%s", executionID, exec.Status)
            lastStatus = string(exec.Status)
        }
        switch exec.Status {
        case model.ExecStatusCompleted:
            if taskID != "" {
                d.taskStore.SetExecution(ctx, taskID, executionID, model.TaskStatusCompleted) //nolint:errcheck
            }
            if exec.Result != "" {
                return exec.Result
            }
            return "✅ 任务已完成"
        case model.ExecStatusFailed:
            if taskID != "" {
                d.taskStore.SetExecution(ctx, taskID, executionID, model.TaskStatusFailed) //nolint:errcheck
            }
            return "❌ 任务执行失败: " + exec.Result
        }
        select {
        case <-time.After(pollInterval):
        case <-ctx.Done():
            return timeoutMsg
        }
    }
    return timeoutMsg
}

func (d *Dispatcher) handleResult(res internalResult) {
    replyTo := res.task.ReplyTo
    if res.task.ReplySessionKey != "" {
        replyTo.SessionKey = res.task.ReplySessionKey
    }
    select {
    case d.out <- msgsender.SenderEvent{Type: msgsender.SenderEventResult, ReplyTo: replyTo, Content: res.content}:
    case <-d.ctx.Done():
        return
    }

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

> Note: The `GetMessageSessionID` lookup for `--resume` requires adding a method to `MessageStore` in the store package (see Task 8 below). For now, the interface is defined; implement the store method concurrently.

- [ ] **Step 5: Run tests**

```bash
go test ./internal/dispatcher/ -v
```

Expected: all PASS

- [ ] **Step 6: Build to catch any compile errors across the repo**

```bash
go build ./...
```

Expected: errors only in `main.go` (wiring not yet updated — expected at this stage)

- [ ] **Step 7: Commit**

```bash
git add internal/dispatcher/task.go internal/dispatcher/dispatcher.go internal/dispatcher/dispatcher_test.go
git commit -m "feat(dispatcher): refactor to accept DispatchTask; remove msgrouter dependency"
```

---

### Task 8: Add SetMessageExecution to MessageStore

The Dispatcher needs `SetMessageExecution` to write `execution_id`/`session_id` back to `platform_messages` for immediate tasks (enabling future `--resume`). The existing `GetSession(ctx, sessionKey)` method already handles session lookup.

**Files:**
- Modify: `internal/store/message_store.go`
- Modify: `internal/store/message_store_test.go`

- [ ] **Step 1: Write the failing test**

Add to `internal/store/message_store_test.go`:

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
        (id, session_key, platform, content, raw, platform_msg_id, status, received_at)
        VALUES ('m1', 'sk', 'feishu', 'hi', '', '', 'bee_processed', 1)`)

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

- [ ] **Step 2: Run test to verify it fails**

```bash
go test ./internal/store/ -run TestMessageStore_SetMessageExecution_OnlyWhenBeeProcessed -v
```

Expected: FAIL — `ms.SetMessageExecution undefined`

- [ ] **Step 3: Implement SetMessageExecution in message_store.go**

```go
// SetMessageExecution writes execution_id and session_id back to a platform_messages row,
// but only when status = 'bee_processed'. This is a no-op if the Feeder rolled the row back.
func (s *MessageStore) SetMessageExecution(ctx context.Context, messageID, executionID, sessionID string) error {
    _, err := s.db.ExecContext(ctx,
        `UPDATE platform_messages
         SET execution_id = ?, session_id = ?
         WHERE id = ? AND status = 'bee_processed'`,
        executionID, sessionID, messageID,
    )
    return err
}
```

- [ ] **Step 4: Run tests**

```bash
go test ./internal/store/ -run TestMessageStore -v
```

Expected: all PASS

- [ ] **Step 5: Commit**

```bash
git add internal/store/message_store.go internal/store/message_store_test.go
git commit -m "feat(store): add SetMessageExecution for dispatcher session writeback"
```

---

## Chunk 5: TaskScheduler

### Task 9: Create internal/taskscheduler/scheduler.go

**Files:**
- Create: `internal/taskscheduler/scheduler.go`
- Create: `internal/taskscheduler/scheduler_test.go`

- [ ] **Step 1: Write failing tests**

Create `internal/taskscheduler/scheduler_test.go`:

```go
package taskscheduler_test

import (
    "context"
    "database/sql"
    "testing"
    "time"

    "github.com/robobee/core/internal/dispatcher"
    "github.com/robobee/core/internal/model"
    "github.com/robobee/core/internal/store"
    "github.com/robobee/core/internal/taskscheduler"
)

func setupDB(t *testing.T) (*sql.DB, *store.TaskStore) {
    t.Helper()
    db, err := store.InitDB(t.TempDir() + "/test.db")
    if err != nil {
        t.Fatalf("InitDB: %v", err)
    }
    db.Exec(`INSERT INTO workers (id,name,work_dir,status,created_at,updated_at) VALUES ('w1','W','/',`+"`idle`"+`,1,1)`)
    db.Exec(`INSERT INTO platform_messages (id,session_key,platform,content,received_at) VALUES ('m1','sk','feishu','hi',1)`)
    return db, store.NewTaskStore(db)
}

func TestScheduler_ImmediateTask_Dispatched(t *testing.T) {
    db, ts := setupDB(t)
    defer db.Close()

    now := time.Now().UnixMilli()
    ts.Create(context.Background(), model.Task{
        MessageID: "m1", WorkerID: "w1", Instruction: "go",
        Type: model.TaskTypeImmediate, Status: model.TaskStatusPending,
        CreatedAt: now, UpdatedAt: now,
    })

    dispCh := make(chan dispatcher.DispatchTask, 10)
    sched := taskscheduler.New(ts, dispCh, 50*time.Millisecond)

    ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
    defer cancel()
    go sched.Run(ctx)

    select {
    case task := <-dispCh:
        if task.WorkerID != "w1" {
            t.Errorf("unexpected worker: %s", task.WorkerID)
        }
        if task.TaskType != model.TaskTypeImmediate {
            t.Errorf("unexpected task type: %s", task.TaskType)
        }
    case <-ctx.Done():
        t.Fatal("timeout: no task dispatched")
    }
}

func TestScheduler_CountdownTask_NotDispatchedBeforeTime(t *testing.T) {
    db, ts := setupDB(t)
    defer db.Close()

    now := time.Now().UnixMilli()
    future := now + 60_000 // 1 minute from now
    ts.Create(context.Background(), model.Task{
        MessageID: "m1", WorkerID: "w1", Instruction: "go",
        Type: model.TaskTypeCountdown, Status: model.TaskStatusPending,
        ScheduledAt: &future,
        CreatedAt: now, UpdatedAt: now,
    })

    dispCh := make(chan dispatcher.DispatchTask, 10)
    sched := taskscheduler.New(ts, dispCh, 50*time.Millisecond)

    ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
    defer cancel()
    go sched.Run(ctx)

    select {
    case task := <-dispCh:
        t.Errorf("should not have dispatched future task, got: %+v", task)
    case <-ctx.Done():
        // Expected: no dispatch
    }
}
```

- [ ] **Step 2: Run tests to verify they fail**

```bash
go test ./internal/taskscheduler/ -v
```

Expected: FAIL — package does not exist yet

- [ ] **Step 3: Implement scheduler**

Create `internal/taskscheduler/scheduler.go`:

```go
package taskscheduler

import (
    "context"
    "log"
    "time"

    "github.com/robfig/cron/v3"
    "github.com/robobee/core/internal/dispatcher"
    "github.com/robobee/core/internal/model"
    "github.com/robobee/core/internal/platform"
    "github.com/robobee/core/internal/store"
)

// Scheduler polls for due tasks and sends them to the Dispatcher.
type Scheduler struct {
    taskStore *store.TaskStore
    dispatchCh chan<- dispatcher.DispatchTask
    pollInterval time.Duration
}

// New creates a Scheduler.
func New(taskStore *store.TaskStore, dispatchCh chan<- dispatcher.DispatchTask, pollInterval time.Duration) *Scheduler {
    return &Scheduler{
        taskStore:    taskStore,
        dispatchCh:   dispatchCh,
        pollInterval: pollInterval,
    }
}

// RecoverRunning resets all 'running' tasks to 'pending'.
// Must be called synchronously at startup AFTER the Feeder's RecoverFeeding.
func (s *Scheduler) RecoverRunning(ctx context.Context) {
    n, err := s.taskStore.ResetRunningToPending(ctx)
    if err != nil {
        log.Printf("taskscheduler: recover running tasks: %v", err)
        return
    }
    if n > 0 {
        log.Printf("taskscheduler: reset %d running task(s) to pending", n)
    }
}

// Run polls for due tasks on each tick until ctx is cancelled.
func (s *Scheduler) Run(ctx context.Context) {
    ticker := time.NewTicker(s.pollInterval)
    defer ticker.Stop()
    for {
        select {
        case <-ticker.C:
            s.poll(ctx)
        case <-ctx.Done():
            return
        }
    }
}

func (s *Scheduler) poll(ctx context.Context) {
    nowMS := time.Now().UnixMilli()
    tasks, err := s.taskStore.ClaimDueTasks(ctx, nowMS)
    if err != nil {
        log.Printf("taskscheduler: claim due tasks: %v", err)
        return
    }

    for _, ct := range tasks {
        // For scheduled tasks, compute the real next_run_at (from now) and update.
        // This overwrites the sentinel value set by ClaimDueTasks and also handles
        // the next_run_at=NULL case after startup recovery.
        if ct.Type == model.TaskTypeScheduled && ct.CronExpr != "" {
            sched, err := cron.ParseStandard(ct.CronExpr)
            if err != nil {
                log.Printf("taskscheduler: invalid cron %q for task %s: %v", ct.CronExpr, ct.ID, err)
                s.taskStore.SetExecution(ctx, ct.ID, "", model.TaskStatusFailed) //nolint:errcheck
                continue
            }
            // Miss policy: skip — always compute from time.Now(), not from the missed next_run_at
            next := sched.Next(time.Now()).UnixMilli()
            s.taskStore.UpdateNextRunAt(ctx, ct.ID, next) //nolint:errcheck
        }

        sessionKey := ct.MessageSessionKey
        replySessionKey := ct.ReplySessionKey
        effectiveSession := sessionKey
        if replySessionKey != "" {
            effectiveSession = replySessionKey
        }

        dt := dispatcher.DispatchTask{
            TaskID:          ct.ID,
            WorkerID:        ct.WorkerID,
            SessionKey:      sessionKey,
            Instruction:     ct.Instruction,
            ReplyTo:         platform.InboundMessage{Platform: ct.MessagePlatform, SessionKey: effectiveSession},
            TaskType:        ct.Type,
            MessageID:       ct.MessageID,
            ReplySessionKey: replySessionKey,
        }

        select {
        case s.dispatchCh <- dt:
        case <-ctx.Done():
            return
        }
    }
}
```

- [ ] **Step 4: Run tests**

```bash
go test ./internal/taskscheduler/ -v
```

Expected: all PASS

- [ ] **Step 5: Commit**

```bash
git add internal/taskscheduler/scheduler.go internal/taskscheduler/scheduler_test.go
git commit -m "feat(taskscheduler): add task scheduler with atomic claim and cron support"
```

---

## Chunk 6: Bee Package (Feeder + BeeProcess)

### Task 10: Create bee/bee_process.go

**Files:**
- Create: `internal/bee/bee_process.go`

This file encapsulates spawning and waiting for the bee Claude process. No test for the process itself (it wraps `exec.Cmd`); integration is tested via Feeder.

- [ ] **Step 1: Create the file**

```go
package bee

import (
    "bufio"
    "context"
    "fmt"
    "log"
    "os"
    "os/exec"
    "path/filepath"
)

// BeeProcess represents a single short-lived bee Claude invocation.
type BeeProcess struct {
    binary string
    mcpURL string
    apiKey string
}

// NewBeeProcess creates a BeeProcess.
func NewBeeProcess(binary, mcpURL, apiKey string) *BeeProcess {
    return &BeeProcess{binary: binary, mcpURL: mcpURL, apiKey: apiKey}
}

// WriteCLAUDEMD writes (or overwrites) the CLAUDE.md file in workDir with persona content.
func WriteCLAUDEMD(workDir, persona string) error {
    if err := os.MkdirAll(workDir, 0o755); err != nil {
        return fmt.Errorf("mkdir bee workdir: %w", err)
    }
    path := filepath.Join(workDir, "CLAUDE.md")
    return os.WriteFile(path, []byte(persona), 0o644)
}

// Run spawns the bee process with the given prompt and waits for it to exit.
// Returns nil on exit code 0, an error otherwise.
func (p *BeeProcess) Run(ctx context.Context, workDir, prompt string) error {
    args := []string{
        "--dangerously-skip-permissions",
        "--output-format", "stream-json",
        "--mcp-server", "robobee=" + p.mcpURL,
        "--mcp-api-key", p.apiKey,
        "-p", prompt,
    }
    cmd := exec.CommandContext(ctx, p.binary, args...)
    cmd.Dir = workDir

    stdout, err := cmd.StdoutPipe()
    if err != nil {
        return fmt.Errorf("stdout pipe: %w", err)
    }
    stderr, err := cmd.StderrPipe()
    if err != nil {
        return fmt.Errorf("stderr pipe: %w", err)
    }

    if err := cmd.Start(); err != nil {
        return fmt.Errorf("start bee: %w", err)
    }

    // Drain stdout/stderr to prevent pipe buffer from blocking
    go func() {
        scanner := bufio.NewScanner(stdout)
        for scanner.Scan() {
            log.Printf("bee: %s", scanner.Text())
        }
    }()
    go func() {
        scanner := bufio.NewScanner(stderr)
        for scanner.Scan() {
            log.Printf("bee stderr: %s", scanner.Text())
        }
    }()

    if err := cmd.Wait(); err != nil {
        return fmt.Errorf("bee exited with error: %w", err)
    }
    return nil
}
```

- [ ] **Step 2: Verify build**

```bash
go build ./internal/bee/
```

Expected: no errors.

- [ ] **Step 3: Commit**

```bash
git add internal/bee/bee_process.go
git commit -m "feat(bee): add BeeProcess for spawning bee Claude invocations"
```

---

### Task 11: Create bee/feeder.go

**Files:**
- Create: `internal/bee/feeder.go`
- Create: `internal/bee/feeder_test.go`

- [ ] **Step 1: Write failing tests**

Create `internal/bee/feeder_test.go`:

```go
package bee_test

import (
    "context"
    "database/sql"
    "testing"
    "time"

    "github.com/robobee/core/internal/bee"
    "github.com/robobee/core/internal/dispatcher"
    "github.com/robobee/core/internal/store"
)

func setupFeederDB(t *testing.T) (*sql.DB, *store.MessageStore, *store.TaskStore) {
    t.Helper()
    db, err := store.InitDB(t.TempDir() + "/test.db")
    if err != nil {
        t.Fatalf("InitDB: %v", err)
    }
    db.Exec(`INSERT INTO platform_messages (id, session_key, platform, content, status, received_at)
             VALUES ('m1', 'feishu:c:u', 'feishu', 'hello', 'received', 1)`)
    return db, store.NewMessageStore(db), store.NewTaskStore(db)
}

// mockBeeRunner is a test double that records the prompt it was called with.
type mockBeeRunner struct {
    calledWithPrompt string
    err              error
}

func (m *mockBeeRunner) Run(_ context.Context, _, prompt string) error {
    m.calledWithPrompt = prompt
    return m.err
}

func TestFeeder_ClaimsMessages_And_InvokesBee(t *testing.T) {
    db, ms, ts := setupFeederDB(t)
    defer db.Close()

    runner := &mockBeeRunner{}
    clearCh := make(chan dispatcher.DispatchTask, 10)
    cfg := bee.FeederConfig{
        Interval:           50 * time.Millisecond,
        BatchSize:          5,
        Timeout:            5 * time.Second,
        QueueWarnThreshold: 100,
        WorkDir:            t.TempDir(),
    }

    f := bee.NewFeeder(ms, ts, runner, clearCh, cfg)
    ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
    defer cancel()

    go f.Run(ctx)
    time.Sleep(200 * time.Millisecond)

    if runner.calledWithPrompt == "" {
        t.Error("expected bee runner to be called with a prompt")
    }

    // Message should now be bee_processed
    var status string
    db.QueryRow(`SELECT status FROM platform_messages WHERE id='m1'`).Scan(&status)
    if status != "bee_processed" {
        t.Errorf("expected bee_processed, got %q", status)
    }
}

func TestFeeder_RollsBack_OnBeeFailure(t *testing.T) {
    db, ms, ts := setupFeederDB(t)
    defer db.Close()

    runner := &mockBeeRunner{err: fmt.Errorf("bee crashed")}
    clearCh := make(chan dispatcher.DispatchTask, 10)
    cfg := bee.FeederConfig{
        Interval:           50 * time.Millisecond,
        BatchSize:          5,
        Timeout:            5 * time.Second,
        QueueWarnThreshold: 100,
        WorkDir:            t.TempDir(),
    }

    f := bee.NewFeeder(ms, ts, runner, clearCh, cfg)
    ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
    defer cancel()

    go f.Run(ctx)
    time.Sleep(150 * time.Millisecond)

    var status string
    db.QueryRow(`SELECT status FROM platform_messages WHERE id='m1'`).Scan(&status)
    if status != "received" {
        t.Errorf("expected rollback to received, got %q", status)
    }
}
```

> Add `"fmt"` to the test imports.

- [ ] **Step 2: Run tests to verify they fail**

```bash
go test ./internal/bee/ -v
```

Expected: FAIL — `bee.NewFeeder` undefined

- [ ] **Step 3: Implement feeder.go**

Create `internal/bee/feeder.go`:

```go
package bee

import (
    "context"
    "fmt"
    "log"
    "strings"
    "time"

    "github.com/robobee/core/internal/dispatcher"
    "github.com/robobee/core/internal/msgingest"
    "github.com/robobee/core/internal/store"
)

// FeederConfig holds Feeder tuning parameters.
type FeederConfig struct {
    Interval           time.Duration
    BatchSize          int
    Timeout            time.Duration
    QueueWarnThreshold int
    WorkDir            string
    Persona            string
    Binary             string // claude CLI path
    MCPBaseURL         string // e.g. "http://localhost:8080"
    MCPAPIKey          string
}

// BeeRunner abstracts the bee process invocation (real or test double).
type BeeRunner interface {
    Run(ctx context.Context, workDir, prompt string) error
}

// Feeder polls platform_messages for unprocessed messages and feeds them to bee.
type Feeder struct {
    msgStore *store.MessageStore
    taskStore *store.TaskStore
    runner   BeeRunner
    clearCh  chan<- dispatcher.DispatchTask
    cfg      FeederConfig
}

// NewFeeder creates a Feeder.
func NewFeeder(ms *store.MessageStore, ts *store.TaskStore, runner BeeRunner, clearCh chan<- dispatcher.DispatchTask, cfg FeederConfig) *Feeder {
    return &Feeder{
        msgStore:  ms,
        taskStore: ts,
        runner:    runner,
        clearCh:   clearCh,
        cfg:       cfg,
    }
}

// RecoverFeeding resets any messages stuck in 'feeding' status back to 'received'
// and deletes their associated pending tasks.
// Must be called synchronously at startup BEFORE TaskScheduler.RecoverRunning.
func (f *Feeder) RecoverFeeding(ctx context.Context) {
    ids, err := f.msgStore.ResetFeedingToReceived(ctx)
    if err != nil {
        log.Printf("feeder: recover feeding: %v", err)
        return
    }
    if len(ids) == 0 {
        return
    }
    if err := f.taskStore.DeletePendingByMessageIDs(ctx, ids); err != nil {
        log.Printf("feeder: delete orphaned tasks: %v", err)
    }
    log.Printf("feeder: recovered %d feeding message(s)", len(ids))
}

// Run polls for unprocessed messages on each tick. Call in a goroutine.
func (f *Feeder) Run(ctx context.Context) {
    ticker := time.NewTicker(f.cfg.Interval)
    defer ticker.Stop()
    for {
        select {
        case <-ticker.C:
            f.tick(ctx)
        case <-ctx.Done():
            return
        }
    }
}

func (f *Feeder) tick(ctx context.Context) {
    // Check queue health
    count, _ := f.msgStore.CountReceived(ctx)
    if count > f.cfg.QueueWarnThreshold {
        log.Printf("feeder: WARNING: %d unprocessed messages in queue (threshold: %d)", count, f.cfg.QueueWarnThreshold)
    }

    // Claim a batch atomically
    msgs, err := f.msgStore.ClaimBatch(ctx, f.cfg.BatchSize)
    if err != nil {
        log.Printf("feeder: claim batch: %v", err)
        return
    }
    if len(msgs) == 0 {
        return
    }

    // Separate clear commands from regular messages
    var clearMsgs, regularMsgs []store.ClaimedMessage
    for _, m := range msgs {
        if detectClear(m.Content) {
            clearMsgs = append(clearMsgs, m)
        } else {
            regularMsgs = append(regularMsgs, m)
        }
    }

    // Handle clear commands directly (bypass bee)
    for _, m := range clearMsgs {
        f.msgStore.MarkBeeProcessed(ctx, []string{m.ID}) //nolint:errcheck
        select {
        case f.clearCh <- dispatcher.DispatchTask{
            TaskType:   "clear",
            SessionKey: m.SessionKey,
            ReplyTo: platform.InboundMessage{
                Platform:   m.Platform,
                SessionKey: m.SessionKey,
            },
        }:
        default:
        }
    }

    if len(regularMsgs) == 0 {
        return
    }

    // Write CLAUDE.md with persona
    if err := WriteCLAUDEMD(f.cfg.WorkDir, f.cfg.Persona); err != nil {
        log.Printf("feeder: write CLAUDE.md: %v", err)
        f.rollback(ctx, regularMsgs)
        return
    }

    prompt := buildPrompt(regularMsgs)
    msgIDs := make([]string, len(regularMsgs))
    for i, m := range regularMsgs {
        msgIDs[i] = m.ID
    }

    beeCtx, cancel := context.WithTimeout(ctx, f.cfg.Timeout)
    defer cancel()

    if err := f.runner.Run(beeCtx, f.cfg.WorkDir, prompt); err != nil {
        log.Printf("feeder: bee run failed: %v", err)
        f.rollback(ctx, regularMsgs)
        return
    }

    if err := f.msgStore.MarkBeeProcessed(ctx, msgIDs); err != nil {
        log.Printf("feeder: mark bee_processed: %v", err)
    }
}

func (f *Feeder) rollback(ctx context.Context, msgs []store.ClaimedMessage) {
    ids := make([]string, len(msgs))
    for i, m := range msgs {
        ids[i] = m.ID
    }
    if err := f.taskStore.DeletePendingByMessageIDs(ctx, ids); err != nil {
        log.Printf("feeder: rollback delete tasks: %v", err)
    }
    if err := f.msgStore.ResetFeedingBatch(ctx, ids); err != nil {
        log.Printf("feeder: rollback messages: %v", err)
    }
}

func detectClear(content string) bool {
    return strings.TrimSpace(strings.ToLower(content)) == "clear"
}

func buildPrompt(msgs []store.ClaimedMessage) string {
    var sb strings.Builder
    fmt.Fprintf(&sb, "以下是 %d 条待处理用户消息，请为每条消息创建相应的任务。\n\n", len(msgs))
    for i, m := range msgs {
        fmt.Fprintf(&sb, "--- 消息 %d ---\n来源: %s | 会话: %s | 消息ID: %s\n内容: %s\n\n",
            i+1, m.Platform, m.SessionKey, m.ID, m.Content)
    }
    sb.WriteString("请使用 create_task 工具为每条消息中的每个任务指派创建任务记录。")
    return sb.String()
}
```

> Note: `platform.InboundMessage` import needs to be added; add `"github.com/robobee/core/internal/platform"` to imports.

- [ ] **Step 4: Add required MessageStore methods**

The Feeder needs these MessageStore methods (add to `internal/store/message_store.go` and test them):

Method signatures (all in `internal/store/message_store.go`):
- `ClaimBatch(ctx context.Context, batchSize int) ([]ClaimedMessage, error)` — atomic SELECT + UPDATE received→feeding
- `MarkBeeProcessed(ctx context.Context, ids []string) error`
- `ResetFeedingBatch(ctx context.Context, ids []string) error` — rollback feeding→received
- `ResetFeedingToReceived(ctx context.Context) ([]string, error)` — startup recovery; returns IDs of rows reset so caller can delete orphaned tasks
- `CountReceived(ctx context.Context) (int, error)` — queue health check

**`ClaimedMessage` struct** (add to `internal/store/message_store.go` before the method definitions):

```go
// ClaimedMessage is a platform_messages row claimed by the Feeder.
// It contains all fields the Feeder needs to build the bee prompt and route clear commands.
type ClaimedMessage struct {
    ID         string // platform_messages.id
    SessionKey string // platform_messages.session_key
    Platform   string // platform_messages.platform
    Content    string // platform_messages.content
}
```

**`ClaimBatch`** selects only `WHERE status = 'received'` and updates to `'feeding'` atomically:

Implement `ClaimBatch` with an atomic transaction:

```go
func (s *MessageStore) ClaimBatch(ctx context.Context, batchSize int) ([]ClaimedMessage, error) {
    tx, err := s.db.BeginTx(ctx, nil)
    if err != nil {
        return nil, fmt.Errorf("begin tx: %w", err)
    }
    defer tx.Rollback() //nolint:errcheck

    rows, err := tx.QueryContext(ctx,
        `SELECT id, session_key, platform, content FROM platform_messages
         WHERE status = 'received'
         ORDER BY received_at ASC LIMIT ?`, batchSize)
    if err != nil {
        return nil, fmt.Errorf("select batch: %w", err)
    }
    var msgs []ClaimedMessage
    for rows.Next() {
        var m ClaimedMessage
        if err := rows.Scan(&m.ID, &m.SessionKey, &m.Platform, &m.Content); err != nil {
            rows.Close()
            return nil, fmt.Errorf("scan: %w", err)
        }
        msgs = append(msgs, m)
    }
    rows.Close()
    if err := rows.Err(); err != nil {
        return nil, err
    }
    if len(msgs) == 0 {
        return nil, nil
    }

    ids := make([]string, len(msgs))
    for i, m := range msgs {
        ids[i] = m.ID
    }
    if err := updateStatusBatch(ctx, tx, ids, "feeding"); err != nil {
        return nil, err
    }
    return msgs, tx.Commit()
}
```

Implement `ResetFeedingToReceived` — returns the IDs of reset rows so the caller can delete orphaned tasks:

```go
// ResetFeedingToReceived resets all messages stuck in 'feeding' back to 'received'.
// Returns the IDs of affected rows so the caller can delete orphaned pending tasks.
// Called at startup before TaskScheduler.RecoverRunning.
func (s *MessageStore) ResetFeedingToReceived(ctx context.Context) ([]string, error) {
    rows, err := s.db.QueryContext(ctx,
        `SELECT id FROM platform_messages WHERE status = 'feeding'`)
    if err != nil {
        return nil, fmt.Errorf("select feeding: %w", err)
    }
    var ids []string
    for rows.Next() {
        var id string
        if err := rows.Scan(&id); err != nil {
            rows.Close()
            return nil, err
        }
        ids = append(ids, id)
    }
    rows.Close()
    if err := rows.Err(); err != nil {
        return nil, err
    }
    if len(ids) == 0 {
        return nil, nil
    }
    if err := updateStatusBatch(ctx, s.db, ids, "received"); err != nil {
        return nil, fmt.Errorf("reset feeding: %w", err)
    }
    return ids, nil
}
```

> Note: `updateStatusBatch` is an existing helper in `message_store.go` (used by `UpdateStatusBatch`). Use it here — pass `s.db` (not a tx) since this runs at startup outside a transaction.

Write tests for these new MessageStore methods in `internal/store/message_store_test.go` covering:
- `ClaimBatch` claims the right number and sets status to `feeding`
- `ClaimBatch` is idempotent (second call on same messages returns empty — already `feeding`)
- `MarkBeeProcessed` sets status to `bee_processed`
- `ResetFeedingBatch` restores status to `received`
- `ResetFeedingToReceived` returns IDs of reset rows and resets all `feeding` rows

- [ ] **Step 5: Run all tests**

```bash
go test ./internal/bee/ ./internal/store/ -v
```

Expected: all PASS

- [ ] **Step 6: Commit**

```bash
git add internal/bee/ internal/store/message_store.go internal/store/message_store_test.go
git commit -m "feat(bee): add Feeder with batch claim, bee process invocation, rollback"
```

---

## Chunk 7: Wiring & Cleanup

### Task 12: Update main.go

**Files:**
- Modify: `cmd/server/main.go`

- [ ] **Step 1: Rewrite main.go**

Replace the pipeline wiring section:

```go
package main

import (
    "context"
    "fmt"
    "log"
    "os"
    "os/signal"
    "syscall"

    "github.com/robobee/core/internal/api"
    "github.com/robobee/core/internal/bee"
    "github.com/robobee/core/internal/config"
    "github.com/robobee/core/internal/dispatcher"
    "github.com/robobee/core/internal/mcp"
    "github.com/robobee/core/internal/msgingest"
    "github.com/robobee/core/internal/msgsender"
    "github.com/robobee/core/internal/platform"
    "github.com/robobee/core/internal/platform/dingtalk"
    "github.com/robobee/core/internal/platform/feishu"
    "github.com/robobee/core/internal/store"
    "github.com/robobee/core/internal/taskscheduler"
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

    db, err := store.InitDB(cfg.Database.Path)
    if err != nil {
        log.Fatalf("failed to init database: %v", err)
    }
    defer db.Close()

    workerStore := store.NewWorkerStore(db)
    execStore := store.NewExecutionStore(db)
    msgStore := store.NewMessageStore(db)
    taskStore := store.NewTaskStore(db)

    mgr := worker.NewManager(cfg, workerStore, execStore)

    // MCP server (required by bee)
    if cfg.MCP.APIKey == "" {
        log.Fatal("mcp.api_key must be set — bee requires MCP to create tasks")
    }
    mcpSrv := mcp.NewServer(workerStore, mgr, taskStore)

    // Build MCP base URL for bee process
    mcpBaseURL := fmt.Sprintf("http://%s:%d", cfg.Server.Host, cfg.Server.Port)

    // Dispatch channel shared between TaskScheduler and Feeder (for clear commands)
    dispatchCh := make(chan dispatcher.DispatchTask, 128)

    // Startup recovery (synchronous, before goroutines)
    feederCfg := bee.FeederConfig{
        Interval:           cfg.Bee.Feeder.Interval,
        BatchSize:          cfg.Bee.Feeder.BatchSize,
        Timeout:            cfg.Bee.Feeder.Timeout,
        QueueWarnThreshold: cfg.Bee.Feeder.QueueWarnThreshold,
        WorkDir:            cfg.Bee.WorkDir,
        Persona:            cfg.Bee.Persona,
        Binary:             cfg.Runtime.ClaudeCode.Binary,
        MCPBaseURL:         mcpBaseURL,
        MCPAPIKey:          cfg.MCP.APIKey,
    }
    beeProcess := bee.NewBeeProcess(
        cfg.Runtime.ClaudeCode.Binary,
        mcpBaseURL+"/mcp/sse",
        cfg.MCP.APIKey,
    )
    feeder := bee.NewFeeder(msgStore, taskStore, beeProcess, dispatchCh, feederCfg)
    feeder.RecoverFeeding(context.Background())

    sched := taskscheduler.New(taskStore, dispatchCh, cfg.Bee.Feeder.Interval)
    sched.RecoverRunning(context.Background())

    // Pipeline
    ctx, cancel := context.WithCancel(context.Background())
    sendersByPlatform := make(map[string]platform.PlatformSenderAdapter)

    ingest := msgingest.New(msgStore, cfg.MessageQueue.DebounceWindow)
    disp := dispatcher.New(mgr, taskStore, msgStore, dispatchCh)
    sender := msgsender.New(sendersByPlatform, disp.Out())

    go ingest.Run(ctx)
    go feeder.Run(ctx)
    go sched.Run(ctx)
    go disp.Run(ctx)
    go sender.Run(ctx)

    // Register platforms
    if cfg.Feishu.Enabled {
        p := feishu.NewPlatform(cfg.Feishu)
        sendersByPlatform[p.ID()] = p.Sender()
        go p.Receiver().Start(ctx, ingest.Dispatch)
    }
    if cfg.DingTalk.Enabled {
        p := dingtalk.NewPlatform(cfg.DingTalk)
        sendersByPlatform[p.ID()] = p.Sender()
        go p.Receiver().Start(ctx, ingest.Dispatch)
    }

    srv := api.NewServer(workerStore, execStore, mgr, mcpSrv, cfg.MCP.APIKey)

    quit := make(chan os.Signal, 1)
    signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
    go func() {
        <-quit
        log.Println("Shutting down...")
        cancel()
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

- [ ] **Step 2: Build**

```bash
go build ./...
```

Expected: no errors (or only errors related to missing store methods if any remain from earlier tasks — fix those first).

- [ ] **Step 3: Commit**

```bash
git add cmd/server/main.go
git commit -m "feat(main): wire bee feeder, task scheduler, refactored dispatcher"
```

---

### Task 13: Remove msgrouter package

**Files:**
- Delete: `internal/msgrouter/gateway.go`
- Delete: `internal/msgrouter/gateway_test.go`

- [ ] **Step 1: Verify nothing imports msgrouter**

```bash
grep -r "msgrouter" --include="*.go" .
```

Expected: no matches (all references removed in previous tasks).

- [ ] **Step 2: Delete the package**

```bash
rm internal/msgrouter/gateway.go internal/msgrouter/gateway_test.go
rmdir internal/msgrouter 2>/dev/null || true
```

- [ ] **Step 3: Build and test**

```bash
go build ./...
go test ./...
```

Expected: all PASS, no references to msgrouter.

- [ ] **Step 4: Commit**

```bash
git commit -m "chore: remove msgrouter package (replaced by bee feeder + task scheduler)"
```

---

## Final Verification

- [ ] **Run full test suite**

```bash
go test ./... -v 2>&1 | tail -30
```

Expected: all PASS, zero FAIL.

- [ ] **Build the binary**

```bash
go build -o /tmp/robobee-core ./cmd/server/
```

Expected: successful build.

- [ ] **Commit if any stray fixes were needed**

```bash
git add -A && git status
# commit any remaining changes
```
