# Feishu Bot Integration Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Integrate a Feishu bot that routes user messages to the appropriate Worker via AI, with async response delivery and multi-turn conversation support.

**Architecture:** Feishu long-connection (WebSocket) service starts alongside the cron scheduler in `main.go`, sharing the same `Manager` and `WorkerStore`. AI routing calls DeepSeek to pick the best worker based on message content. Chat-to-session mapping is persisted in SQLite so conversations survive restarts.

**Tech Stack:** Go, `github.com/larksuite/oapi-sdk-go/v3`, existing `ai.Client` (DeepSeek), SQLite, `worker.Manager`

**Spec:** `docs/superpowers/specs/2026-03-09-feishu-bot-integration-design.md`

---

## Spec Deviations (intentional)

The spec lists `internal/feishu/session.go` for DB access — this plan places session DB logic in `internal/store/feishu_session_store.go` instead, consistent with how all other stores are organized in this codebase.

The spec's `UpsertSession(session *FeishuSession)` takes a struct pointer; this plan uses `UpsertSession(chatID, workerID, sessionID, lastExecutionID string)` with positional args to avoid creating a struct solely for passing to one function.

The spec's `Start(ctx, cfg, handler)` takes a pre-built handler; this plan's `Start(ctx, cfg, workerStore, sessionStore, mgr, aiClient)` constructs Router and Handler internally to keep wiring inside the feishu package and simplify `main.go`.

---

## File Map

| File | Action | Responsibility |
|------|--------|----------------|
| `internal/config/config.go` | Modify | Add `FeishuConfig` struct and field to `Config` |
| `config.yaml` | Modify | Add `feishu:` block |
| `internal/store/db.go` | Modify | Add `feishu_sessions` table migration |
| `internal/store/feishu_session_store.go` | Create | DB CRUD for feishu chat→session mapping |
| `internal/store/feishu_session_store_test.go` | Create | Tests for session store |
| `internal/ai/client.go` | Modify | Add `RouteToWorker()` method and `WorkerSummary` type |
| `internal/ai/client_test.go` | Create | Tests for RouteToWorker with mock HTTP server |
| `internal/worker/manager.go` | Modify | Add `GetExecution(id)` helper method |
| `internal/feishu/router.go` | Create | Wraps ai.Client: lists workers, calls RouteToWorker |
| `internal/feishu/router_test.go` | Create | Tests for Router.Route using in-memory DB + mock HTTP |
| `internal/feishu/handler.go` | Create | OnP2MessageReceiveV1 handler: ack, route, execute, reply |
| `internal/feishu/client.go` | Create | `Start()` function that wires Lark WS client |
| `cmd/server/main.go` | Modify | Start feishu service alongside scheduler |

> **Note:** `internal/feishu/session.go` from the spec is not created — session DB access lives in `internal/store/feishu_session_store.go`.

---

## Chunk 1: Foundation — Config, DB, Session Store

### Task 1: Add Feishu config

**Files:**
- Modify: `internal/config/config.go`
- Modify: `config.yaml`

- [ ] **Step 1: Add FeishuConfig to config.go**

In `internal/config/config.go`, add the struct:

```go
type FeishuConfig struct {
    Enabled   bool   `yaml:"enabled"`
    AppID     string `yaml:"app_id"`
    AppSecret string `yaml:"app_secret"`
}
```

Add `Feishu FeishuConfig \`yaml:"feishu"\`` to the `Config` struct after `AI AIConfig`.

- [ ] **Step 2: Add feishu block to config.yaml**

Append to `config.yaml`:

```yaml
feishu:
  enabled: false
  app_id: ""
  app_secret: ""
```

- [ ] **Step 3: Verify config loads**

```bash
cd /Users/tengteng/work/robobee/core
go build ./...
```

Expected: no compilation errors.

- [ ] **Step 4: Commit**

```bash
git add internal/config/config.go config.yaml
git commit -m "feat: add feishu config struct"
```

---

### Task 2: Add feishu_sessions table migration

