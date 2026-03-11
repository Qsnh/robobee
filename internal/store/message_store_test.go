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

	if err := s.Create(ctx, "msg-1", "feishu:chat1:userA", "feishu", "hello world"); err != nil {
		t.Fatalf("Create: %v", err)
	}
}

func TestMessageStore_SetWorkerID(t *testing.T) {
	s := setupMessageStore(t)
	ctx := context.Background()

	s.Create(ctx, "msg-1", "feishu:chat1:userA", "feishu", "hello")
	if err := s.SetWorkerID(ctx, "msg-1", "worker-abc"); err != nil {
		t.Fatalf("SetWorkerID: %v", err)
	}
}

func TestMessageStore_SetStatus(t *testing.T) {
	s := setupMessageStore(t)
	ctx := context.Background()

	s.Create(ctx, "msg-1", "feishu:chat1:userA", "feishu", "hello")
	if err := s.SetStatus(ctx, "msg-1", "debouncing"); err != nil {
		t.Fatalf("SetStatus: %v", err)
	}
}

func TestMessageStore_UpdateStatusBatch(t *testing.T) {
	s := setupMessageStore(t)
	ctx := context.Background()

	s.Create(ctx, "msg-1", "feishu:chat1:userA", "feishu", "a")
	s.Create(ctx, "msg-2", "feishu:chat1:userA", "feishu", "b")

	if err := s.UpdateStatusBatch(ctx, []string{"msg-1", "msg-2"}, "debouncing"); err != nil {
		t.Fatalf("UpdateStatusBatch: %v", err)
	}
}

func TestMessageStore_MarkMerged(t *testing.T) {
	s := setupMessageStore(t)
	ctx := context.Background()

	s.Create(ctx, "msg-1", "feishu:chat1:userA", "feishu", "msg1")
	s.Create(ctx, "msg-2", "feishu:chat1:userA", "feishu", "msg2")
	s.Create(ctx, "msg-3", "feishu:chat1:userA", "feishu", "msg3")

	if err := s.MarkMerged(ctx, "msg-1", []string{"msg-2", "msg-3"}); err != nil {
		t.Fatalf("MarkMerged: %v", err)
	}
}

func TestMessageStore_MarkTerminal_Done(t *testing.T) {
	s := setupMessageStore(t)
	ctx := context.Background()

	s.Create(ctx, "msg-1", "feishu:chat1:userA", "feishu", "hello")
	if err := s.MarkTerminal(ctx, []string{"msg-1"}, "done"); err != nil {
		t.Fatalf("MarkTerminal done: %v", err)
	}
}

func TestMessageStore_MarkTerminal_Failed(t *testing.T) {
	s := setupMessageStore(t)
	ctx := context.Background()

	s.Create(ctx, "msg-1", "feishu:chat1:userA", "feishu", "hello")
	if err := s.MarkTerminal(ctx, []string{"msg-1"}, "failed"); err != nil {
		t.Fatalf("MarkTerminal failed: %v", err)
	}
}

func TestMessageStore_GetUnfinished(t *testing.T) {
	s := setupMessageStore(t)
	ctx := context.Background()

	// received — no worker_id, should be excluded
	s.Create(ctx, "msg-received", "feishu:chat1:userA", "feishu", "received")

	// routed — has worker_id, should be returned
	s.Create(ctx, "msg-routed", "feishu:chat1:userA", "feishu", "routed")
	s.SetWorkerID(ctx, "msg-routed", "worker-1")

	// done — terminal, should be excluded
	s.Create(ctx, "msg-done", "feishu:chat1:userA", "feishu", "done")
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
