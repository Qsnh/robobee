package mcp_test

import (
	"encoding/json"
	"testing"

	_ "github.com/mattn/go-sqlite3"
	"github.com/robobee/core/internal/config"
	"github.com/robobee/core/internal/mcp"
	"github.com/robobee/core/internal/model"
	"github.com/robobee/core/internal/store"
	"github.com/robobee/core/internal/worker"
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
	ts := store.NewTaskStore(db)
	cfg := config.Config{
		Workers: config.WorkersConfig{BaseDir: t.TempDir()},
		Runtime: config.RuntimeConfig{
			ClaudeCode: config.RuntimeEntry{Binary: "claude"},
		},
	}
	mgr := worker.NewManager(cfg, ws, es)
	return mcp.NewServer(ws, mgr, ts)
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
	if len(schemas) != 8 {
		t.Errorf("expected 8 tool schemas, got %d", len(schemas))
	}
}

func TestToolSchemas_IncludesTaskTools(t *testing.T) {
	schemas := mcp.ToolSchemas()
	names := make(map[string]bool)
	for _, s := range schemas {
		names[s.Name] = true
	}
	for _, want := range []string{"create_task", "list_tasks", "cancel_task"} {
		if !names[want] {
			t.Errorf("missing tool schema: %s", want)
		}
	}
}

func TestListWorkers_ReturnsEmptySlice_NotNull(t *testing.T) {
	s := setupMCPServer(t)
	result, err := s.CallTool("list_workers", mustMarshal(t, map[string]any{}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	workers := result.([]model.Worker)
	if workers == nil {
		t.Error("expected non-nil slice, got nil")
	}
}
