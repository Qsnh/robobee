# Design: Replace ai.Client with Claude Code CLI for Routing and Cron

Date: 2026-03-11

## Background

Currently, worker routing (员工分配) and cron expression generation both use `ai.Client`, which makes HTTP calls to an OpenAI-compatible endpoint configured via `config.ai.base_url`. Worker *execution* already uses Claude Code CLI (`claude_runtime.go`). This design replaces the HTTP-based `ai.Client` with Claude Code CLI for the remaining two AI usages.

## Scope

- `botrouter.Router.Route()` — selects the most suitable worker for an incoming message
- `worker.Manager.ResolveCron()` — converts a natural language schedule to a cron expression (internally calls `cronResolver.CronFromDescription`)

## Architecture

### New Files

**`internal/ai/interfaces.go`**

Two narrow interfaces, one per concern. `WorkerSummary` is also defined here (moved from the deleted `client.go`):

```go
type WorkerSummary struct {
    ID          string
    Name        string
    Description string
}

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
claude -p <prompt> --output-format text --allowedTools
```

- `--output-format text` — returns plain text, no stream parsing needed
- `--allowedTools` with no arguments — restricts Claude to zero tools (pure text reasoning, no file/bash access). Verified against Claude Code CLI: flag is `--allowedTools` / `--allowed-tools`, space/comma-separated list; empty list = no tools allowed.
- No `--dangerously-skip-permissions` — not needed when no tools are allowed
- No `cmd.Dir` set — inherits server process working directory (routing/cron prompts are self-contained)
- **Output size cap**: stdout is read with a 64 KiB limit; responses exceeding this are an error
- **Timeout**: `ClaudeCodeClient` wraps the incoming context with a 30-second deadline internally, matching the prior `http.Client` timeout. Callers using `context.Background()` (e.g., cron generation at worker creation) are protected by this internal cap.
- **Binary validation**: `NewClaudeCodeClient` returns an error if the binary path is empty string, failing fast on misconfiguration.

### Modified Files

**`internal/botrouter/router.go`**
- Field `aiClient *ai.Client` → `router ai.WorkerRouter`
- Constructor `NewRouter(aiClient *ai.Client, ...)` → `NewRouter(router ai.WorkerRouter, ...)`
- Empty-workers guard (returning `"no workers available"`) remains the router's responsibility, not the interface's

**`internal/worker/manager.go`**
- Field `aiClient *ai.Client` → `cronResolver ai.CronResolver`
- Constructor updated accordingly
- `m.aiClient.CronFromDescription(...)` → `m.cronResolver.CronFromDescription(...)`
- Public method `ResolveCron` is unchanged; only the internal delegation changes

**`internal/config/config.go`**
- Remove `AIConfig` struct (`BaseURL`, `APIKey`, `Model`)
- Remove `AI AIConfig` field from `Config`

**`config.example.yaml`**
- Remove the `ai:` section (`base_url`, `api_key`, `model`)

**`cmd/server/main.go`**
- Remove `ai.NewClient(cfg.AI)` call
- Inject `ai.NewClaudeCodeClient(cfg.Runtime.ClaudeCode.Binary)` into botrouter and manager

### Deleted Files

- `internal/ai/client.go` — HTTP-based client, no longer needed
- `internal/ai/client_test.go` — tests for deleted client

### Test Updates

**`internal/botrouter/router_test.go`**
- Replace `httptest.NewServer` + `ai.NewClient` setup with a simple mock struct implementing `WorkerRouter`

**`internal/ai/claude_code_client_test.go`** (new)
- Unit tests use a fake binary (a small shell script or Go test helper) on PATH to simulate `claude` output
- Tests cover: valid worker ID returned, valid cron returned, output exceeding 64 KiB limit, subprocess non-zero exit

## Security

For routing and cron queries, Claude Code runs with `--allowedTools` (empty list), preventing any tool invocation. This is more restrictive than the `--dangerously-skip-permissions` flag used by worker execution runtimes.

Note: the `-p` argument embeds user-supplied message content (from Feishu/DingTalk) alongside operator-controlled worker names/descriptions. This carries the same prompt-injection risk as the prior HTTP-based approach. The `--allowedTools ""` restriction ensures that even if a message attempts to manipulate Claude, no tools can be executed. Content leakage between workers is not introduced by this change.

## Configuration

No new config fields required. `ClaudeCodeClient` reuses the existing `config.Runtime.ClaudeCode.Binary` path. The `config.AI` section is removed entirely. Operators must remove the `ai:` block from their config files; the updated `config.example.yaml` reflects this.

## Data Flow

```
Incoming message
      │
      ▼
botrouter.Router.Route()
      │  (guards: no workers → error before AI call)
      ▼
ClaudeCodeClient.RouteToWorker()
      │  runs: claude -p <prompt> --output-format text --allowedTools
      │  timeout: 30s, output cap: 64 KiB
      ▼
worker ID (plain text)
      │
      ▼
worker.Manager.ExecuteWorker()
```

## Out of Scope

- Worker execution runtime (already uses Claude Code CLI, no change)
- Any automated config migration tooling
