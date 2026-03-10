package store

import (
	"testing"
)

func TestMailSessionStore(t *testing.T) {
	db, err := InitDB(":memory:")
	if err != nil {
		t.Fatalf("InitDB: %v", err)
	}
	defer db.Close()

	s := NewMailSessionStore(db)

	// GetSession on empty store returns nil
	sess, err := s.GetSession("thread-1")
	if err != nil {
		t.Fatalf("GetSession: %v", err)
	}
	if sess != nil {
		t.Errorf("expected nil session, got %+v", sess)
	}

	// Upsert creates a session
	if err := s.UpsertSession("thread-1", "worker-a", "claude-sess-1", "exec-1"); err != nil {
		t.Fatalf("UpsertSession: %v", err)
	}

	sess, err = s.GetSession("thread-1")
	if err != nil {
		t.Fatalf("GetSession after upsert: %v", err)
	}
	if sess == nil {
		t.Fatal("expected session, got nil")
	}
	if sess.ThreadID != "thread-1" || sess.WorkerID != "worker-a" || sess.SessionID != "claude-sess-1" || sess.LastExecutionID != "exec-1" {
		t.Errorf("unexpected session: %+v", sess)
	}

	// Upsert updates existing session
	if err := s.UpsertSession("thread-1", "worker-b", "claude-sess-2", "exec-2"); err != nil {
		t.Fatalf("UpsertSession update: %v", err)
	}
	sess, _ = s.GetSession("thread-1")
	if sess.WorkerID != "worker-b" || sess.LastExecutionID != "exec-2" {
		t.Errorf("expected updated session, got %+v", sess)
	}
}
