# MCP Worker Management Service Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add an MCP (Model Context Protocol) HTTP SSE server to the existing Gin backend, exposing 5 worker CRUD tools so LLMs can manage workers via the MCP protocol.

**Architecture:** A new `internal/mcp` package implements JSON-RPC 2.0 over HTTP SSE (two-channel pattern). `MCPServer` manages sessions (SSE connections keyed by UUID) and dispatches JSON-RPC method calls to tool handlers. It is wired into the existing Gin server as a route group with API Key auth.

**Tech Stack:** Go standard library (`net/http`, `encoding/json`, `sync`), Gin framework (already present), SQLite via existing stores.

---

## File Map

| File | Action | Responsibility |
|------|--------|----------------|
| `internal/config/config.go` | Modify | Add `MCPConfig` struct with `APIKey` field |
| `config.example.yaml` | Modify | Add `mcp.api_key` example |
| `internal/mcp/auth.go` | Create | `APIKeyMiddleware(key string) gin.HandlerFunc` |
| `internal/mcp/auth_test.go` | Create | Tests for auth middleware |
| `internal/mcp/server.go` | Create | `MCPServer`, session map, SSE handler, message handler, JSON-RPC dispatch |
| `internal/mcp/tools.go` | Create | Tool schemas (for `tools/list`) + 5 tool handler functions |
| `internal/mcp/tools_test.go` | Create | Tests for each tool handler |
| `internal/api/router.go` | Modify | Accept `*mcp.MCPServer` param, add `X-API-Key` to CORS, register `/mcp` routes |
| `cmd/server/main.go` | Modify | Construct `mcp.NewServer` when API key is configured, pass to `api.NewServer` |

---

## Chunk 1: Config and Auth Middleware

### Task 1: Add MCP config

**Files:**
- Modify: `internal/config/config.go`
- Modify: `config.example.yaml`

- [ ] **Step 1: Add `MCPConfig` to Config struct**

In `internal/config/config.go`, add the following struct and field:

```go
type MCPConfig struct {
    APIKey string `yaml:"api_key"`
}
```

Add `MCP MCPConfig \`yaml:"mcp"\`` to the `Config` struct after `MessageQueue`.

- [ ] **Step 2: Update `config.example.yaml`**

Append to `config.example.yaml`:

```yaml

mcp:
  api_key: ""  # Set a secret key to enable the MCP server endpoint
```

- [ ] **Step 3: Verify project still builds**

```bash
cd /Users/tengteng/work/robobee/core && go build ./...
```

Expected: no output (success).

- [ ] **Step 4: Commit**

```bash
git add internal/config/config.go config.example.yaml
git commit -m "feat(config): add MCP API key config field"
```

---

### Task 2: API Key middleware

**Files:**
- Create: `internal/mcp/auth.go`
- Create: `internal/mcp/auth_test.go`

- [ ] **Step 1: Write failing test**

Create `internal/mcp/auth_test.go`:

```go
package mcp_test

import (
    "net/http"
    "net/http/httptest"
    "testing"

    "github.com/gin-gonic/gin"
    "github.com/robobee/core/internal/mcp"
)

func init() {
    gin.SetMode(gin.TestMode)
}

func newRouter(key string) *gin.Engine {
    r := gin.New()
    r.Use(mcp.APIKeyMiddleware(key))
    r.GET("/test", func(c *gin.Context) { c.Status(http.StatusOK) })
    return r
}

func TestAPIKeyMiddleware_NoHeader(t *testing.T) {
    r := newRouter("secret")
    w := httptest.NewRecorder()
    req, _ := http.NewRequest(http.MethodGet, "/test", nil)
    r.ServeHTTP(w, req)
    if w.Code != http.StatusUnauthorized {
        t.Errorf("expected 401, got %d", w.Code)
    }
}

func TestAPIKeyMiddleware_WrongKey(t *testing.T) {
    r := newRouter("secret")
    w := httptest.NewRecorder()
    req, _ := http.NewRequest(http.MethodGet, "/test", nil)
    req.Header.Set("X-API-Key", "wrong")
    r.ServeHTTP(w, req)
    if w.Code != http.StatusUnauthorized {
        t.Errorf("expected 401, got %d", w.Code)
    }
}

func TestAPIKeyMiddleware_CorrectKey(t *testing.T) {
    r := newRouter("secret")
    w := httptest.NewRecorder()
    req, _ := http.NewRequest(http.MethodGet, "/test", nil)
    req.Header.Set("X-API-Key", "secret")
    r.ServeHTTP(w, req)
    if w.Code != http.StatusOK {
        t.Errorf("expected 200, got %d", w.Code)
    }
}
```

