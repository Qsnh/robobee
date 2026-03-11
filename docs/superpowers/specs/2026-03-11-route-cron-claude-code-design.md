# Design: Replace ai.Client with Claude Code CLI for Routing and Cron

Date: 2026-03-11

## Background

Currently, worker routing (员工分配) and cron expression generation both use `ai.Client`, which makes HTTP calls to an OpenAI-compatible endpoint configured via `config.ai.base_url`. Worker *execution* already uses Claude Code CLI (`claude_runtime.go`). This design replaces the HTTP-based `ai.Client` with Claude Code CLI for the remaining two AI usages.

## Scope

- `botrouter.Router.Route()` — selects the most suitable worker for an incoming message
- `worker.Manager.CronFromDescription()` — converts a natural language schedule to a cron expression

## Architecture

### New Files

**`internal/ai/interfaces.go`**

Two narrow interfaces, one per concern:

```go
type WorkerRouter interface {
    RouteToWorker(ctx context.Context, message string, workers []WorkerSummary) (string, error)
}

type CronResolver interface {
    CronFromDescription(ctx context.Context, description string) (string, error)
}
```

**`internal/ai/claude_code_client.go`**

`ClaudeCodeClient` implements both interfaces. It runs the `claude` binary for one-shot text queries:

```
claude -p <prompt> --output-format text --allowedTools ""
```

- `--output-format text` — returns plain text, no stream parsing needed
- `--allowedTools ""` — restricts Claude to zero tools (pure text reasoning, no file/bash access)
- No `--dangerously-skip-permissions` — not needed when no tools are allowed
- No `cmd.Dir` set — inherits server process working directory (routing/cron prompts are self-contained)

### Modified Files

**`internal/botrouter/router.go`**
- Field `aiClient *ai.Client` → `router ai.WorkerRouter`
- Constructor `NewRouter(aiClient *ai.Client, ...)` → `NewRouter(router ai.WorkerRouter, ...)`

**`internal/worker/manager.go`**
- Field `aiClient *ai.Client` → `cronResolver ai.CronResolver`
- Constructor updated accordingly
- `m.aiClient.CronFromDescription(...)` → `m.cronResolver.CronFromDescription(...)`

**`internal/config/config.go`**
- Remove `AIConfig` struct (`BaseURL`, `APIKey`, `Model`)
- Remove `AI AIConfig` field from `Config`

**`cmd/server/main.go`**
- Remove `ai.NewClient(cfg.AI)` call
- Inject `ai.NewClaudeCodeClient(cfg.Runtime.ClaudeCode.Binary)` into botrouter and manager

### Deleted Files

- `internal/ai/client.go` — HTTP-based client, no longer needed
- `internal/ai/client_test.go` — tests for deleted client

### Test Updates

**`internal/botrouter/router_test.go`**
- Replace `httptest.NewServer` + `ai.NewClient` setup with a simple mock implementing `WorkerRouter`

## Security

For routing and cron queries, Claude Code runs with `--allowedTools ""`, which prevents any tool invocation (no file reads, no bash execution). This is more restrictive and safer than `--dangerously-skip-permissions` used by worker runtimes.

## Configuration

No new config fields required. `ClaudeCodeClient` reuses the existing `config.Runtime.ClaudeCode.Binary` path. The `config.AI` section is removed entirely.

## Data Flow

```
Incoming message
      │
      ▼
botrouter.Router.Route()
      │
      ▼
ClaudeCodeClient.RouteToWorker()
      │  runs: claude -p <prompt> --output-format text --allowedTools ""
      ▼
worker ID (plain text)
      │
      ▼
worker.Manager.ExecuteWorker()
```

## Out of Scope

- Worker execution runtime (already uses Claude Code CLI, no change)
- Any YAML config migration tooling (AIConfig removal is a breaking config change; operators must remove the `ai:` section from their config files)
