package store

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/robobee/core/internal/platform"
)

// BatchMsg is a single row for a bulk insert via CreateBatch.
type BatchMsg struct {
	ID            string
	SessionKey    string
	Platform      string
	Content       string
	Raw           string
	PlatformMsgID string
	MessageTime   int64
	Status        string // "received" or "merged"
	MergedInto    string // non-empty only when Status == "merged"
}

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
// messageTime is stored as received_at; pass 0 to use server time.
func (s *MessageStore) Create(ctx context.Context, id, sessionKey, platform, content, raw, platformMsgID string, messageTime int64) (bool, error) {
	if messageTime == 0 {
		messageTime = time.Now().UnixMilli()
	}
	now := time.Now().UnixMilli()
	result, err := s.db.ExecContext(ctx,
		`INSERT OR IGNORE INTO platform_messages (id, session_key, platform, content, raw, platform_msg_id, received_at, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		id, sessionKey, platform, content, raw, platformMsgID, messageTime, now, now,
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

// SetStatus updates the status of a single message.
func (s *MessageStore) SetStatus(ctx context.Context, id, status string) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE platform_messages SET status = ?, updated_at = ? WHERE id = ?`,
		status, time.Now().UnixMilli(), id,
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
	args := make([]any, 0, len(ids)+2)
	args = append(args, status, time.Now().UnixMilli())
	for _, id := range ids {
		args = append(args, id)
	}
	_, err := s.db.ExecContext(ctx,
		fmt.Sprintf(`UPDATE platform_messages SET status = ?, updated_at = ? WHERE id IN (%s)`, placeholders),
		args...,
	)
	return err
}

// MarkMerged sets primaryID status to "merged" and records merged_into on all mergedIDs.
func (s *MessageStore) MarkMerged(ctx context.Context, primaryID string, mergedIDs []string) error {
	now := time.Now().UnixMilli()
	if _, err := s.db.ExecContext(ctx,
		`UPDATE platform_messages SET status = 'merged', updated_at = ? WHERE id = ?`, now, primaryID,
	); err != nil {
		return err
	}
	for _, id := range mergedIDs {
		if _, err := s.db.ExecContext(ctx,
			`UPDATE platform_messages SET status = 'merged', merged_into = ?, updated_at = ? WHERE id = ?`,
			primaryID, now, id,
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
	now := time.Now().UnixMilli()
	args := make([]any, 0, len(ids)+3)
	args = append(args, status, now, now) // status=?, processed_at=?, updated_at=?
	for _, id := range ids {
		args = append(args, id)
	}
	_, err := s.db.ExecContext(ctx,
		fmt.Sprintf(`UPDATE platform_messages SET status = ?, processed_at = ?, updated_at = ? WHERE id IN (%s)`, placeholders),
		args...,
	)
	return err
}

// GetSession returns the session state derived from the latest message row for
// the given sessionKey. Returns nil if no session exists, the latest row is a
// 'clear' sentinel, or no execution has been written yet (execution_id is empty).
func (s *MessageStore) GetSession(ctx context.Context, sessionKey string) (*platform.Session, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT status, execution_id, session_id, platform
		FROM platform_messages
		WHERE session_key = ?
		  AND (execution_id != '' OR status = 'clear')
		ORDER BY received_at DESC, rowid DESC
		LIMIT 1`, sessionKey)

	var status, executionID, sessionID, plt string
	if err := row.Scan(&status, &executionID, &sessionID, &plt); err == sql.ErrNoRows {
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
		SessionID:       sessionID,
		LastExecutionID: executionID,
	}, nil
}

// SetExecution records execution metadata on the given message row.
func (s *MessageStore) SetExecution(ctx context.Context, msgID, executionID, sessionID string) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE platform_messages SET execution_id = ?, session_id = ?, updated_at = ? WHERE id = ?`,
		executionID, sessionID, time.Now().UnixMilli(), msgID)
	return err
}

// SetMessageExecution writes execution_id and session_id back to a platform_messages row,
// but only when status = 'bee_processed'. This is a no-op if the Feeder rolled the row back.
func (s *MessageStore) SetMessageExecution(ctx context.Context, messageID, executionID, sessionID string) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE platform_messages
         SET execution_id = ?, session_id = ?, updated_at = ?
         WHERE id = ? AND status = 'bee_processed'`,
		executionID, sessionID, time.Now().UnixMilli(), messageID,
	)
	return err
}

// InsertClearSentinel inserts a 'clear' sentinel row to mark the session as reset.
func (s *MessageStore) InsertClearSentinel(ctx context.Context, id, sessionKey, plt string) error {
	now := time.Now().UnixMilli()
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO platform_messages (id, session_key, platform, content, status, received_at, created_at, updated_at)
		 VALUES (?, ?, ?, '', 'clear', ?, ?, ?)`,
		id, sessionKey, plt, now, now, now)
	return err
}