- [ ] **Step 2: Run test to verify it fails**

```bash
cd /Users/tengteng/work/robobee/core && go test ./internal/mcp/... 2>&1
```

Expected: compile error — package `mcp` does not exist yet.

- [ ] **Step 3: Implement auth middleware**

Create `internal/mcp/auth.go`:

```go
package mcp

import (
    "net/http"

    "github.com/gin-gonic/gin"
)

// APIKeyMiddleware returns a Gin middleware that requires X-API-Key header to match key.
func APIKeyMiddleware(key string) gin.HandlerFunc {
    return func(c *gin.Context) {
        if c.GetHeader("X-API-Key") != key {
            c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
            return
        }
        c.Next()
    }
}
```

- [ ] **Step 4: Run tests to verify they pass**

```bash
cd /Users/tengteng/work/robobee/core && go test ./internal/mcp/... -v -run TestAPIKeyMiddleware
```

Expected: all 3 tests PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/mcp/auth.go internal/mcp/auth_test.go
git commit -m "feat(mcp): add API key auth middleware"
```

---

## Chunk 2: Core MCP Server

### Task 3: JSON-RPC types and MCPServer

**Files:**
- Create: `internal/mcp/server.go`

- [ ] **Step 1: Create `server.go` with JSON-RPC types and MCPServer struct**

Create `internal/mcp/server.go`:

```go
package mcp

import (
    "encoding/json"
    "fmt"
    "net/http"
    "sync"

    "github.com/gin-gonic/gin"
    "github.com/google/uuid"
    "github.com/robobee/core/internal/store"
    "github.com/robobee/core/internal/worker"
)

// JSON-RPC 2.0 types

type rpcRequest struct {
    JSONRPC string          `json:"jsonrpc"`
    ID      any             `json:"id"`
    Method  string          `json:"method"`
    Params  json.RawMessage `json:"params,omitempty"`
}

type rpcResponse struct {
    JSONRPC string    `json:"jsonrpc"`
    ID      any       `json:"id,omitempty"`
    Result  any       `json:"result,omitempty"`
    Error   *rpcError `json:"error,omitempty"`
}

type rpcError struct {
    Code    int    `json:"code"`
    Message string `json:"message"`
}

func errResponse(id any, code int, msg string) rpcResponse {
    return rpcResponse{
        JSONRPC: "2.0",
        ID:      id,
        Error:   &rpcError{Code: code, Message: msg},
    }
}

func okResponse(id any, result any) rpcResponse {
    return rpcResponse{JSONRPC: "2.0", ID: id, Result: result}
}

// MCPServer manages SSE sessions and dispatches JSON-RPC tool calls.
type MCPServer struct {
    workerStore *store.WorkerStore
    manager     *worker.Manager

    mu       sync.Mutex
    sessions map[string]chan rpcResponse // session_id -> response channel
}

// NewServer creates an MCPServer. Call RegisterRoutes to attach it to a Gin router group.
func NewServer(ws *store.WorkerStore, mgr *worker.Manager) *MCPServer {
    return &MCPServer{
        workerStore: ws,
        manager:     mgr,
        sessions:    make(map[string]chan rpcResponse),
    }
}

// RegisterRoutes attaches /sse and /messages to the provided router group.
// The caller is responsible for applying auth middleware to the group before calling this.
func (s *MCPServer) RegisterRoutes(rg *gin.RouterGroup) {
    rg.GET("/sse", s.handleSSE)
    rg.POST("/messages", s.handleMessages)
}

// handleSSE establishes the SSE connection, creates a session, and streams responses.
func (s *MCPServer) handleSSE(c *gin.Context) {
    sessionID := uuid.New().String()
    ch := make(chan rpcResponse, 16)

    s.mu.Lock()
    s.sessions[sessionID] = ch
    s.mu.Unlock()

    defer func() {
        s.mu.Lock()
        delete(s.sessions, sessionID)
        s.mu.Unlock()
    }()

    c.Header("Content-Type", "text/event-stream")
    c.Header("Cache-Control", "no-cache")
    c.Header("Connection", "keep-alive")

    // Send endpoint event so client knows where to POST
    endpointURL := fmt.Sprintf("/mcp/messages?session_id=%s", sessionID)
    fmt.Fprintf(c.Writer, "event: endpoint\ndata: %s\n\n", endpointURL)
    c.Writer.Flush()

    ctx := c.Request.Context()
    for {
        select {
        case <-ctx.Done():
            return
        case resp, ok := <-ch:
            if !ok {
                return
            }
            data, _ := json.Marshal(resp)
            fmt.Fprintf(c.Writer, "event: message\ndata: %s\n\n", data)
            c.Writer.Flush()
        }
    }
}

