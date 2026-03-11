# Replace ai.Client with Claude Code CLI for Routing and Cron — Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace the HTTP-based `ai.Client` (OpenAI-compatible API) with Claude Code CLI invocations for worker routing and cron expression generation.

**Architecture:** Define two narrow interfaces (`WorkerRouter`, `CronResolver`) in a new `interfaces.go`. Implement both via `ClaudeCodeClient` which runs `claude -p <prompt> --output-format text --allowedTools ""` (no tools, 30s timeout, 64 KiB output cap). Consumers (`botrouter.Router`, `worker.Manager`) depend on the interfaces, not the concrete type.

**Tech Stack:** Go stdlib (`os/exec`, `io`, `context`), existing `config.RuntimeEntry` for binary path.

---

## Chunk 1: New AI Layer

### Task 1: Define interfaces and WorkerSummary

**Files:**
- Create: `internal/ai/interfaces.go`
- Delete: `internal/ai/client.go` (deleted in Task 7)

- [ ] **Step 1: Create `internal/ai/interfaces.go`**

```go
package ai

import "context"

// WorkerSummary carries the minimal info needed for routing decisions.
type WorkerSummary struct {
	ID          string
	Name        string
	Description string
}

// WorkerRouter selects the most suitable worker for an incoming message.
type WorkerRouter interface {
	RouteToWorker(ctx context.Context, message string, workers []WorkerSummary) (string, error)
}

// CronResolver converts a natural language schedule description to a cron expression.
type CronResolver interface {
	CronFromDescription(ctx context.Context, description string) (string, error)
}
```

- [ ] **Step 2: Remove duplicate `WorkerSummary` from `internal/ai/client.go`**

`client.go` currently defines `WorkerSummary` at line 102. Delete those lines (the struct definition and its comment) so both files can coexist in the same package without a duplicate-type compile error:

```go
// DELETE from client.go:
// WorkerSummary is used for AI routing decisions.
type WorkerSummary struct {
	ID          string
	Name        string
	Description string
}
```

- [ ] **Step 3: Verify it compiles**

```bash
go build ./internal/ai/...
```
Expected: no errors

- [ ] **Step 4: Commit**

```bash
git add internal/ai/interfaces.go internal/ai/client.go
git commit -m "feat: add WorkerRouter and CronResolver interfaces"
```

---

### Task 2: Implement ClaudeCodeClient

**Files:**
- Create: `internal/ai/claude_code_client.go`

- [ ] **Step 1: Create `internal/ai/claude_code_client.go`**

```go
package ai

import (
	"context"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"time"
)

const (
	maxOutputBytes = 64 * 1024 // 64 KiB
	queryTimeout   = 30 * time.Second
)

// ClaudeCodeClient runs the claude CLI binary for one-shot text queries.
// It implements WorkerRouter and CronResolver.
type ClaudeCodeClient struct {
	binary string
}

// NewClaudeCodeClient creates a client using the given claude binary path.
// Returns an error if binary is empty.
func NewClaudeCodeClient(binary string) (*ClaudeCodeClient, error) {
	if binary == "" {
		return nil, fmt.Errorf("claude binary path must not be empty")
	}
	return &ClaudeCodeClient{binary: binary}, nil
}

// run executes a one-shot claude query and returns the trimmed text output.
// It wraps ctx with a 30-second deadline and caps output at 64 KiB.
// No working directory is set; the process inherits the server's cwd.
// --allowedTools is passed with no values to restrict tool use to none.
func (c *ClaudeCodeClient) run(ctx context.Context, prompt string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, queryTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, c.binary,
		"--output-format", "text",
		"-p", prompt,
		"--allowedTools", "", // empty string arg = empty tool list (no tools allowed)
	)

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return "", fmt.Errorf("stdout pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return "", fmt.Errorf("start claude: %w", err)
	}

	out, readErr := io.ReadAll(io.LimitReader(stdout, int64(maxOutputBytes)+1))
	waitErr := cmd.Wait()

	if readErr != nil {
		return "", fmt.Errorf("read output: %w", readErr)
	}
	if len(out) > maxOutputBytes {
		return "", fmt.Errorf("claude output exceeded %d bytes", maxOutputBytes)
	}
	if waitErr != nil {
		return "", fmt.Errorf("claude exited with error: %w", waitErr)
	}

	return strings.TrimSpace(string(out)), nil
}

// RouteToWorker implements WorkerRouter.
func (c *ClaudeCodeClient) RouteToWorker(ctx context.Context, message string, workers []WorkerSummary) (string, error) {
	var sb strings.Builder
	validIDs := make(map[string]bool, len(workers))
	for _, w := range workers {
		fmt.Fprintf(&sb, "- ID: %s, Name: %s, Description: %s\n", w.ID, w.Name, w.Description)
		validIDs[w.ID] = true
	}

	prompt := fmt.Sprintf(
		"You are a task router. Given a list of workers and a user message, return ONLY the ID of the most suitable worker. No explanation, no markdown, just the ID.\n\nWorkers:\n%s\nUser message: %s",
		sb.String(), message,
	)

	workerID, err := c.run(ctx, prompt)
	if err != nil {
		return "", err
	}
	if !validIDs[workerID] {
		return "", fmt.Errorf("claude returned unknown worker ID %q", workerID)
	}
	return workerID, nil
}

// CronFromDescription implements CronResolver.
func (c *ClaudeCodeClient) CronFromDescription(ctx context.Context, description string) (string, error) {
	prompt := fmt.Sprintf(
		"You are a cron expression generator. Convert the schedule description to a valid 5-field cron expression (minute hour day month weekday). Return ONLY the cron expression, nothing else. No explanations, no markdown.\n\n%s",
		description,
	)

	cron, err := c.run(ctx, prompt)
	if err != nil {
		return "", err
	}
	if cron == "" {
		return "", fmt.Errorf("claude returned empty cron expression")
	}
	return cron, nil
}
```

