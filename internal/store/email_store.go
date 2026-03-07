package store

import (
	"database/sql"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/robobee/core/internal/model"
)

type EmailStore struct {
	db *sql.DB
}

func NewEmailStore(db *sql.DB) *EmailStore {
	return &EmailStore{db: db}
}

func (s *EmailStore) Create(e model.Email) (model.Email, error) {
	e.ID = uuid.New().String()
	e.CreatedAt = time.Now().UTC()

	_, err := s.db.Exec(
		`INSERT INTO emails (id, execution_id, from_addr, to_addr, cc_addr, subject, body, in_reply_to, direction, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		e.ID, e.ExecutionID, e.FromAddr, e.ToAddr, e.CCAddr, e.Subject, e.Body, e.InReplyTo, e.Direction, e.CreatedAt,
	)
	if err != nil {
		return model.Email{}, fmt.Errorf("insert email: %w", err)
	}
	return e, nil
}

func (s *EmailStore) ListByExecutionID(executionID string) ([]model.Email, error) {
	rows, err := s.db.Query(
		`SELECT id, execution_id, from_addr, to_addr, cc_addr, subject, body, in_reply_to, direction, created_at
		 FROM emails WHERE execution_id = ? ORDER BY created_at ASC`, executionID,
	)
	if err != nil {
		return nil, fmt.Errorf("list emails: %w", err)
	}
	defer rows.Close()

	var emails []model.Email
	for rows.Next() {
		var e model.Email
		if err := rows.Scan(&e.ID, &e.ExecutionID, &e.FromAddr, &e.ToAddr, &e.CCAddr, &e.Subject, &e.Body, &e.InReplyTo, &e.Direction, &e.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan email: %w", err)
		}
		emails = append(emails, e)
	}
	return emails, rows.Err()
}