// handleMessages receives a JSON-RPC request and pushes the response to the SSE channel.
func (s *MCPServer) handleMessages(c *gin.Context) {
    sessionID := c.Query("session_id")

    s.mu.Lock()
    ch, ok := s.sessions[sessionID]
    s.mu.Unlock()

    if !ok {
        c.JSON(http.StatusBadRequest, gin.H{"error": "unknown session_id"})
        return
    }

    var req rpcRequest
    if err := c.ShouldBindJSON(&req); err != nil {
        ch <- errResponse(nil, -32700, "parse error: "+err.Error())
        c.Status(http.StatusAccepted)
        return
    }

    resp := s.dispatch(req)

    // Notifications (no ID) get no response
    if req.ID != nil {
        ch <- resp
    }

    c.Status(http.StatusAccepted)
}

// dispatch routes a JSON-RPC request to the appropriate handler.
func (s *MCPServer) dispatch(req rpcRequest) rpcResponse {
    switch req.Method {
    case "initialize":
        return okResponse(req.ID, map[string]any{
            "protocolVersion": "2024-11-05",
            "serverInfo":      map[string]string{"name": "robobee-mcp", "version": "1.0.0"},
            "capabilities":    map[string]any{"tools": map[string]any{}},
        })

    case "initialized":
        // Notification — no response needed
        return rpcResponse{}

    case "tools/list":
        return okResponse(req.ID, map[string]any{"tools": toolSchemas()})

    case "tools/call":
        return s.handleToolCall(req)

    default:
        return errResponse(req.ID, -32601, fmt.Sprintf("method not found: %s", req.Method))
    }
}

// handleToolCall dispatches tools/call to the appropriate tool handler.
func (s *MCPServer) handleToolCall(req rpcRequest) rpcResponse {
    var params struct {
        Name      string          `json:"name"`
        Arguments json.RawMessage `json:"arguments"`
    }
    if err := json.Unmarshal(req.Params, &params); err != nil {
        return errResponse(req.ID, -32602, "invalid params: "+err.Error())
    }

    result, err := s.callTool(params.Name, params.Arguments)
    if err != nil {
        return errResponse(req.ID, -32603, err.Error())
    }

    data, _ := json.Marshal(result)
    return okResponse(req.ID, map[string]any{
        "content": []map[string]string{{"type": "text", "text": string(data)}},
    })
}
```

- [ ] **Step 2: Build to verify compilation**

```bash
cd /Users/tengteng/work/robobee/core && go build ./internal/mcp/...
```

Expected: compile error about missing `toolSchemas` and `callTool` — that's fine, they come in Task 4.

- [ ] **Step 3: Commit skeleton**

```bash
git add internal/mcp/server.go
git commit -m "feat(mcp): add MCPServer with SSE session management and JSON-RPC dispatch"
```

---

## Chunk 3: Tool Handlers

### Task 4: Tool schemas and handlers

**Files:**
- Create: `internal/mcp/tools.go`
- Create: `internal/mcp/tools_test.go`

- [ ] **Step 1: Write failing tests**

Create `internal/mcp/tools_test.go`:

```go
package mcp_test

import (
    "database/sql"
    "encoding/json"
    "testing"

    _ "github.com/mattn/go-sqlite3"
    "github.com/robobee/core/internal/config"
    "github.com/robobee/core/internal/model"
    "github.com/robobee/core/internal/store"
    "github.com/robobee/core/internal/worker"
    "github.com/robobee/core/internal/mcp"
)

func setupMCPServer(t *testing.T) *mcp.MCPServer {
    t.Helper()
    db, err := store.InitDB(t.TempDir() + "/test.db")
    if err != nil {
        t.Fatalf("InitDB: %v", err)
    }
    t.Cleanup(func() { db.Close() })

    ws := store.NewWorkerStore(db)
    es := store.NewExecutionStore(db)
    cfg := config.Config{
        Workers: config.WorkersConfig{BaseDir: t.TempDir()},
        Runtime: config.RuntimeConfig{
            ClaudeCode: config.RuntimeEntry{Binary: "claude"},
        },
    }
    mgr := worker.NewManager(cfg, ws, es)
    return mcp.NewServer(ws, mgr)
}

