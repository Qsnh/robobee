# Config Consistency Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Eliminate duplicate config structs, standardize config injection across components, and split `main.go` into a thin entry point + `app.go` assembly layer.

**Architecture:** Four sequential tasks, each ending in a compilable and tested state. Config changes are additive first; bee and worker are updated next; `main.go` is replaced last.

**Tech Stack:** Go, `gopkg.in/yaml.v3`, `go test`

**Spec:** `docs/superpowers/specs/2026-03-12-config-consistency-design.md`

---

## Chunk 1: Config and Worker

### Task 1: Add derived fields to `config.BeeConfig`

**Files:**
- Modify: `internal/config/config.go`
- Modify: `internal/config/config_bee_test.go`

- [ ] **Step 1: Write failing tests for derived fields**

Add to `internal/config/config_bee_test.go`:

```go
func TestBeeConfig_DerivedFields(t *testing.T) {
	f, _ := os.CreateTemp("", "*.yaml")
	f.WriteString(`
server:
  host: "localhost"
  port: 8080
mcp:
  api_key: "test-key"
runtime:
  claude_code:
    binary: "claude-custom"
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

	if cfg.Bee.MCPBaseURL != "http://localhost:8080" {
		t.Errorf("MCPBaseURL: want http://localhost:8080 got %q", cfg.Bee.MCPBaseURL)
	}
	if cfg.Bee.MCPAPIKey != "test-key" {
		t.Errorf("MCPAPIKey: want test-key got %q", cfg.Bee.MCPAPIKey)
	}
	if cfg.Bee.Binary != "claude-custom" {
		t.Errorf("Binary: want claude-custom got %q", cfg.Bee.Binary)
	}
}