**Files:**
- Modify: `internal/store/db.go`

- [ ] **Step 1: Add migration entry**

In `internal/store/db.go`, add to the `migrations` slice:

```go
`CREATE TABLE IF NOT EXISTS feishu_sessions (
    chat_id TEXT NOT NULL,
    worker_id TEXT NOT NULL,
    session_id TEXT NOT NULL,
    last_execution_id TEXT NOT NULL DEFAULT '',
    updated_at DATETIME NOT NULL DEFAULT (datetime('now')),
    PRIMARY KEY (chat_id)
)`,
```

`CREATE TABLE IF NOT EXISTS` is idempotent — no duplicate-column guard needed.

- [ ] **Step 2: Verify migration runs**

```bash
go build ./...
```

Expected: no errors.

- [ ] **Step 3: Commit**

```bash
git add internal/store/db.go
git commit -m "feat: add feishu_sessions table migration"
```

---

### Task 3: Create feishu session store

**Files:**
- Create: `internal/store/feishu_session_store.go`
- Create: `internal/store/feishu_session_store_test.go`

- [ ] **Step 1: Write the failing tests**

Create `internal/store/feishu_session_store_test.go`:

```go
package store_test

import (
    "testing"

    "github.com/robobee/core/internal/store"
)

func newFeishuSessionStore(t *testing.T) *store.FeishuSessionStore {
    t.Helper()
    db, err := store.InitDB(":memory:")
    if err != nil {
        t.Fatalf("init db: %v", err)
    }
    t.Cleanup(func() { db.Close() })
    return store.NewFeishuSessionStore(db)
}

func TestFeishuSessionStore_GetSession_NotFound(t *testing.T) {
    s := newFeishuSessionStore(t)
    sess, err := s.GetSession("nonexistent")
    if err != nil {
        t.Fatalf("unexpected error: %v", err)
    }
    if sess != nil {
        t.Fatalf("expected nil, got %+v", sess)
    }
}

func TestFeishuSessionStore_UpsertAndGet(t *testing.T) {
    s := newFeishuSessionStore(t)

    err := s.UpsertSession("chat1", "worker1", "session1", "exec1")
    if err != nil {
        t.Fatalf("upsert: %v", err)
    }

    sess, err := s.GetSession("chat1")
    if err != nil {
        t.Fatalf("get: %v", err)
    }
    if sess == nil {
        t.Fatal("expected session, got nil")
    }
    if sess.WorkerID != "worker1" {
        t.Errorf("worker_id: got %q, want %q", sess.WorkerID, "worker1")
    }
    if sess.SessionID != "session1" {
        t.Errorf("session_id: got %q, want %q", sess.SessionID, "session1")
    }
    if sess.LastExecutionID != "exec1" {
        t.Errorf("last_execution_id: got %q, want %q", sess.LastExecutionID, "exec1")
    }
}

func TestFeishuSessionStore_Upsert_Updates(t *testing.T) {
    s := newFeishuSessionStore(t)

    _ = s.UpsertSession("chat1", "worker1", "session1", "exec1")
    _ = s.UpsertSession("chat1", "worker2", "session2", "exec2")

    sess, _ := s.GetSession("chat1")
    if sess.WorkerID != "worker2" {
        t.Errorf("expected updated worker_id worker2, got %q", sess.WorkerID)
    }
    if sess.LastExecutionID != "exec2" {
        t.Errorf("expected updated exec exec2, got %q", sess.LastExecutionID)
    }
}
```

- [ ] **Step 2: Run tests to verify they fail**

```bash
go test ./internal/store/... -run TestFeishuSession -v
```

Expected: compile error — `FeishuSessionStore` does not exist yet.

- [ ] **Step 3: Create the store implementation**

Create `internal/store/feishu_session_store.go`:

