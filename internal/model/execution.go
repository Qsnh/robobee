package model

import "time"

type ExecutionStatus string

const (
	ExecStatusPending          ExecutionStatus = "pending"
	ExecStatusRunning          ExecutionStatus = "running"
	ExecStatusAwaitingApproval ExecutionStatus = "awaiting_approval"
	ExecStatusApproved         ExecutionStatus = "approved"
	ExecStatusRejected         ExecutionStatus = "rejected"
	ExecStatusCompleted        ExecutionStatus = "completed"
	ExecStatusFailed           ExecutionStatus = "failed"
)

type TaskExecution struct {
	ID           string          `json:"id" db:"id"`
	TaskID       string          `json:"task_id" db:"task_id"`
	SessionID    string          `json:"session_id" db:"session_id"`
	Status       ExecutionStatus `json:"status" db:"status"`
	Result       string          `json:"result,omitempty" db:"result"`
	AIProcessPID int             `json:"ai_process_pid,omitempty" db:"ai_process_pid"`
	StartedAt    *time.Time      `json:"started_at,omitempty" db:"started_at"`
	CompletedAt  *time.Time      `json:"completed_at,omitempty" db:"completed_at"`
}
