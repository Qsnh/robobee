# MCP Worker Management Service — Design Spec

**Date:** 2026-03-12
**Status:** Approved

---

## Overview

Add an MCP (Model Context Protocol) server to the existing robobee/core system, allowing LLMs to manage Workers via the MCP protocol over HTTP SSE. The MCP server integrates into the existing Gin HTTP server as a new route group, reusing the existing `worker.Manager` and `store.WorkerStore` without modification.

The intended MCP client is an LLM tool-use agent (e.g. Claude Desktop, claude-code), not a browser. Browser-initiated SSE via `EventSource` is not supported because it cannot send custom headers.

---

## Architecture

### Transport

MCP uses JSON-RPC 2.0 over HTTP SSE (two-channel pattern):

- `GET /mcp/sse` — Client establishes a long-lived SSE connection. The server immediately sends an `endpoint` event containing a session-scoped POST URL: `data: /mcp/messages?session_id=<uuid>`. The client uses this URL for all subsequent requests.
- `POST /mcp/messages?session_id=<uuid>` — Client sends JSON-RPC requests. Responses are pushed back over the corresponding SSE connection identified by `session_id`.

### Session Lifecycle

1. Client connects to `GET /mcp/sse` (with `X-API-Key` header).
2. Server creates a session (`uuid`), registers a response channel, and sends `event: endpoint\ndata: /mcp/messages?session_id=<uuid>`.
3. Client sends `POST /mcp/messages?session_id=<uuid>` with a JSON-RPC `initialize` request.
4. Server responds with `serverInfo` and `capabilities` over SSE, then awaits an `initialized` notification.
5. Client sends `initialized` notification; session is now ready for tool calls.
6. Client disconnects → server cleans up session channel immediately.

### Data Flow

```
LLM Client
  ├─ GET  /mcp/sse                     ← SSE connection; receives endpoint event + responses
  └─ POST /mcp/messages?session_id=X   ← JSON-RPC requests (with X-API-Key header)
          │
          ▼
    internal/mcp/
      server.go   ← Session management, SSE push, JSON-RPC dispatch
      tools.go    ← Tool schema definitions and handler implementations
      auth.go     ← API Key middleware
          │
          ├─ worker.Manager   ← create_worker, delete_worker
          └─ store.WorkerStore ← list_workers, get_worker, update_worker
```

### Layer Mapping (consistent with existing REST handlers)

| Tool | Calls |
|------|-------|
| `list_workers` | `workerStore.List()` |
| `get_worker` | `workerStore.GetByID()` |
| `create_worker` | `manager.CreateWorker()` (handles dir creation + CLAUDE.md init) |
| `update_worker` | `workerStore.GetByID()` + `workerStore.Update()` |
| `delete_worker` | `manager.DeleteWorker()` (handles optional work_dir removal) |

### Authentication

- Config field: `mcp.api_key` in `config.yaml`
- Mechanism: `X-API-Key` request header on both `GET /mcp/sse` and `POST /mcp/messages`
- On mismatch or missing key: HTTP 401 Unauthorized
- If `mcp.api_key` is empty in config, MCP routes are not registered at all
- The existing CORS middleware `AllowHeaders` must be extended to include `X-API-Key`

---

## MCP Protocol Methods

The server handles the following JSON-RPC methods:

### Lifecycle (required by MCP spec)

- **`initialize`** — Client sends capabilities; server responds with `serverInfo: {name: "robobee-mcp", version: "1.0"}` and `capabilities: {tools: {}}`.
- **`initialized`** — Client notification; server marks session ready. No response sent.
- **`tools/list`** — Returns JSON Schema definitions for all 5 tools (used by clients to discover available tools).

### Tool Invocation

- **`tools/call`** — Dispatches to one of the 5 CRUD tool handlers based on `params.name`.

---

## MCP Tools

### `list_workers`
- **Description:** List all workers
- **Parameters:** none
- **Returns:** JSON array of Worker objects; always `[]` (never `null`) when no workers exist

### `get_worker`
- **Description:** Get a single worker by ID
- **Parameters:** `worker_id` (string, required)
- **Returns:** Worker object or JSON-RPC error if not found

### `create_worker`
- **Description:** Create a new worker (also initializes work directory and CLAUDE.md)
- **Parameters:**
  - `name` (string, required)
  - `description` (string, optional)
  - `prompt` (string, optional)
  - `work_dir` (string, optional) — defaults to `cfg.Workers.BaseDir/<uuid>`
- **Returns:** Created Worker object

### `update_worker`
- **Description:** Update a worker's name, description, or prompt
- **Parameters:**
  - `worker_id` (string, required)
  - `name` (string, optional)
  - `description` (string, optional)
  - `prompt` (string, optional)
- **Note:** `work_dir` is intentionally not updatable (consistent with existing REST API)
- **Returns:** Updated Worker object

### `delete_worker`
- **Description:** Delete a worker
- **Parameters:**
  - `worker_id` (string, required)
  - `delete_work_dir` (bool, optional, default `false`) — also removes the worker's working directory from disk
- **Returns:** `{"status": "deleted"}` or JSON-RPC error

---

## Error Handling

- JSON-RPC standard error codes:
  - `-32600` Invalid request
  - `-32601` Method not found
  - `-32602` Invalid params
  - `-32603` Internal error
- Business errors (e.g. worker not found) returned as JSON-RPC error responses with descriptive `message` field.
- SSE connections that disconnect are detected via context cancellation; session is cleaned up immediately, no goroutine leak.

---

## File Changes

### New Files

```
internal/mcp/
  server.go     # MCPServer struct, SSE session management, JSON-RPC dispatch
  tools.go      # Tool schema definitions (for tools/list) and handler implementations
  auth.go       # API Key middleware (reads from config)
```

### Modified Files

| File | Change |
|------|--------|
| `internal/api/router.go` | Register `/mcp/sse` and `/mcp/messages` routes; add `X-API-Key` to CORS `AllowHeaders` |
| `internal/config/config.go` | Add `MCP struct { APIKey string \`yaml:"api_key"\` }` to Config |
| `config.example.yaml` | Add `mcp:\n  api_key: "your-secret-key"` example |
| `cmd/server/main.go` | Construct `mcp.NewServer(workerStore, manager)` and pass to `api.NewServer` |

### Integration Pattern

`mcp.NewServer(workerStore *store.WorkerStore, manager *worker.Manager) *MCPServer` returns an `MCPServer` that exposes a `RegisterRoutes(rg *gin.RouterGroup)` method. `api.NewServer` receives the `*MCPServer` as an optional parameter and calls `RegisterRoutes` when it is non-nil.

`manager` already encapsulates `config.Config` internally; no config needs to be passed separately to `mcp.NewServer`.

---

## Dependencies

No new external Go dependencies. The MCP protocol layer (JSON-RPC 2.0 + SSE) is implemented using Go standard library (`net/http`, `encoding/json`, `sync`).

---

## Out of Scope

- Worker execution management (execute, get_execution, stop_execution) — not in this iteration.
- stdio MCP transport — HTTP SSE only.
- Per-user authorization — single shared API key.
- Browser `EventSource` clients — not supported (cannot send custom headers).