```go
package store

import (
    "database/sql"
    "time"
)

type FeishuSession struct {
    ChatID          string
    WorkerID        string
    SessionID       string
    LastExecutionID string
    UpdatedAt       time.Time
}

type FeishuSessionStore struct {
    db *sql.DB
}

func NewFeishuSessionStore(db *sql.DB) *FeishuSessionStore {
    return &FeishuSessionStore{db: db}
}

// GetSession returns nil if not found.
func (s *FeishuSessionStore) GetSession(chatID string) (*FeishuSession, error) {
    row := s.db.QueryRow(
        `SELECT chat_id, worker_id, session_id, last_execution_id, updated_at
         FROM feishu_sessions WHERE chat_id = ?`, chatID)

    var sess FeishuSession
    err := row.Scan(&sess.ChatID, &sess.WorkerID, &sess.SessionID, &sess.LastExecutionID, &sess.UpdatedAt)
    if err == sql.ErrNoRows {
        return nil, nil
    }
    if err != nil {
        return nil, err
    }
    return &sess, nil
}

// UpsertSession creates or updates the session mapping for a chat.
// Uses positional args instead of a struct for simplicity (deviation from spec).
func (s *FeishuSessionStore) UpsertSession(chatID, workerID, sessionID, lastExecutionID string) error {
    _, err := s.db.Exec(
        `INSERT INTO feishu_sessions (chat_id, worker_id, session_id, last_execution_id, updated_at)
         VALUES (?, ?, ?, ?, ?)
         ON CONFLICT(chat_id) DO UPDATE SET
             worker_id = excluded.worker_id,
             session_id = excluded.session_id,
             last_execution_id = excluded.last_execution_id,
             updated_at = excluded.updated_at`,
        chatID, workerID, sessionID, lastExecutionID, time.Now().UTC())
    return err
}
```

- [ ] **Step 4: Run tests to verify they pass**

```bash
go test ./internal/store/... -run TestFeishuSession -v
```

Expected: all 3 tests PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/store/feishu_session_store.go internal/store/feishu_session_store_test.go
git commit -m "feat: add feishu session store"
```

---

## Chunk 2: AI Routing

### Task 4: Add RouteToWorker to ai/client.go

**Files:**
- Modify: `internal/ai/client.go`
- Create: `internal/ai/client_test.go`

- [ ] **Step 1: Write the failing tests**

Create `internal/ai/client_test.go`:

```go
package ai_test

import (
    "context"
    "encoding/json"
    "net/http"
    "net/http/httptest"
    "testing"

    "github.com/robobee/core/internal/ai"
    "github.com/robobee/core/internal/config"
)

func newTestAIClient(t *testing.T, handler http.HandlerFunc) *ai.Client {
    t.Helper()
    srv := httptest.NewServer(handler)
    t.Cleanup(srv.Close)
    return ai.NewClient(config.AIConfig{
        BaseURL: srv.URL,
        APIKey:  "test-key",
        Model:   "test-model",
    })
}

func TestRouteToWorker_ReturnsWorkerID(t *testing.T) {
    client := newTestAIClient(t, func(w http.ResponseWriter, r *http.Request) {
        resp := map[string]interface{}{
            "choices": []map[string]interface{}{
                {"message": map[string]string{"content": "worker-abc"}},
            },
        }
        json.NewEncoder(w).Encode(resp)
    })

    workers := []ai.WorkerSummary{
        {ID: "worker-abc", Name: "mas", Description: "market analyst"},
        {ID: "worker-xyz", Name: "nova", Description: "code reviewer"},
    }

    id, err := client.RouteToWorker(context.Background(), "analyze market data", workers)
    if err != nil {
        t.Fatalf("unexpected error: %v", err)
    }
    if id != "worker-abc" {
        t.Errorf("got %q, want %q", id, "worker-abc")
    }
}

