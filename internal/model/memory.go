package model

import "time"

type WorkerMemory struct {
	ID          string    `json:"id" db:"id"`
	WorkerID    string    `json:"worker_id" db:"worker_id"`
	ExecutionID string    `json:"execution_id" db:"execution_id"`
	Summary     string    `json:"summary" db:"summary"`
	CreatedAt   time.Time `json:"created_at" db:"created_at"`
}
