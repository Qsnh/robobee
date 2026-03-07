package store

import (
	"database/sql"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/robobee/core/internal/model"
)

type MemoryStore struct {
	db *sql.DB
}

func NewMemoryStore(db *sql.DB) *MemoryStore {
	return &MemoryStore{db: db}
}

func (s *MemoryStore) Create(m model.WorkerMemory) (model.WorkerMemory, error) {
	m.ID = uuid.New().String()
	m.CreatedAt = time.Now().UTC()

	_, err := s.db.Exec(
		`INSERT INTO worker_memories (id, worker_id, execution_id, summary, created_at)
		 VALUES (?, ?, ?, ?, ?)`,
		m.ID, m.WorkerID, m.ExecutionID, m.Summary, m.CreatedAt,
	)
	if err != nil {
		return model.WorkerMemory{}, fmt.Errorf("insert memory: %w", err)
	}
	return m, nil
}

func (s *MemoryStore) ListByWorkerID(workerID string) ([]model.WorkerMemory, error) {
	rows, err := s.db.Query(
		`SELECT id, worker_id, execution_id, summary, created_at
		 FROM worker_memories WHERE worker_id = ? ORDER BY created_at DESC`, workerID,
	)
	if err != nil {
		return nil, fmt.Errorf("list memories: %w", err)
	}
	defer rows.Close()

	var memories []model.WorkerMemory
	for rows.Next() {
		var m model.WorkerMemory
		if err := rows.Scan(&m.ID, &m.WorkerID, &m.ExecutionID, &m.Summary, &m.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan memory: %w", err)
		}
		memories = append(memories, m)
	}
	return memories, rows.Err()
}