func mustMarshal(t *testing.T, v any) json.RawMessage {
    t.Helper()
    b, err := json.Marshal(v)
    if err != nil {
        t.Fatalf("marshal: %v", err)
    }
    return b
}

func TestCallTool_ListWorkers_Empty(t *testing.T) {
    s := setupMCPServer(t)
    result, err := s.CallTool("list_workers", mustMarshal(t, map[string]any{}))
    if err != nil {
        t.Fatalf("CallTool: %v", err)
    }
    workers, ok := result.([]model.Worker)
    if !ok {
        t.Fatalf("expected []model.Worker, got %T", result)
    }
    if len(workers) != 0 {
        t.Errorf("expected empty slice, got %d workers", len(workers))
    }
}

func TestCallTool_CreateWorker(t *testing.T) {
    s := setupMCPServer(t)
    result, err := s.CallTool("create_worker", mustMarshal(t, map[string]any{
        "name":        "TestBot",
        "description": "A test bot",
        "prompt":      "You are a test bot.",
    }))
    if err != nil {
        t.Fatalf("CallTool: %v", err)
    }
    w, ok := result.(model.Worker)
    if !ok {
        t.Fatalf("expected model.Worker, got %T", result)
    }
    if w.ID == "" {
        t.Error("expected non-empty worker ID")
    }
    if w.Name != "TestBot" {
        t.Errorf("expected name TestBot, got %s", w.Name)
    }
}

func TestCallTool_GetWorker(t *testing.T) {
    s := setupMCPServer(t)
    created, _ := s.CallTool("create_worker", mustMarshal(t, map[string]any{"name": "Bot"}))
    w := created.(model.Worker)

    result, err := s.CallTool("get_worker", mustMarshal(t, map[string]any{"worker_id": w.ID}))
    if err != nil {
        t.Fatalf("CallTool: %v", err)
    }
    fetched, ok := result.(model.Worker)
    if !ok {
        t.Fatalf("expected model.Worker, got %T", result)
    }
    if fetched.ID != w.ID {
        t.Errorf("expected ID %s, got %s", w.ID, fetched.ID)
    }
}

func TestCallTool_GetWorker_NotFound(t *testing.T) {
    s := setupMCPServer(t)
    _, err := s.CallTool("get_worker", mustMarshal(t, map[string]any{"worker_id": "nonexistent"}))
    if err == nil {
        t.Error("expected error for missing worker")
    }
}

func TestCallTool_UpdateWorker(t *testing.T) {
    s := setupMCPServer(t)
    created, _ := s.CallTool("create_worker", mustMarshal(t, map[string]any{"name": "OldName"}))
    w := created.(model.Worker)

    result, err := s.CallTool("update_worker", mustMarshal(t, map[string]any{
        "worker_id": w.ID,
        "name":      "NewName",
        "prompt":    "New prompt",
    }))
    if err != nil {
        t.Fatalf("CallTool: %v", err)
    }
    updated := result.(model.Worker)
    if updated.Name != "NewName" {
        t.Errorf("expected NewName, got %s", updated.Name)
    }
    if updated.Prompt != "New prompt" {
        t.Errorf("expected new prompt, got %s", updated.Prompt)
    }
    // Description not provided — should remain unchanged (patch semantics)
    if updated.Description != w.Description {
        t.Errorf("description changed unexpectedly: %s", updated.Description)
    }
}

func TestCallTool_DeleteWorker(t *testing.T) {
    s := setupMCPServer(t)
    created, _ := s.CallTool("create_worker", mustMarshal(t, map[string]any{"name": "Bot"}))
    w := created.(model.Worker)

    _, err := s.CallTool("delete_worker", mustMarshal(t, map[string]any{"worker_id": w.ID}))
    if err != nil {
        t.Fatalf("CallTool: %v", err)
    }

    // Verify it's gone
    _, err = s.CallTool("get_worker", mustMarshal(t, map[string]any{"worker_id": w.ID}))
    if err == nil {
        t.Error("expected error after delete")
    }
}

func TestCallTool_UnknownTool(t *testing.T) {
    s := setupMCPServer(t)
    _, err := s.CallTool("nonexistent_tool", mustMarshal(t, map[string]any{}))
    if err == nil {
        t.Error("expected error for unknown tool")
    }
}

