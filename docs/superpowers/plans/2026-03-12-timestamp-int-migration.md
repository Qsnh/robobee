# Timestamp-to-Integer Migration Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace all six `DATETIME` timestamp columns with `INTEGER` (Unix milliseconds) across the DB schema, Go models, Go stores, tests, and frontend.

**Architecture:** Edit migrations 1–3 in place (pre-launch, no data migration needed), update Go struct fields from `time.Time`/`*time.Time` to `int64`/`*int64`, update store write paths to use `time.Now().UnixMilli()`, rewrite the four millisecond-precision tests to assert on `int64 > 0`, and fix the two frontend sort comparisons that used `localeCompare`.

**Tech Stack:** Go 1.23, SQLite via `github.com/mattn/go-sqlite3`, TypeScript/React frontend

**Spec:** `docs/superpowers/specs/2026-03-11-timestamp-int-migration-design.md`

---

## Chunk 1: Go Backend

### Task 1: Rewrite precision tests to expect int64 (they will fail — that's the point)

**Files:**
- Modify: `internal/store/execution_store_test.go:64-121`
- Modify: `internal/store/message_store_test.go:299-337`

- [ ] **Step 1: Rewrite `TestExecutionStore_Create_StartedAtMillisecondPrecision`**

Replace the body of this test (lines 64–94 of `execution_store_test.go`). The old version scanned into `string` and checked ISO format. The new version scans into `int64` and checks it is positive. Also update the struct field assertion from `*time.Time != nil` to `*int64 != nil`.

```go
func TestExecutionStore_Create_StartedAtMillisecondPrecision(t *testing.T) {
	db, err := InitDB(t.TempDir() + "/test.db")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	ws := NewWorkerStore(db)
	es := NewExecutionStore(db)

	w, _ := ws.Create(model.Worker{Name: "Bot", WorkDir: "/tmp/bot"})
	exec, err := es.Create(w.ID, "test")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	var startedAt int64
	err = db.QueryRow(`SELECT started_at FROM worker_executions WHERE id = ?`, exec.ID).Scan(&startedAt)
	if err != nil {
		t.Fatalf("scan started_at: %v", err)
	}
	if startedAt <= 0 {
		t.Errorf("started_at %d: want positive Unix millisecond timestamp", startedAt)
	}

	if exec.StartedAt == nil {
		t.Error("exec.StartedAt must not be nil")
	}
}
```

- [ ] **Step 2: Rewrite `TestExecutionStore_UpdateResult_CompletedAtMillisecondPrecision`**

Replace the body of this test (lines 96–121 of `execution_store_test.go`).

```go
func TestExecutionStore_UpdateResult_CompletedAtMillisecondPrecision(t *testing.T) {
	db, err := InitDB(t.TempDir() + "/test.db")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	ws := NewWorkerStore(db)
	es := NewExecutionStore(db)

	w, _ := ws.Create(model.Worker{Name: "Bot", WorkDir: "/tmp/bot"})
	exec, _ := es.Create(w.ID, "test")

	if err := es.UpdateResult(exec.ID, "output", model.ExecStatusCompleted); err != nil {
		t.Fatalf("UpdateResult: %v", err)
	}

	var completedAt int64
	err = db.QueryRow(`SELECT completed_at FROM worker_executions WHERE id = ?`, exec.ID).Scan(&completedAt)
	if err != nil {
		t.Fatalf("scan completed_at: %v", err)
	}
	if completedAt <= 0 {
		t.Errorf("completed_at %d: want positive Unix millisecond timestamp", completedAt)
	}
}
```

- [ ] **Step 3: Rewrite `TestMessageStore_MarkTerminal_ProcessedAtMillisecondPrecision`**

Replace the body of this test (lines 299–318 of `message_store_test.go`).

```go
func TestMessageStore_MarkTerminal_ProcessedAtMillisecondPrecision(t *testing.T) {
	s := setupMessageStore(t)
	ctx := context.Background()

	s.Create(ctx, "msg-1", "feishu:chat1:userA", "feishu", "hello", "", "") //nolint
	if err := s.MarkTerminal(ctx, []string{"msg-1"}, "done"); err != nil {
		t.Fatalf("MarkTerminal: %v", err)
	}

	var processedAt int64
	err := s.db.QueryRowContext(ctx,
		`SELECT processed_at FROM platform_messages WHERE id = ?`, "msg-1",
	).Scan(&processedAt)
	if err != nil {
		t.Fatalf("scan processed_at: %v", err)
	}
	if processedAt <= 0 {
		t.Errorf("processed_at %d: want positive Unix millisecond timestamp", processedAt)
	}
}
```

