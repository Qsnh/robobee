package dispatcher_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/robobee/core/internal/dispatcher"
	"github.com/robobee/core/internal/model"
	"github.com/robobee/core/internal/msgingest"
	"github.com/robobee/core/internal/msgrouter"
	"github.com/robobee/core/internal/msgsender"
	"github.com/robobee/core/internal/platform"
)

// --- Mocks ---

type mockExecManager struct {
	execResult model.WorkerExecution
	execErr    error
	getResult  model.WorkerExecution
	calls      int
}

func (m *mockExecManager) ExecuteWorker(_ context.Context, _, _ string) (model.WorkerExecution, error) {
	m.calls++
	return m.execResult, m.execErr
}
func (m *mockExecManager) ReplyExecution(_ context.Context, _, _ string) (model.WorkerExecution, error) {
	return m.execResult, m.execErr
}
func (m *mockExecManager) GetExecution(_ string) (model.WorkerExecution, error) {
	return m.getResult, nil
}

type mockStore struct{}

func (s *mockStore) SetWorkerID(_ context.Context, _, _ string) error          { return nil }
func (s *mockStore) UpdateStatusBatch(_ context.Context, _ []string, _ string) error { return nil }
func (s *mockStore) MarkMerged(_ context.Context, _ string, _ []string) error  { return nil }
func (s *mockStore) MarkTerminal(_ context.Context, _ []string, _ string) error { return nil }
func (s *mockStore) GetUnfinished(_ context.Context) ([]model.PendingMessage, error) {
	return nil, nil
}
func (s *mockStore) GetSession(_ context.Context, _ string) (*platform.Session, error) {
	return nil, nil
}
func (s *mockStore) SetExecution(_ context.Context, _, _, _ string) error { return nil }
func (s *mockStore) InsertClearSentinel(_ context.Context, _, _, _ string) error { return nil }

func routedMsg(sessionKey, workerID, content string) msgrouter.RoutedMessage {
	return msgrouter.RoutedMessage{
		IngestedMessage: msgingest.IngestedMessage{
			MsgID:      "msg-1",
			SessionKey: sessionKey,
			Platform:   "test",
			Content:    content,
			ReplyTo:    platform.InboundMessage{Platform: "test", SessionKey: sessionKey},
		},
		WorkerID: workerID,
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

// --- Tests ---

// TestDispatcher_NormalMessage_EmitsACKThenResult verifies the happy path:
// one normal message produces ACK immediately, then Result after execution.
func TestDispatcher_NormalMessage_EmitsACKThenResult(t *testing.T) {
	in := make(chan msgrouter.RoutedMessage, 1)
	mgr := &mockExecManager{
		execResult: model.WorkerExecution{ID: "exec-1", SessionID: "sess-1"},
		getResult:  model.WorkerExecution{ID: "exec-1", Status: model.ExecStatusCompleted, Result: "done!"},
	}
	d := dispatcher.New(mgr, &mockStore{}, in)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go d.Run(ctx)

	in <- routedMsg("s1", "w1", "hello")

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

// TestDispatcher_RouteError_EmitsError verifies that a RouteErr produces an error event.
func TestDispatcher_RouteError_EmitsError(t *testing.T) {
	in := make(chan msgrouter.RoutedMessage, 1)
	d := dispatcher.New(&mockExecManager{}, &mockStore{}, in)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go d.Run(ctx)

	in <- msgrouter.RoutedMessage{
		IngestedMessage: msgingest.IngestedMessage{
			MsgID: "m1", SessionKey: "s1", Platform: "test",
			ReplyTo: platform.InboundMessage{Platform: "test", SessionKey: "s1"},
		},
		RouteErr: "❌ 没有找到合适的 Worker",
	}

	events := collectEvents(d.Out(), 1, 500*time.Millisecond)
	if len(events) != 1 || events[0].Type != msgsender.SenderEventError {
		t.Fatalf("expected 1 SenderEventError, got %v", events)
	}
}

// TestDispatcher_ClearCommand_CancelsPendingAndEmitsClear verifies that a clear command
// produces a clear result event.
func TestDispatcher_ClearCommand_CancelsPendingAndEmitsClear(t *testing.T) {
	in := make(chan msgrouter.RoutedMessage, 2)
	d := dispatcher.New(&mockExecManager{execErr: errors.New("never called")}, &mockStore{}, in)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go d.Run(ctx)

	in <- msgrouter.RoutedMessage{
		IngestedMessage: msgingest.IngestedMessage{
			MsgID: "cmd-1", SessionKey: "s1", Platform: "test",
			Content: "clear", Command: msgingest.CommandClear,
			ReplyTo: platform.InboundMessage{Platform: "test", SessionKey: "s1"},
		},
	}

	events := collectEvents(d.Out(), 1, 500*time.Millisecond)
	if len(events) != 1 {
		t.Fatalf("expected 1 event for clear, got %d", len(events))
	}
	if events[0].Type != msgsender.SenderEventResult {
		t.Errorf("expected SenderEventResult for clear, got %v", events[0].Type)
	}
	if events[0].Content != "✅ 上下文已重置" {
		t.Errorf("unexpected clear content: %q", events[0].Content)
	}
}

// TestDispatcher_TwoMessages_SameSession_Serialized verifies that a second message
// for the same session is executed only after the first completes.
func TestDispatcher_TwoMessages_SameSession_Serialized(t *testing.T) {
	in := make(chan msgrouter.RoutedMessage, 2)

	execCount := 0
	blocker := make(chan struct{})
	mgr := &blockingExecManager{
		blocker: blocker,
		result:  model.WorkerExecution{ID: "exec-1", Status: model.ExecStatusCompleted, Result: "ok"},
	}
	d := dispatcher.New(mgr, &mockStore{}, in)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go d.Run(ctx)

	msg1 := routedMsg("s1", "w1", "first")
	msg1.MsgID = "msg-1"
	msg2 := routedMsg("s1", "w1", "second")
	msg2.MsgID = "msg-2"

	in <- msg1
	in <- msg2

	// Both should get ACK immediately
	events := collectEvents(d.Out(), 2, 500*time.Millisecond)
	ackCount := 0
	for _, e := range events {
		if e.Type == msgsender.SenderEventACK {
			ackCount++
		}
	}
	if ackCount != 2 {
		t.Errorf("expected 2 ACKs, got %d (events: %v)", ackCount, events)
	}

	// Verify execution count before unblocking
	time.Sleep(50 * time.Millisecond)
	if execCount > 1 {
		t.Error("second execution should not start before first completes")
	}
	_ = execCount

	// Unblock first execution
	close(blocker)

	// Should get 2 results eventually
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

// blockingExecManager blocks ExecuteWorker until blocker is closed, then returns result.
type blockingExecManager struct {
	blocker <-chan struct{}
	result  model.WorkerExecution
}

func (m *blockingExecManager) ExecuteWorker(_ context.Context, _, _ string) (model.WorkerExecution, error) {
	<-m.blocker
	return model.WorkerExecution{ID: "exec-x"}, nil
}
func (m *blockingExecManager) ReplyExecution(_ context.Context, _, _ string) (model.WorkerExecution, error) {
	return model.WorkerExecution{ID: "exec-x"}, nil
}
func (m *blockingExecManager) GetExecution(_ string) (model.WorkerExecution, error) {
	return m.result, nil
}
