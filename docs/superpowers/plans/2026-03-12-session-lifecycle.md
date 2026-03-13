# Session Lifecycle Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Introduce a `session_contexts` table to track per-agent (bee + workers) Claude session IDs per sessionKey, enabling session continuity and proper `/clear` reset.

**Architecture:** A new `SessionStore` wraps the `session_contexts` table. Feeder is refactored to process each `sessionKey` group independently with session resume. Dispatcher replaces `GetSession`+`ReplyExecution` with `GetSessionContext`+`ExecuteWorkerWithSession`.

**Tech Stack:** Go, SQLite (via `database/sql` + `go-sqlite3`), `github.com/google/uuid`

**Spec:** `docs/superpowers/specs/2026-03-12-session-lifecycle-design.md`

**Pre-existing test failure:** `TestFeeder_RollsBack_OnBeeFailure` already fails due to a timing race unrelated to this feature. It will be fixed when we rewrite the feeder tests in Chunk 2.

---

## Chunk 1: session_contexts Store Layer

### Task 1: Add migration for session_contexts table

**Files:**
- Modify: `internal/store/db.go`

- [ ] **Step 1: Add migration version 12**

In `internal/store/db.go`, append to the `migrations` slice after version 11:

```go
{
    version: 12,
    name:    "20260312_create_table_session_contexts",
    sql: `CREATE TABLE IF NOT EXISTS session_contexts (
        session_key  TEXT    NOT NULL,
        agent_id     TEXT    NOT NULL,
        session_id   TEXT    NOT NULL,
        updated_at   INTEGER NOT NULL,
        PRIMARY KEY (session_key, agent_id)
    )`,
},
```

- [ ] **Step 2: Verify migration runs**

```bash
go build ./internal/store/...
```

Expected: no compile errors.

- [ ] **Step 3: Commit**

```bash
git add internal/store/db.go
git commit -m "feat(store): add session_contexts migration (version 12)"
```

---

### Task 2: Implement SessionStore

**Files:**
- Create: `internal/store/session_store.go`
- Create: `internal/store/session_store_test.go`

- [ ] **Step 1: Write the failing test**

Create `internal/store/session_store_test.go`:

```go
package store_test

import (
    "context"
    "database/sql"
    "testing"

    "github.com/robobee/core/internal/store"
)

func setupSessionDB(t *testing.T) (*sql.DB, *store.SessionStore) {
    t.Helper()
    db, err := store.InitDB(t.TempDir() + "/test.db")
    if err != nil {
        t.Fatalf("InitDB: %v", err)
    }
    t.Cleanup(func() { db.Close() })
    return db, store.NewSessionStore(db)
}

func TestSessionStore_GetSessionContext_MissReturnsEmpty(t *testing.T) {
    _, ss := setupSessionDB(t)
    got, err := ss.GetSessionContext(context.Background(), "feishu:c:u", store.BeeAgentID)
    if err != nil {
        t.Fatalf("unexpected error: %v", err)
    }
    if got != "" {
        t.Errorf("expected empty string on miss, got %q", got)
    }
}

func TestSessionStore_UpsertAndGet(t *testing.T) {
    _, ss := setupSessionDB(t)
    ctx := context.Background()

    if err := ss.UpsertSessionContext(ctx, "feishu:c:u", store.BeeAgentID, "sess-abc"); err != nil {
        t.Fatalf("upsert: %v", err)
    }
    got, err := ss.GetSessionContext(ctx, "feishu:c:u", store.BeeAgentID)
    if err != nil {
        t.Fatalf("get: %v", err)
    }
    if got != "sess-abc" {
        t.Errorf("expected sess-abc, got %q", got)
    }
}

func TestSessionStore_Upsert_Overwrites(t *testing.T) {
    _, ss := setupSessionDB(t)
    ctx := context.Background()

    ss.UpsertSessionContext(ctx, "k", store.BeeAgentID, "old") //nolint:errcheck
    ss.UpsertSessionContext(ctx, "k", store.BeeAgentID, "new") //nolint:errcheck

    got, _ := ss.GetSessionContext(ctx, "k", store.BeeAgentID)
    if got != "new" {
        t.Errorf("expected new, got %q", got)
    }
}

func TestSessionStore_AgentsAreIsolated(t *testing.T) {
    _, ss := setupSessionDB(t)
    ctx := context.Background()

    ss.UpsertSessionContext(ctx, "k", store.BeeAgentID, "bee-sess")   //nolint:errcheck
    ss.UpsertSessionContext(ctx, "k", "worker-1", "worker-sess")       //nolint:errcheck

    beeSess, _ := ss.GetSessionContext(ctx, "k", store.BeeAgentID)
    workerSess, _ := ss.GetSessionContext(ctx, "k", "worker-1")
    if beeSess != "bee-sess" {
        t.Errorf("bee: expected bee-sess, got %q", beeSess)
    }
    if workerSess != "worker-sess" {
        t.Errorf("worker: expected worker-sess, got %q", workerSess)
    }
}

func TestSessionStore_ClearSessionContexts(t *testing.T) {
    _, ss := setupSessionDB(t)
    ctx := context.Background()

    ss.UpsertSessionContext(ctx, "k", store.BeeAgentID, "bee-sess")  //nolint:errcheck
    ss.UpsertSessionContext(ctx, "k", "worker-1", "w1-sess")          //nolint:errcheck
    ss.UpsertSessionContext(ctx, "other", store.BeeAgentID, "other")  //nolint:errcheck

    if err := ss.ClearSessionContexts(ctx, "k"); err != nil {
        t.Fatalf("clear: %v", err)
    }

    beeSess, _ := ss.GetSessionContext(ctx, "k", store.BeeAgentID)
    w1Sess, _ := ss.GetSessionContext(ctx, "k", "worker-1")
    otherSess, _ := ss.GetSessionContext(ctx, "other", store.BeeAgentID)

    if beeSess != "" {
        t.Errorf("expected bee session cleared, got %q", beeSess)
    }
    if w1Sess != "" {
        t.Errorf("expected worker session cleared, got %q", w1Sess)
    }
    if otherSess != "other" {
        t.Errorf("other key must not be cleared, got %q", otherSess)
    }
}
```

