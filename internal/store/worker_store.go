package store

import (
	"database/sql"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/robobee/core/internal/model"
)

type WorkerStore struct {
	db *sql.DB
}

func NewWorkerStore(db *sql.DB) *WorkerStore {
	return &WorkerStore{db: db}
}

func (s *WorkerStore) Create(w model.Worker) (model.Worker, error) {
	w.ID = uuid.New().String()
	w.Status = model.WorkerStatusIdle
	w.CreatedAt = time.Now().UTC()
	w.UpdatedAt = w.CreatedAt

	_, err := s.db.Exec(
		`INSERT INTO workers (id, name, description, prompt, email, runtime_type, work_dir, trigger_type, cron_expression, recipients, requires_approval, status, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		w.ID, w.Name, w.Description, w.Prompt, w.Email, w.RuntimeType, w.WorkDir,
		w.TriggerType, w.CronExpression, string(w.Recipients), w.RequiresApproval,
		w.Status, w.CreatedAt, w.UpdatedAt,
	)
	if err != nil {
		return model.Worker{}, fmt.Errorf("insert worker: %w", err)
	}
	return w, nil
}

const workerColumns = `id, name, description, prompt, email, runtime_type, work_dir, trigger_type, cron_expression, recipients, requires_approval, status, created_at, updated_at`

func scanWorker(scanner interface{ Scan(...any) error }) (model.Worker, error) {
	var w model.Worker
	var recipients string
	err := scanner.Scan(
		&w.ID, &w.Name, &w.Description, &w.Prompt, &w.Email, &w.RuntimeType,
		&w.WorkDir, &w.TriggerType, &w.CronExpression, &recipients,
		&w.RequiresApproval, &w.Status, &w.CreatedAt, &w.UpdatedAt,
	)
	if err != nil {
		return model.Worker{}, err
	}
	w.Recipients = []byte(recipients)
	return w, nil
}

func (s *WorkerStore) GetByID(id string) (model.Worker, error) {
	row := s.db.QueryRow(`SELECT `+workerColumns+` FROM workers WHERE id = ?`, id)
	w, err := scanWorker(row)
	if err != nil {
		return model.Worker{}, fmt.Errorf("get worker: %w", err)
	}
	return w, nil
}

func (s *WorkerStore) GetByEmail(email string) (model.Worker, error) {
	row := s.db.QueryRow(`SELECT `+workerColumns+` FROM workers WHERE email = ?`, email)
	w, err := scanWorker(row)
	if err != nil {
		return model.Worker{}, fmt.Errorf("get worker by email: %w", err)
	}
	return w, nil
}

func (s *WorkerStore) List() ([]model.Worker, error) {
	rows, err := s.db.Query(`SELECT ` + workerColumns + ` FROM workers ORDER BY created_at DESC`)
	if err != nil {
		return nil, fmt.Errorf("list workers: %w", err)
	}
	defer rows.Close()

	var workers []model.Worker
	for rows.Next() {
		w, err := scanWorker(rows)
		if err != nil {
			return nil, fmt.Errorf("scan worker: %w", err)
		}
		workers = append(workers, w)
	}
	return workers, rows.Err()
}

func (s *WorkerStore) ListCronWorkers() ([]model.Worker, error) {
	rows, err := s.db.Query(
		`SELECT `+workerColumns+` FROM workers WHERE trigger_type = 'cron' AND cron_expression != ''`,
	)
	if err != nil {
		return nil, fmt.Errorf("list cron workers: %w", err)
	}
	defer rows.Close()

	var workers []model.Worker
	for rows.Next() {
		w, err := scanWorker(rows)
		if err != nil {
			return nil, fmt.Errorf("scan worker: %w", err)
		}
		workers = append(workers, w)
	}
	return workers, rows.Err()
}

func (s *WorkerStore) Update(w model.Worker) (model.Worker, error) {
	w.UpdatedAt = time.Now().UTC()
	_, err := s.db.Exec(
		`UPDATE workers SET name=?, description=?, prompt=?, email=?, runtime_type=?, work_dir=?,
		 trigger_type=?, cron_expression=?, recipients=?, requires_approval=?, status=?, updated_at=?
		 WHERE id=?`,
		w.Name, w.Description, w.Prompt, w.Email, w.RuntimeType, w.WorkDir,
		w.TriggerType, w.CronExpression, string(w.Recipients), w.RequiresApproval,
		w.Status, w.UpdatedAt, w.ID,
	)
	if err != nil {
		return model.Worker{}, fmt.Errorf("update worker: %w", err)
	}
	return w, nil
}

func (s *WorkerStore) UpdateStatus(id string, status model.WorkerStatus) error {
	_, err := s.db.Exec(`UPDATE workers SET status=?, updated_at=? WHERE id=?`, status, time.Now().UTC(), id)
	return err
}

func (s *WorkerStore) Delete(id string) error {
	_, err := s.db.Exec(`DELETE FROM workers WHERE id=?`, id)
	return err
}
