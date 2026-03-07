package model

import "time"

type RuntimeType string

const (
	RuntimeClaudeCode RuntimeType = "claude_code"
	RuntimeCodex      RuntimeType = "codex"
)

type WorkerStatus string

const (
	WorkerStatusIdle    WorkerStatus = "idle"
	WorkerStatusWorking WorkerStatus = "working"
	WorkerStatusError   WorkerStatus = "error"
)

type Worker struct {
	ID          string       `json:"id" db:"id"`
	Name        string       `json:"name" db:"name"`
	Description string       `json:"description" db:"description"`
	Email       string       `json:"email" db:"email"`
	RuntimeType RuntimeType  `json:"runtime_type" db:"runtime_type"`
	WorkDir     string       `json:"work_dir" db:"work_dir"`
	Status      WorkerStatus `json:"status" db:"status"`
	CreatedAt   time.Time    `json:"created_at" db:"created_at"`
	UpdatedAt   time.Time    `json:"updated_at" db:"updated_at"`
}