- [ ] **Step 2: Run tests to verify they fail**

```bash
go test ./internal/store/... -run TestSessionStore -v
```

Expected: compile error — `store.SessionStore`, `store.NewSessionStore`, `store.BeeAgentID` do not exist yet.

- [ ] **Step 3: Implement session_store.go**

Create `internal/store/session_store.go`:

```go
package store

import (
    "context"
    "database/sql"
    "time"
)

// BeeAgentID is the agent_id value used for bee brain session tracking.
const BeeAgentID = "bee"

// SessionStore persists session context to the session_contexts table.
type SessionStore struct {
    db *sql.DB
}

// NewSessionStore constructs a SessionStore.
func NewSessionStore(db *sql.DB) *SessionStore {
    return &SessionStore{db: db}
}

// UpsertSessionContext writes or overwrites the session_id for (sessionKey, agentID).
func (s *SessionStore) UpsertSessionContext(ctx context.Context, sessionKey, agentID, sessionID string) error {
    _, err := s.db.ExecContext(ctx,
        `INSERT INTO session_contexts (session_key, agent_id, session_id, updated_at)
         VALUES (?, ?, ?, ?)
         ON CONFLICT(session_key, agent_id) DO UPDATE
         SET session_id = excluded.session_id, updated_at = excluded.updated_at`,
        sessionKey, agentID, sessionID, time.Now().UnixMilli(),
    )
    return err
}

// GetSessionContext returns the session_id for (sessionKey, agentID).
// Returns ("", nil) when no row exists — this is normal for the first message,
// not a database error. Returns non-nil error only on database failure.
func (s *SessionStore) GetSessionContext(ctx context.Context, sessionKey, agentID string) (string, error) {
    var sessionID string
    err := s.db.QueryRowContext(ctx,
        `SELECT session_id FROM session_contexts WHERE session_key = ? AND agent_id = ?`,
        sessionKey, agentID,
    ).Scan(&sessionID)
    if err == sql.ErrNoRows {
        return "", nil
    }
    return sessionID, err
}

// ClearSessionContexts deletes all session_contexts rows for sessionKey,
// resetting session state for bee and all workers under that key.
func (s *SessionStore) ClearSessionContexts(ctx context.Context, sessionKey string) error {
    _, err := s.db.ExecContext(ctx,
        `DELETE FROM session_contexts WHERE session_key = ?`,
        sessionKey,
    )
    return err
}
```

- [ ] **Step 4: Run tests to verify they pass**

```bash
go test ./internal/store/... -run TestSessionStore -v
```

Expected: all 5 tests PASS.

- [ ] **Step 5: Run full store tests**

```bash
go test ./internal/store/... -v
```

Expected: all store tests pass (no regressions).

- [ ] **Step 6: Commit**

```bash
git add internal/store/session_store.go internal/store/session_store_test.go
git commit -m "feat(store): add SessionStore for session_contexts table"
```

---

## Chunk 2: Feeder Refactor

### Task 3: Update BeeRunner interface and BeeProcess.Run

**Files:**
- Modify: `internal/bee/bee_process.go`
- Modify: `internal/bee/feeder.go` (interface only)

