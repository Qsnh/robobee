package store

import (
	"context"
	"testing"
)

func setupMessageStore(t *testing.T) *MessageStore {
	t.Helper()
	db, err := InitDB(t.TempDir() + "/test.db")
	if err != nil {
		t.Fatalf("InitDB: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return NewMessageStore(db)
}

func TestMessageStore_Create(t *testing.T) {
	s := setupMessageStore(t)
	ctx := context.Background()

	if _, err := s.Create(ctx, "msg-1", "feishu:chat1:userA", "feishu", "hello world", `{"text":"hello world"}`, ""); err != nil {
		t.Fatalf("Create: %v", err)
	}

	var raw string
	if err := s.db.QueryRowContext(ctx, `SELECT raw FROM platform_messages WHERE id = ?`, "msg-1").Scan(&raw); err != nil {
		t.Fatalf("query raw: %v", err)
	}
	if raw != `{"text":"hello world"}` {
		t.Errorf("raw: got %q, want %q", raw, `{"text":"hello world"}`)
	}
}

func TestMessageStore_SetWorkerID(t *testing.T) {
	s := setupMessageStore(t)
	ctx := context.Background()

	s.Create(ctx, "msg-1", "feishu:chat1:userA", "feishu", "hello", "", "") //nolint
	if err := s.SetWorkerID(ctx, "msg-1", "worker-abc"); err != nil {
		t.Fatalf("SetWorkerID: %v", err)
	}
}

func TestMessageStore_SetStatus(t *testing.T) {
	s := setupMessageStore(t)
	ctx := context.Background()

	s.Create(ctx, "msg-1", "feishu:chat1:userA", "feishu", "hello", "", "") //nolint
	if err := s.SetStatus(ctx, "msg-1", "debouncing"); err != nil {
		t.Fatalf("SetStatus: %v", err)
	}
}

func TestMessageStore_UpdateStatusBatch(t *testing.T) {
	s := setupMessageStore(t)
	ctx := context.Background()

	s.Create(ctx, "msg-1", "feishu:chat1:userA", "feishu", "a", "", "") //nolint
	s.Create(ctx, "msg-2", "feishu:chat1:userA", "feishu", "b", "", "") //nolint

	if err := s.UpdateStatusBatch(ctx, []string{"msg-1", "msg-2"}, "debouncing"); err != nil {
		t.Fatalf("UpdateStatusBatch: %v", err)
	}
}

func TestMessageStore_MarkMerged(t *testing.T) {
	s := setupMessageStore(t)
	ctx := context.Background()

	s.Create(ctx, "msg-1", "feishu:chat1:userA", "feishu", "msg1", "", "") //nolint
	s.Create(ctx, "msg-2", "feishu:chat1:userA", "feishu", "msg2", "", "") //nolint
	s.Create(ctx, "msg-3", "feishu:chat1:userA", "feishu", "msg3", "", "") //nolint

	if err := s.MarkMerged(ctx, "msg-1", []string{"msg-2", "msg-3"}); err != nil {
		t.Fatalf("MarkMerged: %v", err)
	}
}

func TestMessageStore_MarkTerminal_Done(t *testing.T) {
	s := setupMessageStore(t)
	ctx := context.Background()

	s.Create(ctx, "msg-1", "feishu:chat1:userA", "feishu", "hello", "", "") //nolint
	if err := s.MarkTerminal(ctx, []string{"msg-1"}, "done"); err != nil {
		t.Fatalf("MarkTerminal done: %v", err)
	}
}

func TestMessageStore_MarkTerminal_Failed(t *testing.T) {
	s := setupMessageStore(t)
	ctx := context.Background()

	s.Create(ctx, "msg-1", "feishu:chat1:userA", "feishu", "hello", "", "") //nolint
	if err := s.MarkTerminal(ctx, []string{"msg-1"}, "failed"); err != nil {
		t.Fatalf("MarkTerminal failed: %v", err)
	}
}

func TestMessageStore_GetUnfinished(t *testing.T) {
	s := setupMessageStore(t)
	ctx := context.Background()

	// received — no worker_id, should be excluded
	s.Create(ctx, "msg-received", "feishu:chat1:userA", "feishu", "received", "", "") //nolint

	// routed — has worker_id, should be returned
	s.Create(ctx, "msg-routed", "feishu:chat1:userA", "feishu", "routed", "", "") //nolint
	s.SetWorkerID(ctx, "msg-routed", "worker-1")

	// done — terminal, should be excluded
	s.Create(ctx, "msg-done", "feishu:chat1:userA", "feishu", "done", "", "") //nolint
	s.SetWorkerID(ctx, "msg-done", "worker-1")
	s.MarkTerminal(ctx, []string{"msg-done"}, "done")

	pending, err := s.GetUnfinished(ctx)
	if err != nil {
		t.Fatalf("GetUnfinished: %v", err)
	}
	if len(pending) != 1 {
		t.Errorf("expected 1 pending message, got %d", len(pending))
	}
	if len(pending) > 0 && pending[0].ID != "msg-routed" {
		t.Errorf("expected msg-routed, got %s", pending[0].ID)
	}
}

func TestMessageStore_GetSession_NoRows(t *testing.T) {
	s := setupMessageStore(t)
	ctx := context.Background()

	sess, err := s.GetSession(ctx, "feishu:chat1:userA")
	if err != nil {
		t.Fatalf("GetSession: %v", err)
	}
	if sess != nil {
		t.Errorf("expected nil session for unknown key, got %+v", sess)
	}
}

func TestMessageStore_GetSession_AfterExecution(t *testing.T) {
	s := setupMessageStore(t)
	ctx := context.Background()

	s.Create(ctx, "msg-1", "feishu:chat1:userA", "feishu", "hello", "", "") //nolint
	s.SetWorkerID(ctx, "msg-1", "worker-abc")
	if err := s.SetExecution(ctx, "msg-1", "exec-1", "sess-1"); err != nil {
		t.Fatalf("SetExecution: %v", err)
	}
	s.MarkTerminal(ctx, []string{"msg-1"}, "done")

	sess, err := s.GetSession(ctx, "feishu:chat1:userA")
	if err != nil {
		t.Fatalf("GetSession: %v", err)
	}
	if sess == nil {
		t.Fatal("expected session, got nil")
	}
	if sess.LastExecutionID != "exec-1" {
		t.Errorf("LastExecutionID: got %q, want %q", sess.LastExecutionID, "exec-1")
	}
	if sess.SessionID != "sess-1" {
		t.Errorf("SessionID: got %q, want %q", sess.SessionID, "sess-1")
	}
	if sess.WorkerID != "worker-abc" {
		t.Errorf("WorkerID: got %q, want %q", sess.WorkerID, "worker-abc")
	}
	if sess.Platform != "feishu" {
		t.Errorf("Platform: got %q, want %q", sess.Platform, "feishu")
	}
	if sess.Key != "feishu:chat1:userA" {
		t.Errorf("Key: got %q, want %q", sess.Key, "feishu:chat1:userA")
	}
}

func TestMessageStore_SetExecution(t *testing.T) {
	s := setupMessageStore(t)
	ctx := context.Background()

	s.Create(ctx, "msg-1", "feishu:chat1:userA", "feishu", "hello", "", "") //nolint
	if err := s.SetExecution(ctx, "msg-1", "exec-42", "sess-42"); err != nil {
		t.Fatalf("SetExecution: %v", err)
	}
	// Verify via GetSession (the only way to read back execution metadata)
	// Need worker_id set so GetSession returns a result
	s.SetWorkerID(ctx, "msg-1", "worker-abc")
	sess, err := s.GetSession(ctx, "feishu:chat1:userA")
	if err != nil {
		t.Fatalf("GetSession: %v", err)
	}
	if sess == nil {
		t.Fatal("expected session, got nil")
	}
	if sess.LastExecutionID != "exec-42" {
		t.Errorf("LastExecutionID: got %q, want exec-42", sess.LastExecutionID)
	}
}

func TestMessageStore_GetSession_AfterClear(t *testing.T) {
	s := setupMessageStore(t)
	ctx := context.Background()

	s.Create(ctx, "msg-1", "feishu:chat1:userA", "feishu", "hello", "", "") //nolint
	s.SetWorkerID(ctx, "msg-1", "worker-abc")
	s.SetExecution(ctx, "msg-1", "exec-1", "sess-1")
	s.MarkTerminal(ctx, []string{"msg-1"}, "done")

	if err := s.InsertClearSentinel(ctx, "clear-1", "feishu:chat1:userA", "feishu"); err != nil {
		t.Fatalf("InsertClearSentinel: %v", err)
	}

	sess, err := s.GetSession(ctx, "feishu:chat1:userA")
	if err != nil {
		t.Fatalf("GetSession after clear: %v", err)
	}
	if sess != nil {
		t.Errorf("expected nil after clear, got %+v", sess)
	}
}

func TestMessageStore_GetSession_FirstMessageNoExecution(t *testing.T) {
	s := setupMessageStore(t)
	ctx := context.Background()

	// message exists but SetExecution hasn't been called yet
	s.Create(ctx, "msg-1", "feishu:chat1:userA", "feishu", "hello", "", "") //nolint
	s.SetWorkerID(ctx, "msg-1", "worker-abc")

	sess, err := s.GetSession(ctx, "feishu:chat1:userA")
	if err != nil {
		t.Fatalf("GetSession: %v", err)
	}
	if sess != nil {
		t.Errorf("expected nil when execution_id is empty, got %+v", sess)
	}
}

func TestMessageStore_InsertClearSentinel_NotRecoverable(t *testing.T) {
	s := setupMessageStore(t)
	ctx := context.Background()

	if err := s.InsertClearSentinel(ctx, "clear-1", "feishu:chat1:userA", "feishu"); err != nil {
		t.Fatalf("InsertClearSentinel: %v", err)
	}

	// Clear sentinel must never appear in GetUnfinished (worker_id='')
	pending, err := s.GetUnfinished(ctx)
	if err != nil {
		t.Fatalf("GetUnfinished: %v", err)
	}
	for _, m := range pending {
		if m.ID == "clear-1" {
			t.Error("clear sentinel should not appear in GetUnfinished")
		}
	}
}

func TestMessageStore_Create_Dedup_FirstInsertReturnsTrue(t *testing.T) {
	s := setupMessageStore(t)
	ctx := context.Background()

	inserted, err := s.Create(ctx, "msg-1", "feishu:chat1:userA", "feishu", "hello", "", "feishu-msg-abc")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if !inserted {
		t.Error("first insert: want inserted=true, got false")
	}
}

func TestMessageStore_Create_Dedup_DuplicatePlatformMsgID(t *testing.T) {
	s := setupMessageStore(t)
	ctx := context.Background()

	s.Create(ctx, "msg-1", "feishu:chat1:userA", "feishu", "hello", "", "feishu-msg-abc") //nolint
	inserted, err := s.Create(ctx, "msg-2", "feishu:chat1:userA", "feishu", "hello", "", "feishu-msg-abc")
	if err != nil {
		t.Fatalf("duplicate Create: %v", err)
	}
	if inserted {
		t.Error("duplicate insert: want inserted=false, got true")
	}
}

func TestMessageStore_Create_Dedup_EmptyPlatformMsgIDNotDeduped(t *testing.T) {
	s := setupMessageStore(t)
	ctx := context.Background()

	inserted1, err := s.Create(ctx, "msg-1", "feishu:chat1:userA", "feishu", "hello", "", "")
	if err != nil || !inserted1 {
		t.Fatalf("first empty-id insert: err=%v inserted=%v", err, inserted1)
	}
	inserted2, err := s.Create(ctx, "msg-2", "feishu:chat1:userA", "feishu", "hello", "", "")
	if err != nil || !inserted2 {
		t.Fatalf("second empty-id insert: err=%v inserted=%v", err, inserted2)
	}
}

func TestMessageStore_MarkTerminal_ProcessedAtMillisecondPrecision(t *testing.T) {
	s := setupMessageStore(t)
	ctx := context.Background()

	s.Create(ctx, "msg-1", "feishu:chat1:userA", "feishu", "hello", "", "") //nolint
	if err := s.MarkTerminal(ctx, []string{"msg-1"}, "done"); err != nil {
		t.Fatalf("MarkTerminal: %v", err)
	}

	var processedAt string
	err := s.db.QueryRowContext(ctx,
		`SELECT processed_at FROM platform_messages WHERE id = ?`, "msg-1",
	).Scan(&processedAt)
	if err != nil {
		t.Fatalf("scan processed_at: %v", err)
	}
	if len(processedAt) < 24 || processedAt[19] != '.' || processedAt[len(processedAt)-1] != 'Z' {
		t.Errorf("processed_at %q: want millisecond format like 2026-03-11T10:30:00.123Z", processedAt)
	}
}

func TestMessageStore_Create_ReceivedAtMillisecondPrecision(t *testing.T) {
	s := setupMessageStore(t)
	ctx := context.Background()

	s.Create(ctx, "msg-ms", "feishu:chat1:userA", "feishu", "hello", "", "") //nolint

	var receivedAt string
	err := s.db.QueryRowContext(ctx,
		`SELECT received_at FROM platform_messages WHERE id = ?`, "msg-ms",
	).Scan(&receivedAt)
	if err != nil {
		t.Fatalf("scan received_at: %v", err)
	}
	// must match 2026-03-11T10:30:00.123Z (millisecond suffix + Z)
	if len(receivedAt) < 24 || receivedAt[19] != '.' || receivedAt[len(receivedAt)-1] != 'Z' {
		t.Errorf("received_at %q: want millisecond format like 2026-03-11T10:30:00.123Z", receivedAt)
	}
}

func TestMessageStore_InsertClearSentinel_UnaffectedByDedupSchema(t *testing.T) {
	s := setupMessageStore(t)
	ctx := context.Background()

	// Two clear sentinels for different sessions — both must succeed
	if err := s.InsertClearSentinel(ctx, "clear-a", "feishu:chat1:userA", "feishu"); err != nil {
		t.Fatalf("InsertClearSentinel A: %v", err)
	}
	if err := s.InsertClearSentinel(ctx, "clear-b", "feishu:chat2:userB", "feishu"); err != nil {
		t.Fatalf("InsertClearSentinel B: %v", err)
	}
}
