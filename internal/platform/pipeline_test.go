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

// stubPipelineStore implements platform.MessageStore for pipeline tests.
type stubPipelineStore struct {
	sessions       map[string]*Session
	clearSentinels []string
	executions     map[string][2]string // msgID -> [executionID, sessionID]
	clearErr       error
}

func newStubPipelineStore() *stubPipelineStore {
	return &stubPipelineStore{
		sessions:   make(map[string]*Session),
		executions: make(map[string][2]string),
	}
}

func (s *stubPipelineStore) Create(_ context.Context, _, _, _, _ string) error  { return nil }
func (s *stubPipelineStore) SetWorkerID(_ context.Context, _, _ string) error   { return nil }
func (s *stubPipelineStore) SetStatus(_ context.Context, _, _ string) error     { return nil }
func (s *stubPipelineStore) UpdateStatusBatch(_ context.Context, _ []string, _ string) error {
	return nil
}
func (s *stubPipelineStore) MarkMerged(_ context.Context, _ string, _ []string) error { return nil }
func (s *stubPipelineStore) MarkTerminal(_ context.Context, _ []string, _ string) error {
	return nil
}
func (s *stubPipelineStore) GetUnfinished(_ context.Context) ([]model.PendingMessage, error) {
	return nil, nil
}
func (s *stubPipelineStore) GetSession(_ context.Context, key string) (*Session, error) {
	sess, ok := s.sessions[key]
	if !ok {
		return nil, nil
	}
	return sess, nil
}
func (s *stubPipelineStore) SetExecution(_ context.Context, msgID, execID, sessID string) error {
	s.executions[msgID] = [2]string{execID, sessID}
	return nil
}
func (s *stubPipelineStore) InsertClearSentinel(_ context.Context, id, key, _ string) error {
	if s.clearErr != nil {
		return s.clearErr
	}
	s.clearSentinels = append(s.clearSentinels, id)
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

// --- Helpers ---

func newPipeline(router MessageRouter, store MessageStore, mgr ExecutionManager) *Pipeline {
	return NewPipeline(router, store, mgr)
}

func msg(content string) InboundMessage {
	return InboundMessage{
		Platform:   "test",
		SessionKey: "test:session1",
		Content:    content,
	}
}

// --- Tests ---

func TestPipeline_ClearCommand(t *testing.T) {
	store := newStubPipelineStore()
	p := newPipeline(&stubRouter{workerID: "w1"}, store, &stubManager{})
	result := p.Handle(context.Background(), msg("clear"))

	if result != clearMessage {
		t.Errorf("want %q, got %q", clearMessage, result)
	}
	if len(store.clearSentinels) == 0 {
		t.Error("InsertClearSentinel should have been called")
	}
}

func TestPipeline_ClearCommand_SentinelError(t *testing.T) {
	store := newStubPipelineStore()
	store.clearErr = errors.New("db error")
	p := newPipeline(&stubRouter{workerID: "w1"}, store, &stubManager{})
	result := p.Handle(context.Background(), msg("clear"))

	if result != errorMessage {
		t.Errorf("want %q, got %q", errorMessage, result)
	}
}

func TestPipeline_HandleRouted_NewSession(t *testing.T) {
	mgr := &stubManager{exec: model.WorkerExecution{
		ID:        "exec-1",
		SessionID: "sess-1",
		Status:    model.ExecStatusCompleted,
		Result:    "deployed",
	}}
	store := newStubPipelineStore()
	p := newPipeline(&stubRouter{workerID: "w1"}, store, mgr)
	result := p.HandleRouted(context.Background(), msg("deploy"), "w-deploy", "msg-1")

	if result != "deployed" {
		t.Errorf("want deployed, got %s", result)
	}
	if got, ok := store.executions["msg-1"]; !ok || got[0] != "exec-1" {
		t.Errorf("SetExecution not called correctly: %v", store.executions)
	}
}

func TestPipeline_HandleRouted_ExistingSession(t *testing.T) {
	store := newStubPipelineStore()
	store.sessions["test:session1"] = &Session{
		Key:             "test:session1",
		Platform:        "test",
		WorkerID:        "w1",
		LastExecutionID: "prev-exec",
	}
	mgr := &stubManager{exec: model.WorkerExecution{
		ID:        "exec-2",
		SessionID: "sess-2",
		Status:    model.ExecStatusCompleted,
		Result:    "continued",
	}}
	p := newPipeline(&stubRouter{workerID: "w1"}, store, mgr)
	result := p.HandleRouted(context.Background(), msg("continue"), "w1", "msg-2")

	if result != "continued" {
		t.Errorf("want continued, got %s", result)
	}
}

func TestPipeline_Route_ReturnsWorkerID(t *testing.T) {
	p := newPipeline(&stubRouter{workerID: "w-deploy"}, newStubPipelineStore(), &stubManager{})
	id, err := p.Route(context.Background(), "deploy the app")
	if err != nil {
		t.Fatalf("Route: %v", err)
	}
	if id != "w-deploy" {
		t.Errorf("want w-deploy, got %s", id)
	}
}

func TestPipeline_Route_Error(t *testing.T) {
	p := newPipeline(&stubRouter{err: errors.New("none")}, newStubPipelineStore(), &stubManager{})
	_, err := p.Route(context.Background(), "anything")
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestPipeline_HandleRouted_FailedExecution(t *testing.T) {
	mgr := &stubManager{exec: model.WorkerExecution{
		Status: model.ExecStatusFailed,
		Result: "build error",
	}}
	p := newPipeline(&stubRouter{workerID: "w1"}, newStubPipelineStore(), mgr)
	result := p.HandleRouted(context.Background(), msg("build"), "w1", "msg-1")

	want := "❌ 任务执行失败: build error"
	if result != want {
		t.Errorf("want %q, got %q", want, result)
	}
}

func TestPipeline_HandleRouted_CompletedWithEmptyResult(t *testing.T) {
	mgr := &stubManager{exec: model.WorkerExecution{
		Status: model.ExecStatusCompleted,
		Result: "",
	}}
	p := newPipeline(&stubRouter{workerID: "w1"}, newStubPipelineStore(), mgr)
	result := p.HandleRouted(context.Background(), msg("run task"), "w1", "msg-1")

	if result != "✅ 任务已完成" {
		t.Errorf("want '✅ 任务已完成', got %q", result)
	}
}

func TestPipeline_HandleRouted_ExecuteWorkerError(t *testing.T) {
	mgr := &stubManager{err: errors.New("worker unavailable")}
	p := newPipeline(&stubRouter{workerID: "w1"}, newStubPipelineStore(), mgr)
	result := p.HandleRouted(context.Background(), msg("run"), "w1", "msg-1")

	if result != errorMessage {
		t.Errorf("want %q, got %q", errorMessage, result)
	}
}

func TestPipeline_GroupChat_TwoUsersGetIndependentSessions(t *testing.T) {
	store := newStubPipelineStore()
	store.sessions["feishu:chat123:userA"] = &Session{
		Key: "feishu:chat123:userA", LastExecutionID: "exec-a",
	}
	mgr := &stubManager{exec: model.WorkerExecution{
		ID: "exec-new", Status: model.ExecStatusCompleted, Result: "done",
	}}
	p := newPipeline(&stubRouter{workerID: "w1"}, store, mgr)

	msgUserA := InboundMessage{Platform: "feishu", SenderID: "userA", SessionKey: "feishu:chat123:userA", Content: "continue"}
	msgUserB := InboundMessage{Platform: "feishu", SenderID: "userB", SessionKey: "feishu:chat123:userB", Content: "start new"}

	p.HandleRouted(context.Background(), msgUserA, "w1", "msg-a")
	p.HandleRouted(context.Background(), msgUserB, "w1", "msg-b")

	if store.executions["msg-a"][0] != "exec-new" {
		t.Errorf("userA execution not recorded")
	}
	if store.executions["msg-b"][0] != "exec-new" {
		t.Errorf("userB execution not recorded")
	}
}
