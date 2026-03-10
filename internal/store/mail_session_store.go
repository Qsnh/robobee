package store

import (
	"database/sql"
	"time"
)

type MailSession struct {
	ThreadID        string
	WorkerID        string
	SessionID       string
	LastExecutionID string
	UpdatedAt       time.Time
}

// GetWorkerID and GetLastExecutionID implement the threadSession interface
// used by the mail handler. Defined here (same package) to avoid
// the Go restriction against defining methods on non-local types.
func (s *MailSession) GetWorkerID() string        { return s.WorkerID }
func (s *MailSession) GetLastExecutionID() string { return s.LastExecutionID }

type MailSessionStore struct {
	db *sql.DB
}

func NewMailSessionStore(db *sql.DB) *MailSessionStore {
	return &MailSessionStore{db: db}
}

// GetSession returns nil if not found.
func (s *MailSessionStore) GetSession(threadID string) (*MailSession, error) {
	row := s.db.QueryRow(
		`SELECT thread_id, worker_id, session_id, last_execution_id, updated_at
         FROM mail_sessions WHERE thread_id = ?`, threadID)

	var sess MailSession
	err := row.Scan(&sess.ThreadID, &sess.WorkerID, &sess.SessionID, &sess.LastExecutionID, &sess.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &sess, nil
}

// UpsertSession creates or updates the session mapping for a mail thread.
func (s *MailSessionStore) UpsertSession(threadID, workerID, sessionID, lastExecutionID string) error {
	_, err := s.db.Exec(
		`INSERT INTO mail_sessions (thread_id, worker_id, session_id, last_execution_id, updated_at)
         VALUES (?, ?, ?, ?, ?)
         ON CONFLICT(thread_id) DO UPDATE SET
             worker_id = excluded.worker_id,
             session_id = excluded.session_id,
             last_execution_id = excluded.last_execution_id,
             updated_at = excluded.updated_at`,
		threadID, workerID, sessionID, lastExecutionID, time.Now().UTC())
	return err
}
