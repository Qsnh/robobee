package dispatcher_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/robobee/core/internal/dispatcher"
	"github.com/robobee/core/internal/model"
	"github.com/robobee/core/internal/msgsender"
	"github.com/robobee/core/internal/platform"
)

// --- Mocks ---

type mockExecManager struct {
	execResult model.WorkerExecution
	getResult  model.WorkerExecution
	// resumedWithSessionID records the sessionID passed to ExecuteWorkerWithSession
	resumedWithSessionID string
}

func (m *mockExecManager) ExecuteWorker(_ context.Context, _, _ string) (model.WorkerExecution, error) {
	return m.execResult, nil
}
func (m *mockExecManager) ExecuteWorkerWithSession(_ context.Context, _, _, sessionID string) (model.WorkerExecution, error) {
	m.resumedWithSessionID = sessionID
	return m.execResult, nil
}
func (m *mockExecManager) GetExecution(_ string) (model.WorkerExecution, error) {
	return m.getResult, nil
}

type mockTaskStore struct{}

func (s *mockTaskStore) SetExecution(_ context.Context, _, _, _ string) error { return nil }

type mockMsgStore struct{}

func (s *mockMsgStore) SetMessageExecution(_ context.Context, _, _, _ string) error { return nil }

type mockSessionStore struct {
	data    map[string]string // "sessionKey|agentID" -> sessionID
	cleared []string          // sessionKeys cleared
}

func newMockSessionStore() *mockSessionStore {
	return &mockSessionStore{data: make(map[string]string)}
}

func (s *mockSessionStore) GetSessionContext(_ context.Context, sessionKey, agentID string) (string, error) {
	return s.data[sessionKey+"|"+agentID], nil
}
func (s *mockSessionStore) UpsertSessionContext(_ context.Context, sessionKey, agentID, sessionID string) error {
	s.data[sessionKey+"|"+agentID] = sessionID
	return nil
}
func (s *mockSessionStore) ClearSessionContexts(_ context.Context, sessionKey string) error {
	s.cleared = append(s.cleared, sessionKey)
	return nil
}

func newDispatcher(mgr dispatcher.ExecutionManager, ss dispatcher.SessionStore) (*dispatcher.Dispatcher, chan dispatcher.DispatchTask) {
	in := make(chan dispatcher.DispatchTask, 4)
	d := dispatcher.New(mgr, &mockTaskStore{}, &mockMsgStore{}, ss, in)
	return d, in
}

func dispatchTask(taskType, sessionKey, workerID, instruction string) dispatcher.DispatchTask {
	return dispatcher.DispatchTask{
		TaskID:      "task-1",
		WorkerID:    workerID,
		SessionKey:  sessionKey,
		Instruction: instruction,
		ReplyTo:     platform.InboundMessage{Platform: "test", SessionKey: sessionKey},
		TaskType:    taskType,
		MessageID:   "msg-1",
	}
}

func collectEvents(out <-chan msgsender.SenderEvent, n int, timeout time.Duration) []msgsender.SenderEvent {
	var events []msgsender.SenderEvent
	deadline := time.After(timeout)
	for len(events) < n {
		select {
		case evt := <-out:
			events = append(events, evt)
		case <-deadline:
			return events
		}
	}
	return events
}

func TestDispatcher_ImmediateTask_EmitsACKThenResult(t *testing.T) {
	mgr := &mockExecManager{
		execResult: model.WorkerExecution{ID: "exec-1", SessionID: "sess-1"},
		getResult:  model.WorkerExecution{ID: "exec-1", Status: model.ExecStatusCompleted, Result: "done!"},
	}
	d, in := newDispatcher(mgr, newMockSessionStore())

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go d.Run(ctx)

	in <- dispatchTask("immediate", "s1", "w1", "check weather")

	events := collectEvents(d.Out(), 2, 2*time.Second)
	if len(events) < 2 {
		t.Fatalf("expected ACK+Result, got %d events", len(events))
	}
	if events[0].Type != msgsender.SenderEventACK {
		t.Errorf("first event should be ACK, got %v", events[0].Type)
	}
	if events[1].Type != msgsender.SenderEventResult {
		t.Errorf("second event should be Result, got %v", events[1].Type)
	}
	if events[1].Content != "done!" {
		t.Errorf("unexpected result content: %q", events[1].Content)
	}
}

func TestDispatcher_ClearTask_EmitsClearResultAndClearsSession(t *testing.T) {
	ss := newMockSessionStore()
	d, in := newDispatcher(&mockExecManager{}, ss)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go d.Run(ctx)

	in <- dispatcher.DispatchTask{
		TaskType:   "clear",
		SessionKey: "s1",
		ReplyTo:    platform.InboundMessage{Platform: "test", SessionKey: "s1"},
	}

	events := collectEvents(d.Out(), 1, 500*time.Millisecond)
	if len(events) != 1 || events[0].Type != msgsender.SenderEventResult {
		t.Fatalf("expected 1 result for clear, got %v", events)
	}
	if events[0].Content != "✅ 上下文已重置" {
		t.Errorf("unexpected clear content: %q", events[0].Content)
	}

	// Give dispatcher goroutine time to call ClearSessionContexts
	time.Sleep(50 * time.Millisecond)
	if len(ss.cleared) == 0 || ss.cleared[0] != "s1" {
		t.Errorf("expected ClearSessionContexts called with s1, got %v", ss.cleared)
	}
}

