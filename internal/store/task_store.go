package store

import (
	"database/sql"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/robobee/core/internal/model"
)

type TaskStore struct {
	db *sql.DB
}

func NewTaskStore(db *sql.DB) *TaskStore {
	return &TaskStore{db: db}
}

func (s *TaskStore) Create(t model.Task) (model.Task, error) {
	t.ID = uuid.New().String()
	t.CreatedAt = time.Now().UTC()
	t.UpdatedAt = t.CreatedAt

	_, err := s.db.Exec(
		`INSERT INTO tasks (id, worker_id, name, plan, trigger_type, cron_expression, recipients, requires_approval, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		t.ID, t.WorkerID, t.Name, t.Plan, t.TriggerType, t.CronExpression, string(t.Recipients), t.RequiresApproval, t.CreatedAt, t.UpdatedAt,
	)
	if err != nil {
		return model.Task{}, fmt.Errorf("insert task: %w", err)
	}
	return t, nil
}

func (s *TaskStore) GetByID(id string) (model.Task, error) {
	var t model.Task
	var recipients string
	err := s.db.QueryRow(
		`SELECT id, worker_id, name, plan, trigger_type, cron_expression, recipients, requires_approval, created_at, updated_at
		 FROM tasks WHERE id = ?`, id,
	).Scan(&t.ID, &t.WorkerID, &t.Name, &t.Plan, &t.TriggerType, &t.CronExpression, &recipients, &t.RequiresApproval, &t.CreatedAt, &t.UpdatedAt)
	if err != nil {
		return model.Task{}, fmt.Errorf("get task: %w", err)
	}
	t.Recipients = []byte(recipients)
	return t, nil
}

func (s *TaskStore) ListByWorkerID(workerID string) ([]model.Task, error) {
	rows, err := s.db.Query(
		`SELECT id, worker_id, name, plan, trigger_type, cron_expression, recipients, requires_approval, created_at, updated_at
		 FROM tasks WHERE worker_id = ? ORDER BY created_at DESC`, workerID,
	)
	if err != nil {
		return nil, fmt.Errorf("list tasks: %w", err)
	}
	defer rows.Close()

	var tasks []model.Task
	for rows.Next() {
		var t model.Task
		var recipients string
		if err := rows.Scan(&t.ID, &t.WorkerID, &t.Name, &t.Plan, &t.TriggerType, &t.CronExpression, &recipients, &t.RequiresApproval, &t.CreatedAt, &t.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan task: %w", err)
		}
		t.Recipients = []byte(recipients)
		tasks = append(tasks, t)
	}
	return tasks, rows.Err()
}

func (s *TaskStore) ListCronTasks() ([]model.Task, error) {
	rows, err := s.db.Query(
		`SELECT id, worker_id, name, plan, trigger_type, cron_expression, recipients, requires_approval, created_at, updated_at
		 FROM tasks WHERE trigger_type = 'cron' AND cron_expression != ''`,
	)
	if err != nil {
		return nil, fmt.Errorf("list cron tasks: %w", err)
	}
	defer rows.Close()

	var tasks []model.Task
	for rows.Next() {
		var t model.Task
		var recipients string
		if err := rows.Scan(&t.ID, &t.WorkerID, &t.Name, &t.Plan, &t.TriggerType, &t.CronExpression, &recipients, &t.RequiresApproval, &t.CreatedAt, &t.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan task: %w", err)
		}
		t.Recipients = []byte(recipients)
		tasks = append(tasks, t)
	}
	return tasks, rows.Err()
}

func (s *TaskStore) Update(t model.Task) (model.Task, error) {
	t.UpdatedAt = time.Now().UTC()
	_, err := s.db.Exec(
		`UPDATE tasks SET name=?, plan=?, trigger_type=?, cron_expression=?, recipients=?, requires_approval=?, updated_at=? WHERE id=?`,
		t.Name, t.Plan, t.TriggerType, t.CronExpression, string(t.Recipients), t.RequiresApproval, t.UpdatedAt, t.ID,
	)
	if err != nil {
		return model.Task{}, fmt.Errorf("update task: %w", err)
	}
	return t, nil
}

func (s *TaskStore) Delete(id string) error {
	_, err := s.db.Exec(`DELETE FROM tasks WHERE id=?`, id)
	return err
}