func TestRouteToWorker_InvalidID_ReturnsError(t *testing.T) {
    client := newTestAIClient(t, func(w http.ResponseWriter, r *http.Request) {
        resp := map[string]interface{}{
            "choices": []map[string]interface{}{
                {"message": map[string]string{"content": "nonexistent-id"}},
            },
        }
        json.NewEncoder(w).Encode(resp)
    })

    workers := []ai.WorkerSummary{
        {ID: "worker-abc", Name: "mas", Description: "market analyst"},
    }

    _, err := client.RouteToWorker(context.Background(), "some message", workers)
    if err == nil {
        t.Fatal("expected error for invalid worker ID, got nil")
    }
}
```

- [ ] **Step 2: Run tests to verify they fail**

```bash
go test ./internal/ai/... -run TestRouteToWorker -v
```

Expected: compile error — `WorkerSummary` and `RouteToWorker` not found.

- [ ] **Step 3: Add WorkerSummary type and RouteToWorker method to ai/client.go**

Append to `internal/ai/client.go`:

```go
// WorkerSummary is used for AI routing decisions.
type WorkerSummary struct {
    ID          string
    Name        string
    Description string
}

// RouteToWorker uses AI to select the most appropriate worker for a message.
// Returns an error if the AI response does not match any worker ID in the list.
func (c *Client) RouteToWorker(ctx context.Context, message string, workers []WorkerSummary) (string, error) {
    var workerList strings.Builder
    validIDs := make(map[string]bool, len(workers))
    for _, w := range workers {
        fmt.Fprintf(&workerList, "- ID: %s, Name: %s, Description: %s\n", w.ID, w.Name, w.Description)
        validIDs[w.ID] = true
    }

    reqBody := chatRequest{
        Model: c.model,
        Messages: []chatMessage{
            {
                Role:    "system",
                Content: "You are a task router. Given a list of workers and a user message, return ONLY the ID of the most suitable worker. No explanation, no markdown, just the ID.",
            },
            {
                Role:    "user",
                Content: fmt.Sprintf("Workers:\n%s\nUser message: %s", workerList.String(), message),
            },
        },
    }

    data, err := json.Marshal(reqBody)
    if err != nil {
        return "", fmt.Errorf("marshal request: %w", err)
    }

    req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/chat/completions", bytes.NewReader(data))
    if err != nil {
        return "", fmt.Errorf("create request: %w", err)
    }
    req.Header.Set("Content-Type", "application/json")
    req.Header.Set("Authorization", "Bearer "+c.apiKey)

    resp, err := c.httpClient.Do(req)
    if err != nil {
        return "", fmt.Errorf("http request: %w", err)
    }
    defer resp.Body.Close()

    if resp.StatusCode < 200 || resp.StatusCode >= 300 {
        return "", fmt.Errorf("AI service returned status %d", resp.StatusCode)
    }

    var chatResp chatResponse
    if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&chatResp); err != nil {
        return "", fmt.Errorf("decode response: %w", err)
    }

    if len(chatResp.Choices) == 0 {
        return "", fmt.Errorf("AI service returned no choices")
    }

    workerID := strings.TrimSpace(chatResp.Choices[0].Message.Content)
    if !validIDs[workerID] {
        return "", fmt.Errorf("AI returned unknown worker ID %q", workerID)
    }

    return workerID, nil
}
```

- [ ] **Step 4: Run tests to verify they pass**

```bash
go test ./internal/ai/... -run TestRouteToWorker -v
```

Expected: both tests PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/ai/client.go internal/ai/client_test.go
git commit -m "feat: add RouteToWorker to ai client"
```

---

## Chunk 3: Feishu Package

### Task 5: Add Feishu SDK dependency

**Files:**
- Modify: `go.mod`, `go.sum`

> **Do this before creating any files in `internal/feishu/` — those files import the SDK and won't compile without it.**

- [ ] **Step 1: Add the SDK**

```bash
cd /Users/tengteng/work/robobee/core
go get github.com/larksuite/oapi-sdk-go/v3
go mod tidy
```

Expected: `go.mod` updated with `github.com/larksuite/oapi-sdk-go/v3`, `go.sum` updated.

- [ ] **Step 2: Commit**

```bash
git add go.mod go.sum
git commit -m "chore: add larksuite oapi-sdk-go dependency"
```

---

### Task 6: Add Manager.GetExecution helper

