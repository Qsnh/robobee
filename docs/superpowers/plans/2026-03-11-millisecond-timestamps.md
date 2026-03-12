# Millisecond Timestamp Precision Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Store all four timestamp columns (`received_at`, `processed_at`, `started_at`, `completed_at`) with millisecond precision to make sub-second message ordering deterministic.

**Architecture:** Go code explicitly formats timestamps as `"2006-01-02T15:04:05.000Z"` before binding to SQL, bypassing SQLite's second-precision `datetime('now')`. Schema `DEFAULT` expressions are updated as defensive fallbacks. No model struct or API changes required.

**Tech Stack:** Go, SQLite via `mattn/go-sqlite3`, `database/sql`

---

## Chunk 1: Schema defaults + message_store timestamps

### Task 1: Schema defaults in `db.go`

**Files:**
- Modify: `internal/store/db.go:71` (received_at DEFAULT)
- Modify: `internal/store/db.go:55-56` (started_at / completed_at DEFAULTs)

- [ ] **Step 1: Update `CREATE TABLE` defaults**

  In `internal/store/db.go`, change the three `datetime('now')` defaults that belong to our target columns:

  `platform_messages.received_at` (line 71):
  ```sql
  received_at     DATETIME NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
  ```

  `worker_executions.started_at` and `completed_at` (lines 55-56):
  ```sql
  started_at   DATETIME DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
  completed_at DATETIME DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
  ```

  Leave `workers.created_at` and `workers.updated_at` unchanged — they are out of scope.

- [ ] **Step 2: Verify existing tests still pass**

  ```bash
  go test ./internal/store/... -v
  ```
  Expected: all existing tests PASS.

- [ ] **Step 3: Commit**

  ```bash
  git add internal/store/db.go
  git commit -m "feat: update schema defaults to millisecond-precision strftime"
  ```

---

### Task 2: `message_store.go` — `received_at` millisecond precision

**Files:**
- Modify: `internal/store/message_store.go:27-31` (Create INSERT)
- Test: `internal/store/message_store_test.go`

- [ ] **Step 1: Write the failing test**

  Add to `internal/store/message_store_test.go`:

  ```go
  func TestMessageStore_Create_ReceivedAtMillisecondPrecision(t *testing.T) {
      s := setupMessageStore(t)
      ctx := context.Background()

      s.Create(ctx, "msg-ms", "feishu:chat1:userA", "feishu", "hello", "", "") //nolint

      var receivedAt string
      err := s.db.QueryRowContext(ctx,
          `SELECT received_at FROM platform_messages WHERE id = ?`, "msg-ms",
      ).Scan(&receivedAt)
      if err != nil {
          t.Fatalf("scan received_at: %v", err)
      }
      // must match 2026-03-11T10:30:00.123Z (millisecond suffix + Z)
      if len(receivedAt) < 24 || receivedAt[19] != '.' || receivedAt[len(receivedAt)-1] != 'Z' {
          t.Errorf("received_at %q: want millisecond format like 2026-03-11T10:30:00.123Z", receivedAt)
      }
  }
  ```

- [ ] **Step 2: Run test to verify it fails**

  ```bash
  go test ./internal/store/... -run TestMessageStore_Create_ReceivedAtMillisecondPrecision -v
  ```
  Expected: FAIL — `received_at` has no `.` (second-precision output from `datetime('now')`).

- [ ] **Step 3: Update `Create()` to pass `received_at` explicitly**

  In `internal/store/message_store.go`, change `Create()`:

  ```go
  func (s *MessageStore) Create(ctx context.Context, id, sessionKey, platform, content, raw, platformMsgID string) (bool, error) {
      result, err := s.db.ExecContext(ctx,
          `INSERT OR IGNORE INTO platform_messages (id, session_key, platform, content, raw, platform_msg_id, received_at)
           VALUES (?, ?, ?, ?, ?, ?, ?)`,
          id, sessionKey, platform, content, raw, platformMsgID,
          time.Now().UTC().Format("2006-01-02T15:04:05.000Z"),
      )
      if err != nil {
          return false, err
      }
      n, err := result.RowsAffected()
      if err != nil {
          return false, err
      }
      return n == 1, nil
  }
  ```

  Add `"time"` to the import block if not present (it should already be unused — check; if not present, add it).

