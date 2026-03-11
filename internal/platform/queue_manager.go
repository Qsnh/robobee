package platform

import (
	"context"
	"log"
	"strings"
	"sync"
	"time"
)

// QueueManager manages per-(session_key, worker_id) sessionQueues.
type QueueManager struct {
	queues   map[string]*sessionQueue
	mu       sync.RWMutex
	msgStore MessageStore
	executor Executor
	debounce time.Duration
}

// NewQueueManager constructs a QueueManager.
func NewQueueManager(msgStore MessageStore, executor Executor, debounce time.Duration) *QueueManager {
	return &QueueManager{
		queues:   make(map[string]*sessionQueue),
		msgStore: msgStore,
		executor: executor,
		debounce: debounce,
	}
}

func queueKey(sessionKey, workerID string) string {
	return sessionKey + "|" + workerID
}

// Enqueue adds a message to the appropriate sessionQueue.
func (m *QueueManager) Enqueue(msg InboundMessage, workerID, msgID string) {
	key := queueKey(msg.SessionKey, workerID)

	m.mu.Lock()
	sq, ok := m.queues[key]
	if !ok {
		sq = newSessionQueue(msg.SessionKey, workerID, m.debounce, m.wrappedExecutor(key), m.msgStore)
		m.queues[key] = sq
	}
	m.mu.Unlock()

	content := msg.Content
	if content == "" {
		content = msgID
	}
	sq.enqueue(msgID, content, msg)
}

// wrappedExecutor wraps the user executor to perform idle cleanup after each execution.
// Cleanup is deferred to a goroutine so that sessionQueue.onDone() has time to
// clear isExecuting before we test isIdle().
func (m *QueueManager) wrappedExecutor(key string) Executor {
	return func(sessionKey, workerID, content string, replyTo InboundMessage, primaryMsgID string) {
		m.executor(sessionKey, workerID, content, replyTo, primaryMsgID)
		// onDone() runs after this function returns, so schedule cleanup asynchronously.
		go func() {
			// Yield to let onDone complete.
			time.Sleep(time.Millisecond)
			m.mu.Lock()
			if sq, ok := m.queues[key]; ok && sq.isIdle() {
				delete(m.queues, key)
			}
			m.mu.Unlock()
		}()
	}
}

// IsActiveSession reports whether there is an active (debouncing, executing, or
// pending) queue for the given session key.
func (m *QueueManager) IsActiveSession(sessionKey string) bool {
	prefix := sessionKey + "|"
	m.mu.RLock()
	defer m.mu.RUnlock()
	for key := range m.queues {
		if strings.HasPrefix(key, prefix) {
			return true
		}
	}
	return false
}

// CancelSession stops and clears all queues for the given session key.
func (m *QueueManager) CancelSession(sessionKey string) {
	prefix := sessionKey + "|"
	m.mu.Lock()
	defer m.mu.Unlock()
	for key, sq := range m.queues {
		if key == sessionKey || strings.HasPrefix(key, prefix) {
			sq.cancel()
			delete(m.queues, key)
		}
	}
}

// RecoverFromDB restores and immediately executes pending queues from the DB.
// Call once at startup before handling new messages.
func (m *QueueManager) RecoverFromDB() {
	msgs, err := m.msgStore.GetUnfinished(context.Background())
	if err != nil {
		log.Printf("queue: RecoverFromDB failed: %v", err)
		return
	}
	if len(msgs) == 0 {
		return
	}

	type group struct {
		content string
		ids     []string
		replyTo InboundMessage
	}
	groups := make(map[string]*group)

	for _, msg := range msgs {
		key := queueKey(msg.SessionKey, msg.WorkerID)
		g, ok := groups[key]
		if !ok {
			g = &group{
				replyTo: InboundMessage{
					Platform:   msg.Platform,
					SessionKey: msg.SessionKey,
				},
			}
			groups[key] = g
		}
		if g.content == "" {
			g.content = msg.Content
		} else {
			g.content = g.content + mergedSeparator + msg.Content
		}
		g.ids = append(g.ids, msg.ID)
	}

	for key, g := range groups {
		parts := strings.SplitN(key, "|", 2)
		if len(parts) != 2 {
			continue
		}
		sessionKey, workerID := parts[0], parts[1]

		sq := newSessionQueue(sessionKey, workerID, m.debounce, m.wrappedExecutor(key), m.msgStore)
		sq.isExecuting = true

		m.mu.Lock()
		m.queues[key] = sq
		m.mu.Unlock()

		m.msgStore.UpdateStatusBatch(context.Background(), g.ids, "executing") //nolint:errcheck

		ids := g.ids
		content := g.content
		replyTo := g.replyTo
		go sq.runExecutor(ids, content, replyTo)
	}

	log.Printf("queue: recovered %d session(s) from DB", len(groups))
}
