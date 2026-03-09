package model

import (
	"time"
)

type WorkerStatus string

const (
	WorkerStatusIdle    WorkerStatus = "idle"
	WorkerStatusWorking WorkerStatus = "working"
	WorkerStatusError   WorkerStatus = "error"
)

type Worker struct {
	ID              string       `json:"id" db:"id"`
	Name            string       `json:"name" db:"name"`
	Description     string       `json:"description" db:"description"`
	Prompt          string       `json:"prompt" db:"prompt"`
	WorkDir         string       `json:"work_dir" db:"work_dir"`
	CronExpression  string       `json:"cron_expression,omitempty" db:"cron_expression"`
	ScheduleEnabled bool         `json:"schedule_enabled" db:"schedule_enabled"`
	Status          WorkerStatus `json:"status" db:"status"`
	CreatedAt       time.Time    `json:"created_at" db:"created_at"`
	UpdatedAt       time.Time    `json:"updated_at" db:"updated_at"`
}
