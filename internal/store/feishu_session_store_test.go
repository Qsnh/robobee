package store_test

import (
	"testing"

	"github.com/robobee/core/internal/store"
)

func newFeishuSessionStore(t *testing.T) *store.FeishuSessionStore {
	t.Helper()
	db, err := store.InitDB(":memory:")
	if err != nil {
		t.Fatalf("init db: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return store.NewFeishuSessionStore(db)
}

func TestFeishuSessionStore_GetSession_NotFound(t *testing.T) {
	s := newFeishuSessionStore(t)
	sess, err := s.GetSession("nonexistent")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if sess != nil {
		t.Fatalf("expected nil, got %+v", sess)
	}
}

func TestFeishuSessionStore_UpsertAndGet(t *testing.T) {
	s := newFeishuSessionStore(t)

	err := s.UpsertSession("chat1", "worker1", "session1", "exec1")
	if err != nil {
		t.Fatalf("upsert: %v", err)
	}

	sess, err := s.GetSession("chat1")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if sess == nil {
		t.Fatal("expected session, got nil")
	}
	if sess.WorkerID != "worker1" {
		t.Errorf("worker_id: got %q, want %q", sess.WorkerID, "worker1")
	}
	if sess.SessionID != "session1" {
		t.Errorf("session_id: got %q, want %q", sess.SessionID, "session1")
	}
	if sess.LastExecutionID != "exec1" {
		t.Errorf("last_execution_id: got %q, want %q", sess.LastExecutionID, "exec1")
	}
}

func TestFeishuSessionStore_Upsert_Updates(t *testing.T) {
	s := newFeishuSessionStore(t)

	_ = s.UpsertSession("chat1", "worker1", "session1", "exec1")
	_ = s.UpsertSession("chat1", "worker2", "session2", "exec2")

	sess, _ := s.GetSession("chat1")
	if sess.WorkerID != "worker2" {
		t.Errorf("expected updated worker_id worker2, got %q", sess.WorkerID)
	}
	if sess.LastExecutionID != "exec2" {
		t.Errorf("expected updated exec exec2, got %q", sess.LastExecutionID)
	}
}