- [ ] **Step 4: Run test to verify it passes**

  ```bash
  go test ./internal/store/... -run TestMessageStore_Create_ReceivedAtMillisecondPrecision -v
  ```
  Expected: PASS.

- [ ] **Step 5: Run full store tests**

  ```bash
  go test ./internal/store/... -v
  ```
  Expected: all PASS.

- [ ] **Step 6: Commit**

  ```bash
  git add internal/store/message_store.go internal/store/message_store_test.go
  git commit -m "feat: store received_at with millisecond precision in Create()"
  ```

---

### Task 3: `message_store.go` — `processed_at` millisecond precision

**Files:**
- Modify: `internal/store/message_store.go:98-114` (MarkTerminal)
- Test: `internal/store/message_store_test.go`

- [ ] **Step 1: Write the failing test**

  Add to `internal/store/message_store_test.go`:

  ```go
  func TestMessageStore_MarkTerminal_ProcessedAtMillisecondPrecision(t *testing.T) {
      s := setupMessageStore(t)
      ctx := context.Background()

      s.Create(ctx, "msg-1", "feishu:chat1:userA", "feishu", "hello", "", "") //nolint
      if err := s.MarkTerminal(ctx, []string{"msg-1"}, "done"); err != nil {
          t.Fatalf("MarkTerminal: %v", err)
      }

      var processedAt string
      err := s.db.QueryRowContext(ctx,
          `SELECT processed_at FROM platform_messages WHERE id = ?`, "msg-1",
      ).Scan(&processedAt)
      if err != nil {
          t.Fatalf("scan processed_at: %v", err)
      }
      if len(processedAt) < 24 || processedAt[19] != '.' || processedAt[len(processedAt)-1] != 'Z' {
          t.Errorf("processed_at %q: want millisecond format like 2026-03-11T10:30:00.123Z", processedAt)
      }
  }
  ```

- [ ] **Step 2: Run test to verify it fails**

  ```bash
  go test ./internal/store/... -run TestMessageStore_MarkTerminal_ProcessedAtMillisecondPrecision -v
  ```
  Expected: FAIL — `processed_at` has second-precision from `datetime('now')`.

- [ ] **Step 3: Update `MarkTerminal()` to bind `processed_at` from Go**

  In `internal/store/message_store.go`, change `MarkTerminal()`:

  ```go
  func (s *MessageStore) MarkTerminal(ctx context.Context, ids []string, status string) error {
      if len(ids) == 0 {
          return nil
      }
      placeholders := strings.Repeat("?,", len(ids))
      placeholders = placeholders[:len(placeholders)-1]
      now := time.Now().UTC().Format("2006-01-02T15:04:05.000Z")
      args := make([]any, 0, len(ids)+2)
      args = append(args, status, now) // status=?, processed_at=?
      for _, id := range ids {
          args = append(args, id)
      }
      _, err := s.db.ExecContext(ctx,
          fmt.Sprintf(`UPDATE platform_messages SET status = ?, processed_at = ? WHERE id IN (%s)`, placeholders),
          args...,
      )
      return err
  }
  ```

  Add `"time"` to the import block if not already present.

- [ ] **Step 4: Run test to verify it passes**

  ```bash
  go test ./internal/store/... -run TestMessageStore_MarkTerminal_ProcessedAtMillisecondPrecision -v
  ```
  Expected: PASS.

- [ ] **Step 5: Run full store tests**

  ```bash
  go test ./internal/store/... -v
  ```
  Expected: all PASS.

- [ ] **Step 6: Commit**

  ```bash
  git add internal/store/message_store.go internal/store/message_store_test.go
  git commit -m "feat: store processed_at with millisecond precision in MarkTerminal()"
  ```

---

## Chunk 2: execution_store timestamps

### Task 4: `execution_store.go` — `started_at` millisecond precision

**Files:**
- Modify: `internal/store/execution_store.go:20-39` (create)
- Test: `internal/store/execution_store_test.go`

