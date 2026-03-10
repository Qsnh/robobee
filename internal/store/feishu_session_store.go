package store

import (
	"database/sql"
	"time"
)

type FeishuSession struct {
	ChatID          string
	WorkerID        string
	SessionID       string
	LastExecutionID string
	UpdatedAt       time.Time
}

type FeishuSessionStore struct {
	db *sql.DB
}

func NewFeishuSessionStore(db *sql.DB) *FeishuSessionStore {
	return &FeishuSessionStore{db: db}
}

// GetSession returns nil if not found.
func (s *FeishuSessionStore) GetSession(chatID string) (*FeishuSession, error) {
	row := s.db.QueryRow(
		`SELECT chat_id, worker_id, session_id, last_execution_id, updated_at
         FROM feishu_sessions WHERE chat_id = ?`, chatID)

	var sess FeishuSession
	err := row.Scan(&sess.ChatID, &sess.WorkerID, &sess.SessionID, &sess.LastExecutionID, &sess.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &sess, nil
}

// DeleteSession removes the session record for a chat.
func (s *FeishuSessionStore) DeleteSession(chatID string) error {
	_, err := s.db.Exec(`DELETE FROM feishu_sessions WHERE chat_id = ?`, chatID)
	return err
}

// UpsertSession creates or updates the session mapping for a chat.
func (s *FeishuSessionStore) UpsertSession(chatID, workerID, sessionID, lastExecutionID string) error {
	_, err := s.db.Exec(
		`INSERT INTO feishu_sessions (chat_id, worker_id, session_id, last_execution_id, updated_at)
         VALUES (?, ?, ?, ?, ?)
         ON CONFLICT(chat_id) DO UPDATE SET
             worker_id = excluded.worker_id,
             session_id = excluded.session_id,
             last_execution_id = excluded.last_execution_id,
             updated_at = excluded.updated_at`,
		chatID, workerID, sessionID, lastExecutionID, time.Now().UTC())
	return err
}