func TestDispatcher_ImmediateTask_ResumesWhenSessionExists(t *testing.T) {
	ss := newMockSessionStore()
	ss.data["s1|w1"] = "prior-session-id"

	mgr := &mockExecManager{
		execResult: model.WorkerExecution{ID: "exec-1", SessionID: "prior-session-id"},
		getResult:  model.WorkerExecution{ID: "exec-1", Status: model.ExecStatusCompleted, Result: "resumed!"},
	}
	d, in := newDispatcher(mgr, ss)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go d.Run(ctx)

	in <- dispatchTask("immediate", "s1", "w1", "follow-up")
	collectEvents(d.Out(), 2, 2*time.Second)

	if mgr.resumedWithSessionID != "prior-session-id" {
		t.Errorf("expected ExecuteWorkerWithSession called with prior-session-id, got %q", mgr.resumedWithSessionID)
	}
}

func TestDispatcher_ImmediateTask_FreshWhenNoSession(t *testing.T) {
	ss := newMockSessionStore() // no prior session

	mgr := &mockExecManager{
		execResult: model.WorkerExecution{ID: "exec-1", SessionID: "new-session"},
		getResult:  model.WorkerExecution{ID: "exec-1", Status: model.ExecStatusCompleted, Result: "fresh!"},
	}
	d, in := newDispatcher(mgr, ss)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go d.Run(ctx)

	in <- dispatchTask("immediate", "s1", "w1", "first message")
	collectEvents(d.Out(), 2, 2*time.Second)

	if mgr.resumedWithSessionID != "" {
		t.Errorf("expected ExecuteWorker (not ExecuteWorkerWithSession) for fresh start, but resume was called with %q", mgr.resumedWithSessionID)
	}
}

func TestDispatcher_ImmediateTask_ResumeFails_FallsBackToFresh(t *testing.T) {
	ss := newMockSessionStore()
	ss.data["s1|w1"] = "broken-session-id"

	// Manager returns error on ExecuteWorkerWithSession, succeeds on ExecuteWorker
	mgr := &fallbackExecManager{
		freshResult: model.WorkerExecution{ID: "exec-fresh", SessionID: "new-session"},
		getResult:   model.WorkerExecution{ID: "exec-fresh", Status: model.ExecStatusCompleted, Result: "fallback-ok"},
	}
	d, in := newDispatcher(mgr, ss)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go d.Run(ctx)

	in <- dispatchTask("immediate", "s1", "w1", "message after broken session")
	events := collectEvents(d.Out(), 2, 2*time.Second)

	// Should still get ACK + result
	if len(events) < 2 {
		t.Fatalf("expected ACK+Result even after resume failure, got %d events", len(events))
	}
	resultEvents := 0
	for _, e := range events {
		if e.Type == msgsender.SenderEventResult && e.Content == "fallback-ok" {
			resultEvents++
		}
	}
	if resultEvents == 0 {
		t.Error("expected fallback result 'fallback-ok' in events")
	}

	// Stale session should be cleared
	time.Sleep(50 * time.Millisecond)
	if len(ss.cleared) == 0 {
		t.Error("expected ClearSessionContexts called after resume failure")
	}
}

func TestDispatcher_TwoTasks_SameSession_Serialized(t *testing.T) {
	blocker := make(chan struct{})
	mgr := &blockingExecManager{blocker: blocker}
	d, in := newDispatcher(mgr, newMockSessionStore())

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go d.Run(ctx)

	t1 := dispatchTask("immediate", "s1", "w1", "first")
	t1.TaskID = "task-1"
	t2 := dispatchTask("immediate", "s1", "w1", "second")
	t2.TaskID = "task-2"

	in <- t1
	in <- t2

	// Both should get ACK
	events := collectEvents(d.Out(), 2, 500*time.Millisecond)
	ackCount := 0
	for _, e := range events {
		if e.Type == msgsender.SenderEventACK {
			ackCount++
		}
	}
	if ackCount != 2 {
		t.Errorf("expected 2 ACKs, got %d", ackCount)
	}

	// Unblock first execution
	close(blocker)

	// Should get 2 results
	results := collectEvents(d.Out(), 2, 2*time.Second)
	resultCount := 0
	for _, e := range results {
		if e.Type == msgsender.SenderEventResult {
			resultCount++
		}
	}
	if resultCount != 2 {
		t.Errorf("expected 2 results, got %d", resultCount)
	}
}

type blockingExecManager struct {
	blocker <-chan struct{}
}

func (m *blockingExecManager) ExecuteWorker(_ context.Context, _, _ string) (model.WorkerExecution, error) {
	<-m.blocker
	return model.WorkerExecution{ID: "exec-x"}, nil
}
func (m *blockingExecManager) ExecuteWorkerWithSession(_ context.Context, _, _, _ string) (model.WorkerExecution, error) {
	<-m.blocker
	return model.WorkerExecution{ID: "exec-x"}, nil
}
func (m *blockingExecManager) GetExecution(_ string) (model.WorkerExecution, error) {
	return model.WorkerExecution{ID: "exec-x", Status: model.ExecStatusCompleted, Result: "ok"}, nil
}

type fallbackExecManager struct {
	freshResult model.WorkerExecution
	getResult   model.WorkerExecution
}

func (m *fallbackExecManager) ExecuteWorker(_ context.Context, _, _ string) (model.WorkerExecution, error) {
	return m.freshResult, nil
}
func (m *fallbackExecManager) ExecuteWorkerWithSession(_ context.Context, _, _, _ string) (model.WorkerExecution, error) {
	return model.WorkerExecution{}, fmt.Errorf("session broken")
}
func (m *fallbackExecManager) GetExecution(_ string) (model.WorkerExecution, error) {
	return m.getResult, nil
}
