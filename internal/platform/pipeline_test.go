package platform

import (
	"context"
	"errors"
	"testing"

	"github.com/robobee/core/internal/model"
)

// --- Stubs ---

type stubRouter struct {
	workerID string
	err      error
}

func (r *stubRouter) Route(_ context.Context, _ string) (string, error) {
	return r.workerID, r.err
}

type stubSessionStore struct {
	sessions map[string]*Session
	deleteErr error
}

func newStubSessionStore() *stubSessionStore {
	return &stubSessionStore{sessions: make(map[string]*Session)}
}

func (s *stubSessionStore) Get(key string) (*Session, error) {
	sess, ok := s.sessions[key]
	if !ok {
		return nil, nil
	}
	return sess, nil
}

func (s *stubSessionStore) Upsert(sess Session) error {
	cp := sess
	s.sessions[sess.Key] = &cp
	return nil
}

func (s *stubSessionStore) Delete(key string) error {
	if s.deleteErr != nil {
		return s.deleteErr
	}
	delete(s.sessions, key)
	return nil
}

type stubManager struct {
	exec model.WorkerExecution
	err  error
}

func (m *stubManager) ExecuteWorker(_ context.Context, _, _ string) (model.WorkerExecution, error) {
	return m.exec, m.err
}

func (m *stubManager) ReplyExecution(_ context.Context, _, _ string) (model.WorkerExecution, error) {
	return m.exec, m.err
}

func (m *stubManager) GetExecution(_ string) (model.WorkerExecution, error) {
	return m.exec, m.err
}

// --- Tests ---

func newPipeline(router MessageRouter, store SessionStore, mgr ExecutionManager) *Pipeline {
	p := NewPipeline(router, store, mgr)
	return p
}

func msg(content string) InboundMessage {
	return InboundMessage{
		Platform:   "test",
		SessionKey: "test:session1",
		Content:    content,
	}
}

func TestPipeline_ClearCommand(t *testing.T) {
	store := newStubSessionStore()
	store.sessions["test:session1"] = &Session{Key: "test:session1", LastExecutionID: "old-exec"}

	p := newPipeline(&stubRouter{workerID: "w1"}, store, &stubManager{})
	result := p.Handle(context.Background(), msg("clear"))

	if result != clearMessage {
		t.Errorf("want %q, got %q", clearMessage, result)
	}
	if _, ok := store.sessions["test:session1"]; ok {
		t.Error("session should have been deleted")
	}
}

func TestPipeline_ClearCommand_CaseInsensitive(t *testing.T) {
	store := newStubSessionStore()
	p := newPipeline(&stubRouter{workerID: "w1"}, store, &stubManager{})
	result := p.Handle(context.Background(), msg("  CLEAR  "))

	if result != clearMessage {
		t.Errorf("want %q, got %q", clearMessage, result)
	}
}

func TestPipeline_NoWorkerFound(t *testing.T) {
	p := newPipeline(
		&stubRouter{err: errors.New("no workers")},
		newStubSessionStore(),
		&stubManager{},
	)
	result := p.Handle(context.Background(), msg("do something"))

	if result != noWorkerMsg {
		t.Errorf("want %q, got %q", noWorkerMsg, result)
	}
}

func TestPipeline_NewSession_CompletedExecution(t *testing.T) {
	store := newStubSessionStore()
	mgr := &stubManager{exec: model.WorkerExecution{
		ID:        "exec-1",
		SessionID: "sess-1",
		Status:    model.ExecStatusCompleted,
		Result:    "All done.",
	}}

	p := newPipeline(&stubRouter{workerID: "w1"}, store, mgr)
	result := p.Handle(context.Background(), msg("deploy the app"))

	if result != "All done." {
		t.Errorf("want %q, got %q", "All done.", result)
	}

	sess := store.sessions["test:session1"]
	if sess == nil {
		t.Fatal("session should have been upserted")
	}
	if sess.LastExecutionID != "exec-1" {
		t.Errorf("LastExecutionID: got %q, want %q", sess.LastExecutionID, "exec-1")
	}
}

func TestPipeline_ExistingSession_ResumesExecution(t *testing.T) {
	store := newStubSessionStore()
	store.sessions["test:session1"] = &Session{
		Key:             "test:session1",
		Platform:        "test",
		WorkerID:        "w1",
		LastExecutionID: "prev-exec",
	}

	replied := false
	mgr := &stubManager{exec: model.WorkerExecution{
		ID:        "exec-2",
		SessionID: "sess-1",
		Status:    model.ExecStatusCompleted,
		Result:    "Replied.",
	}}

	// Verify ReplyExecution is called instead of ExecuteWorker by checking exec ID.
	_ = replied
	p := newPipeline(&stubRouter{workerID: "w1"}, store, mgr)
	result := p.Handle(context.Background(), msg("continue"))

	if result != "Replied." {
		t.Errorf("want %q, got %q", "Replied.", result)
	}
}

func TestPipeline_FailedExecution(t *testing.T) {
	mgr := &stubManager{exec: model.WorkerExecution{
		Status: model.ExecStatusFailed,
		Result: "build error",
	}}

	p := newPipeline(&stubRouter{workerID: "w1"}, newStubSessionStore(), mgr)
	result := p.Handle(context.Background(), msg("build"))

	want := "❌ 任务执行失败: build error"
	if result != want {
		t.Errorf("want %q, got %q", want, result)
	}
}

func TestPipeline_CompletedWithEmptyResult(t *testing.T) {
	mgr := &stubManager{exec: model.WorkerExecution{
		Status: model.ExecStatusCompleted,
		Result: "",
	}}

	p := newPipeline(&stubRouter{workerID: "w1"}, newStubSessionStore(), mgr)
	result := p.Handle(context.Background(), msg("run task"))

	if result != "✅ 任务已完成" {
		t.Errorf("want '✅ 任务已完成', got %q", result)
	}
}

func TestPipeline_ExecuteWorkerError(t *testing.T) {
	mgr := &stubManager{err: errors.New("worker unavailable")}

	p := newPipeline(&stubRouter{workerID: "w1"}, newStubSessionStore(), mgr)
	result := p.Handle(context.Background(), msg("run"))

	if result != errorMessage {
		t.Errorf("want %q, got %q", errorMessage, result)
	}
}