- [ ] **Step 1: Write the failing test**

  Add to `internal/store/execution_store_test.go`:

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

      // Verify the raw DB value has millisecond precision
      var startedAt string
      err = db.QueryRow(`SELECT started_at FROM worker_executions WHERE id = ?`, exec.ID).Scan(&startedAt)
      if err != nil {
          t.Fatalf("scan started_at: %v", err)
      }
      if len(startedAt) < 24 || startedAt[19] != '.' || startedAt[len(startedAt)-1] != 'Z' {
          t.Errorf("started_at %q: want millisecond format like 2026-03-11T10:30:00.123Z", startedAt)
      }

      // Verify the returned struct still has a valid *time.Time
      if exec.StartedAt == nil {
          t.Error("exec.StartedAt must not be nil")
      }
  }
  ```

- [ ] **Step 2: Run test to verify it fails**

  ```bash
  go test ./internal/store/... -run TestExecutionStore_Create_StartedAtMillisecondPrecision -v
  ```
  Expected: FAIL — `started_at` stored as `time.Time` by the driver without millisecond-format guarantee.

- [ ] **Step 3: Update `create()` to pass a formatted string to the INSERT**

  In `internal/store/execution_store.go`, change `create()`:

  ```go
  func (s *ExecutionStore) create(workerID, triggerInput, sessionID string) (model.WorkerExecution, error) {
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
      if err != nil {
          return model.WorkerExecution{}, fmt.Errorf("insert execution: %w", err)
      }
      return exec, nil
  }
  ```

- [ ] **Step 4: Run test to verify it passes**

  ```bash
  go test ./internal/store/... -run TestExecutionStore_Create_StartedAtMillisecondPrecision -v
  ```
  Expected: PASS.

- [ ] **Step 5: Run full store tests**

  ```bash
  go test ./internal/store/... -v
  ```
  Expected: all PASS.

- [ ] **Step 6: Commit**

  ```bash
  git add internal/store/execution_store.go internal/store/execution_store_test.go
  git commit -m "feat: store started_at with millisecond precision in create()"
  ```

---

### Task 5: `execution_store.go` — `completed_at` millisecond precision

**Files:**
- Modify: `internal/store/execution_store.go:143-147` (UpdateResult)
- Test: `internal/store/execution_store_test.go`

- [ ] **Step 1: Write the failing test**

  Add to `internal/store/execution_store_test.go`:

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

      var completedAt string
      err = db.QueryRow(`SELECT completed_at FROM worker_executions WHERE id = ?`, exec.ID).Scan(&completedAt)
      if err != nil {
          t.Fatalf("scan completed_at: %v", err)
      }
      if len(completedAt) < 24 || completedAt[19] != '.' || completedAt[len(completedAt)-1] != 'Z' {
          t.Errorf("completed_at %q: want millisecond format like 2026-03-11T10:30:00.123Z", completedAt)
      }
  }
  ```


- [ ] **Step 2: Run test to verify it fails**

  ```bash
  go test ./internal/store/... -run TestExecutionStore_UpdateResult_CompletedAtMillisecondPrecision -v
  ```
  Expected: FAIL.

- [ ] **Step 3: Update `UpdateResult()` to pass a formatted string**

  In `internal/store/execution_store.go`, change `UpdateResult()`:

  ```go
  func (s *ExecutionStore) UpdateResult(id string, result string, status model.ExecutionStatus) error {
      completedAt := time.Now().UTC().Format("2006-01-02T15:04:05.000Z")
      _, err := s.db.Exec(
          `UPDATE worker_executions SET result=?, status=?, completed_at=? WHERE id=?`,
          result, status, completedAt, id,
      )
      return err
  }
  ```

- [ ] **Step 4: Run test to verify it passes**

  ```bash
  go test ./internal/store/... -run TestExecutionStore_UpdateResult_CompletedAtMillisecondPrecision -v
  ```
  Expected: PASS.

- [ ] **Step 5: Run all store tests**

  ```bash
  go test ./internal/store/... -v
  ```
  Expected: all PASS.

- [ ] **Step 6: Run full project tests**

  ```bash
  go test ./... 2>&1
  ```
  Expected: all PASS (no regressions anywhere).

- [ ] **Step 7: Commit**

  ```bash
  git add internal/store/execution_store.go internal/store/execution_store_test.go
  git commit -m "feat: store completed_at with millisecond precision in UpdateResult()"
  ```
