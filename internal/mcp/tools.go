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
					"worker_id":       map[string]string{"type": "string", "description": "Worker ID"},
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