func TestBeeConfig_BinaryDefault(t *testing.T) {
	f, _ := os.CreateTemp("", "*.yaml")
	f.WriteString(`
server:
  host: "localhost"
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

	if cfg.Bee.Binary != "claude" {
		t.Errorf("Binary default: want claude got %q", cfg.Bee.Binary)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

```bash
cd /Users/tengteng/work/robobee/core
go test ./internal/config/...
```

Expected: FAIL — `cfg.Bee.MCPBaseURL`, `cfg.Bee.MCPAPIKey`, `cfg.Bee.Binary` fields don't exist yet.

- [ ] **Step 3: Add derived fields to `BeeConfig` struct**

In `internal/config/config.go`, update the `BeeConfig` struct:

```go
type BeeConfig struct {
	Name    string       `yaml:"name"`
	WorkDir string       `yaml:"work_dir"`
	Persona string       `yaml:"persona"`
	Feeder  FeederConfig `yaml:"feeder"`

	// Derived fields — not in YAML, computed by Load()
	MCPBaseURL string `yaml:"-"` // http://host:port (no path suffix)
	MCPAPIKey  string `yaml:"-"` // copied from MCPConfig.APIKey
	Binary     string `yaml:"-"` // copied from Runtime.ClaudeCode.Binary
}
```

- [ ] **Step 4: Add `Binary` default in `applyDefaults`**

In `applyDefaults`, add after the existing bee defaults:

```go
if cfg.Runtime.ClaudeCode.Binary == "" {
	cfg.Runtime.ClaudeCode.Binary = "claude"
}
```

- [ ] **Step 5: Compute derived fields in `Load()`**

In `Load()`, add these three lines after `applyDefaults(&cfg)` and before `return cfg, nil`:

```go
cfg.Bee.MCPBaseURL = fmt.Sprintf("http://%s:%d", cfg.Server.Host, cfg.Server.Port)
cfg.Bee.MCPAPIKey = cfg.MCP.APIKey
cfg.Bee.Binary = cfg.Runtime.ClaudeCode.Binary
```

- [ ] **Step 6: Run tests to verify they pass**

```bash
go test ./internal/config/...
```

Expected: PASS

- [ ] **Step 7: Commit**

```bash
git add internal/config/config.go internal/config/config_bee_test.go
git commit -m "feat(config): add derived fields MCPBaseURL, MCPAPIKey, Binary to BeeConfig"
```

---

### Task 2: Narrow `worker.Manager` config injection

**Files:**
- Modify: `internal/worker/manager.go`
- Modify: `cmd/server/main.go` (update only the `NewManager` call site)

- [ ] **Step 1: Update `Manager` struct, `NewManager` signature, and all internal `m.cfg.*` accesses in a single edit**

> **Important:** Removing the `cfg config.Config` field from the struct will break compilation until all `m.cfg.*` usages are updated. Do both sub-steps as one combined edit before building.

**1a.** Replace the struct and constructor in `internal/worker/manager.go`:

```go
type Manager struct {
	workersCfg     config.WorkersConfig
	runtimeCfg     config.RuntimeConfig
	workerStore    *store.WorkerStore
	executionStore *store.ExecutionStore

	activeRuntimes map[string]Runtime
	logSubscribers map[string][]chan Output
	mu             sync.RWMutex
}

func NewManager(
	wc config.WorkersConfig,
	rc config.RuntimeConfig,
	ws *store.WorkerStore,
	es *store.ExecutionStore,
) *Manager {
	return &Manager{
		workersCfg:     wc,
		runtimeCfg:     rc,
		workerStore:    ws,
		executionStore: es,
		activeRuntimes: make(map[string]Runtime),
		logSubscribers: make(map[string][]chan Output),
	}
}
```

**1b.** Replace every `m.cfg.*` access in the same file. Run `grep -n "m\.cfg\." internal/worker/manager.go` to find all occurrences, then update:

| Old | New | Found in |
|-----|-----|----------|
| `m.cfg.Workers.BaseDir` | `m.workersCfg.BaseDir` | `CreateWorker` |
| `m.cfg.Runtime.ClaudeCode.Binary` | `m.runtimeCfg.ClaudeCode.Binary` | `ExecuteWorker`, `ExecuteWorkerWithSession`, `ReplyExecution` |
| `m.cfg.Runtime.ClaudeCode.Timeout` | `m.runtimeCfg.ClaudeCode.Timeout` | `ExecuteWorker`, `ExecuteWorkerWithSession`, `ReplyExecution` |

Note: `launchRuntime` is called by the above functions but has no direct `m.cfg.*` accesses of its own. If `grep` finds no occurrences there, that is expected.

- [ ] **Step 2: Update the `NewManager` call in `cmd/server/main.go`**

Find the line `mgr := worker.NewManager(cfg, workerStore, execStore)` and change to:
```go
mgr := worker.NewManager(cfg.Workers, cfg.Runtime, workerStore, execStore)
```

- [ ] **Step 3: Verify compilation and tests pass**

```bash
go build ./...
go test ./internal/worker/...
```

Expected: no errors.

- [ ] **Step 4: Commit**

```bash
git add internal/worker/manager.go cmd/server/main.go
git commit -m "refactor(worker): narrow Manager config to WorkersConfig + RuntimeConfig"
```

---

## Chunk 2: Bee and Server

### Task 3: Delete `bee.FeederConfig`, update bee package to use `config.BeeConfig`

**Files:**
- Modify: `internal/bee/feeder.go`
- Modify: `internal/bee/feeder_test.go`
- Modify: `internal/bee/bee_process.go`
- Modify: `cmd/server/main.go` (update bee construction call sites)

- [ ] **Step 1: Update `feeder_test.go` to use `config.BeeConfig`**

Replace the `newFeeder` helper (lines 65-75 of `feeder_test.go`):

```go
func newFeeder(ms *store.MessageStore, ts *store.TaskStore, ss *store.SessionStore, runner bee.BeeRunner) *bee.Feeder {
	clearCh := make(chan dispatcher.DispatchTask, 10)
	cfg := config.BeeConfig{}
	cfg.Feeder.Interval = 50 * time.Millisecond
	cfg.Feeder.BatchSize = 10
	cfg.Feeder.Timeout = 5 * time.Second
	cfg.Feeder.QueueWarnThreshold = 100
	cfg.WorkDir = "/tmp"
	return bee.NewFeeder(ms, ts, ss, runner, clearCh, cfg)
}
```

Add `"github.com/robobee/core/internal/config"` to the test file imports.

- [ ] **Step 2: Update `feeder.go` — replace `FeederConfig` with `config.BeeConfig`**

In `internal/bee/feeder.go`:

1. Delete the `FeederConfig` struct (lines 17-28).
2. Add import `"github.com/robobee/core/internal/config"`.
3. Change the `cfg` field in the `Feeder` struct from `FeederConfig` to `config.BeeConfig`.
4. Update `NewFeeder` signature:
   ```go
   func NewFeeder(ms *store.MessageStore, ts *store.TaskStore, ss *store.SessionStore, runner BeeRunner, clearCh chan<- dispatcher.DispatchTask, cfg config.BeeConfig) *Feeder {
   ```
5. Update all internal field accesses (field names are unchanged, only the struct type changes):
   - `f.cfg.Interval` → `f.cfg.Feeder.Interval`
   - `f.cfg.BatchSize` → `f.cfg.Feeder.BatchSize`
   - `f.cfg.Timeout` → `f.cfg.Feeder.Timeout`
   - `f.cfg.QueueWarnThreshold` → `f.cfg.Feeder.QueueWarnThreshold`
   - `f.cfg.WorkDir` → `f.cfg.WorkDir` (unchanged)
   - `f.cfg.Persona` → `f.cfg.Persona` (unchanged)

- [ ] **Step 3: Update `bee_process.go` — replace three-arg constructor with `config.BeeConfig`**

Replace `NewBeeProcess` in `internal/bee/bee_process.go`:

```go
// NewBeeProcess creates a BeeProcess.
func NewBeeProcess(cfg config.BeeConfig) *BeeProcess {
	return &BeeProcess{
		binary: cfg.Binary,
		mcpURL: cfg.MCPBaseURL + "/mcp/sse",
		apiKey: cfg.MCPAPIKey,
	}
}
```

Add import `"github.com/robobee/core/internal/config"`. Remove unused `time` import if present. The `BeeProcess` struct fields (`binary`, `mcpURL`, `apiKey`) and all other methods are unchanged.

- [ ] **Step 4: Update `cmd/server/main.go` bee construction**

The manual `feederCfg` build block and old `NewBeeProcess` call (lines 64-79) will be replaced. Update to:

```go
beeProcess := bee.NewBeeProcess(cfg.Bee)
feeder := bee.NewFeeder(msgStore, taskStore, sessionStore, beeProcess, dispatchCh, cfg.Bee)
```

Remove the now-unused `feederCfg` variable and the `mcpBaseURL` variable (since `cfg.Bee.MCPBaseURL` already has it).

Also update the MCP API key guard to use the derived field:
```go
if cfg.MCP.APIKey == "" {
    log.Fatal("mcp.api_key must be set — bee requires MCP to create tasks")
}
```
(This line is unchanged — keep it as-is.)

- [ ] **Step 5: Run tests**

```bash
go build ./...
go test ./internal/bee/...
```

Expected: all pass.

- [ ] **Step 6: Commit**

```bash
git add internal/bee/feeder.go internal/bee/feeder_test.go internal/bee/bee_process.go cmd/server/main.go
git commit -m "refactor(bee): replace FeederConfig with config.BeeConfig, simplify construction"
```

---

### Task 4: Split `cmd/server/main.go` into `main.go` + `app.go`

**Files:**
- Create: `cmd/server/app.go`
- Modify: `cmd/server/main.go` (full replacement)

Key types and signatures confirmed from source:
- `dispatcher.New(manager, taskStore, sessionStore, in <-chan DispatchTask) *Dispatcher` — 4 args, no msgStore
- `msgsender.New(senders map[string]platform.PlatformSenderAdapter, in <-chan SenderEvent) *msgsender.Gateway`
- `platform.PlatformReceiverAdapter.Start(ctx, dispatch) error` — returns error
- `platform.Platform` interface already covers `ID() + Receiver() + Sender()`
- Maps are reference types: create `sendersByPlatform` before `buildPipeline`, pass it in, populate later

- [ ] **Step 1: Create `cmd/server/app.go`**

```go
package main

import (
	"context"
	"database/sql"
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

// App holds all wired-up components and runs the server.
type App struct {
	db      *sql.DB
	server  *api.Server
	runners []func(ctx context.Context)
	addr    string
}

// Run starts all goroutines, waits for a signal, then shuts down.
func (a *App) Run() {
	ctx, cancel := context.WithCancel(context.Background())

	for _, r := range a.runners {
		r := r
		go r(ctx)
	}

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-quit
		log.Println("Shutting down...")
		cancel()
		a.db.Close()
		os.Exit(0)
	}()

	log.Printf("RoboBee Core starting on %s", a.addr)
	if err := a.server.Run(a.addr); err != nil {
		log.Fatalf("server error: %v", err)
	}
}

// buildApp wires all components together. Returns a ready-to-run App.
func buildApp(cfg config.Config) (*App, error) {
	if cfg.MCP.APIKey == "" {
		log.Fatal("mcp.api_key must be set — bee requires MCP to create tasks")
	}

	db, s, err := buildStores(cfg.Database)
	if err != nil {
		return nil, err
	}

	mgr := buildWorkerManager(cfg.Workers, cfg.Runtime, s)
	mcpSrv := mcp.NewServer(s.workerStore, mgr, s.taskStore)

	dispatchCh := make(chan dispatcher.DispatchTask, 128)

	// Create sender map before pipeline — maps are reference types,
	// so msgsender.New holds the same map and sees entries added below.
	sendersByPlatform := make(map[string]platform.PlatformSenderAdapter)

	feeder, sched := buildBee(cfg.Bee, s, dispatchCh)
	ingest, disp, sender := buildPipeline(cfg.MessageQueue, s, mgr, dispatchCh, sendersByPlatform)
	platforms := buildPlatforms(cfg.Feishu, cfg.DingTalk)

	// Populate sender map before goroutines start
	for _, p := range platforms {
		sendersByPlatform[p.ID()] = p.Sender()
	}

	// Synchronous startup recovery — must run before goroutines start
	feeder.RecoverFeeding(context.Background())
	sched.RecoverRunning(context.Background())

	runners := []func(ctx context.Context){
		func(ctx context.Context) { ingest.Run(ctx) },
		func(ctx context.Context) { feeder.Run(ctx) },
		func(ctx context.Context) { sched.Run(ctx) },
		func(ctx context.Context) { disp.Run(ctx) },
		func(ctx context.Context) { sender.Run(ctx) },
	}
	for _, p := range platforms {
		recv := p.Receiver()
		runners = append(runners, func(ctx context.Context) {
			if err := recv.Start(ctx, ingest.Dispatch); err != nil {
				log.Printf("platform receiver error: %v", err)
			}
		})
	}

	srv := buildAPIServer(cfg.MCP, s, mgr, mcpSrv)
	addr := fmt.Sprintf("%s:%d", cfg.Server.Host, cfg.Server.Port)

	return &App{db: db, server: srv, runners: runners, addr: addr}, nil
}

// appStores groups all store instances for passing to sub-builders.
// Named appStores (not stores) to avoid collision with the store package.
type appStores struct {
	workerStore  *store.WorkerStore
	execStore    *store.ExecutionStore
	msgStore     *store.MessageStore
	taskStore    *store.TaskStore
	sessionStore *store.SessionStore
}

func buildStores(cfg config.DatabaseConfig) (*sql.DB, appStores, error) {
	db, err := store.InitDB(cfg.Path)
	if err != nil {
		return nil, appStores{}, fmt.Errorf("init database: %w", err)
	}
	return db, appStores{
		workerStore:  store.NewWorkerStore(db),
		execStore:    store.NewExecutionStore(db),
		msgStore:     store.NewMessageStore(db),
		taskStore:    store.NewTaskStore(db),
		sessionStore: store.NewSessionStore(db),
	}, nil
}

func buildWorkerManager(wc config.WorkersConfig, rc config.RuntimeConfig, s appStores) *worker.Manager {
	return worker.NewManager(wc, rc, s.workerStore, s.execStore)
}

func buildBee(cfg config.BeeConfig, s appStores, dispatchCh chan dispatcher.DispatchTask) (*bee.Feeder, *taskscheduler.Scheduler) {
	beeProcess := bee.NewBeeProcess(cfg)
	feeder := bee.NewFeeder(s.msgStore, s.taskStore, s.sessionStore, beeProcess, dispatchCh, cfg)
	sched := taskscheduler.New(s.taskStore, dispatchCh, cfg.Feeder.Interval)
	return feeder, sched
}

func buildPipeline(
	cfg config.MessageQueueConfig,
	s appStores,
	mgr *worker.Manager,
	dispatchCh chan dispatcher.DispatchTask,
	senders map[string]platform.PlatformSenderAdapter,
) (*msgingest.Gateway, *dispatcher.Dispatcher, *msgsender.Gateway) {
	ingest := msgingest.New(s.msgStore, cfg.DebounceWindow)
	disp := dispatcher.New(mgr, s.taskStore, s.sessionStore, dispatchCh)
	sender := msgsender.New(senders, disp.Out())
	return ingest, disp, sender
}

func buildPlatforms(fc config.FeishuConfig, dc config.DingTalkConfig) []platform.Platform {
	var result []platform.Platform
	if fc.Enabled {
		result = append(result, feishu.NewPlatform(fc))
	}
	if dc.Enabled {
		result = append(result, dingtalk.NewPlatform(dc))
	}
	return result
}

func buildAPIServer(cfg config.MCPConfig, s appStores, mgr *worker.Manager, mcpSrv *mcp.Server) *api.Server {
	return api.NewServer(s.workerStore, s.execStore, mgr, mcpSrv, cfg.APIKey)
}
```

- [ ] **Step 2: Replace `cmd/server/main.go` with thin entry point**

```go
package main

import (
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

	app, err := buildApp(cfg)
	if err != nil {
		log.Fatalf("failed to build app: %v", err)
	}

	app.Run()
}
```

- [ ] **Step 3: Build and verify no compile errors**

```bash
go build ./...
```

Expected: builds cleanly.

- [ ] **Step 4: Run all tests**

```bash
go test ./...
```

Expected: all pass.

- [ ] **Step 5: Commit**

```bash
git add cmd/server/main.go cmd/server/app.go
git commit -m "refactor(server): split main.go into thin entry point + app.go assembly layer"
```
