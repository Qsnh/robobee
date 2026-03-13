package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/robfig/cron/v3"
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
		{
			Name:        "create_task",
			Description: "Create a task assigning a worker to handle a user instruction from a message",
			InputSchema: map[string]any{
				"type":     "object",
				"required": []string{"message_id", "worker_id", "instruction", "type"},
				"properties": map[string]any{
					"message_id":        map[string]string{"type": "string", "description": "ID of the originating platform message"},
					"worker_id":         map[string]string{"type": "string", "description": "Worker ID to assign"},
					"instruction":       map[string]string{"type": "string", "description": "Specific instruction for the worker"},
					"type":              map[string]any{"type": "string", "enum": []string{"immediate", "countdown", "scheduled"}, "description": "Task type"},
					"scheduled_at":      map[string]string{"type": "integer", "description": "Unix ms; required for countdown, must be >= now+5s"},
					"cron_expr":         map[string]string{"type": "string", "description": "5-field cron expression; required for scheduled"},
					"reply_session_key": map[string]string{"type": "string", "description": "Reply target override session key; required for scheduled"},
				},
			},
		},
		{
			Name:        "list_tasks",
			Description: "List tasks for a given message, optionally filtered by status",
			InputSchema: map[string]any{
				"type":     "object",
				"required": []string{"message_id"},
				"properties": map[string]any{
					"message_id": map[string]string{"type": "string", "description": "ID of the originating platform message"},
					"status":     map[string]string{"type": "string", "description": "Optional status filter"},
				},
			},
		},
		{
			Name:        "cancel_task",
			Description: "Cancel a pending or scheduled task",
			InputSchema: map[string]any{
				"type":     "object",
				"required": []string{"task_id"},
				"properties": map[string]any{
					"task_id": map[string]string{"type": "string", "description": "Task ID to cancel"},
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
	case "create_task":
		return s.toolCreateTask(args)
	case "list_tasks":
		return s.toolListTasks(args)
	case "cancel_task":
		return s.toolCancelTask(args)
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

func (s *MCPServer) toolCreateTask(args json.RawMessage) (any, error) {
	var params struct {
		MessageID       string `json:"message_id"`
		WorkerID        string `json:"worker_id"`
		Instruction     string `json:"instruction"`
		Type            string `json:"type"`
		ScheduledAt     *int64 `json:"scheduled_at"`
		CronExpr        string `json:"cron_expr"`
		ReplySessionKey string `json:"reply_session_key"`
	}
	if err := json.Unmarshal(args, &params); err != nil {
		return nil, fmt.Errorf("invalid args: %w", err)
	}
	if params.MessageID == "" {
		return nil, fmt.Errorf("message_id is required")
	}
	if params.WorkerID == "" {
		return nil, fmt.Errorf("worker_id is required")
	}
	if params.Instruction == "" {
		return nil, fmt.Errorf("instruction is required")
	}
	switch params.Type {
	case "immediate", "countdown", "scheduled":
	default:
		return nil, fmt.Errorf("type must be immediate, countdown, or scheduled")
	}

	nowMS := time.Now().UnixMilli()

	switch params.Type {
	case "countdown":
		if params.ScheduledAt == nil {
			return nil, fmt.Errorf("scheduled_at is required for countdown tasks")
		}
		if *params.ScheduledAt < nowMS+5000 {
			return nil, fmt.Errorf("scheduled_at must be at least 5 seconds in the future")
		}
	case "scheduled":
		if params.CronExpr == "" {
			return nil, fmt.Errorf("cron_expr is required for scheduled tasks")
		}
		if params.ReplySessionKey == "" {
			return nil, fmt.Errorf("reply_session_key is required for scheduled tasks")
		}
	}

	var nextRunAt *int64
	if params.Type == "scheduled" {
		sched, err := cron.ParseStandard(params.CronExpr)
		if err != nil {
			task := model.Task{
				MessageID:   params.MessageID,
				WorkerID:    params.WorkerID,
				Instruction: params.Instruction,
				Type:        params.Type,
				Status:      model.TaskStatusCancelled,
				CronExpr:    params.CronExpr,
				CreatedAt:   nowMS,
				UpdatedAt:   nowMS,
			}
			id, createErr := s.taskStore.Create(context.Background(), task)
			if createErr != nil {
				return nil, fmt.Errorf("create cancelled task: %w", createErr)
			}
			return map[string]string{"task_id": id, "status": "cancelled", "reason": "invalid cron_expr: " + err.Error()}, nil
		}
		next := sched.Next(time.Now()).UnixMilli()
		nextRunAt = &next
	}

	task := model.Task{
		MessageID:       params.MessageID,
		WorkerID:        params.WorkerID,
		Instruction:     params.Instruction,
		Type:            params.Type,
		Status:          model.TaskStatusPending,
		ScheduledAt:     params.ScheduledAt,
		CronExpr:        params.CronExpr,
		NextRunAt:       nextRunAt,
		ReplySessionKey: params.ReplySessionKey,
		CreatedAt:       nowMS,
		UpdatedAt:       nowMS,
	}

	id, err := s.taskStore.Create(context.Background(), task)
	if err != nil {
		return nil, fmt.Errorf("create task: %w", err)
	}
	return map[string]string{"task_id": id, "status": "pending"}, nil
}

func (s *MCPServer) toolListTasks(args json.RawMessage) (any, error) {
	var params struct {
		MessageID string `json:"message_id"`
		Status    string `json:"status"`
	}
	if err := json.Unmarshal(args, &params); err != nil {
		return nil, fmt.Errorf("invalid args: %w", err)
	}
	if params.MessageID == "" {
		return nil, fmt.Errorf("message_id is required")
	}
	tasks, err := s.taskStore.ListByMessageID(context.Background(), params.MessageID, params.Status)
	if err != nil {
		return nil, fmt.Errorf("list tasks: %w", err)
	}
	if tasks == nil {
		tasks = []model.Task{}
	}
	return tasks, nil
}

func (s *MCPServer) toolCancelTask(args json.RawMessage) (any, error) {
	var params struct {
		TaskID string `json:"task_id"`
	}
	if err := json.Unmarshal(args, &params); err != nil {
		return nil, fmt.Errorf("invalid args: %w", err)
	}
	if params.TaskID == "" {
		return nil, fmt.Errorf("task_id is required")
	}
	if err := s.taskStore.CancelTask(context.Background(), params.TaskID); err != nil {
		return nil, fmt.Errorf("cancel task: %w", err)
	}
	return map[string]string{"task_id": params.TaskID, "status": "cancelled"}, nil
}