- [ ] **Step 4: Rewrite `TestMessageStore_Create_ReceivedAtMillisecondPrecision`**

Replace the body of this test (lines 320–337 of `message_store_test.go`).

```go
func TestMessageStore_Create_ReceivedAtMillisecondPrecision(t *testing.T) {
	s := setupMessageStore(t)
	ctx := context.Background()

	s.Create(ctx, "msg-ms", "feishu:chat1:userA", "feishu", "hello", "", "") //nolint

	var receivedAt int64
	err := s.db.QueryRowContext(ctx,
		`SELECT received_at FROM platform_messages WHERE id = ?`, "msg-ms",
	).Scan(&receivedAt)
	if err != nil {
		t.Fatalf("scan received_at: %v", err)
	}
	if receivedAt <= 0 {
		t.Errorf("received_at %d: want positive Unix millisecond timestamp", receivedAt)
	}
}
```

- [ ] **Step 5: Run the four rewritten tests to confirm they FAIL**

```bash
cd /path/to/core
go test ./internal/store/... -run "TestExecutionStore_Create_StartedAtMillisecondPrecision|TestExecutionStore_UpdateResult_CompletedAtMillisecondPrecision|TestMessageStore_Create_ReceivedAtMillisecondPrecision|TestMessageStore_MarkTerminal_ProcessedAtMillisecondPrecision" -v
```

Expected: all four tests FAIL (scanning a string into int64 will error).

---

### Task 2: Update DB schema migrations

**Files:**
- Modify: `internal/store/db.go:20-69`

- [ ] **Step 1: Update migration 1 — `workers` table**

In `db.go`, change the `workers` CREATE TABLE statement. Replace:
```sql
created_at DATETIME NOT NULL DEFAULT (datetime('now')),
updated_at DATETIME NOT NULL DEFAULT (datetime('now'))
```
With:
```sql
created_at INTEGER NOT NULL,
updated_at INTEGER NOT NULL
```

- [ ] **Step 2: Update migration 2 — `worker_executions` table**

Replace:
```sql
started_at   DATETIME DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
completed_at DATETIME DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
```
With:
```sql
started_at   INTEGER,
completed_at INTEGER,
```

- [ ] **Step 3: Update migration 3 — `platform_messages` table**

Replace:
```sql
received_at     DATETIME NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
processed_at    DATETIME
```
With:
```sql
received_at     INTEGER NOT NULL,
processed_at    INTEGER
```

---

### Task 3: Update Go models

**Files:**
- Modify: `internal/model/worker.go`
- Modify: `internal/model/execution.go`

- [ ] **Step 1: Update `internal/model/worker.go`**

Replace the file content:

```go
package model

type WorkerStatus string

const (
	WorkerStatusIdle    WorkerStatus = "idle"
	WorkerStatusWorking WorkerStatus = "working"
	WorkerStatusError   WorkerStatus = "error"
)

type Worker struct {
	ID                  string       `json:"id" db:"id"`
	Name                string       `json:"name" db:"name"`
	Description         string       `json:"description" db:"description"`
	Prompt              string       `json:"prompt" db:"prompt"`
	WorkDir             string       `json:"work_dir" db:"work_dir"`
	CronExpression      string       `json:"cron_expression,omitempty" db:"cron_expression"`
	ScheduleDescription string       `json:"schedule_description,omitempty" db:"schedule_description"`
	ScheduleEnabled     bool         `json:"schedule_enabled" db:"schedule_enabled"`
	Status              WorkerStatus `json:"status" db:"status"`
	CreatedAt           int64        `json:"created_at" db:"created_at"`
	UpdatedAt           int64        `json:"updated_at" db:"updated_at"`
}
```

(Removes the `"time"` import since `int64` needs no import.)

- [ ] **Step 2: Update `internal/model/execution.go`**

Replace the file content:

```go
package model

type ExecutionStatus string

const (
	ExecStatusPending   ExecutionStatus = "pending"
	ExecStatusRunning   ExecutionStatus = "running"
	ExecStatusCompleted ExecutionStatus = "completed"
	ExecStatusFailed    ExecutionStatus = "failed"
)

type WorkerExecution struct {
	ID           string          `json:"id" db:"id"`
	WorkerID     string          `json:"worker_id" db:"worker_id"`
	WorkerName   string          `json:"worker_name,omitempty" db:"-"`
	SessionID    string          `json:"session_id" db:"session_id"`
	TriggerInput string          `json:"trigger_input,omitempty" db:"trigger_input"`
	Status       ExecutionStatus `json:"status" db:"status"`
	Result       string          `json:"result,omitempty" db:"result"`
	Logs         string          `json:"logs,omitempty" db:"logs"`
	AIProcessPID int             `json:"ai_process_pid,omitempty" db:"ai_process_pid"`
	StartedAt    *int64          `json:"started_at,omitempty" db:"started_at"`
	CompletedAt  *int64          `json:"completed_at,omitempty" db:"completed_at"`
}
```

