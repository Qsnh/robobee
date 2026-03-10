# DingTalk Bot Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add DingTalk chatbot support mirroring the existing Feishu bot — ack + async polling + reply via session webhook, with a shared AI router extracted from the Feishu package.

**Architecture:** Extract the worker router into `internal/botrouter` shared by both platforms. Create `internal/dingtalk` mirroring `internal/feishu` structure. Each platform is self-contained except for the shared router. Session state lives in a separate `dingtalk_sessions` SQLite table.

**Tech Stack:** `github.com/open-dingtalk/dingtalk-stream-sdk-go`, SQLite (existing), Go standard library.

---

## Chunk 1: Extract shared botrouter

### Task 1: Create `internal/botrouter` package with tests

**Files:**
- Create: `internal/botrouter/router.go`
- Create: `internal/botrouter/router_test.go`

- [ ] **Step 1: Write the failing test**

Create `internal/botrouter/router_test.go`:

```go
package botrouter_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/robobee/core/internal/ai"
	"github.com/robobee/core/internal/botrouter"
	"github.com/robobee/core/internal/config"
	"github.com/robobee/core/internal/model"
	"github.com/robobee/core/internal/store"
)

func newRouterWithWorkers(t *testing.T, aiHandler http.HandlerFunc, workers []model.Worker) *botrouter.Router {
	t.Helper()

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

	srv := httptest.NewServer(aiHandler)
	t.Cleanup(srv.Close)
	aiClient := ai.NewClient(config.AIConfig{BaseURL: srv.URL, APIKey: "test", Model: "test"})

	return botrouter.NewRouter(aiClient, ws)
}

func TestRouter_Route_PicksCorrectWorker(t *testing.T) {
	workers := []model.Worker{
		{ID: "w1", Name: "mas", Description: "market analyst", WorkDir: t.TempDir()},
		{ID: "w2", Name: "nova", Description: "code reviewer", WorkDir: t.TempDir()},
	}

	router := newRouterWithWorkers(t, func(w http.ResponseWriter, r *http.Request) {
		resp := map[string]any{
			"choices": []map[string]any{
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

- [ ] **Step 2: Run test to verify it fails**

```bash
go test ./internal/botrouter/...
```

Expected: compile error — package `botrouter` does not exist yet.

- [ ] **Step 3: Implement `internal/botrouter/router.go`**

```go
package botrouter

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
// Uses WorkerStore.List() to fetch all workers live.
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

- [ ] **Step 4: Run test to verify it passes**

```bash
go test ./internal/botrouter/...
```

Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/botrouter/
git commit -m "feat: add botrouter package with shared AI worker router"
```

---

### Task 2: Update feishu package to use botrouter

**Files:**
- Delete: `internal/feishu/router.go`
- Delete: `internal/feishu/router_test.go`
- Modify: `internal/feishu/handler.go` — change `*Router` → `*botrouter.Router`
- Modify: `internal/feishu/client.go` — use `botrouter.NewRouter`

- [ ] **Step 1: Delete feishu router files**

> Note: The spec says "update import path" for these files, but full deletion is the correct approach — once `botrouter` holds the implementation and tests, there is no remaining purpose for a feishu-level `Router` type or shim. Test coverage moves entirely to `internal/botrouter`.

```bash
rm internal/feishu/router.go internal/feishu/router_test.go
```

- [ ] **Step 2: Update `internal/feishu/handler.go`**

Change the import block and `Handler` struct. Replace:

```go
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
```

With:

```go
import (
	"context"
	"encoding/json"
	"log"
	"time"

	lark "github.com/larksuite/oapi-sdk-go/v3"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"

	"github.com/robobee/core/internal/botrouter"
	"github.com/robobee/core/internal/model"
	"github.com/robobee/core/internal/store"
	"github.com/robobee/core/internal/worker"
)
```

Replace the `Handler` struct and constructor:

```go
type Handler struct {
	larkClient   *lark.Client
	router       *botrouter.Router
	sessionStore *store.FeishuSessionStore
	manager      *worker.Manager
}

