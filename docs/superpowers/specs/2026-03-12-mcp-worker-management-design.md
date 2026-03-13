# MCP Worker Management Service — Design Spec

**Date:** 2026-03-12
**Status:** Approved

---

## Overview

Add an MCP (Model Context Protocol) server to the existing robobee/core system, allowing LLMs to manage Workers via the MCP protocol over HTTP SSE. The MCP server integrates into the existing Gin HTTP server as a new route group, reusing the existing `worker.Manager` and `store.WorkerStore` without modification.

---

## Architecture

### Transport

MCP uses JSON-RPC 2.0 over HTTP SSE:

- `GET /mcp/sse` — Client establishes a long-lived SSE connection to receive server-sent responses.
- `POST /mcp/messages` — Client sends JSON-RPC requests. Responses are pushed over the SSE connection.

### Data Flow

```
LLM Client
  ├─ GET  /mcp/sse      ← SSE long connection (receives responses)
  └─ POST /mcp/messages ← JSON-RPC requests (with API Key header)
          │
          ▼
    internal/mcp/
      server.go   ← Session management, SSE push, JSON-RPC dispatch
      tools.go    ← Tool schema definitions and handler implementations
      auth.go     ← API Key middleware
          │
          ▼
    worker.Manager   ← Existing business logic (unchanged)
    store.WorkerStore ← Existing data layer (unchanged)
```

### Authentication

- Config field: `mcp.api_key` in `config.yaml`
- Mechanism: `X-API-Key` request header on both SSE connection and POST requests
- On mismatch: HTTP 401 Unauthorized
- If `mcp.api_key` is empty, MCP routes are not registered

---

## MCP Tools

Five tools covering full Worker CRUD:

### `list_workers`
- **Description:** List all workers
- **Parameters:** none
- **Returns:** JSON array of Worker objects

### `get_worker`
- **Description:** Get a single worker by ID
- **Parameters:** `worker_id` (string, required)
- **Returns:** Worker object or error

### `create_worker`
- **Description:** Create a new worker
- **Parameters:**
  - `name` (string, required)
  - `description` (string, optional)
  - `prompt` (string, optional)
  - `work_dir` (string, optional) — defaults to system-assigned directory
- **Returns:** Created Worker object

### `update_worker`
- **Description:** Update an existing worker's metadata
- **Parameters:**
  - `worker_id` (string, required)
  - `name` (string, optional)
  - `description` (string, optional)
  - `prompt` (string, optional)
- **Returns:** Updated Worker object

### `delete_worker`
- **Description:** Delete a worker
- **Parameters:**
  - `worker_id` (string, required)
  - `delete_work_dir` (bool, optional, default false) — also deletes the worker's working directory
- **Returns:** `{"status": "deleted"}` or error

---

## File Changes

### New Files

```
internal/mcp/
  server.go     # MCPServer struct, SSE session management, JSON-RPC dispatch
  tools.go      # Tool schema definitions and handler implementations
  auth.go       # API Key middleware
```

### Modified Files

| File | Change |
|------|--------|
| `internal/api/router.go` | Register `/mcp/sse` and `/mcp/messages` routes |
| `internal/config/config.go` | Add `MCP struct { APIKey string }` field |
| `config.example.yaml` | Add `mcp.api_key` example entry |
| `cmd/server/main.go` | Inject `mcp.NewServer` into existing server setup |

---

## Dependencies

- No new external Go dependencies. The MCP protocol layer (JSON-RPC 2.0 + SSE) is implemented directly using Go's standard library (`net/http`, `encoding/json`).
- `mcp.NewServer(workerStore *store.WorkerStore, manager *worker.Manager)` — depends only on existing components.

---

## Error Handling

- JSON-RPC errors use standard error codes: `-32600` (invalid request), `-32601` (method not found), `-32602` (invalid params), `-32603` (internal error).
- Business errors (worker not found, etc.) returned as JSON-RPC error responses with descriptive messages.
- SSE connections that disconnect are cleaned up immediately.

---

## Out of Scope

- Worker execution management (execute, get_execution, stop_execution) — not included in this iteration.
- stdio MCP transport — HTTP SSE only.
- Per-user authorization — single shared API key.