- [ ] **Step 1: Update BeeRunner interface in feeder.go**

In `internal/bee/feeder.go`, replace lines 29-31:

```go
// BeeRunner abstracts the bee process invocation (real or test double).
type BeeRunner interface {
    Run(ctx context.Context, workDir, prompt string) error
}
```

with:

```go
// BeeRunner abstracts the bee process invocation (real or test double).
type BeeRunner interface {
    Run(ctx context.Context, workDir, prompt, sessionID string, resume bool) error
}
```

- [ ] **Step 2: Update BeeProcess.Run in bee_process.go**

In `internal/bee/bee_process.go`, replace the `Run` method (lines 36-78) with:

```go
// Run spawns the bee process with the given prompt and waits for it to exit.
// If sessionID is non-empty and resume is true, passes --resume <sessionID>.
// If sessionID is non-empty and resume is false, passes --session-id <sessionID>.
// Returns nil on exit code 0, an error otherwise.
func (p *BeeProcess) Run(ctx context.Context, workDir, prompt, sessionID string, resume bool) error {
    args := []string{
        "--dangerously-skip-permissions",
        "--output-format", "stream-json",
        "--mcp-server", "robobee=" + p.mcpURL,
        "--mcp-api-key", p.apiKey,
        "-p", prompt,
    }
    if sessionID != "" {
        if resume {
            args = append(args, "--resume", sessionID)
        } else {
            args = append(args, "--session-id", sessionID)
        }
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

- [ ] **Step 3: Verify compile**

```bash
go build ./internal/bee/...
```

Expected: compile error — `feeder.go` still calls `runner.Run` with the old 3-argument signature; `feeder_test.go` uses old mock. These will be fixed in the next tasks.

---

### Task 4: Rewrite Feeder.tick() for per-sessionKey fan-out

**Files:**
- Modify: `internal/bee/feeder.go`

The entire body of `tick()` is being structurally rewritten. `Feeder` also gains a `sessionStore` field.

- [ ] **Step 1: Add imports and sessionStore field**

In `internal/bee/feeder.go`, update imports to add `sync` and `uuid`:

```go
import (
    "context"
    "fmt"
    "log"
    "strings"
    "sync"
    "time"

    "github.com/google/uuid"
    "github.com/robobee/core/internal/dispatcher"
    "github.com/robobee/core/internal/platform"
    "github.com/robobee/core/internal/store"
)
```

Add `sessionStore` to `Feeder` struct (replace lines 33-40):

```go
// Feeder polls platform_messages for unprocessed messages and feeds them to bee.
type Feeder struct {
    msgStore     *store.MessageStore
    taskStore    *store.TaskStore
    sessionStore *store.SessionStore
    runner       BeeRunner
    clearCh      chan<- dispatcher.DispatchTask
    cfg          FeederConfig
}
```

Update `NewFeeder` (replace lines 43-51):

```go
// NewFeeder creates a Feeder.
func NewFeeder(ms *store.MessageStore, ts *store.TaskStore, ss *store.SessionStore, runner BeeRunner, clearCh chan<- dispatcher.DispatchTask, cfg FeederConfig) *Feeder {
    return &Feeder{
        msgStore:     ms,
        taskStore:    ts,
        sessionStore: ss,
        runner:       runner,
        clearCh:      clearCh,
        cfg:          cfg,
    }
}
```

- [ ] **Step 2: Rewrite tick() and add processBeeGroup()**

Replace the `tick()` method (lines 85-157) and the `rollback()` method (lines 159-170) with:

```go
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

    // Write CLAUDE.md with persona once, before fan-out (all goroutines share workDir)
    if err := WriteCLAUDEMD(f.cfg.WorkDir, f.cfg.Persona); err != nil {
        log.Printf("feeder: write CLAUDE.md: %v", err)
        f.rollback(ctx, regularMsgs)
        return
    }

    // Group by sessionKey and process each group concurrently
    groups := make(map[string][]store.ClaimedMessage)
    for _, m := range regularMsgs {
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

// processBeeGroup invokes bee for a single sessionKey's messages, managing session continuity.
func (f *Feeder) processBeeGroup(ctx context.Context, sessionKey string, msgs []store.ClaimedMessage) {
    // Look up existing session for this sessionKey
    sessionID, err := f.sessionStore.GetSessionContext(ctx, sessionKey, store.BeeAgentID)
    if err != nil {
        log.Printf("feeder: get session context for %s: %v", sessionKey, err)
        f.rollback(ctx, msgs)
        return
    }
    resume := sessionID != ""
    if sessionID == "" {
        sessionID = uuid.New().String()
    }

    prompt := buildPrompt(msgs)
    beeCtx, cancel := context.WithTimeout(ctx, f.cfg.Timeout)
    defer cancel()

    if err := f.runner.Run(beeCtx, f.cfg.WorkDir, prompt, sessionID, resume); err != nil {
        log.Printf("feeder: bee run failed for %s: %v", sessionKey, err)
        f.rollback(ctx, msgs)
        return
    }

    // Persist session_id before marking messages processed
    if err := f.sessionStore.UpsertSessionContext(ctx, sessionKey, store.BeeAgentID, sessionID); err != nil {
        log.Printf("feeder: upsert session context for %s: %v", sessionKey, err)
        // non-fatal: continue to mark messages processed
    }

    msgIDs := make([]string, len(msgs))
    for i, m := range msgs {
        msgIDs[i] = m.ID
    }
    if err := f.msgStore.MarkBeeProcessed(ctx, msgIDs); err != nil {
        log.Printf("feeder: mark bee_processed for %s: %v", sessionKey, err)
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
```

- [ ] **Step 3: Update main.go to pass SessionStore to NewFeeder**

In `cmd/server/main.go`, add `sessionStore` after `taskStore` on line 46:

```go
workerStore := store.NewWorkerStore(db)
execStore := store.NewExecutionStore(db)
msgStore := store.NewMessageStore(db)
taskStore := store.NewTaskStore(db)
sessionStore := store.NewSessionStore(db)
```

And update the `NewFeeder` call (line 79):

```go
feeder := bee.NewFeeder(msgStore, taskStore, sessionStore, beeProcess, dispatchCh, feederCfg)
```

- [ ] **Step 4: Verify compile**

```bash
go build ./...
```

Expected: compile errors only from `feeder_test.go` (mockBeeRunner has old signature). Fix in next task.

---

### Task 5: Rewrite feeder_test.go

**Files:**
- Modify: `internal/bee/feeder_test.go`

The existing tests are rewritten. `mockBeeRunner` gets the new signature. We test `processBeeGroup` behavior directly to avoid timer races.

- [ ] **Step 1: Replace feeder_test.go**

```go
package bee_test

import (
    "context"
    "database/sql"
    "fmt"
    "testing"
    "time"

    "github.com/robobee/core/internal/bee"
    "github.com/robobee/core/internal/dispatcher"
    "github.com/robobee/core/internal/store"
)

func setupFeederDB(t *testing.T) (*sql.DB, *store.MessageStore, *store.TaskStore, *store.SessionStore) {
    t.Helper()
    db, err := store.InitDB(t.TempDir() + "/test.db")
    if err != nil {
        t.Fatalf("InitDB: %v", err)
    }
    t.Cleanup(func() { db.Close() })
    return db, store.NewMessageStore(db), store.NewTaskStore(db), store.NewSessionStore(db)
}

func insertMessage(t *testing.T, db *sql.DB, id, sessionKey, content string) {
    t.Helper()
    now := time.Now().UnixMilli()
    _, err := db.Exec(
        `INSERT INTO platform_messages (id, session_key, platform, content, status, received_at, created_at, updated_at)
         VALUES (?, ?, 'feishu', ?, 'received', ?, ?, ?)`,
        id, sessionKey, content, now, now, now,
    )
    if err != nil {
        t.Fatalf("insert message: %v", err)
    }
}

// mockBeeRunner records all Run calls.
type mockBeeRunner struct {
    calls []beeCall
    err   error
}

type beeCall struct {
    prompt    string
    sessionID string
    resume    bool
}

func (m *mockBeeRunner) Run(_ context.Context, _, prompt, sessionID string, resume bool) error {
    m.calls = append(m.calls, beeCall{prompt: prompt, sessionID: sessionID, resume: resume})
    return m.err
}

func newFeeder(ms *store.MessageStore, ts *store.TaskStore, ss *store.SessionStore, runner bee.BeeRunner) *bee.Feeder {
    clearCh := make(chan dispatcher.DispatchTask, 10)
    cfg := bee.FeederConfig{
        Interval:           50 * time.Millisecond,
        BatchSize:          10,
        Timeout:            5 * time.Second,
        QueueWarnThreshold: 100,
        WorkDir:            "/tmp",
    }
    return bee.NewFeeder(ms, ts, ss, runner, clearCh, cfg)
}

// TestFeeder_FirstTick_UsesNewSessionID verifies that on the first message for a sessionKey,
// bee is called with a fresh UUID sessionID and resume=false.
func TestFeeder_FirstTick_UsesNewSessionID(t *testing.T) {
    db, ms, ts, ss := setupFeederDB(t)
    insertMessage(t, db, "m1", "feishu:c:u", "hello")

    runner := &mockBeeRunner{}
    f := newFeeder(ms, ts, ss, runner)

    ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
    defer cancel()
    go f.Run(ctx)
    time.Sleep(150 * time.Millisecond)

    if len(runner.calls) == 0 {
        t.Fatal("expected bee runner to be called")
    }
    call := runner.calls[0]
    if call.sessionID == "" {
        t.Error("expected non-empty sessionID on first call")
    }
    if call.resume {
        t.Error("expected resume=false on first call")
    }

    // Session context should be persisted
    got, err := ss.GetSessionContext(context.Background(), "feishu:c:u", store.BeeAgentID)
    if err != nil {
        t.Fatalf("get session context: %v", err)
    }
    if got != call.sessionID {
        t.Errorf("persisted sessionID mismatch: want %q got %q", call.sessionID, got)
    }

    // Message should be bee_processed
    var status string
    db.QueryRow(`SELECT status FROM platform_messages WHERE id='m1'`).Scan(&status)
    if status != "bee_processed" {
        t.Errorf("expected bee_processed, got %q", status)
    }
}

// TestFeeder_SecondTick_ResumesSession verifies that after a session_id is established,
// subsequent bee calls use resume=true with the stored sessionID.
func TestFeeder_SecondTick_ResumesSession(t *testing.T) {
    db, ms, ts, ss := setupFeederDB(t)
    ctx := context.Background()

    // Pre-seed a session context as if a prior tick already ran
    if err := ss.UpsertSessionContext(ctx, "feishu:c:u", store.BeeAgentID, "existing-session"); err != nil {
        t.Fatalf("seed session: %v", err)
    }

    insertMessage(t, db, "m1", "feishu:c:u", "follow-up")

    runner := &mockBeeRunner{}
    f := newFeeder(ms, ts, ss, runner)

    tickCtx, cancel := context.WithTimeout(ctx, 300*time.Millisecond)
    defer cancel()
    go f.Run(tickCtx)
    time.Sleep(150 * time.Millisecond)

    if len(runner.calls) == 0 {
        t.Fatal("expected bee runner to be called")
    }
    call := runner.calls[0]
    if call.sessionID != "existing-session" {
        t.Errorf("expected existing-session, got %q", call.sessionID)
    }
    if !call.resume {
        t.Error("expected resume=true on second call")
    }
}

// TestFeeder_OnBeeFailure_RollsBackAndDoesNotUpdateSession verifies that a bee failure
// resets messages to 'received' and does NOT write to session_contexts.
func TestFeeder_OnBeeFailure_RollsBackAndDoesNotUpdateSession(t *testing.T) {
    db, ms, ts, ss := setupFeederDB(t)
    insertMessage(t, db, "m1", "feishu:c:u", "hello")

    runner := &mockBeeRunner{err: fmt.Errorf("bee crashed")}
    f := newFeeder(ms, ts, ss, runner)

    ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
    defer cancel()
    go f.Run(ctx)
    time.Sleep(150 * time.Millisecond)

    var status string
    db.QueryRow(`SELECT status FROM platform_messages WHERE id='m1'`).Scan(&status)
    if status != "received" {
        t.Errorf("expected rollback to received, got %q", status)
    }

    got, _ := ss.GetSessionContext(context.Background(), "feishu:c:u", store.BeeAgentID)
    if got != "" {
        t.Errorf("session context should not be written on failure, got %q", got)
    }
}

// TestFeeder_MultipleSessionKeys_ProcessedIndependently verifies that two sessionKeys
// in the same batch each get their own bee invocation with independent session tracking.
func TestFeeder_MultipleSessionKeys_ProcessedIndependently(t *testing.T) {
    db, ms, ts, ss := setupFeederDB(t)
    insertMessage(t, db, "m1", "feishu:c:u1", "message from user1")
    insertMessage(t, db, "m2", "feishu:c:u2", "message from user2")

    runner := &mockBeeRunner{}
    f := newFeeder(ms, ts, ss, runner)

    ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
    defer cancel()
    go f.Run(ctx)
    time.Sleep(150 * time.Millisecond)

    if len(runner.calls) != 2 {
        t.Fatalf("expected 2 bee invocations (one per sessionKey), got %d", len(runner.calls))
    }

    // Each sessionKey should have its own session context
    sess1, _ := ss.GetSessionContext(context.Background(), "feishu:c:u1", store.BeeAgentID)
    sess2, _ := ss.GetSessionContext(context.Background(), "feishu:c:u2", store.BeeAgentID)
    if sess1 == "" {
        t.Error("session context for u1 should be set")
    }
    if sess2 == "" {
        t.Error("session context for u2 should be set")
    }
    if sess1 == sess2 {
        t.Error("session IDs for different sessionKeys must differ")
    }
}
```

- [ ] **Step 2: Run tests**

```bash
go test ./internal/bee/... -v
```

Expected: all 4 new tests PASS. (Old `TestFeeder_ClaimsMessages_And_InvokesBee` and `TestFeeder_RollsBack_OnBeeFailure` are removed.)

- [ ] **Step 3: Run all tests**

```bash
go test ./...
```

Expected: all packages pass (the pre-existing feeder failure is now fixed).

- [ ] **Step 4: Commit**

```bash
git add internal/bee/bee_process.go internal/bee/feeder.go internal/bee/feeder_test.go cmd/server/main.go
git commit -m "feat(bee): refactor feeder for per-sessionKey bee invocations with session continuity"
```

---

## Chunk 3: Dispatcher + Manager Refactor

### Task 6: Add Manager.ExecuteWorkerWithSession

**Files:**
- Modify: `internal/worker/manager.go`

- [ ] **Step 1: Add the method**

In `internal/worker/manager.go`, add `ExecuteWorkerWithSession` after `ExecuteWorker` (after line 126):

```go
// ExecuteWorkerWithSession runs a worker resuming an existing Claude session identified by sessionID.
// This is used by the Dispatcher when a prior session exists for the (sessionKey, workerID) pair.
func (m *Manager) ExecuteWorkerWithSession(ctx context.Context, workerID, triggerInput, sessionID string) (model.WorkerExecution, error) {
    worker, err := m.workerStore.GetByID(workerID)
    if err != nil {
        return model.WorkerExecution{}, fmt.Errorf("get worker: %w", err)
    }

    exec, err := m.executionStore.CreateWithSessionID(workerID, triggerInput, sessionID)
    if err != nil {
        return model.WorkerExecution{}, fmt.Errorf("create execution with session: %w", err)
    }

    if err := m.workerStore.UpdateStatus(worker.ID, model.WorkerStatusWorking); err != nil {
        log.Printf("failed to update worker status: %v", err)
    }

    rt := NewClaudeRuntime(m.cfg.Runtime.ClaudeCode.Binary)
    timeout := m.cfg.Runtime.ClaudeCode.Timeout

    if err := m.launchRuntime(exec, worker, rt, timeout, triggerInput, true); err != nil {
        m.executionStore.UpdateResult(exec.ID, err.Error(), model.ExecStatusFailed)
        m.workerStore.UpdateStatus(worker.ID, model.WorkerStatusError)
        return exec, fmt.Errorf("start runtime: %w", err)
    }

    return exec, nil
}
```

- [ ] **Step 2: Verify compile**

```bash
go build ./internal/worker/...
```

Expected: compiles cleanly.

- [ ] **Step 3: Commit**

```bash
git add internal/worker/manager.go
git commit -m "feat(worker): add ExecuteWorkerWithSession for direct session-ID-based resume"
```

---

### Task 7: Update Dispatcher interfaces and add SessionStore

**Files:**
- Modify: `internal/dispatcher/dispatcher.go`

- [ ] **Step 1: Update interfaces and struct**

Replace the interface and struct definitions (lines 23-68) with:

```go
// ExecutionManager manages worker executions.
type ExecutionManager interface {
    ExecuteWorker(ctx context.Context, workerID, input string) (model.WorkerExecution, error)
    // ExecuteWorkerWithSession resumes an existing Claude session identified by sessionID.
    ExecuteWorkerWithSession(ctx context.Context, workerID, input, sessionID string) (model.WorkerExecution, error)
    GetExecution(id string) (model.WorkerExecution, error)
}

// TaskStore is the subset of store.TaskStore used by the Dispatcher.
type TaskStore interface {
    SetExecution(ctx context.Context, taskID, executionID, status string) error
}

// MessageStore is the subset of store.MessageStore used by the Dispatcher.
type MessageStore interface {
    SetMessageExecution(ctx context.Context, messageID, executionID, sessionID string) error
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
    content  string
}

// Dispatcher serializes worker executions per (SessionKey, WorkerID) and emits SenderEvents.
type Dispatcher struct {
    ctx          context.Context
    manager      ExecutionManager
    taskStore    TaskStore
    msgStore     MessageStore
    sessionStore SessionStore
    in           <-chan DispatchTask
    results      chan internalResult
    out          chan msgsender.SenderEvent
    queues       map[string]*queueState
}
```

- [ ] **Step 2: Update New() constructor**

Replace `New()` (lines 71-81) with:

```go
// New constructs a Dispatcher.
func New(manager ExecutionManager, taskStore TaskStore, msgStore MessageStore, sessionStore SessionStore, in <-chan DispatchTask) *Dispatcher {
    return &Dispatcher{
        manager:      manager,
        taskStore:    taskStore,
        msgStore:     msgStore,
        sessionStore: sessionStore,
        in:           in,
        results:      make(chan internalResult, 64),
        out:          make(chan msgsender.SenderEvent, 64),
        queues:       make(map[string]*queueState),
    }
}
```

- [ ] **Step 3: Update handleInbound — add ClearSessionContexts to clear path**

Replace the clear block in `handleInbound` (lines 111-127) with:

```go
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
```

- [ ] **Step 4: Update executeAsync — replace GetSession+ReplyExecution with GetSessionContext+ExecuteWorkerWithSession**

Replace `executeAsync` (lines 159-206) with:

```go
func (d *Dispatcher) executeAsync(ctx context.Context, key string, task DispatchTask, replyTo platform.InboundMessage) {
    var exec model.WorkerExecution
    var err error

    // For immediate tasks, attempt --resume if a prior session exists.
    if task.TaskType == model.TaskTypeImmediate {
        sessionID, sessErr := d.sessionStore.GetSessionContext(ctx, task.SessionKey, task.WorkerID)
        if sessErr != nil {
            log.Printf("dispatcher: get session context error: %v", sessErr)
        }
        if sessionID != "" {
            log.Printf("dispatcher: resuming session=%s for task %s", sessionID, task.TaskID)
            exec, err = d.manager.ExecuteWorkerWithSession(ctx, task.WorkerID, task.Instruction, sessionID)
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

    result := d.waitForResult(ctx, exec.ID, task.TaskID, task.SessionKey, task.WorkerID)
    select {
    case d.results <- internalResult{queueKey: key, task: task, content: result}:
    case <-ctx.Done():
    }
}
```

- [ ] **Step 5: Update waitForResult — add UpsertSessionContext on success**

Replace `waitForResult` (lines 208-243) with:

```go
func (d *Dispatcher) waitForResult(ctx context.Context, executionID, taskID, sessionKey, workerID string) string {
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
            // Persist session_id for future resume (only on success)
            if sessionKey != "" && workerID != "" {
                if err := d.sessionStore.UpsertSessionContext(ctx, sessionKey, workerID, exec.SessionID); err != nil {
                    log.Printf("dispatcher: upsert session context: %v", err)
                }
            }
            if exec.Result != "" {
                return exec.Result
            }
            return "✅ 任务已完成"
        case model.ExecStatusFailed:
            if taskID != "" {
                d.taskStore.SetExecution(ctx, taskID, executionID, model.TaskStatusFailed) //nolint:errcheck
            }
            // Do NOT update session_contexts on failure
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
```

- [ ] **Step 6: Update main.go — pass sessionStore to dispatcher.New**

In `cmd/server/main.go`, replace the dispatcher construction (line 90):

```go
disp := dispatcher.New(mgr, taskStore, msgStore, sessionStore, dispatchCh)
```

- [ ] **Step 7: Verify compile**

```bash
go build ./...
```

Expected: compile errors only from `dispatcher_test.go` (mocks need updating). Fix in next task.

---

### Task 8: Update dispatcher_test.go

**Files:**
- Modify: `internal/dispatcher/dispatcher_test.go`

- [ ] **Step 1: Update mocks and constructor calls**

Replace the mock definitions and `dispatcher.New` calls in `internal/dispatcher/dispatcher_test.go`:

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
    // resumedWithSessionID records the sessionID passed to ExecuteWorkerWithSession
    resumedWithSessionID string
}

func (m *mockExecManager) ExecuteWorker(_ context.Context, _, _ string) (model.WorkerExecution, error) {
    return m.execResult, nil
}
func (m *mockExecManager) ExecuteWorkerWithSession(_ context.Context, _, _, sessionID string) (model.WorkerExecution, error) {
    m.resumedWithSessionID = sessionID
    return m.execResult, nil
}
func (m *mockExecManager) GetExecution(_ string) (model.WorkerExecution, error) {
    return m.getResult, nil
}

type mockTaskStore struct{}

func (s *mockTaskStore) SetExecution(_ context.Context, _, _, _ string) error { return nil }

type mockMsgStore struct{}

func (s *mockMsgStore) SetMessageExecution(_ context.Context, _, _, _ string) error { return nil }

type mockSessionStore struct {
    data    map[string]string // "sessionKey|agentID" -> sessionID
    cleared []string          // sessionKeys cleared
}

func newMockSessionStore() *mockSessionStore {
    return &mockSessionStore{data: make(map[string]string)}
}

func (s *mockSessionStore) GetSessionContext(_ context.Context, sessionKey, agentID string) (string, error) {
    return s.data[sessionKey+"|"+agentID], nil
}
func (s *mockSessionStore) UpsertSessionContext(_ context.Context, sessionKey, agentID, sessionID string) error {
    s.data[sessionKey+"|"+agentID] = sessionID
    return nil
}
func (s *mockSessionStore) ClearSessionContexts(_ context.Context, sessionKey string) error {
    s.cleared = append(s.cleared, sessionKey)
    return nil
}

func newDispatcher(mgr dispatcher.ExecutionManager, ss dispatcher.SessionStore) (*dispatcher.Dispatcher, chan dispatcher.DispatchTask) {
    in := make(chan dispatcher.DispatchTask, 4)
    d := dispatcher.New(mgr, &mockTaskStore{}, &mockMsgStore{}, ss, in)
    return d, in
}

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
    mgr := &mockExecManager{
        execResult: model.WorkerExecution{ID: "exec-1", SessionID: "sess-1"},
        getResult:  model.WorkerExecution{ID: "exec-1", Status: model.ExecStatusCompleted, Result: "done!"},
    }
    d, in := newDispatcher(mgr, newMockSessionStore())

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

func TestDispatcher_ClearTask_EmitsClearResultAndClearsSession(t *testing.T) {
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

    events := collectEvents(d.Out(), 1, 500*time.Millisecond)
    if len(events) != 1 || events[0].Type != msgsender.SenderEventResult {
        t.Fatalf("expected 1 result for clear, got %v", events)
    }
    if events[0].Content != "✅ 上下文已重置" {
        t.Errorf("unexpected clear content: %q", events[0].Content)
    }

    // Give dispatcher goroutine time to call ClearSessionContexts
    time.Sleep(50 * time.Millisecond)
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

    in <- dispatchTask("immediate", "s1", "w1", "follow-up")
    collectEvents(d.Out(), 2, 2*time.Second)

    if mgr.resumedWithSessionID != "prior-session-id" {
        t.Errorf("expected ExecuteWorkerWithSession called with prior-session-id, got %q", mgr.resumedWithSessionID)
    }
}

func TestDispatcher_ImmediateTask_FreshWhenNoSession(t *testing.T) {
    ss := newMockSessionStore() // no prior session

    mgr := &mockExecManager{
        execResult: model.WorkerExecution{ID: "exec-1", SessionID: "new-session"},
        getResult:  model.WorkerExecution{ID: "exec-1", Status: model.ExecStatusCompleted, Result: "fresh!"},
    }
    d, in := newDispatcher(mgr, ss)

    ctx, cancel := context.WithCancel(context.Background())
    defer cancel()
    go d.Run(ctx)

    in <- dispatchTask("immediate", "s1", "w1", "first message")
    collectEvents(d.Out(), 2, 2*time.Second)

    if mgr.resumedWithSessionID != "" {
        t.Errorf("expected ExecuteWorker (not ExecuteWorkerWithSession) for fresh start, but resume was called with %q", mgr.resumedWithSessionID)
    }
}

func TestDispatcher_TwoTasks_SameSession_Serialized(t *testing.T) {
    blocker := make(chan struct{})
    mgr := &blockingExecManager{blocker: blocker}
    d, in := newDispatcher(mgr, newMockSessionStore())

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
func (m *blockingExecManager) ExecuteWorkerWithSession(_ context.Context, _, _, _ string) (model.WorkerExecution, error) {
    <-m.blocker
    return model.WorkerExecution{ID: "exec-x"}, nil
}
func (m *blockingExecManager) GetExecution(_ string) (model.WorkerExecution, error) {
    return model.WorkerExecution{ID: "exec-x", Status: model.ExecStatusCompleted, Result: "ok"}, nil
}
```

- [ ] **Step 2: Run dispatcher tests**

```bash
go test ./internal/dispatcher/... -v
```

Expected: all 5 tests PASS.

- [ ] **Step 3: Run all tests**

```bash
go test ./...
```

Expected: all packages pass with no failures.

- [ ] **Step 4: Commit**

```bash
git add internal/dispatcher/dispatcher.go internal/dispatcher/dispatcher_test.go cmd/server/main.go
git commit -m "feat(dispatcher): replace GetSession+ReplyExecution with session_contexts-based session lookup"
```

---

### Task 9: Final verification

- [ ] **Step 1: Build the full binary**

```bash
go build ./cmd/server/...
```

Expected: compiles cleanly.

- [ ] **Step 2: Run all tests with race detector**

```bash
go test -race ./...
```

Expected: all tests pass, no race conditions detected.

- [ ] **Step 3: Commit (if any fixes needed from race detector)**

```bash
git add -A
git commit -m "fix: address race conditions found by race detector"
```

(Skip this step if no issues found.)