- [ ] **Step 2: Verify it compiles**

```bash
go build ./internal/ai/...
```
Expected: no errors

- [ ] **Step 3: Commit**

```bash
git add internal/ai/claude_code_client.go
git commit -m "feat: implement ClaudeCodeClient using Claude Code CLI"
```

---

### Task 3: Test ClaudeCodeClient

**Files:**
- Create: `internal/ai/claude_code_client_test.go`

We use a shell-script fake binary per test case — write the script to a temp dir, chmod +x, pass the path as the binary. No network, no real claude binary needed.

- [ ] **Step 1: Write the failing tests**

```go
package ai_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/robobee/core/internal/ai"
)

// fakeClaude creates a shell script that prints output and exits with exitCode.
func fakeClaude(t *testing.T, output string, exitCode int) string {
	t.Helper()
	dir := t.TempDir()
	// Use printf to avoid trailing newline issues; shell %% escapes % in fmt.Sprintf
	script := fmt.Sprintf("#!/bin/sh\nprintf '%%s' %q\nexit %d\n", output, exitCode)
	path := filepath.Join(dir, "claude")
	if err := os.WriteFile(path, []byte(script), 0755); err != nil {
		t.Fatalf("write fake binary: %v", err)
	}
	return path
}

func newClient(t *testing.T, binary string) *ai.ClaudeCodeClient {
	t.Helper()
	c, err := ai.NewClaudeCodeClient(binary)
	if err != nil {
		t.Fatalf("NewClaudeCodeClient: %v", err)
	}
	return c
}

func TestNewClaudeCodeClient_EmptyBinary_ReturnsError(t *testing.T) {
	_, err := ai.NewClaudeCodeClient("")
	if err == nil {
		t.Fatal("expected error for empty binary, got nil")
	}
}

func TestClaudeCodeClient_RouteToWorker_ReturnsValidID(t *testing.T) {
	bin := fakeClaude(t, "w1", 0)
	c := newClient(t, bin)

	workers := []ai.WorkerSummary{
		{ID: "w1", Name: "analyst", Description: "market analysis"},
		{ID: "w2", Name: "coder", Description: "code review"},
	}
	id, err := c.RouteToWorker(context.Background(), "analyze sales data", workers)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if id != "w1" {
		t.Errorf("got %q, want %q", id, "w1")
	}
}

func TestClaudeCodeClient_RouteToWorker_UnknownID_ReturnsError(t *testing.T) {
	bin := fakeClaude(t, "unknown-id", 0)
	c := newClient(t, bin)

	workers := []ai.WorkerSummary{{ID: "w1", Name: "a", Description: "b"}}
	_, err := c.RouteToWorker(context.Background(), "any message", workers)
	if err == nil {
		t.Fatal("expected error for unknown worker ID, got nil")
	}
}

func TestClaudeCodeClient_CronFromDescription_ReturnsCron(t *testing.T) {
	bin := fakeClaude(t, "0 9 * * 1-5", 0)
	c := newClient(t, bin)

	cron, err := c.CronFromDescription(context.Background(), "every weekday at 9am")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cron != "0 9 * * 1-5" {
		t.Errorf("got %q, want %q", cron, "0 9 * * 1-5")
	}
}

func TestClaudeCodeClient_OutputExceedsLimit_ReturnsError(t *testing.T) {
	// Generate output larger than 64 KiB
	huge := strings.Repeat("x", 64*1024+1)
	bin := fakeClaude(t, huge, 0)
	c := newClient(t, bin)

	_, err := c.CronFromDescription(context.Background(), "daily")
	if err == nil {
		t.Fatal("expected error for oversized output, got nil")
	}
}

func TestClaudeCodeClient_NonZeroExit_ReturnsError(t *testing.T) {
	bin := fakeClaude(t, "", 1)
	c := newClient(t, bin)

	_, err := c.CronFromDescription(context.Background(), "daily")
	if err == nil {
		t.Fatal("expected error for non-zero exit, got nil")
	}
}
```