(Removes the `"time"` import.)

- [ ] **Step 3: Check for compile errors**

```bash
go build ./internal/model/...
```

Expected: compiles cleanly. If there are errors in files that import `model`, that is expected — they will be fixed in the next tasks.

---

### Task 4: Update `worker_store.go`

**Files:**
- Modify: `internal/store/worker_store.go`

- [ ] **Step 1: Update `Create` method**

In `worker_store.go`, replace:
```go
w.CreatedAt = time.Now().UTC()
w.UpdatedAt = w.CreatedAt
```
With:
```go
w.CreatedAt = time.Now().UnixMilli()
w.UpdatedAt = w.CreatedAt
```

- [ ] **Step 2: Update `Update` method**

Replace:
```go
w.UpdatedAt = time.Now().UTC()
```
With:
```go
w.UpdatedAt = time.Now().UnixMilli()
```

- [ ] **Step 3: Update `UpdateStatus` method**

Replace:
```go
_, err := s.db.Exec(`UPDATE workers SET status=?, updated_at=? WHERE id=?`, status, time.Now().UTC(), id)
```
With:
```go
_, err := s.db.Exec(`UPDATE workers SET status=?, updated_at=? WHERE id=?`, status, time.Now().UnixMilli(), id)
```

- [ ] **Step 4: Verify `"time"` import is still present** (needed for `time.Now().UnixMilli()`)

- [ ] **Step 5: Build to check**

```bash
go build ./internal/store/...
```

Expected: may still have errors from `execution_store.go` and `message_store.go` — that is fine, those are fixed next.

---

### Task 5: Update `execution_store.go`

**Files:**
- Modify: `internal/store/execution_store.go`

- [ ] **Step 1: Update the `create` function**

Replace this block in `create`:
```go
now := time.Now().UTC()
startedAtStr := now.Format("2006-01-02T15:04:05.000Z")
exec := model.WorkerExecution{
    ID:           uuid.New().String(),
    WorkerID:     workerID,
    SessionID:    sessionID,
    TriggerInput: triggerInput,
    Status:       model.ExecStatusPending,
    StartedAt:    &now,
}
_, err := s.db.Exec(
    `INSERT INTO worker_executions (id, worker_id, session_id, trigger_input, status, result, ai_process_pid, started_at)
     VALUES (?, ?, ?, ?, ?, '', 0, ?)`,
    exec.ID, exec.WorkerID, exec.SessionID, exec.TriggerInput, exec.Status, startedAtStr,
)
```
With:
```go
millis := time.Now().UnixMilli()
exec := model.WorkerExecution{
    ID:           uuid.New().String(),
    WorkerID:     workerID,
    SessionID:    sessionID,
    TriggerInput: triggerInput,
    Status:       model.ExecStatusPending,
    StartedAt:    &millis,
}
_, err := s.db.Exec(
    `INSERT INTO worker_executions (id, worker_id, session_id, trigger_input, status, result, ai_process_pid, started_at)
     VALUES (?, ?, ?, ?, ?, '', 0, ?)`,
    exec.ID, exec.WorkerID, exec.SessionID, exec.TriggerInput, exec.Status, millis,
)
```

- [ ] **Step 2: Update `UpdateResult` method**

Replace:
```go
completedAt := time.Now().UTC().Format("2006-01-02T15:04:05.000Z")
_, err := s.db.Exec(`UPDATE worker_executions SET result=?, status=?, completed_at=? WHERE id=?`, result, status, completedAt, id)
```
With:
```go
_, err := s.db.Exec(`UPDATE worker_executions SET result=?, status=?, completed_at=? WHERE id=?`, result, status, time.Now().UnixMilli(), id)
```

- [ ] **Step 3: Verify `"time"` import is still present**

---

### Task 6: Update `message_store.go`

**Files:**
- Modify: `internal/store/message_store.go`

- [ ] **Step 1: Update `Create` method**

Replace:
```go
time.Now().UTC().Format("2006-01-02T15:04:05.000Z"),
```
With:
```go
time.Now().UnixMilli(),
```
(This is the last argument in the `INSERT OR IGNORE` call.)

- [ ] **Step 2: Update `MarkTerminal` method**

