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
		`INSERT INTO workers (id, name, description, email, runtime_type, work_dir, status, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		w.ID, w.Name, w.Description, w.Email, w.RuntimeType, w.WorkDir, w.Status, w.CreatedAt, w.UpdatedAt,
	)
	if err != nil {
		return model.Worker{}, fmt.Errorf("insert worker: %w", err)
	}
	return w, nil
}

func (s *WorkerStore) GetByID(id string) (model.Worker, error) {
	var w model.Worker
	err := s.db.QueryRow(
		`SELECT id, name, description, email, runtime_type, work_dir, status, created_at, updated_at
		 FROM workers WHERE id = ?`, id,
	).Scan(&w.ID, &w.Name, &w.Description, &w.Email, &w.RuntimeType, &w.WorkDir, &w.Status, &w.CreatedAt, &w.UpdatedAt)
	if err != nil {
		return model.Worker{}, fmt.Errorf("get worker: %w", err)
	}
	return w, nil
}

func (s *WorkerStore) GetByEmail(email string) (model.Worker, error) {
	var w model.Worker
	err := s.db.QueryRow(
		`SELECT id, name, description, email, runtime_type, work_dir, status, created_at, updated_at
		 FROM workers WHERE email = ?`, email,
	).Scan(&w.ID, &w.Name, &w.Description, &w.Email, &w.RuntimeType, &w.WorkDir, &w.Status, &w.CreatedAt, &w.UpdatedAt)
	if err != nil {
		return model.Worker{}, fmt.Errorf("get worker by email: %w", err)
	}
	return w, nil
}

func (s *WorkerStore) List() ([]model.Worker, error) {
	rows, err := s.db.Query(
		`SELECT id, name, description, email, runtime_type, work_dir, status, created_at, updated_at FROM workers ORDER BY created_at DESC`,
	)
	if err != nil {
		return nil, fmt.Errorf("list workers: %w", err)
	}
	defer rows.Close()

	var workers []model.Worker
	for rows.Next() {
		var w model.Worker
		if err := rows.Scan(&w.ID, &w.Name, &w.Description, &w.Email, &w.RuntimeType, &w.WorkDir, &w.Status, &w.CreatedAt, &w.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan worker: %w", err)
		}
		workers = append(workers, w)
	}
	return workers, rows.Err()
}

func (s *WorkerStore) Update(w model.Worker) (model.Worker, error) {
	w.UpdatedAt = time.Now().UTC()
	_, err := s.db.Exec(
		`UPDATE workers SET name=?, description=?, email=?, runtime_type=?, work_dir=?, status=?, updated_at=? WHERE id=?`,
		w.Name, w.Description, w.Email, w.RuntimeType, w.WorkDir, w.Status, w.UpdatedAt, w.ID,
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
