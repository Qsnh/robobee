package platform

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/robobee/core/internal/model"
)

// stubMessageStore is a no-op MessageStore for queue tests.
type stubMessageStore struct {
	mu      sync.Mutex
	updates []string
}

func (s *stubMessageStore) Create(_ context.Context, _, _, _, _, _, _ string) (bool, error) {
	return true, nil
}
func (s *stubMessageStore) SetWorkerID(_ context.Context, _, _ string) error  { return nil }
func (s *stubMessageStore) SetStatus(_ context.Context, _, _ string) error    { return nil }
func (s *stubMessageStore) UpdateStatusBatch(_ context.Context, ids []string, status string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, id := range ids {
		s.updates = append(s.updates, status+":"+id)
	}
	return nil
}
func (s *stubMessageStore) MarkMerged(_ context.Context, _ string, _ []string) error { return nil }
func (s *stubMessageStore) MarkTerminal(_ context.Context, _ []string, _ string) error {
	return nil
}
func (s *stubMessageStore) GetUnfinished(_ context.Context) ([]model.PendingMessage, error) {
	return nil, nil
}
func (s *stubMessageStore) GetSession(_ context.Context, _ string) (*Session, error) {
	return nil, nil
}
func (s *stubMessageStore) SetExecution(_ context.Context, _, _, _ string) error { return nil }
func (s *stubMessageStore) InsertClearSentinel(_ context.Context, _, _, _ string) error {
	return nil
}

func TestSessionQueue_SingleMessage_ExecutesAfterDebounce(t *testing.T) {
	executed := make(chan string, 1)
	executor := func(_, _, content string, _ InboundMessage, _ string) { executed <- content }

	q := newSessionQueue("sk1", "w1", 50*time.Millisecond, executor, &stubMessageStore{})
	q.enqueue("msg-1", "hello", InboundMessage{SessionKey: "sk1", Content: "hello"})

	select {
	case got := <-executed:
		if got != "hello" {
			t.Errorf("want hello, got %s", got)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("executor not called within timeout")
	}
}

func TestSessionQueue_TwoRapidMessages_MergedIntoOne(t *testing.T) {
	executed := make(chan string, 1)
	executor := func(_, _, content string, _ InboundMessage, _ string) { executed <- content }

	q := newSessionQueue("sk1", "w1", 100*time.Millisecond, executor, &stubMessageStore{})
	q.enqueue("msg-1", "first", InboundMessage{SessionKey: "sk1", Content: "first"})
	time.Sleep(20 * time.Millisecond)
	q.enqueue("msg-2", "second", InboundMessage{SessionKey: "sk1", Content: "second"})

	select {
	case got := <-executed:
		if !strings.Contains(got, "first") || !strings.Contains(got, "second") {
			t.Errorf("want both messages merged, got %q", got)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("executor not called within timeout")
	}

	// Only one execution should fire.
	select {
	case extra := <-executed:
		t.Errorf("unexpected second execution: %s", extra)
	case <-time.After(200 * time.Millisecond):
		// good
	}
}

func TestSessionQueue_RollingDebounce_TimerResetsOnNewMessage(t *testing.T) {
	executedAt := make(chan time.Time, 1)
	debounce := 80 * time.Millisecond
	executor := func(_, _, _ string, _ InboundMessage, _ string) { executedAt <- time.Now() }

	q := newSessionQueue("sk1", "w1", debounce, executor, &stubMessageStore{})
	start := time.Now()
	q.enqueue("msg-1", "a", InboundMessage{SessionKey: "sk1"})
	time.Sleep(50 * time.Millisecond) // before debounce fires
	q.enqueue("msg-2", "b", InboundMessage{SessionKey: "sk1"})
	// timer resets; fires ~80ms after msg-2

	select {
	case fired := <-executedAt:
		elapsed := fired.Sub(start)
		// At least 50ms (msg-2 delay) + 80ms (debounce) = 130ms
		if elapsed < 120*time.Millisecond {
			t.Errorf("timer not reset: elapsed=%v (too short)", elapsed)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("executor not called")
	}
}

func TestSessionQueue_MessageDuringExecution_QueuedAndExecutedAfter(t *testing.T) {
	blockExec := make(chan struct{})
	execOrder := make(chan string, 10)
	executor := func(_, _, content string, _ InboundMessage, _ string) {
		execOrder <- content
		<-blockExec
	}

	q := newSessionQueue("sk1", "w1", 20*time.Millisecond, executor, &stubMessageStore{})
	q.enqueue("msg-1", "first", InboundMessage{SessionKey: "sk1", Content: "first"})

	select {
	case <-execOrder:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("first execution never started")
	}

	q.enqueue("msg-2", "second", InboundMessage{SessionKey: "sk1", Content: "second"})
	time.Sleep(50 * time.Millisecond) // let debounce fire into pending

	close(blockExec) // release first execution

	select {
	case got := <-execOrder:
		if !strings.Contains(got, "second") {
			t.Errorf("want second message, got %q", got)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("second execution never started")
	}
}

func TestSessionQueue_Cancel_ClearsPendingWork(t *testing.T) {
	executed := make(chan string, 1)
	executor := func(_, _, content string, _ InboundMessage, _ string) { executed <- content }

	q := newSessionQueue("sk1", "w1", 200*time.Millisecond, executor, &stubMessageStore{})
	q.enqueue("msg-1", "hello", InboundMessage{SessionKey: "sk1"})
	q.cancel()

	select {
	case got := <-executed:
		t.Errorf("executor should not run after cancel, got %q", got)
	case <-time.After(400 * time.Millisecond):
		// good
	}
}

func TestSessionQueue_IsIdle_AfterExecution(t *testing.T) {
	done := make(chan struct{})
	executor := func(_, _, _ string, _ InboundMessage, _ string) { close(done) }

	q := newSessionQueue("sk1", "w1", 20*time.Millisecond, executor, &stubMessageStore{})
	q.enqueue("msg-1", "hi", InboundMessage{SessionKey: "sk1"})

	<-done
	time.Sleep(20 * time.Millisecond) // allow onDone to complete
	if !q.isIdle() {
		t.Error("queue should be idle after execution with no pending work")
	}
}

func TestSessionQueue_ExecutorReceivesPrimaryMsgID(t *testing.T) {
	var capturedPrimaryID string
	executor := func(_, _, _ string, _ InboundMessage, primaryMsgID string) {
		capturedPrimaryID = primaryMsgID
	}
	store := &stubMessageStore{}
	q := newSessionQueue("sk", "w1", 10*time.Millisecond, executor, store)
	q.enqueue("msg-first", "hello", InboundMessage{})
	q.enqueue("msg-second", "world", InboundMessage{})
	time.Sleep(50 * time.Millisecond)

	if capturedPrimaryID != "msg-second" {
		t.Errorf("primaryMsgID: got %q, want %q", capturedPrimaryID, "msg-second")
	}
}
