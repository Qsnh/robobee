package model

import (
	"encoding/json"
	"time"
)

type TriggerType string

const (
	TriggerManual TriggerType = "manual"
	TriggerEmail  TriggerType = "email"
	TriggerCron   TriggerType = "cron"
)

type Task struct {
	ID               string          `json:"id" db:"id"`
	WorkerID         string          `json:"worker_id" db:"worker_id"`
	Name             string          `json:"name" db:"name"`
	Plan             string          `json:"plan" db:"plan"`
	TriggerType      TriggerType     `json:"trigger_type" db:"trigger_type"`
	CronExpression   string          `json:"cron_expression,omitempty" db:"cron_expression"`
	Recipients       json.RawMessage `json:"recipients" db:"recipients"`
	RequiresApproval bool            `json:"requires_approval" db:"requires_approval"`
	CreatedAt        time.Time       `json:"created_at" db:"created_at"`
	UpdatedAt        time.Time       `json:"updated_at" db:"updated_at"`
}
