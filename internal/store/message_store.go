package store

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/robobee/core/internal/model"
	"github.com/robobee/core/internal/platform"
)

// MessageStore persists platform messages to the platform_messages table.
type MessageStore struct {
	db *sql.DB
}

// NewMessageStore constructs a MessageStore.
func NewMessageStore(db *sql.DB) *MessageStore {
	return &MessageStore{db: db}
}

// Create inserts a new message record with status "received".
// Returns inserted=false (no error) when platform_msg_id is non-empty and already exists.
// If platform_msg_id is empty, the insert always proceeds (no dedup).
func (s *MessageStore) Create(ctx context.Context, id, sessionKey, platform, content, raw, platformMsgID string) (bool, error) {
	result, err := s.db.ExecContext(ctx,
		`INSERT OR IGNORE INTO platform_messages (id, session_key, platform, content, raw, platform_msg_id, received_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		id, sessionKey, platform, content, raw, platformMsgID,
		time.Now().UnixMilli(),
	)
	if err != nil {
		return false, err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return false, err
	}
	return n == 1, nil
}

// SetWorkerID sets the worker_id and advances status to "routed".
func (s *MessageStore) SetWorkerID(ctx context.Context, id, workerID string) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE platform_messages SET worker_id = ?, status = 'routed' WHERE id = ?`,
		workerID, id,
	)
	return err
}

// SetStatus updates the status of a single message.
func (s *MessageStore) SetStatus(ctx context.Context, id, status string) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE platform_messages SET status = ? WHERE id = ?`,
		status, id,
	)
	return err
}

// UpdateStatusBatch sets the same status on all provided message IDs.
func (s *MessageStore) UpdateStatusBatch(ctx context.Context, ids []string, status string) error {
	if len(ids) == 0 {
		return nil
	}
	placeholders := strings.Repeat("?,", len(ids))
	placeholders = placeholders[:len(placeholders)-1]
	args := make([]any, 0, len(ids)+1)
	args = append(args, status)
	for _, id := range ids {
		args = append(args, id)
	}
	_, err := s.db.ExecContext(ctx,
		fmt.Sprintf(`UPDATE platform_messages SET status = ? WHERE id IN (%s)`, placeholders),
		args...,
	)
	return err
}

// MarkMerged sets primaryID status to "merged" and records merged_into on all mergedIDs.
func (s *MessageStore) MarkMerged(ctx context.Context, primaryID string, mergedIDs []string) error {
	if _, err := s.db.ExecContext(ctx,
		`UPDATE platform_messages SET status = 'merged' WHERE id = ?`, primaryID,
	); err != nil {
		return err
	}
	for _, id := range mergedIDs {
		if _, err := s.db.ExecContext(ctx,
			`UPDATE platform_messages SET status = 'merged', merged_into = ? WHERE id = ?`,
			primaryID, id,
		); err != nil {
			return err
		}
	}
	return nil
}

// MarkTerminal sets status to "done" or "failed" and records processed_at.
func (s *MessageStore) MarkTerminal(ctx context.Context, ids []string, status string) error {
	if len(ids) == 0 {
		return nil
	}
	placeholders := strings.Repeat("?,", len(ids))
	placeholders = placeholders[:len(placeholders)-1]
	args := make([]any, 0, len(ids)+2)
	args = append(args, status, time.Now().UnixMilli()) // status=?, processed_at=?
	for _, id := range ids {
		args = append(args, id)
	}
	_, err := s.db.ExecContext(ctx,
		fmt.Sprintf(`UPDATE platform_messages SET status = ?, processed_at = ? WHERE id IN (%s)`, placeholders),
		args...,
	)
	return err
}

// GetUnfinished returns messages with an active status that have a worker_id assigned,
// ordered by received_at ASC. Used for startup recovery.
func (s *MessageStore) GetUnfinished(ctx context.Context) ([]model.PendingMessage, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, session_key, worker_id, platform, content
		FROM platform_messages
		WHERE status IN ('routed', 'debouncing', 'merged', 'executing')
		  AND worker_id != ''
		ORDER BY received_at ASC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var msgs []model.PendingMessage
	for rows.Next() {
		var m model.PendingMessage
		if err := rows.Scan(&m.ID, &m.SessionKey, &m.WorkerID, &m.Platform, &m.Content); err != nil {
			return nil, err
		}
		msgs = append(msgs, m)
	}
	return msgs, rows.Err()
}

// GetSession returns the session state derived from the latest message row for
// the given sessionKey. Returns nil if no session exists, the latest row is a
// 'clear' sentinel, or no execution has been written yet (execution_id is empty).
func (s *MessageStore) GetSession(ctx context.Context, sessionKey string) (*platform.Session, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT status, worker_id, execution_id, session_id, platform
		FROM platform_messages
		WHERE session_key = ?
		  AND (execution_id != '' OR status = 'clear')
		ORDER BY received_at DESC, rowid DESC
		LIMIT 1`, sessionKey)

	var status, workerID, executionID, sessionID, plt string
	if err := row.Scan(&status, &workerID, &executionID, &sessionID, &plt); err == sql.ErrNoRows {
		return nil, nil
	} else if err != nil {
		return nil, err
	}
	if status == "clear" || executionID == "" {
		return nil, nil
	}
	return &platform.Session{
		Key:             sessionKey,
		Platform:        plt,
		WorkerID:        workerID,
		SessionID:       sessionID,
		LastExecutionID: executionID,
	}, nil
}

// SetExecution records execution metadata on the given message row.
func (s *MessageStore) SetExecution(ctx context.Context, msgID, executionID, sessionID string) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE platform_messages SET execution_id = ?, session_id = ? WHERE id = ?`,
		executionID, sessionID, msgID)
	return err
}

// InsertClearSentinel inserts a 'clear' sentinel row to mark the session as reset.
func (s *MessageStore) InsertClearSentinel(ctx context.Context, id, sessionKey, plt string) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO platform_messages (id, session_key, platform, content, status, received_at)
		 VALUES (?, ?, ?, '', 'clear', ?)`,
		id, sessionKey, plt, time.Now().UnixMilli())
	return err
}