func TestToolSchemas_Count(t *testing.T) {
    schemas := mcp.ToolSchemas()
    if len(schemas) != 5 {
        t.Errorf("expected 5 tool schemas, got %d", len(schemas))
    }
}

func TestListWorkers_ReturnsEmptySlice_NotNull(t *testing.T) {
    s := setupMCPServer(t)
    result, err := s.CallTool("list_workers", mustMarshal(t, map[string]any{}))
    if err != nil {
        t.Fatalf("unexpected error: %v", err)
    }
    // Must be a non-nil slice (serializes as [] not null)
    workers := result.([]model.Worker)
    if workers == nil {
        t.Error("expected non-nil slice, got nil")
    }
}
```

- [ ] **Step 2: Run tests to verify they fail**

```bash
cd /Users/tengteng/work/robobee/core && go test ./internal/mcp/... 2>&1 | head -20
```

Expected: compile errors — `CallTool`, `ToolSchemas` not exported yet.

- [ ] **Step 3: Implement tools.go**

Create `internal/mcp/tools.go`:

```go
package mcp

import (
    "encoding/json"
    "fmt"

    "github.com/robobee/core/internal/model"
)

// toolSchema represents a single MCP tool definition returned by tools/list.
type toolSchema struct {
    Name        string         `json:"name"`
    Description string         `json:"description"`
    InputSchema map[string]any `json:"inputSchema"`
}

// ToolSchemas returns the JSON Schema definitions for all 5 worker CRUD tools.
// Exported so tests can verify the count and structure.
func ToolSchemas() []toolSchema {
    return toolSchemas()
}

func toolSchemas() []toolSchema {
    return []toolSchema{
        {
            Name:        "list_workers",
            Description: "List all workers",
            InputSchema: map[string]any{
                "type":       "object",
                "properties": map[string]any{},
            },
        },
        {
            Name:        "get_worker",
            Description: "Get a single worker by ID",
            InputSchema: map[string]any{
                "type":     "object",
                "required": []string{"worker_id"},
                "properties": map[string]any{
                    "worker_id": map[string]string{"type": "string", "description": "Worker ID"},
                },
            },
        },
        {
            Name:        "create_worker",
            Description: "Create a new worker",
            InputSchema: map[string]any{
                "type":     "object",
                "required": []string{"name"},
                "properties": map[string]any{
                    "name":        map[string]string{"type": "string", "description": "Worker name"},
                    "description": map[string]string{"type": "string", "description": "Worker description"},
                    "prompt":      map[string]string{"type": "string", "description": "System prompt"},
                    "work_dir":    map[string]string{"type": "string", "description": "Working directory path (optional, auto-assigned if empty)"},
                },
            },
        },
        {
            Name:        "update_worker",
            Description: "Update a worker's name, description, or prompt (patch semantics: omitted fields unchanged)",
            InputSchema: map[string]any{
                "type":     "object",
                "required": []string{"worker_id"},
                "properties": map[string]any{
                    "worker_id":   map[string]string{"type": "string", "description": "Worker ID"},
                    "name":        map[string]string{"type": "string", "description": "New name"},
                    "description": map[string]string{"type": "string", "description": "New description"},
                    "prompt":      map[string]string{"type": "string", "description": "New system prompt"},
                },
            },
        },
        {
            Name:        "delete_worker",
            Description: "Delete a worker",
            InputSchema: map[string]any{
                "type":     "object",
                "required": []string{"worker_id"},
                "properties": map[string]any{
                    "worker_id":      map[string]string{"type": "string", "description": "Worker ID"},
                    "delete_work_dir": map[string]any{"type": "boolean", "description": "Also delete the worker's working directory from disk", "default": false},
                },
            },
        },
    }
}

// CallTool is exported for testing. Production code calls callTool via handleToolCall.
func (s *MCPServer) CallTool(name string, args json.RawMessage) (any, error) {
    return s.callTool(name, args)
}

// callTool dispatches to the named tool handler and returns the result.
func (s *MCPServer) callTool(name string, args json.RawMessage) (any, error) {
    switch name {
    case "list_workers":
        return s.toolListWorkers(args)
    case "get_worker":
        return s.toolGetWorker(args)
    case "create_worker":
        return s.toolCreateWorker(args)
    case "update_worker":
        return s.toolUpdateWorker(args)
    case "delete_worker":
        return s.toolDeleteWorker(args)
    default:
        return nil, fmt.Errorf("unknown tool: %s", name)
    }
}