**Files:**
- Modify: `internal/worker/manager.go`

- [ ] **Step 1: Add GetExecution method**

Append to `internal/worker/manager.go`:

```go
// GetExecution returns the current state of an execution by ID.
func (m *Manager) GetExecution(id string) (model.WorkerExecution, error) {
    return m.executionStore.GetByID(id)
}
```

- [ ] **Step 2: Verify it compiles**

```bash
go build ./internal/worker/...
```

Expected: no errors.

- [ ] **Step 3: Commit**

```bash
git add internal/worker/manager.go
git commit -m "feat: add GetExecution helper to Manager"
```

---

### Task 7: Create feishu/router.go

**Files:**
- Create: `internal/feishu/router.go`
- Create: `internal/feishu/router_test.go`

- [ ] **Step 1: Write the failing test**

Create `internal/feishu/router_test.go`:

```go
package feishu_test

import (
    "context"
    "encoding/json"
    "net/http"
    "net/http/httptest"
    "testing"

    "github.com/robobee/core/internal/ai"
    "github.com/robobee/core/internal/config"
    "github.com/robobee/core/internal/feishu"
    "github.com/robobee/core/internal/model"
    "github.com/robobee/core/internal/store"
)

func newRouterWithWorkers(t *testing.T, aiHandler http.HandlerFunc, workers []model.Worker) *feishu.Router {
    t.Helper()

    // Set up in-memory DB with workers
    db, err := store.InitDB(":memory:")
    if err != nil {
        t.Fatalf("init db: %v", err)
    }
    t.Cleanup(func() { db.Close() })

    ws := store.NewWorkerStore(db)
    for _, w := range workers {
        if _, err := ws.Create(w); err != nil {
            t.Fatalf("create worker: %v", err)
        }
    }

    // Set up mock AI server
    srv := httptest.NewServer(aiHandler)
    t.Cleanup(srv.Close)
    aiClient := ai.NewClient(config.AIConfig{BaseURL: srv.URL, APIKey: "test", Model: "test"})

    return feishu.NewRouter(aiClient, ws)
}

func TestRouter_Route_PicksCorrectWorker(t *testing.T) {
    workers := []model.Worker{
        {ID: "w1", Name: "mas", Description: "market analyst", WorkDir: t.TempDir()},
        {ID: "w2", Name: "nova", Description: "code reviewer", WorkDir: t.TempDir()},
    }

    router := newRouterWithWorkers(t, func(w http.ResponseWriter, r *http.Request) {
        resp := map[string]interface{}{
            "choices": []map[string]interface{}{
                {"message": map[string]string{"content": "w1"}},
            },
        }
        json.NewEncoder(w).Encode(resp)
    }, workers)

    id, err := router.Route(context.Background(), "analyze sales data")
    if err != nil {
        t.Fatalf("unexpected error: %v", err)
    }
    if id != "w1" {
        t.Errorf("got %q, want %q", id, "w1")
    }
}

func TestRouter_Route_NoWorkers_ReturnsError(t *testing.T) {
    router := newRouterWithWorkers(t, func(w http.ResponseWriter, r *http.Request) {}, []model.Worker{})

    _, err := router.Route(context.Background(), "some message")
    if err == nil {
        t.Fatal("expected error when no workers available")
    }
}
```

- [ ] **Step 2: Run tests to verify they fail**

```bash
go test ./internal/feishu/... -run TestRouter -v
```

Expected: compile error — `feishu.Router` and `feishu.NewRouter` not found.

- [ ] **Step 3: Create feishu/router.go**

Create `internal/feishu/router.go`:

```go
package feishu

import (
    "context"
    "fmt"

    "github.com/robobee/core/internal/ai"
    "github.com/robobee/core/internal/store"
)

type Router struct {
    aiClient    *ai.Client
    workerStore *store.WorkerStore
}

func NewRouter(aiClient *ai.Client, workerStore *store.WorkerStore) *Router {
    return &Router{aiClient: aiClient, workerStore: workerStore}
}

// Route returns the worker ID best suited to handle the message.
// Uses WorkerStore.List() (not ListWorkers — the method is named List in this codebase).
func (r *Router) Route(ctx context.Context, message string) (string, error) {
    workers, err := r.workerStore.List()
    if err != nil {
        return "", fmt.Errorf("list workers: %w", err)
    }
    if len(workers) == 0 {
        return "", fmt.Errorf("no workers available")
    }

    summaries := make([]ai.WorkerSummary, len(workers))
    for i, w := range workers {
        summaries[i] = ai.WorkerSummary{ID: w.ID, Name: w.Name, Description: w.Description}
    }

    return r.aiClient.RouteToWorker(ctx, message, summaries)
}
```

- [ ] **Step 4: Run tests to verify they pass**

```bash
go test ./internal/feishu/... -run TestRouter -v
```

Expected: both tests PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/feishu/router.go internal/feishu/router_test.go
git commit -m "feat: add feishu message router with tests"
```

---

### Task 8: Create feishu/handler.go

**Files:**
- Create: `internal/feishu/handler.go`

> **Known limitation:** The async goroutine in `process()` uses `context.Background()` with no cancellation. If the server shuts down mid-execution, the goroutine will continue polling for up to 30 minutes. This is acceptable for now — check the Web UI for results if the server restarts.

- [ ] **Step 1: Create feishu/handler.go**

Create `internal/feishu/handler.go`:

```go
package feishu

import (
    "context"
    "encoding/json"
    "log"
    "time"

    lark "github.com/larksuite/oapi-sdk-go/v3"
    larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
    "github.com/robobee/core/internal/model"
    "github.com/robobee/core/internal/store"
    "github.com/robobee/core/internal/worker"
)

const (
    pollInterval = 2 * time.Second
    pollTimeout  = 30 * time.Minute
    ackMessage   = "⏳ 正在处理，请稍候…"
    errorMessage = "❌ 处理失败，请稍后重试"
    noWorkerMsg  = "❌ 没有找到合适的 Worker，请换个描述试试"
)

type Handler struct {
    larkClient   *lark.Client
    router       *Router
    sessionStore *store.FeishuSessionStore
    manager      *worker.Manager
}

func NewHandler(
    larkClient *lark.Client,
    router *Router,
    sessionStore *store.FeishuSessionStore,
    manager *worker.Manager,
) *Handler {
    return &Handler{
        larkClient:   larkClient,
        router:       router,
        sessionStore: sessionStore,
        manager:      manager,
    }
}

func (h *Handler) OnMessage(ctx context.Context, event *larkim.P2MessageReceiveV1) error {
    msg := event.Event.Message
    if msg == nil || *msg.MessageType != "text" {
        return nil
    }

    var content map[string]string
    if err := json.Unmarshal([]byte(*msg.Content), &content); err != nil {
        return nil
    }
    text := content["text"]
    if text == "" {
        return nil
    }

    chatID := *msg.ChatId
    chatType := *msg.ChatType

    // Acknowledge immediately
    h.sendMessage(ctx, chatID, chatType, *msg.MessageId, ackMessage)

    // Process asynchronously.
    // Known limitation: uses context.Background() — goroutine outlives server shutdown.
    go h.process(chatID, chatType, *msg.MessageId, text)
    return nil
}

func (h *Handler) process(chatID, chatType, messageID, text string) {
    ctx := context.Background()

    workerID, err := h.router.Route(ctx, text)
    if err != nil {
        log.Printf("feishu: route error: %v", err)
        h.sendMessage(ctx, chatID, chatType, messageID, noWorkerMsg)
        return
    }

    sess, err := h.sessionStore.GetSession(chatID)
    if err != nil {
        log.Printf("feishu: get session error: %v", err)
        h.sendMessage(ctx, chatID, chatType, messageID, errorMessage)
        return
    }

    var exec model.WorkerExecution
    if sess != nil && sess.LastExecutionID != "" {
        exec, err = h.manager.ReplyExecution(ctx, sess.LastExecutionID, text)
    } else {
        exec, err = h.manager.ExecuteWorker(ctx, workerID, text)
    }
    if err != nil {
        log.Printf("feishu: execute error: %v", err)
        h.sendMessage(ctx, chatID, chatType, messageID, errorMessage)
        return
    }

    if err := h.sessionStore.UpsertSession(chatID, workerID, exec.SessionID, exec.ID); err != nil {
        log.Printf("feishu: upsert session error: %v", err)
    }

    result := h.waitForResult(exec.ID)
    h.sendMessage(ctx, chatID, chatType, messageID, result)
}

