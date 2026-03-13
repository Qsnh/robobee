# Config Consistency Design

**Date:** 2026-03-12
**Status:** Approved
**Scope:** `internal/config`, `internal/bee`, `internal/worker`, `cmd/server`

---

## Problem

The current configuration system has three consistency issues:

1. **Duplicate struct** — `bee.FeederConfig` mirrors the fields of `config.BeeConfig.Feeder` plus extra fields sourced from other config sections. `main.go` manually copies 9 fields across on startup.

2. **Inconsistent injection** — components receive config in three different styles: full `config.Config` (worker.Manager), decomposed single-field values (`taskscheduler`, `msgingest`), and a custom intermediate struct (`bee.Feeder`). There is no consistent rule.

3. **Bloated main.go** — the 130-line `main()` function owns all component assembly, making it hard to read the startup sequence and understand which config each component uses.

---

## Design

### Part 1: `internal/config` — add derived fields to `BeeConfig`

Add three derived fields to `BeeConfig` that are computed by `Load()` after YAML unmarshalling, not read from YAML:

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

`applyDefaults` gains one new default (for when `Runtime.ClaudeCode.Binary` is not set in YAML):

```go
if cfg.Runtime.ClaudeCode.Binary == "" {
    cfg.Runtime.ClaudeCode.Binary = "claude"
}
```

`Load()` then computes the derived fields **after** the `applyDefaults` call and **before** `return cfg, nil`. Insert these three lines at that position:

```go
cfg.Bee.MCPBaseURL = fmt.Sprintf("http://%s:%d", cfg.Server.Host, cfg.Server.Port)
cfg.Bee.MCPAPIKey  = cfg.MCP.APIKey
cfg.Bee.Binary     = cfg.Runtime.ClaudeCode.Binary
```

Precondition: `Server.Host` and `Server.Port` must be set in the YAML (no defaults are applied for them). If they are empty/zero, `MCPBaseURL` will be malformed. This is the same precondition that exists today in `main.go` and is not changed by this refactor.

This moves the MCP URL construction, API key forwarding, and binary forwarding out of `main.go` and into `config.Load()`, which is the appropriate place for "make the config ready to use".

Note: `config.FeederConfig` (the YAML-mapped nested struct inside `BeeConfig`) is **not** changed — only `bee.FeederConfig` (the intermediate struct in the bee package) is deleted.

### Part 2: `internal/bee` — delete `bee.FeederConfig`

Delete `bee.FeederConfig`. Change `bee.NewFeeder()` and `bee.NewBeeProcess()` to accept `config.BeeConfig` directly:

```go
func NewFeeder(ms *store.MessageStore, ts *store.TaskStore, ss *store.SessionStore, runner BeeRunner, clearCh chan<- dispatcher.DispatchTask, cfg config.BeeConfig) *Feeder
func NewBeeProcess(cfg config.BeeConfig) *BeeProcess
```

The `bee` package will gain a new import on `github.com/robobee/core/internal/config`. This is not a circular import: `config` has no dependency on `bee`.

Inside `NewBeeProcess`, the MCP server URL is constructed by appending the path suffix internally:

```go
mcpServerURL := cfg.MCPBaseURL + "/mcp/sse"
```

This keeps the suffix as an implementation detail of the bee package rather than a config concern. `buildBee` in `app.go` is the sole call site for `NewBeeProcess` after the refactor.

The `Feeder` struct's stored `cfg` field changes type from `bee.FeederConfig` to `config.BeeConfig`. Internal accesses update accordingly (e.g., `cfg.Feeder.Interval`, `cfg.WorkDir`, `cfg.Binary`, `cfg.MCPBaseURL`, `cfg.MCPAPIKey`).

`feeder_test.go` must also be updated to construct a `config.BeeConfig` instead of `bee.FeederConfig`. Before starting, run `grep -r "NewBeeProcess\|bee\.FeederConfig" .` to enumerate all call sites; update each one.

### Part 3: `internal/worker` — narrow Manager's config

Change `Manager` to hold only the config fields it actually uses:

```go
type Manager struct {
    workersCfg  config.WorkersConfig
    runtimeCfg  config.RuntimeConfig
    workerStore *store.WorkerStore
    execStore   *store.ExecutionStore
}

func NewManager(wc config.WorkersConfig, rc config.RuntimeConfig, ws *store.WorkerStore, es *store.ExecutionStore) *Manager
```

All internal usages must be updated from `m.cfg.Workers.BaseDir` → `m.workersCfg.BaseDir` and `m.cfg.Runtime.ClaudeCode.Binary` / `m.cfg.Runtime.ClaudeCode.Timeout` → `m.runtimeCfg.ClaudeCode.Binary` / `m.runtimeCfg.ClaudeCode.Timeout`. These accesses appear in `CreateWorker`, `ExecuteWorker`, `ExecuteWorkerWithSession`, `ReplyExecution`, and `launchRuntime`.