// CreateBatch inserts multiple message rows in a single transaction using
// ClaimedMessage is a platform_messages row claimed by the Feeder.
type ClaimedMessage struct {
	ID         string
	SessionKey string
	Platform   string
	Content    string
}

// ClaimBatch atomically selects up to batchSize 'received' messages and marks them 'feeding'.
func (s *MessageStore) ClaimBatch(ctx context.Context, batchSize int) ([]ClaimedMessage, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck

	rows, err := tx.QueryContext(ctx,
		`SELECT id, session_key, platform, content FROM platform_messages
         WHERE status = 'received'
         ORDER BY received_at ASC LIMIT ?`, batchSize)
	if err != nil {
		return nil, fmt.Errorf("select batch: %w", err)
	}
	var msgs []ClaimedMessage
	for rows.Next() {
		var m ClaimedMessage
		if err := rows.Scan(&m.ID, &m.SessionKey, &m.Platform, &m.Content); err != nil {
			rows.Close()
			return nil, fmt.Errorf("scan: %w", err)
		}
		msgs = append(msgs, m)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(msgs) == 0 {
		return nil, nil
	}

	ids := make([]string, len(msgs))
	for i, m := range msgs {
		ids[i] = m.ID
	}
	placeholders := strings.Repeat("?,", len(ids))
	placeholders = placeholders[:len(placeholders)-1]
	args := make([]any, 0, len(ids)+2)
	args = append(args, "feeding", time.Now().UnixMilli())
	for _, id := range ids {
		args = append(args, id)
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE platform_messages SET status = ?, updated_at = ? WHERE id IN (`+placeholders+`)`, args...); err != nil {
		return nil, fmt.Errorf("update feeding: %w", err)
	}
	return msgs, tx.Commit()
}

// MarkBeeProcessed sets status to 'bee_processed' for the given message IDs.
func (s *MessageStore) MarkBeeProcessed(ctx context.Context, ids []string) error {
	return s.UpdateStatusBatch(ctx, ids, "bee_processed")
}

// ResetFeedingBatch restores 'feeding' messages back to 'received'.
func (s *MessageStore) ResetFeedingBatch(ctx context.Context, ids []string) error {
	return s.UpdateStatusBatch(ctx, ids, "received")
}

// ResetFeedingToReceived resets all messages stuck in 'feeding' back to 'received'.
// Returns the IDs of affected rows so the caller can delete orphaned pending tasks.
func (s *MessageStore) ResetFeedingToReceived(ctx context.Context) ([]string, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id FROM platform_messages WHERE status = 'feeding'`)
	if err != nil {
		return nil, fmt.Errorf("select feeding: %w", err)
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		ids = append(ids, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(ids) == 0 {
		return nil, nil
	}
	if err := s.UpdateStatusBatch(ctx, ids, "received"); err != nil {
		return nil, fmt.Errorf("reset feeding: %w", err)
	}
	return ids, nil
}

// CountReceived returns the number of messages with status 'received'.
func (s *MessageStore) CountReceived(ctx context.Context) (int, error) {
	var count int
	err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM platform_messages WHERE status = 'received'`).Scan(&count)
	return count, err
}

// INSERT OR IGNORE. Returns the number of rows actually inserted.
// MessageTime is used as received_at; falls back to time.Now().UnixMilli() if zero.
func (s *MessageStore) CreateBatch(ctx context.Context, msgs []BatchMsg) (int64, error) {
	if len(msgs) == 0 {
		return 0, nil
	}

	now := time.Now().UnixMilli()
	placeholders := strings.Repeat("(?,?,?,?,?,?,?,?,?,?,?),", len(msgs))
	placeholders = placeholders[:len(placeholders)-1]

	args := make([]any, 0, len(msgs)*11)
	for _, m := range msgs {
		mt := m.MessageTime
		if mt == 0 {
			mt = now
		}
		args = append(args, m.ID, m.SessionKey, m.Platform, m.Content, m.Raw,
			m.PlatformMsgID, mt, m.Status, m.MergedInto, now, now)
	}

	result, err := s.db.ExecContext(ctx,
		fmt.Sprintf(`INSERT OR IGNORE INTO platform_messages
			(id, session_key, platform, content, raw, platform_msg_id, received_at, status, merged_into, created_at, updated_at)
			VALUES %s`, placeholders),
		args...,
	)
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}