// waitForResult polls execution status every 2s until completed/failed or 30m timeout.
func (h *Handler) waitForResult(executionID string) string {
    deadline := time.Now().Add(pollTimeout)
    for time.Now().Before(deadline) {
        exec, err := h.manager.GetExecution(executionID)
        if err != nil {
            log.Printf("feishu: poll execution error: %v", err)
            return errorMessage
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
    return "⏰ 任务超时，请稍后通过 Web 界面查看结果"
}

func (h *Handler) sendMessage(ctx context.Context, chatID, chatType, replyToMessageID, text string) {
    content, _ := json.Marshal(map[string]string{"text": text})

    if chatType == "p2p" {
        resp, err := h.larkClient.Im.Message.Create(ctx,
            larkim.NewCreateMessageReqBuilder().
                ReceiveIdType(larkim.ReceiveIdTypeChatId).
                Body(larkim.NewCreateMessageReqBodyBuilder().
                    MsgType(larkim.MsgTypeText).
                    ReceiveId(chatID).
                    Content(string(content)).
                    Build()).
                Build())
        if err != nil || !resp.Success() {
            log.Printf("feishu: send message error: %v, resp: %+v", err, resp)
        }
    } else {
        resp, err := h.larkClient.Im.Message.Reply(ctx,
            larkim.NewReplyMessageReqBuilder().
                MessageId(replyToMessageID).
                Body(larkim.NewReplyMessageReqBodyBuilder().
                    MsgType(larkim.MsgTypeText).
                    Content(string(content)).
                    Build()).
                Build())
        if err != nil || !resp.Success() {
            log.Printf("feishu: reply message error: %v, resp: %+v", err, resp)
        }
    }
}
```

- [ ] **Step 2: Verify it compiles**

```bash
go build ./internal/feishu/...
```

Expected: no errors.

- [ ] **Step 3: Commit**

```bash
git add internal/feishu/handler.go
git commit -m "feat: add feishu message handler"
```

---

### Task 9: Create feishu/client.go

**Files:**
- Create: `internal/feishu/client.go`

> **Note on Start() signature:** The spec defines `Start(ctx, cfg, handler *Handler)`. This plan's `Start()` takes raw dependencies and constructs Router and Handler internally — this keeps all wiring inside the feishu package and keeps `main.go` clean.

- [ ] **Step 1: Create feishu/client.go**

Create `internal/feishu/client.go`:

```go
package feishu

import (
    "context"
    "log"

    lark "github.com/larksuite/oapi-sdk-go/v3"
    larkcore "github.com/larksuite/oapi-sdk-go/v3/core"
    "github.com/larksuite/oapi-sdk-go/v3/event/dispatcher"
    larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
    larkws "github.com/larksuite/oapi-sdk-go/v3/ws"

    "github.com/robobee/core/internal/ai"
    "github.com/robobee/core/internal/config"
    "github.com/robobee/core/internal/store"
    "github.com/robobee/core/internal/worker"
)

// Start connects to Feishu via WebSocket long connection and blocks until ctx is cancelled.
// Call this in a goroutine from main.go.
func Start(
    ctx context.Context,
    cfg config.FeishuConfig,
    workerStore *store.WorkerStore,
    sessionStore *store.FeishuSessionStore,
    mgr *worker.Manager,
    aiClient *ai.Client,
) error {
    larkClient := lark.NewClient(cfg.AppID, cfg.AppSecret)

    router := NewRouter(aiClient, workerStore)
    handler := NewHandler(larkClient, router, sessionStore, mgr)

    eventHandler := dispatcher.NewEventDispatcher("", "").
        OnP2MessageReceiveV1(func(ctx context.Context, event *larkim.P2MessageReceiveV1) error {
            return handler.OnMessage(ctx, event)
        })

    wsClient := larkws.NewClient(cfg.AppID, cfg.AppSecret,
        larkws.WithEventHandler(eventHandler),
        larkws.WithLogLevel(larkcore.LogLevelInfo),
    )

    log.Println("Feishu bot starting...")
    return wsClient.Start(ctx)
}
```

- [ ] **Step 2: Verify full feishu package compiles**

```bash
go build ./internal/feishu/...
```

Expected: no errors.

- [ ] **Step 3: Commit**

```bash
git add internal/feishu/client.go
git commit -m "feat: add feishu client startup"
```

---

## Chunk 4: Wiring

### Task 10: Wire feishu into main.go

**Files:**
- Modify: `cmd/server/main.go`

The current `main.go` structure (after `sched.Start()`) is:
```go
sched.Start()

srv := api.NewServer(...)

quit := make(chan os.Signal, 1)
signal.Notify(quit, ...)
go func() { <-quit; ... }()

srv.Run(addr)  // blocking
```

Insert the feishu block **after `sched.Start()` and before `srv := api.NewServer(...)`**.

- [ ] **Step 1: Add feishu startup to main.go**

Add `"context"` (stdlib — place in the stdlib import group, before the internal packages group) and `"github.com/robobee/core/internal/feishu"` to the import block (if not already present).

> **Known limitation:** `feishu.Start` is called with `context.Background()`, so the Feishu WebSocket client does not receive a cancellation signal on SIGINT/SIGTERM. The existing `main.go` shutdown handler calls `os.Exit(0)`, which terminates the process (and the goroutine) abruptly — so this is functionally correct. The Feishu SDK may log an unclean disconnect. This is consistent with the existing shutdown pattern in this codebase.

Insert after `sched.Start()`:

```go
// Start Feishu bot if enabled
if cfg.Feishu.Enabled {
    feishuSessionStore := store.NewFeishuSessionStore(db)
    go func() {
        if err := feishu.Start(context.Background(), cfg.Feishu, workerStore, feishuSessionStore, mgr, aiClient); err != nil {
            log.Printf("feishu bot error: %v", err)
        }
    }()
}
```

- [ ] **Step 2: Verify full build**

```bash
go build ./...
```

Expected: no errors.

- [ ] **Step 3: Run all tests**

```bash
go test ./...
```

Expected: all tests pass (feishu package tests + existing store/ai tests).

- [ ] **Step 4: Commit**

```bash
git add cmd/server/main.go
git commit -m "feat: wire feishu bot into server startup"
```

---

### Task 11: Manual integration test

- [ ] **Step 1: Configure credentials**

In `config.yaml`, set:
```yaml
feishu:
  enabled: true
  app_id: "your_app_id"
  app_secret: "your_app_secret"
```

Ensure at least one Worker exists in the system (create via the Web UI or API).

- [ ] **Step 2: Start the server**

```bash
go run ./cmd/server/
```

Expected: both log lines appear:
- `Feishu bot starting...`
- `RoboBee Core starting on 0.0.0.0:8080`

- [ ] **Step 3: Send a message to the bot**

Send a text message to the Feishu bot from a personal chat (p2p).

Expected sequence:
1. Immediate reply: `⏳ 正在处理，请稍候…`
2. After execution completes: result message with worker output

- [ ] **Step 4: Test multi-turn conversation**

Send a follow-up message in the same chat.

Expected: the bot responds using the same Claude session (context from previous exchange is preserved).

- [ ] **Step 5: Final commit if any adjustments were made**

```bash
git add -p
git commit -m "fix: feishu integration adjustments from manual testing"
```
