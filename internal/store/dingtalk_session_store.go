package store

import (
	"database/sql"
	"time"
)

type DingTalkSession struct {
	ChatID          string
	WorkerID        string
	SessionID       string
	LastExecutionID string
	UpdatedAt       time.Time
}

type DingTalkSessionStore struct {
	db *sql.DB
}

func NewDingTalkSessionStore(db *sql.DB) *DingTalkSessionStore {
	return &DingTalkSessionStore{db: db}
}

// GetSession returns nil if not found.
func (s *DingTalkSessionStore) GetSession(chatID string) (*DingTalkSession, error) {
	row := s.db.QueryRow(
		`SELECT chat_id, worker_id, session_id, last_execution_id, updated_at
         FROM dingtalk_sessions WHERE chat_id = ?`, chatID)

	var sess DingTalkSession
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
func (s *DingTalkSessionStore) DeleteSession(chatID string) error {
	_, err := s.db.Exec(`DELETE FROM dingtalk_sessions WHERE chat_id = ?`, chatID)
	return err
}

// UpsertSession creates or updates the session mapping for a chat.
func (s *DingTalkSessionStore) UpsertSession(chatID, workerID, sessionID, lastExecutionID string) error {
	_, err := s.db.Exec(
		`INSERT INTO dingtalk_sessions (chat_id, worker_id, session_id, last_execution_id, updated_at)
         VALUES (?, ?, ?, ?, ?)
         ON CONFLICT(chat_id) DO UPDATE SET
             worker_id = excluded.worker_id,
             session_id = excluded.session_id,
             last_execution_id = excluded.last_execution_id,
             updated_at = excluded.updated_at`,
		chatID, workerID, sessionID, lastExecutionID, time.Now().UTC())
	return err
}