func (s *MCPServer) toolListWorkers(_ json.RawMessage) (any, error) {
    workers, err := s.workerStore.List()
    if err != nil {
        return nil, fmt.Errorf("list workers: %w", err)
    }
    // Always return empty slice, never nil (avoids JSON null)
    if workers == nil {
        workers = []model.Worker{}
    }
    return workers, nil
}

func (s *MCPServer) toolGetWorker(args json.RawMessage) (any, error) {
    var params struct {
        WorkerID string `json:"worker_id"`
    }
    if err := json.Unmarshal(args, &params); err != nil {
        return nil, fmt.Errorf("invalid args: %w", err)
    }
    if params.WorkerID == "" {
        return nil, fmt.Errorf("worker_id is required")
    }
    return s.workerStore.GetByID(params.WorkerID)
}

func (s *MCPServer) toolCreateWorker(args json.RawMessage) (any, error) {
    var params struct {
        Name        string `json:"name"`
        Description string `json:"description"`
        Prompt      string `json:"prompt"`
        WorkDir     string `json:"work_dir"`
    }
    if err := json.Unmarshal(args, &params); err != nil {
        return nil, fmt.Errorf("invalid args: %w", err)
    }
    if params.Name == "" {
        return nil, fmt.Errorf("name is required")
    }
    return s.manager.CreateWorker(params.Name, params.Description, params.Prompt, params.WorkDir)
}

func (s *MCPServer) toolUpdateWorker(args json.RawMessage) (any, error) {
    var params struct {
        WorkerID    string `json:"worker_id"`
        Name        string `json:"name"`
        Description string `json:"description"`
        Prompt      string `json:"prompt"`
    }
    if err := json.Unmarshal(args, &params); err != nil {
        return nil, fmt.Errorf("invalid args: %w", err)
    }
    if params.WorkerID == "" {
        return nil, fmt.Errorf("worker_id is required")
    }

    w, err := s.workerStore.GetByID(params.WorkerID)
    if err != nil {
        return nil, fmt.Errorf("worker not found: %w", err)
    }

    // Patch semantics: only update fields that are provided
    if params.Name != "" {
        w.Name = params.Name
    }
    if params.Description != "" {
        w.Description = params.Description
    }
    if params.Prompt != "" {
        w.Prompt = params.Prompt
    }

    return s.workerStore.Update(w)
}