### Part 4: `cmd/server` — split into `main.go` + `app.go`

**`main.go`** is fully replaced (~25 lines). The existing `mgr := worker.NewManager(cfg, ...)` call and all other component initialization are removed from `main.go` entirely — they live exclusively in `app.go` after this change:

```go
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

**`app.go`** defines `App` and `buildApp`, with sub-builders:

| Function | Config params | Other params | Returns |
|---|---|---|---|
| `buildStores(cfg.Database)` | `DatabaseConfig` | — | `*sql.DB`, stores |
| `buildWorkerManager(cfg.Workers, cfg.Runtime, stores)` | `WorkersConfig`, `RuntimeConfig` | stores | `*worker.Manager` |
| `buildBee(cfg.Bee, stores, dispatchCh)` | `BeeConfig` | stores, `dispatchCh` | `*bee.Feeder`, `*bee.BeeProcess`, `*taskscheduler.Scheduler` |
| `buildPipeline(cfg.MessageQueue, stores, mgr, dispatchCh)` | `MessageQueueConfig` | stores, mgr, `dispatchCh` | `*msgingest.Gateway`, `*dispatcher.Dispatcher`, `*msgsender.Sender` |
| `buildPlatforms(cfg.Feishu, cfg.DingTalk)` | `FeishuConfig`, `DingTalkConfig` | — | platform slice (each with sender + receiver) |
| `buildAPIServer(cfg.MCP, stores, mgr, mcpSrv)` | `MCPConfig` | stores, mgr, mcpSrv | `*api.Server` |

Each sub-builder only imports the config fields it needs — no sub-builder receives the full `Config`.

`buildBee` returns `*taskscheduler.Scheduler` alongside `*bee.Feeder` and `*bee.BeeProcess` because the scheduler shares `dispatchCh` and uses `cfg.Bee.Feeder.Interval`.

`buildPlatforms` returns a slice of platform objects; `buildApp` iterates the slice to build the sender map and collects the `p.Receiver().Start` calls as goroutines to launch in `App.Run()`. Each receiver's `Start(ctx, ingest.Dispatch)` is launched as `go p.Receiver().Start(ctx, ingest.Dispatch)` inside `App.Run()`.

`buildApp` calls these in dependency order. After all sub-builders succeed, it calls the two synchronous recovery functions (before returning `App`):

```go
feeder.RecoverFeeding(context.Background())
sched.RecoverRunning(context.Background())
```

The `App` struct holds everything needed for `Run()`:

```go
type App struct {
    db      *sql.DB
    server  *api.Server
    runners []func(ctx context.Context) // feeder, sched, disp, sender, platform receivers
    addr    string
}

func (a *App) Run() { /* create ctx, launch runners as goroutines, handle signals, call a.db.Close() once on shutdown, call a.server.Run(a.addr) */ }
```

`buildApp` assigns the `*sql.DB` returned by `buildStores` to `App.db`. `App.Run()` calls `app.db.Close()` exactly once in the shutdown path (fixing the existing double-close in `main.go` where `defer db.Close()` and `db.Close()` in the signal handler both fire).

---

## Summary of Changes

| File | Change |
|---|---|
| `internal/config/config.go` | Add `MCPBaseURL`, `MCPAPIKey`, `Binary` derived fields to `BeeConfig`; add `Runtime.ClaudeCode.Binary` default in `applyDefaults`; compute derived fields in `Load()` after `applyDefaults` |
| `internal/bee/feeder.go` | Delete `bee.FeederConfig` (not `config.FeederConfig`); `NewFeeder` takes `config.BeeConfig`; stored `cfg` field type changes; internal field accesses updated |
| `internal/bee/feeder_test.go` | Update to use `config.BeeConfig` instead of `bee.FeederConfig` |
| `internal/bee/bee_process.go` | `NewBeeProcess` takes `config.BeeConfig`; appends `/mcp/sse` to `MCPBaseURL` internally |
| `internal/bee/*_test.go` | Any tests constructing `BeeProcess` with old signature updated |
| `internal/worker/manager.go` | `NewManager` takes `WorkersConfig` + `RuntimeConfig`; stored fields renamed; all internal `m.cfg.*` accesses updated across `CreateWorker`, `ExecuteWorker`, `ExecuteWorkerWithSession`, `ReplyExecution`, `launchRuntime` |
| `cmd/server/main.go` | Reduce to config load + `buildApp` + `app.Run` (~25 lines) |
| `cmd/server/app.go` | New file: `App`, `buildApp`, `buildStores`, `buildWorkerManager`, `buildBee`, `buildPipeline`, `buildPlatforms`, `buildAPIServer`; recovery calls in `buildApp`; goroutines + signal handling + `db.Close()` in `App.Run()` |

---

## What This Does Not Change

- YAML config file format (no user-visible changes)
- Config validation (out of scope)
- Environment variable support (out of scope)
- Any business logic