- [ ] **Step 2: Run tests — verify they pass**

```bash
go test ./internal/ai/... -v
```
Expected: all new `TestClaudeCodeClient_*` tests PASS; `client_test.go` tests also still pass (both coexist until Task 7)

- [ ] **Step 3: Commit**

```bash
git add internal/ai/claude_code_client_test.go
git commit -m "test: add ClaudeCodeClient tests with fake binary"
```

---

## Chunk 2: Update Consumers

### Task 4: Update botrouter to use WorkerRouter interface

**Files:**
- Modify: `internal/botrouter/router.go`
- Modify: `internal/botrouter/router_test.go`

- [ ] **Step 1: Write the new router_test.go first (TDD)**

Replace the entire file — the old test used `httptest` + `ai.NewClient`; the new one uses a mock:

```go
package botrouter_test

import (
	"context"
	"testing"

	"github.com/robobee/core/internal/ai"
	"github.com/robobee/core/internal/botrouter"
	"github.com/robobee/core/internal/model"
	"github.com/robobee/core/internal/store"
)

// mockRouter is a test double for ai.WorkerRouter.
type mockRouter struct {
	routeFunc func(message string, workers []ai.WorkerSummary) (string, error)
}

func (m *mockRouter) RouteToWorker(_ context.Context, message string, workers []ai.WorkerSummary) (string, error) {
	return m.routeFunc(message, workers)
}

func newTestRouter(t *testing.T, mock ai.WorkerRouter, workers []model.Worker) *botrouter.Router {
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
	return botrouter.NewRouter(mock, ws)
}

func TestRouter_Route_PicksCorrectWorker(t *testing.T) {
	workers := []model.Worker{
		{ID: "w1", Name: "mas", Description: "market analyst", WorkDir: t.TempDir()},
		{ID: "w2", Name: "nova", Description: "code reviewer", WorkDir: t.TempDir()},
	}

	mock := &mockRouter{
		routeFunc: func(_ string, _ []ai.WorkerSummary) (string, error) {
			return "w1", nil
		},
	}
	router := newTestRouter(t, mock, workers)

	id, err := router.Route(context.Background(), "analyze sales data")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if id != "w1" {
		t.Errorf("got %q, want %q", id, "w1")
	}
}

func TestRouter_Route_NoWorkers_ReturnsError(t *testing.T) {
	mock := &mockRouter{
		routeFunc: func(_ string, _ []ai.WorkerSummary) (string, error) {
			return "", nil
		},
	}
	router := newTestRouter(t, mock, []model.Worker{})

	_, err := router.Route(context.Background(), "some message")
	if err == nil {
		t.Fatal("expected error when no workers available")
	}
}
```

- [ ] **Step 2: Run test — verify it fails (old router.go still uses *ai.Client)**

```bash
go test ./internal/botrouter/... -v
```
Expected: compile error — `botrouter.NewRouter` still expects `*ai.Client`

- [ ] **Step 3: Update `internal/botrouter/router.go`**

```go
package botrouter

import (
	"context"
	"fmt"

	"github.com/robobee/core/internal/ai"
	"github.com/robobee/core/internal/store"
)

type Router struct {
	router      ai.WorkerRouter
	workerStore *store.WorkerStore
}

func NewRouter(router ai.WorkerRouter, workerStore *store.WorkerStore) *Router {
	return &Router{router: router, workerStore: workerStore}
}

// Route returns the worker ID best suited to handle the message.
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

	return r.router.RouteToWorker(ctx, message, summaries)
}
```

- [ ] **Step 4: Run tests — verify they pass**

```bash
go test ./internal/botrouter/... -v
```
Expected: `PASS` for both `TestRouter_Route_PicksCorrectWorker` and `TestRouter_Route_NoWorkers_ReturnsError`

- [ ] **Step 5: Commit**

```bash
git add internal/botrouter/router.go internal/botrouter/router_test.go
git commit -m "feat: botrouter uses WorkerRouter interface instead of ai.Client"
```

---

### Task 5: Update worker.Manager to use CronResolver interface

**Files:**
- Modify: `internal/worker/manager.go`

- [ ] **Step 1: Update `internal/worker/manager.go`**

Change the `aiClient *ai.Client` field to `cronResolver ai.CronResolver` and update usages:

In the struct and constructor (lines 36-61), change:
```go
// BEFORE
type Manager struct {
	cfg            config.Config
	workerStore    *store.WorkerStore
	executionStore *store.ExecutionStore
	aiClient       *ai.Client
	...
}

func NewManager(
	cfg config.Config,
	ws *store.WorkerStore,
	es *store.ExecutionStore,
	aiClient *ai.Client,
) *Manager {
	return &Manager{
		...
		aiClient:       aiClient,
		...
	}
}
```

To:
```go
// AFTER
type Manager struct {
	cfg            config.Config
	workerStore    *store.WorkerStore
	executionStore *store.ExecutionStore
	cronResolver   ai.CronResolver
	...
}

func NewManager(
	cfg config.Config,
	ws *store.WorkerStore,
	es *store.ExecutionStore,
	cronResolver ai.CronResolver,
) *Manager {
	return &Manager{
		...
		cronResolver:   cronResolver,
		...
	}
}
```

Update the two call sites:
- Line 64: `m.aiClient.CronFromDescription(ctx, description)` → `m.cronResolver.CronFromDescription(ctx, description)`
- Line 94: `m.aiClient.CronFromDescription(context.Background(), scheduleDescription)` → `m.cronResolver.CronFromDescription(context.Background(), scheduleDescription)`

Also remove the `"github.com/robobee/core/internal/ai"` import and replace with the same (it's still needed for `ai.CronResolver`).

- [ ] **Step 2: Verify it compiles (main.go will break — that's expected)**

```bash
go build ./internal/worker/...
```
Expected: PASS (`manager.go` compiles; `main.go` in `cmd/server` is not in scope here)

- [ ] **Step 3: Commit**

```bash
git add internal/worker/manager.go
git commit -m "feat: worker.Manager uses CronResolver interface instead of ai.Client"
```

---

## Chunk 3: Wiring and Cleanup

### Task 6: Remove AIConfig from config and update config.example.yaml

**Files:**
- Modify: `internal/config/config.go`
- Modify: `config.example.yaml`

- [ ] **Step 1: Remove `AIConfig` from `internal/config/config.go`**

Delete lines 23-27 (the `AIConfig` struct) and line 17 (`AI AIConfig` field):

```go
// REMOVE these from config.go:
type AIConfig struct {
	BaseURL string `yaml:"base_url"`
	APIKey  string `yaml:"api_key"`
	Model   string `yaml:"model"`
}
// AND remove this field from Config struct:
AI           AIConfig            `yaml:"ai"`
```

After edit, `Config` struct should have no `AI` field and `AIConfig` type should not exist.

- [ ] **Step 2: Remove `ai:` section from `config.example.yaml`**

Delete these lines from `config.example.yaml`:
```yaml
ai:
  base_url: https://api.openai.com/v1
  api_key: ""
  model: gpt-4o-mini
```

- [ ] **Step 3: Verify config package compiles**

```bash
go build ./internal/config/...
```
Expected: PASS

- [ ] **Step 4: Commit**

```bash
git add internal/config/config.go config.example.yaml
git commit -m "feat: remove AIConfig from config (replaced by ClaudeCodeClient)"
```

---

### Task 7: Update main.go wiring and delete old client files

**Files:**
- Modify: `cmd/server/main.go`
- Delete: `internal/ai/client.go`
- Delete: `internal/ai/client_test.go`

- [ ] **Step 1: Update `cmd/server/main.go`**

Replace:
```go
// Initialize AI client
aiClient := ai.NewClient(cfg.AI)

// Create worker manager
mgr := worker.NewManager(cfg, workerStore, execStore, aiClient)
...
router := botrouter.NewRouter(aiClient, workerStore)
```

With:
```go
// Initialize Claude Code client for routing and cron
aiClient, err := ai.NewClaudeCodeClient(cfg.Runtime.ClaudeCode.Binary)
if err != nil {
	log.Fatalf("failed to create AI client: %v", err)
}

// Create worker manager
mgr := worker.NewManager(cfg, workerStore, execStore, aiClient)
...
router := botrouter.NewRouter(aiClient, workerStore)
```

The `"github.com/robobee/core/internal/ai"` import stays; `err` is already declared from `store.InitDB` so use a new `:=` or declare separately.

- [ ] **Step 2: Verify full project compiles**

```bash
go build ./...
```
Expected: PASS — if there are compile errors about `cfg.AI` usage elsewhere, fix them now

- [ ] **Step 3: Delete old HTTP client files**

```bash
rm internal/ai/client.go internal/ai/client_test.go
```

- [ ] **Step 4: Verify project still compiles and all tests pass**

```bash
go build ./...
go test ./...
```
Expected: PASS for build; all tests pass

- [ ] **Step 5: Commit**

```bash
git add cmd/server/main.go
git rm internal/ai/client.go internal/ai/client_test.go
git commit -m "feat: wire ClaudeCodeClient in main, remove old ai.Client"
```
