package platform

import (
	"context"
	"sync"
	"time"
)

const mergedSeparator = "\n\n---\n\n"

// Executor is called when a debounce window closes and the execution slot is free.
type Executor func(sessionKey, workerID, content string, replyTo InboundMessage, primaryMsgID string)

// sessionQueue manages debounce and execution serialization for one session_key+worker_id pair.
type sessionQueue struct {
	sessionKey string
	workerID   string

	debounceTimer   *time.Timer
	debounceIDs     []string
	debounceContent string
	lastInbound     InboundMessage

	pendingContent string
	pendingIDs     []string

	isExecuting bool
	mu          sync.Mutex

	executor Executor
	store    MessageStore
	debounce time.Duration
}

func newSessionQueue(sessionKey, workerID string, debounce time.Duration, executor Executor, store MessageStore) *sessionQueue {
	return &sessionQueue{
		sessionKey: sessionKey,
		workerID:   workerID,
		executor:   executor,
		store:      store,
		debounce:   debounce,
	}
}

func (q *sessionQueue) enqueue(msgID, content string, inbound InboundMessage) {
	q.mu.Lock()
	defer q.mu.Unlock()

	if q.debounceContent == "" {
		q.debounceContent = content
	} else {
		q.debounceContent = q.debounceContent + mergedSeparator + content
	}
	q.debounceIDs = append(q.debounceIDs, msgID)
	q.lastInbound = inbound

	q.store.UpdateStatusBatch(context.Background(), q.debounceIDs, "debouncing") //nolint:errcheck

	if q.debounceTimer != nil {
		q.debounceTimer.Stop()
	}
	q.debounceTimer = time.AfterFunc(q.debounce, q.onDebounce)
}

func (q *sessionQueue) onDebounce() {
	q.mu.Lock()

	if q.debounceContent == "" {
		q.mu.Unlock()
		return
	}

	mergedContent := q.debounceContent
	ids := q.debounceIDs
	replyTo := q.lastInbound
	q.debounceContent = ""
	q.debounceIDs = nil
	q.debounceTimer = nil

	primaryID := ids[len(ids)-1]
	mergedIDs := ids[:len(ids)-1]
	q.store.MarkMerged(context.Background(), primaryID, mergedIDs) //nolint:errcheck

	if !q.isExecuting {
		q.isExecuting = true
		q.store.UpdateStatusBatch(context.Background(), []string{primaryID}, "executing") //nolint:errcheck
		q.mu.Unlock()
		go q.runExecutor([]string{primaryID}, mergedContent, replyTo)
	} else {
		if q.pendingContent == "" {
			q.pendingContent = mergedContent
		} else {
			q.pendingContent = q.pendingContent + mergedSeparator + mergedContent
		}
		q.pendingIDs = append(q.pendingIDs, ids...)
		q.mu.Unlock()
	}
}

func (q *sessionQueue) runExecutor(ids []string, content string, replyTo InboundMessage) {
	q.executor(q.sessionKey, q.workerID, content, replyTo, ids[0])
	q.onDone(ids)
}

func (q *sessionQueue) onDone(ids []string) {
	q.mu.Lock()
	q.store.MarkTerminal(context.Background(), ids, "done") //nolint:errcheck

	if q.pendingContent != "" {
		nextContent := q.pendingContent
		nextIDs := q.pendingIDs
		nextReplyTo := q.lastInbound
		q.pendingContent = ""
		q.pendingIDs = nil
		q.store.UpdateStatusBatch(context.Background(), nextIDs, "executing") //nolint:errcheck
		q.mu.Unlock()
		go q.runExecutor(nextIDs, nextContent, nextReplyTo)
	} else {
		q.isExecuting = false
		q.mu.Unlock()
	}
}

func (q *sessionQueue) isIdle() bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	return !q.isExecuting && q.debounceContent == "" && q.pendingContent == ""
}

func (q *sessionQueue) cancel() {
	q.mu.Lock()
	defer q.mu.Unlock()

	if q.debounceTimer != nil {
		q.debounceTimer.Stop()
		q.debounceTimer = nil
	}

	var allIDs []string
	allIDs = append(allIDs, q.debounceIDs...)
	allIDs = append(allIDs, q.pendingIDs...)

	q.debounceContent = ""
	q.debounceIDs = nil
	q.pendingContent = ""
	q.pendingIDs = nil

	if len(allIDs) > 0 {
		q.store.MarkTerminal(context.Background(), allIDs, "failed") //nolint:errcheck
	}
}
