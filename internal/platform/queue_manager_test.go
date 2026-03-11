package platform

import (
	"context"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/robobee/core/internal/model"
)

func TestQueueManager_Enqueue_CallsExecutor(t *testing.T) {
	var called atomic.Int32
	executor := func(_, _, _ string, _ InboundMessage, _ string) { called.Add(1) }

	qm := NewQueueManager(&stubMessageStore{}, executor, 20*time.Millisecond)
	qm.Enqueue(InboundMessage{SessionKey: "sk1", Platform: "test"}, "w1", "msg-1")

	time.Sleep(200 * time.Millisecond)
	if called.Load() != 1 {
		t.Errorf("expected 1 execution, got %d", called.Load())
	}
}

func TestQueueManager_DifferentSessions_RunConcurrently(t *testing.T) {
	var mu sync.Mutex
	started := map[string]bool{}
	ready := make(chan struct{}, 1)
	executor := func(sessionKey, _, _ string, _ InboundMessage, _ string) {
		mu.Lock()
		started[sessionKey] = true
		count := len(started)
		mu.Unlock()
		if count >= 2 {
			select {
			case ready <- struct{}{}:
			default:
			}
		}
		time.Sleep(100 * time.Millisecond)
	}

	qm := NewQueueManager(&stubMessageStore{}, executor, 20*time.Millisecond)
	qm.Enqueue(InboundMessage{SessionKey: "sk1", Platform: "test"}, "w1", "msg-1")
	qm.Enqueue(InboundMessage{SessionKey: "sk2", Platform: "test"}, "w2", "msg-2")

	select {
	case <-ready:
	case <-time.After(500 * time.Millisecond):
		t.Error("expected both sessions to run concurrently")
	}
}

func TestQueueManager_CancelSession_StopsPendingWork(t *testing.T) {
	executed := make(chan string, 1)
	executor := func(_, _, content string, _ InboundMessage, _ string) { executed <- content }

	qm := NewQueueManager(&stubMessageStore{}, executor, 200*time.Millisecond)
	qm.Enqueue(InboundMessage{SessionKey: "sk1", Platform: "test"}, "w1", "msg-1")
	qm.CancelSession("sk1")

	select {
	case got := <-executed:
		t.Errorf("executor should not run after cancel, got %q", got)
	case <-time.After(400 * time.Millisecond):
		// good
	}
}

func TestQueueManager_IdleCleanup_RemovesQueue(t *testing.T) {
	done := make(chan struct{})
	executor := func(_, _, _ string, _ InboundMessage, _ string) { close(done) }

	qm := NewQueueManager(&stubMessageStore{}, executor, 20*time.Millisecond)
	qm.Enqueue(InboundMessage{SessionKey: "sk1", Platform: "test"}, "w1", "msg-1")

	<-done
	time.Sleep(50 * time.Millisecond)

	qm.mu.RLock()
	_, exists := qm.queues[queueKey("sk1", "w1")]
	qm.mu.RUnlock()

	if exists {
		t.Error("idle queue should have been removed from manager map")
	}
}

func TestQueueManager_RecoverFromDB_ExecutesPendingMessages(t *testing.T) {
	recStore := &recoverableStore{
		stubMessageStore: &stubMessageStore{},
		pending: []model.PendingMessage{
			{ID: "msg-r1", SessionKey: "sk1", WorkerID: "w1", Platform: "feishu", Content: "recovered"},
		},
	}

	executed := make(chan string, 1)
	executor := func(_, _, content string, _ InboundMessage, _ string) { executed <- content }

	qm := NewQueueManager(recStore, executor, 20*time.Millisecond)
	qm.RecoverFromDB()

	select {
	case got := <-executed:
		if !strings.Contains(got, "recovered") {
			t.Errorf("want recovered content, got %q", got)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("recovered message was never executed")
	}
}

// recoverableStore overrides GetUnfinished to return preset pending messages.
type recoverableStore struct {
	*stubMessageStore
	pending []model.PendingMessage
}

func (r *recoverableStore) GetUnfinished(_ context.Context) ([]model.PendingMessage, error) {
	return r.pending, nil
}