func NewHandler(
	larkClient *lark.Client,
	router *botrouter.Router,
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
```

- [ ] **Step 3: Update `internal/feishu/client.go`**

Change the import block and the `NewRouter` call. Replace:

```go
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
```

With:

```go
import (
	"context"
	"log"

	lark "github.com/larksuite/oapi-sdk-go/v3"
	larkcore "github.com/larksuite/oapi-sdk-go/v3/core"
	"github.com/larksuite/oapi-sdk-go/v3/event/dispatcher"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
	larkws "github.com/larksuite/oapi-sdk-go/v3/ws"

	"github.com/robobee/core/internal/ai"
	"github.com/robobee/core/internal/botrouter"
	"github.com/robobee/core/internal/config"
	"github.com/robobee/core/internal/store"
	"github.com/robobee/core/internal/worker"
)
```

In the `Start` function body, replace `NewRouter(aiClient, workerStore)` with `botrouter.NewRouter(aiClient, workerStore)`.

- [ ] **Step 4: Verify feishu builds and tests pass**

```bash
go build ./internal/feishu/...
go test ./internal/feishu/...
```

Expected: PASS — feishu router tests are deleted; all router coverage now lives in `internal/botrouter`. No feishu-specific unit tests remain after this task.

- [ ] **Step 5: Verify full build**

```bash
go build ./...
```

Expected: no errors

- [ ] **Step 6: Commit**

```bash
git add internal/feishu/
git commit -m "refactor: feishu uses shared botrouter package"
```

---

## Chunk 2: DingTalk session store, config, and DB schema

### Task 3: Add `dingtalk_sessions` table migration

**Files:**
- Modify: `internal/store/db.go`

- [ ] **Step 1: Add migration to `internal/store/db.go`**

In the `migrations` slice in `migrate()`, append after the `feishu_sessions` entry:

```go
`CREATE TABLE IF NOT EXISTS dingtalk_sessions (
    chat_id             TEXT NOT NULL,
    worker_id           TEXT NOT NULL,
    session_id          TEXT NOT NULL,
    last_execution_id   TEXT NOT NULL DEFAULT '',
    updated_at          DATETIME NOT NULL DEFAULT (datetime('now')),
    PRIMARY KEY (chat_id)
)`,
```

- [ ] **Step 2: Run store tests to verify migration doesn't break anything**

```bash
go test ./internal/store/...
```

Expected: PASS

- [ ] **Step 3: Commit**

```bash
git add internal/store/db.go
git commit -m "feat: add dingtalk_sessions table migration"
```

---

### Task 4: Add DingTalk session store with tests (TDD)

**Files:**
- Create: `internal/store/dingtalk_session_store_test.go`
- Create: `internal/store/dingtalk_session_store.go`

- [ ] **Step 1: Write the failing tests**

Create `internal/store/dingtalk_session_store_test.go`:

```go
package store_test

import (
	"testing"

	"github.com/robobee/core/internal/store"
)

func newDingTalkSessionStore(t *testing.T) *store.DingTalkSessionStore {
	t.Helper()
	db, err := store.InitDB(":memory:")
	if err != nil {
		t.Fatalf("init db: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return store.NewDingTalkSessionStore(db)
}

func TestDingTalkSessionStore_GetSession_NotFound(t *testing.T) {
	s := newDingTalkSessionStore(t)
	sess, err := s.GetSession("nonexistent")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if sess != nil {
		t.Fatalf("expected nil, got %+v", sess)
	}
}

func TestDingTalkSessionStore_UpsertAndGet(t *testing.T) {
	s := newDingTalkSessionStore(t)

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

func TestDingTalkSessionStore_Upsert_Updates(t *testing.T) {
	s := newDingTalkSessionStore(t)

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
go test ./internal/store/... -run TestDingTalk
```

Expected: compile error — `store.DingTalkSessionStore` not defined.

- [ ] **Step 3: Implement `internal/store/dingtalk_session_store.go`**

```go
package store

import (
	"database/sql"
	"time"
)

type DingTalkSession struct {
	ChatID          string
	WorkerID        string
	SessionID       string
	LastExecutionID string
	UpdatedAt       time.Time
}

type DingTalkSessionStore struct {
	db *sql.DB
}

func NewDingTalkSessionStore(db *sql.DB) *DingTalkSessionStore {
	return &DingTalkSessionStore{db: db}
}

// GetSession returns nil if not found.
func (s *DingTalkSessionStore) GetSession(chatID string) (*DingTalkSession, error) {
	row := s.db.QueryRow(
		`SELECT chat_id, worker_id, session_id, last_execution_id, updated_at
         FROM dingtalk_sessions WHERE chat_id = ?`, chatID)

	var sess DingTalkSession
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
func (s *DingTalkSessionStore) UpsertSession(chatID, workerID, sessionID, lastExecutionID string) error {
	_, err := s.db.Exec(
		`INSERT INTO dingtalk_sessions (chat_id, worker_id, session_id, last_execution_id, updated_at)
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
go test ./internal/store/...
```

Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/store/dingtalk_session_store.go internal/store/dingtalk_session_store_test.go
git commit -m "feat: add DingTalk session store with tests"
```

---

### Task 5: Add DingTalk config

**Files:**
- Modify: `internal/config/config.go`
- Modify: `config.example.yaml`

- [ ] **Step 1: Add `DingTalkConfig` to `internal/config/config.go`**

Add the struct after `FeishuConfig`:

```go
type DingTalkConfig struct {
	Enabled      bool   `yaml:"enabled"`
	ClientID     string `yaml:"client_id"`
	ClientSecret string `yaml:"client_secret"`
}
```

Add `DingTalk DingTalkConfig` field to the `Config` struct:

```go
type Config struct {
	Server   ServerConfig   `yaml:"server"`
	Database DatabaseConfig `yaml:"database"`
	Workers  WorkersConfig  `yaml:"workers"`
	Runtime  RuntimeConfig  `yaml:"runtime"`
	AI       AIConfig       `yaml:"ai"`
	Feishu   FeishuConfig   `yaml:"feishu"`
	DingTalk DingTalkConfig `yaml:"dingtalk"`
}
```

- [ ] **Step 2: Add DingTalk section to `config.example.yaml`**

Append after the `feishu:` block:

```yaml
dingtalk:
  enabled: false
  client_id: ""
  client_secret: ""
```

- [ ] **Step 3: Verify build**

```bash
go build ./...
```

Expected: no errors

- [ ] **Step 4: Commit**

```bash
git add internal/config/config.go config.example.yaml
git commit -m "feat: add DingTalk config struct"
```

---

## Chunk 3: DingTalk client, handler, and server wiring

### Task 6: Add DingTalk SDK dependency

**Files:**
- Modify: `go.mod`, `go.sum`

- [ ] **Step 1: Add SDK dependency**

```bash
go get github.com/open-dingtalk/dingtalk-stream-sdk-go
```

- [ ] **Step 2: Verify build still passes**

```bash
go build ./...
```

Expected: no errors

- [ ] **Step 3: Commit**

```bash
git add go.mod go.sum
git commit -m "chore: add dingtalk-stream-sdk-go dependency"
```

---

### Task 7: Implement DingTalk handler

**Files:**
- Create: `internal/dingtalk/handler.go`

- [ ] **Step 1: Create `internal/dingtalk/handler.go`**

```go
package dingtalk

import (
	"context"
	"log"
	"strings"
	"time"

	"github.com/open-dingtalk/dingtalk-stream-sdk-go/chatbot"

	"github.com/robobee/core/internal/botrouter"
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
	router       *botrouter.Router
	sessionStore *store.DingTalkSessionStore
	manager      *worker.Manager
}

func NewHandler(
	router *botrouter.Router,
	sessionStore *store.DingTalkSessionStore,
	manager *worker.Manager,
) *Handler {
	return &Handler{
		router:       router,
		sessionStore: sessionStore,
		manager:      manager,
	}
}

func (h *Handler) OnMessage(ctx context.Context, data *chatbot.BotCallbackDataModel) ([]byte, error) {
	text := strings.TrimSpace(data.Text.Content)
	if text == "" {
		return []byte(""), nil
	}

	chatID := data.ConversationId
	sessionWebhook := data.SessionWebhook

	// Acknowledge immediately.
	h.sendMessage(ctx, sessionWebhook, ackMessage)

	// Process asynchronously.
	// Known limitation: uses context.Background() — goroutine outlives server shutdown.
	go h.process(chatID, sessionWebhook, text)
	return []byte(""), nil
}

func (h *Handler) process(chatID, sessionWebhook, text string) {
	ctx := context.Background()

	workerID, err := h.router.Route(ctx, text)
	if err != nil {
		log.Printf("dingtalk: route error: %v", err)
		h.sendMessage(ctx, sessionWebhook, noWorkerMsg)
		return
	}

	sess, err := h.sessionStore.GetSession(chatID)
	if err != nil {
		log.Printf("dingtalk: get session error: %v", err)
		h.sendMessage(ctx, sessionWebhook, errorMessage)
		return
	}

	var exec model.WorkerExecution
	if sess != nil && sess.LastExecutionID != "" {
		exec, err = h.manager.ReplyExecution(ctx, sess.LastExecutionID, text)
	} else {
		exec, err = h.manager.ExecuteWorker(ctx, workerID, text)
	}
	if err != nil {
		log.Printf("dingtalk: execute error: %v", err)
		h.sendMessage(ctx, sessionWebhook, errorMessage)
		return
	}

	if err := h.sessionStore.UpsertSession(chatID, workerID, exec.SessionID, exec.ID); err != nil {
		log.Printf("dingtalk: upsert session error: %v", err)
	}

	result := h.waitForResult(exec.ID)
	h.sendMessage(ctx, sessionWebhook, result)
}

// waitForResult polls execution status every 2s until completed/failed or 30m timeout.
func (h *Handler) waitForResult(executionID string) string {
	deadline := time.Now().Add(pollTimeout)
	for time.Now().Before(deadline) {
		exec, err := h.manager.GetExecution(executionID)
		if err != nil {
			log.Printf("dingtalk: poll execution error: %v", err)
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

func (h *Handler) sendMessage(ctx context.Context, sessionWebhook, text string) {
	replier := chatbot.NewChatbotReplier()
	if err := replier.SimpleReplyText(ctx, sessionWebhook, []byte(text)); err != nil {
		log.Printf("dingtalk: send message error: %v", err)
	}
}
```

- [ ] **Step 2: Verify compilation**

```bash
go build ./internal/dingtalk/...
```

Expected: no errors

- [ ] **Step 3: Commit**

```bash
git add internal/dingtalk/handler.go
git commit -m "feat: add DingTalk message handler"
```

---

### Task 8: Implement DingTalk client

**Files:**
- Create: `internal/dingtalk/client.go`

- [ ] **Step 1: Create `internal/dingtalk/client.go`**

```go
package dingtalk

import (
	"context"
	"log"

	"github.com/open-dingtalk/dingtalk-stream-sdk-go/chatbot"
	"github.com/open-dingtalk/dingtalk-stream-sdk-go/client"

	"github.com/robobee/core/internal/ai"
	"github.com/robobee/core/internal/botrouter"
	"github.com/robobee/core/internal/config"
	"github.com/robobee/core/internal/store"
	"github.com/robobee/core/internal/worker"
)

// Start connects to DingTalk via stream SDK and blocks until the process exits.
// Call this in a goroutine from main.go.
func Start(
	ctx context.Context,
	cfg config.DingTalkConfig,
	workerStore *store.WorkerStore,
	sessionStore *store.DingTalkSessionStore,
	mgr *worker.Manager,
	aiClient *ai.Client,
) error {
	router := botrouter.NewRouter(aiClient, workerStore)
	handler := NewHandler(router, sessionStore, mgr)

	cli := client.NewStreamClient(
		client.WithAppCredential(client.NewAppCredentialConfig(cfg.ClientID, cfg.ClientSecret)),
	)
	cli.RegisterChatBotCallbackRouter(handler.OnMessage)

	log.Println("DingTalk bot starting...")
	return cli.Start(ctx)
}
```

- [ ] **Step 2: Verify compilation**

```bash
go build ./internal/dingtalk/...
```

Expected: no errors

- [ ] **Step 3: Commit**

```bash
git add internal/dingtalk/client.go
git commit -m "feat: add DingTalk stream client startup"
```

---

### Task 9: Wire DingTalk into server startup

**Files:**
- Modify: `cmd/server/main.go`

- [ ] **Step 1: Add DingTalk import and startup to `cmd/server/main.go`**

Add `"github.com/robobee/core/internal/dingtalk"` to the imports block.

After the Feishu block (lines 57-64), add:

```go
// Start DingTalk bot if enabled.
// Known limitation: uses context.Background() so the stream client won't receive
// a cancellation signal on shutdown — os.Exit(0) terminates it abruptly.
if cfg.DingTalk.Enabled {
    dingtalkSessionStore := store.NewDingTalkSessionStore(db)
    go func() {
        if err := dingtalk.Start(context.Background(), cfg.DingTalk, workerStore, dingtalkSessionStore, mgr, aiClient); err != nil {
            log.Printf("dingtalk bot error: %v", err)
        }
    }()
}
```

- [ ] **Step 2: Verify full build**

```bash
go build ./...
```

Expected: no errors

- [ ] **Step 3: Run all tests**

```bash
go test ./...
```

Expected: all PASS

- [ ] **Step 4: Commit**

```bash
git add cmd/server/main.go
git commit -m "feat: wire DingTalk bot into server startup"
```