func (s *MCPServer) toolDeleteWorker(args json.RawMessage) (any, error) {
    var params struct {
        WorkerID      string `json:"worker_id"`
        DeleteWorkDir bool   `json:"delete_work_dir"`
    }
    if err := json.Unmarshal(args, &params); err != nil {
        return nil, fmt.Errorf("invalid args: %w", err)
    }
    if params.WorkerID == "" {
        return nil, fmt.Errorf("worker_id is required")
    }
    if err := s.manager.DeleteWorker(params.WorkerID, params.DeleteWorkDir); err != nil {
        return nil, err
    }
    return map[string]string{"status": "deleted"}, nil
}
```

- [ ] **Step 4: Run tests to verify they pass**

```bash
cd /Users/tengteng/work/robobee/core && go test ./internal/mcp/... -v 2>&1
```

Expected: all tests PASS. If any fail, read error output and fix before committing.

- [ ] **Step 5: Verify full build**

```bash
cd /Users/tengteng/work/robobee/core && go build ./...
```

Expected: no errors.

- [ ] **Step 6: Commit**

```bash
git add internal/mcp/tools.go internal/mcp/tools_test.go internal/mcp/server.go
git commit -m "feat(mcp): add tool schemas and CRUD tool handlers"
```

---

## Chunk 4: Wire-up

### Task 5: Update router, main, and config example

**Files:**
- Modify: `internal/api/router.go`
- Modify: `cmd/server/main.go`

- [ ] **Step 1: Update `api.NewServer` to accept optional `*mcp.MCPServer`**

In `internal/api/router.go`:

1. Add import: `"github.com/robobee/core/internal/mcp"`
2. Add `mcpServer *mcp.MCPServer` field to `Server` struct
3. Update `NewServer` signature:

```go
func NewServer(
    ws *store.WorkerStore,
    es *store.ExecutionStore,
    mgr *worker.Manager,
    mcpSrv *mcp.MCPServer,
) *Server {
```

4. Add `X-API-Key` to CORS `AllowHeaders`:

```go
AllowHeaders: []string{"Origin", "Content-Type", "Authorization", "Accept-Language", "X-API-Key"},
```

5. Store it in the struct: `mcpServer: mcpSrv`

6. In `setupRoutes()`, after the existing `api` group, add:

```go
if s.mcpServer != nil {
    mcpGroup := s.router.Group("/mcp")
    mcpGroup.Use(mcp.APIKeyMiddleware(???))
    s.mcpServer.RegisterRoutes(mcpGroup)
}
```

Wait — the `MCPServer` doesn't hold the API key; the middleware does. We need the API key available at route-registration time. The cleanest approach: pass the API key to `RegisterRoutes`.

Update `RegisterRoutes` signature in `server.go`:

```go
func (s *MCPServer) RegisterRoutes(rg *gin.RouterGroup, apiKey string) {
    rg.Use(APIKeyMiddleware(apiKey))
    rg.GET("/sse", s.handleSSE)
    rg.POST("/messages", s.handleMessages)
}
```

And update `setupRoutes` in `router.go`:

```go
if s.mcpServer != nil {
    mcpGroup := s.router.Group("/mcp")
    s.mcpServer.RegisterRoutes(mcpGroup, s.mcpAPIKey)
}
```

Add `mcpAPIKey string` field to `Server` struct and pass it through `NewServer`:

```go
func NewServer(
    ws *store.WorkerStore,
    es *store.ExecutionStore,
    mgr *worker.Manager,
    mcpSrv *mcp.MCPServer,
    mcpAPIKey string,
) *Server {
```

The complete updated `internal/api/router.go`:

```go
package api

import (
    "github.com/gin-contrib/cors"
    "github.com/gin-gonic/gin"
    "github.com/robobee/core/internal/mcp"
    "github.com/robobee/core/internal/store"
    "github.com/robobee/core/internal/worker"
)

type Server struct {
    router         *gin.Engine
    workerStore    *store.WorkerStore
    executionStore *store.ExecutionStore
    manager        *worker.Manager
    mcpServer      *mcp.MCPServer
    mcpAPIKey      string
}

func NewServer(
    ws *store.WorkerStore,
    es *store.ExecutionStore,
    mgr *worker.Manager,
    mcpSrv *mcp.MCPServer,
    mcpAPIKey string,
) *Server {
    router := gin.Default()
    router.Use(cors.New(cors.Config{
        AllowOrigins:     []string{"*"},
        AllowMethods:     []string{"GET", "POST", "PUT", "DELETE", "OPTIONS"},
        AllowHeaders:     []string{"Origin", "Content-Type", "Authorization", "Accept-Language", "X-API-Key"},
        ExposeHeaders:    []string{"Content-Length"},
        AllowCredentials: false,
    }))
    router.Use(i18nMiddleware())

    s := &Server{
        router:         router,
        workerStore:    ws,
        executionStore: es,
        manager:        mgr,
        mcpServer:      mcpSrv,
        mcpAPIKey:      mcpAPIKey,
    }
    s.setupRoutes()
    return s
}

func (s *Server) setupRoutes() {
    api := s.router.Group("/api")
    {
        // Workers
        api.POST("/workers", s.createWorker)
        api.GET("/workers", s.listWorkers)
        api.GET("/workers/:id", s.getWorker)
        api.PUT("/workers/:id", s.updateWorker)
        api.DELETE("/workers/:id", s.deleteWorker)

        // Worker message trigger
        api.POST("/workers/:id/message", s.sendMessage)

        // Worker executions
        api.GET("/workers/:id/executions", s.listWorkerExecutions)

        // Sessions
        api.GET("/sessions/:sessionId/executions", s.listSessionExecutions)

        // Executions
        api.GET("/executions", s.listExecutions)
        api.GET("/executions/:id", s.getExecution)
        api.POST("/executions/:id/reply", s.replyExecution)
        // WebSocket logs
        api.GET("/executions/:id/logs", s.streamLogs)
    }

    // MCP — only registered when an API key is configured
    if s.mcpServer != nil {
        mcpGroup := s.router.Group("/mcp")
        s.mcpServer.RegisterRoutes(mcpGroup, s.mcpAPIKey)
    }
}

func (s *Server) Run(addr string) error {
    return s.router.Run(addr)
}
```

- [ ] **Step 2: Update `RegisterRoutes` in `server.go`**

Change the signature (in `internal/mcp/server.go`):

```go
func (s *MCPServer) RegisterRoutes(rg *gin.RouterGroup, apiKey string) {
    rg.Use(APIKeyMiddleware(apiKey))
    rg.GET("/sse", s.handleSSE)
    rg.POST("/messages", s.handleMessages)
}
```

- [ ] **Step 3: Update `cmd/server/main.go`**

In `main.go`, replace the existing `api.NewServer` call with:

```go
// Start MCP server if configured
var mcpSrv *mcp.MCPServer
if cfg.MCP.APIKey != "" {
    mcpSrv = mcp.NewServer(workerStore, mgr)
    log.Println("MCP server enabled at /mcp/sse and /mcp/messages")
}

// Start HTTP API
srv := api.NewServer(workerStore, execStore, mgr, mcpSrv, cfg.MCP.APIKey)
```

Add import: `"github.com/robobee/core/internal/mcp"`

- [ ] **Step 4: Build the whole project**

```bash
cd /Users/tengteng/work/robobee/core && go build ./...
```

Expected: no errors. If there are import cycle errors, check that `internal/mcp` only imports `internal/store`, `internal/worker`, `internal/model`, and `github.com/gin-gonic/gin`.

- [ ] **Step 5: Run all tests**

```bash
cd /Users/tengteng/work/robobee/core && go test ./... 2>&1
```

Expected: all existing tests still pass, mcp tests pass.

- [ ] **Step 6: Commit**

```bash
git add internal/api/router.go internal/mcp/server.go cmd/server/main.go
git commit -m "feat(mcp): wire MCPServer into Gin router and main"
```

---

### Task 6: Manual smoke test

- [ ] **Step 1: Set API key in config and start the server**

In `config.yaml`, set:
```yaml
mcp:
  api_key: "test-key-123"
```

```bash
cd /Users/tengteng/work/robobee/core && go run ./cmd/server/main.go
```

Expected log line: `MCP server enabled at /mcp/sse and /mcp/messages`

- [ ] **Step 2: Test SSE connection**

In a second terminal:

```bash
curl -N -H "X-API-Key: test-key-123" http://localhost:8080/mcp/sse
```

Expected: `event: endpoint` event with a `session_id` URL, then connection stays open.

- [ ] **Step 3: Test auth rejection**

```bash
curl -s -o /dev/null -w "%{http_code}" http://localhost:8080/mcp/sse
```

Expected: `401`

- [ ] **Step 4: Send initialize over the session**

Copy the `session_id` from Step 2, then in another terminal:

```bash
SESSION_ID=<paste-session-id-here>
curl -s -X POST \
  -H "Content-Type: application/json" \
  -H "X-API-Key: test-key-123" \
  -d '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{}}}' \
  "http://localhost:8080/mcp/messages?session_id=$SESSION_ID"
```

Expected: `202 Accepted` from POST. SSE terminal receives:
```
event: message
data: {"jsonrpc":"2.0","id":1,"result":{"capabilities":{"tools":{}},"protocolVersion":"2024-11-05","serverInfo":{"name":"robobee-mcp","version":"1.0.0"}}}
```

- [ ] **Step 5: List tools**

```bash
curl -s -X POST \
  -H "Content-Type: application/json" \
  -H "X-API-Key: test-key-123" \
  -d '{"jsonrpc":"2.0","id":2,"method":"tools/list"}' \
  "http://localhost:8080/mcp/messages?session_id=$SESSION_ID"
```

Expected: SSE terminal receives `tools/list` response with 5 tool definitions.

- [ ] **Step 6: Create a worker via MCP**

```bash
curl -s -X POST \
  -H "Content-Type: application/json" \
  -H "X-API-Key: test-key-123" \
  -d '{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"create_worker","arguments":{"name":"MCPBot","description":"Created via MCP","prompt":"You are a helpful bot."}}}' \
  "http://localhost:8080/mcp/messages?session_id=$SESSION_ID"
```

Expected: SSE terminal receives tool result with a new Worker JSON including a non-empty `id`.

- [ ] **Step 7: Final commit**

```bash
git add config.example.yaml
git commit -m "feat(mcp): MCP worker management service complete"
```

---

## Implementation Notes

- **`initialized` notification**: The server accepts `initialized` without response (per MCP spec). Tool calls received before `initialized` are processed normally — no strict gating needed for this use case.
- **SSE keepalive**: Not implemented in this iteration. If deploying behind a proxy with aggressive timeouts, add a ticker goroutine sending `: keepalive\n\n` every 15s.
- **`update_worker` patch semantics**: Only non-empty string fields update; boolean false is the zero value so `delete_work_dir` defaults to false naturally in `toolDeleteWorker`.
- **Import cycle check**: `internal/mcp` must NOT import `internal/api`. The dependency is one-way: `api` imports `mcp`.
