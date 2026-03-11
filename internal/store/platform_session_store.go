package store

import (
	"database/sql"
	"time"

	"github.com/robobee/core/internal/platform"
)

// PlatformSessionStore implements platform.SessionStore using the platform_sessions table.
type PlatformSessionStore struct {
	db *sql.DB
}

func NewPlatformSessionStore(db *sql.DB) *PlatformSessionStore {
	return &PlatformSessionStore{db: db}
}

// Get returns nil if not found.
func (s *PlatformSessionStore) Get(key string) (*platform.Session, error) {
	row := s.db.QueryRow(
		`SELECT session_key, platform, worker_id, session_id, last_execution_id
		 FROM platform_sessions WHERE session_key = ?`, key)

	var sess platform.Session
	err := row.Scan(&sess.Key, &sess.Platform, &sess.WorkerID, &sess.SessionID, &sess.LastExecutionID)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &sess, nil
}

// Upsert creates or updates a session.
func (s *PlatformSessionStore) Upsert(sess platform.Session) error {
	_, err := s.db.Exec(
		`INSERT INTO platform_sessions (session_key, platform, worker_id, session_id, last_execution_id, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?)
		 ON CONFLICT(session_key, platform) DO UPDATE SET
		     worker_id = excluded.worker_id,
		     session_id = excluded.session_id,
		     last_execution_id = excluded.last_execution_id,
		     updated_at = excluded.updated_at`,
		sess.Key, sess.Platform, sess.WorkerID, sess.SessionID, sess.LastExecutionID, time.Now().UTC())
	return err
}

// Delete removes a session by its key.
func (s *PlatformSessionStore) Delete(key string) error {
	_, err := s.db.Exec(`DELETE FROM platform_sessions WHERE session_key = ?`, key)
	return err
}

var _ platform.SessionStore = (*PlatformSessionStore)(nil)
