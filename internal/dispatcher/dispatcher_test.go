package dispatcher_test

import (
	"context"
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
}

func (m *mockExecManager) ExecuteWorker(_ context.Context, _, _ string) (model.WorkerExecution, error) {
	return m.execResult, nil
}
func (m *mockExecManager) ReplyExecution(_ context.Context, _, _ string) (model.WorkerExecution, error) {
	return m.execResult, nil
}
func (m *mockExecManager) GetExecution(_ string) (model.WorkerExecution, error) {
	return m.getResult, nil
}

type mockTaskStore struct{}

func (s *mockTaskStore) SetExecution(_ context.Context, _, _, _ string) error { return nil }

type mockMsgStore struct{}

func (s *mockMsgStore) GetSession(_ context.Context, _ string) (*platform.Session, error) {
	return nil, nil // no prior session; tests use fresh execution
}
func (s *mockMsgStore) SetMessageExecution(_ context.Context, _, _, _ string) error { return nil }

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
	in := make(chan dispatcher.DispatchTask, 1)
	mgr := &mockExecManager{
		execResult: model.WorkerExecution{ID: "exec-1", SessionID: "sess-1"},
		getResult:  model.WorkerExecution{ID: "exec-1", Status: model.ExecStatusCompleted, Result: "done!"},
	}
	d := dispatcher.New(mgr, &mockTaskStore{}, &mockMsgStore{}, in)

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

func TestDispatcher_ClearTask_EmitsClearResult(t *testing.T) {
	in := make(chan dispatcher.DispatchTask, 1)
	d := dispatcher.New(&mockExecManager{}, &mockTaskStore{}, &mockMsgStore{}, in)

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
}

func TestDispatcher_TwoTasks_SameSession_Serialized(t *testing.T) {
	in := make(chan dispatcher.DispatchTask, 2)
	blocker := make(chan struct{})
	mgr := &blockingExecManager{blocker: blocker}
	d := dispatcher.New(mgr, &mockTaskStore{}, &mockMsgStore{}, in)

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
func (m *blockingExecManager) ReplyExecution(_ context.Context, _, _ string) (model.WorkerExecution, error) {
	<-m.blocker
	return model.WorkerExecution{ID: "exec-x"}, nil
}
func (m *blockingExecManager) GetExecution(_ string) (model.WorkerExecution, error) {
	return model.WorkerExecution{ID: "exec-x", Status: model.ExecStatusCompleted, Result: "ok"}, nil
}