Replace:
```go
now := time.Now().UTC().Format("2006-01-02T15:04:05.000Z")
args := make([]any, 0, len(ids)+2)
args = append(args, status, now)
```
With:
```go
args := make([]any, 0, len(ids)+2)
args = append(args, status, time.Now().UnixMilli())
```

- [ ] **Step 3: Update `InsertClearSentinel` method**

Replace:
```go
_, err := s.db.ExecContext(ctx,
    `INSERT INTO platform_messages (id, session_key, platform, content, status)
     VALUES (?, ?, ?, '', 'clear')`,
    id, sessionKey, plt)
```
With:
```go
_, err := s.db.ExecContext(ctx,
    `INSERT INTO platform_messages (id, session_key, platform, content, status, received_at)
     VALUES (?, ?, ?, '', 'clear', ?)`,
    id, sessionKey, plt, time.Now().UnixMilli())
```

- [ ] **Step 4: Verify `"time"` import is still present**

---

### Task 7: Run all Go tests

- [ ] **Step 1: Build to verify no compile errors**

```bash
go build ./...
```

Expected: clean build with no errors.

- [ ] **Step 2: Run all store tests**

```bash
go test ./internal/store/... -v
```

Expected: all tests PASS, including the four rewritten precision tests.

- [ ] **Step 3: Run the full test suite**

```bash
go test ./...
```

Expected: all tests PASS.

- [ ] **Step 4: Commit**

```bash
git add internal/store/db.go \
        internal/model/worker.go \
        internal/model/execution.go \
        internal/store/worker_store.go \
        internal/store/execution_store.go \
        internal/store/message_store.go \
        internal/store/execution_store_test.go \
        internal/store/message_store_test.go
git commit -m "feat: store timestamps as Unix milliseconds (int64)"
```

---

## Chunk 2: Frontend + Local DB Cleanup

### Task 8: Update TypeScript types

**Files:**
- Modify: `web/src/lib/types.ts`

- [ ] **Step 1: Update `Worker` interface**

In `web/src/lib/types.ts`, replace:
```typescript
  created_at: string
  updated_at: string
```
With:
```typescript
  created_at: number
  updated_at: number
```

- [ ] **Step 2: Update `WorkerExecution` interface**

Replace:
```typescript
  started_at: string | null
  completed_at: string | null
```
With:
```typescript
  started_at: number | null
  completed_at: number | null
```

---

### Task 9: Fix sort comparisons in frontend pages

**Files:**
- Modify: `web/src/pages/executions.tsx:34-38`
- Modify: `web/src/pages/worker-detail.tsx:48-52`

- [ ] **Step 1: Fix sort in `executions.tsx`**

Replace:
```typescript
    return Array.from(map.values()).sort((a, b) => {
      const aTime = a[0].started_at ?? ""
      const bTime = b[0].started_at ?? ""
      return bTime.localeCompare(aTime)
    })
```
With:
```typescript
    return Array.from(map.values()).sort((a, b) => {
      return (b[0].started_at ?? 0) - (a[0].started_at ?? 0)
    })
```

- [ ] **Step 2: Fix sort in `worker-detail.tsx`**

Replace:
```typescript
    return Array.from(map.values()).sort((a, b) => {
      const aTime = a[0].started_at ?? ""
      const bTime = b[0].started_at ?? ""
      return bTime.localeCompare(aTime)
    })
```
With:
```typescript
    return Array.from(map.values()).sort((a, b) => {
      return (b[0].started_at ?? 0) - (a[0].started_at ?? 0)
    })
```

- [ ] **Step 3: TypeScript type-check** (run from the repo root)

```bash
cd /path/to/core/web && npx tsc --noEmit
```

Expected: no type errors.

- [ ] **Step 4: Commit frontend changes**

```bash
git add web/src/lib/types.ts \
        web/src/pages/executions.tsx \
        web/src/pages/worker-detail.tsx
git commit -m "feat: update frontend types and sorts for int timestamp fields"
```

---

### Task 10: Delete local DB and verify app starts clean

**Files:**
- Delete: `data/robobee.db`, `data/robobee.db-shm`, `data/robobee.db-wal`

- [ ] **Step 1: Delete the local database files**

`data/robobee.db` is listed in `.gitignore`, so deleting it will not affect git state.

```bash
rm -f data/robobee.db data/robobee.db-shm data/robobee.db-wal
```

- [ ] **Step 2: Start the app and verify it initializes without errors**

```bash
go run ./cmd/...
```

Expected: app starts, runs all migrations (versions 1–9), no errors logged.
