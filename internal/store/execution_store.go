package store

import (
	"database/sql"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/robobee/core/internal/model"
)

type ExecutionStore struct {
	db *sql.DB
}

func NewExecutionStore(db *sql.DB) *ExecutionStore {
	return &ExecutionStore{db: db}
}

func (s *ExecutionStore) Create(taskID string) (model.TaskExecution, error) {
	now := time.Now().UTC()
	exec := model.TaskExecution{
		ID:        uuid.New().String(),
		TaskID:    taskID,
		SessionID: uuid.New().String(),
		Status:    model.ExecStatusPending,
		StartedAt: &now,
	}

	_, err := s.db.Exec(
		`INSERT INTO task_executions (id, task_id, session_id, status, result, ai_process_pid, started_at)
		 VALUES (?, ?, ?, ?, '', 0, ?)`,
		exec.ID, exec.TaskID, exec.SessionID, exec.Status, exec.StartedAt,
	)
	if err != nil {
		return model.TaskExecution{}, fmt.Errorf("insert execution: %w", err)
	}
	return exec, nil
}

func (s *ExecutionStore) GetByID(id string) (model.TaskExecution, error) {
	var e model.TaskExecution
	err := s.db.QueryRow(
		`SELECT id, task_id, session_id, status, result, ai_process_pid, started_at, completed_at
		 FROM task_executions WHERE id = ?`, id,
	).Scan(&e.ID, &e.TaskID, &e.SessionID, &e.Status, &e.Result, &e.AIProcessPID, &e.StartedAt, &e.CompletedAt)
	if err != nil {
		return model.TaskExecution{}, fmt.Errorf("get execution: %w", err)
	}
	return e, nil
}

func (s *ExecutionStore) GetBySessionID(sessionID string) (model.TaskExecution, error) {
	var e model.TaskExecution
	err := s.db.QueryRow(
		`SELECT id, task_id, session_id, status, result, ai_process_pid, started_at, completed_at
		 FROM task_executions WHERE session_id = ?`, sessionID,
	).Scan(&e.ID, &e.TaskID, &e.SessionID, &e.Status, &e.Result, &e.AIProcessPID, &e.StartedAt, &e.CompletedAt)
	if err != nil {
		return model.TaskExecution{}, fmt.Errorf("get execution by session: %w", err)
	}
	return e, nil
}

func (s *ExecutionStore) List() ([]model.TaskExecution, error) {
	rows, err := s.db.Query(
		`SELECT id, task_id, session_id, status, result, ai_process_pid, started_at, completed_at
		 FROM task_executions ORDER BY started_at DESC`,
	)
	if err != nil {
		return nil, fmt.Errorf("list executions: %w", err)
	}
	defer rows.Close()

	var execs []model.TaskExecution
	for rows.Next() {
		var e model.TaskExecution
		if err := rows.Scan(&e.ID, &e.TaskID, &e.SessionID, &e.Status, &e.Result, &e.AIProcessPID, &e.StartedAt, &e.CompletedAt); err != nil {
			return nil, fmt.Errorf("scan execution: %w", err)
		}
		execs = append(execs, e)
	}
	return execs, rows.Err()
}

func (s *ExecutionStore) UpdateStatus(id string, status model.ExecutionStatus) error {
	_, err := s.db.Exec(`UPDATE task_executions SET status=? WHERE id=?`, status, id)
	return err
}

func (s *ExecutionStore) UpdateResult(id string, result string, status model.ExecutionStatus) error {
	now := time.Now().UTC()
	_, err := s.db.Exec(`UPDATE task_executions SET result=?, status=?, completed_at=? WHERE id=?`, result, status, now, id)
	return err
}

func (s *ExecutionStore) UpdatePID(id string, pid int) error {
	_, err := s.db.Exec(`UPDATE task_executions SET ai_process_pid=?, status=? WHERE id=?`, pid, model.ExecStatusRunning, id)
	return err
}
