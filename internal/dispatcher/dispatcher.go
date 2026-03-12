package dispatcher

import (
	"context"
	"log"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/robobee/core/internal/model"
	"github.com/robobee/core/internal/msgingest"
	"github.com/robobee/core/internal/msgrouter"
	"github.com/robobee/core/internal/msgsender"
	"github.com/robobee/core/internal/platform"
)

const (
	pollInterval = 2 * time.Second
	pollTimeout  = 30 * time.Minute
	mergedSep    = "\n\n---\n\n"
	clearMsg     = "✅ 上下文已重置"
	errorMsg     = "❌ 处理失败，请稍后重试"
	ackMsg       = "⏳ 正在处理，请稍候…"
	timeoutMsg   = "⏰ 任务超时，请稍后通过 Web 界面查看结果"
)

// ExecutionManager manages worker executions.
type ExecutionManager interface {
	ExecuteWorker(ctx context.Context, workerID, input string) (model.WorkerExecution, error)
	ReplyExecution(ctx context.Context, execID, input string) (model.WorkerExecution, error)
	GetExecution(id string) (model.WorkerExecution, error)
}

// MessageStore is the subset of store.MessageStore used by the dispatcher.
type MessageStore interface {
	SetWorkerID(ctx context.Context, id, workerID string) error
	UpdateStatusBatch(ctx context.Context, ids []string, status string) error
	MarkMerged(ctx context.Context, primaryID string, mergedIDs []string) error
	MarkTerminal(ctx context.Context, ids []string, status string) error
	GetUnfinished(ctx context.Context) ([]model.PendingMessage, error)
	GetSession(ctx context.Context, sessionKey string) (*platform.Session, error)
	SetExecution(ctx context.Context, msgID, executionID, sessionID string) error
	InsertClearSentinel(ctx context.Context, id, sessionKey, platform string) error
}

type sessionState struct {
	executing      bool
	pendingIDs     []string
	pendingContent string
	lastReplyTo    platform.InboundMessage
	workerID       string
}

type internalResult struct {
	queueKey string
	msgIDs   []string
	replyTo  platform.InboundMessage
	content  string
}

// Dispatcher serializes worker executions per session+worker pair and emits SenderEvents.
type Dispatcher struct {
	ctx      context.Context
	manager  ExecutionManager
	msgStore MessageStore
	in       <-chan msgrouter.RoutedMessage
	results  chan internalResult
	out      chan msgsender.SenderEvent
	sessions map[string]*sessionState
}

// New constructs a Dispatcher.
func New(manager ExecutionManager, msgStore MessageStore, in <-chan msgrouter.RoutedMessage) *Dispatcher {
	return &Dispatcher{
		manager:  manager,
		msgStore: msgStore,
		in:       in,
		results:  make(chan internalResult, 64),
		out:      make(chan msgsender.SenderEvent, 64),
		sessions: make(map[string]*sessionState),
	}
}

// Out returns the channel of outgoing SenderEvents.
func (d *Dispatcher) Out() <-chan msgsender.SenderEvent { return d.out }

// Run processes messages until ctx is cancelled, then closes Out(). Call in a goroutine.
func (d *Dispatcher) Run(ctx context.Context) {
	defer close(d.out)
	d.ctx = ctx
	d.recoverFromDB()
	for {
		select {
		case msg, ok := <-d.in:
			if !ok {
				return
			}
			d.handleInbound(msg)
		case res := <-d.results:
			d.handleResult(res)
		case <-ctx.Done():
			return
		}
	}
}

func queueKey(sessionKey, workerID string) string {
	return sessionKey + "|" + workerID
}

func (d *Dispatcher) handleInbound(msg msgrouter.RoutedMessage) {
	if msg.RouteErr != "" {
		d.msgStore.MarkTerminal(context.Background(), []string{msg.MsgID}, "failed") //nolint:errcheck
		select {
		case d.out <- msgsender.SenderEvent{Type: msgsender.SenderEventError, ReplyTo: msg.ReplyTo, Content: msg.RouteErr}:
		case <-d.ctx.Done():
			return
		}
		return
	}

	if msg.Command == msgingest.CommandClear {
		prefix := msg.SessionKey + "|"
		for key, state := range d.sessions {
			if strings.HasPrefix(key, prefix) {
				if len(state.pendingIDs) > 0 {
					d.msgStore.MarkTerminal(context.Background(), state.pendingIDs, "failed") //nolint:errcheck
				}
				delete(d.sessions, key)
			}
		}
		d.msgStore.InsertClearSentinel(context.Background(), uuid.New().String(), msg.SessionKey, msg.Platform) //nolint:errcheck
		select {
		case d.out <- msgsender.SenderEvent{Type: msgsender.SenderEventResult, ReplyTo: msg.ReplyTo, Content: clearMsg}:
		case <-d.ctx.Done():
			return
		}
		return
	}

	key := queueKey(msg.SessionKey, msg.WorkerID)
	state, ok := d.sessions[key]
	if !ok {
		state = &sessionState{workerID: msg.WorkerID}
		d.sessions[key] = state
	}

	// ACK immediately on queue entry
	select {
	case d.out <- msgsender.SenderEvent{Type: msgsender.SenderEventACK, ReplyTo: msg.ReplyTo, Content: ackMsg}:
	case <-d.ctx.Done():
		return
	}

	if !state.executing {
		state.executing = true
		state.lastReplyTo = msg.ReplyTo
		d.msgStore.UpdateStatusBatch(context.Background(), []string{msg.MsgID}, "executing") //nolint:errcheck
		go d.executeAsync(d.ctx, key, []string{msg.MsgID}, msg.Content, msg.ReplyTo, msg.WorkerID, msg.MsgID)
	} else {
		if state.pendingContent == "" {
			state.pendingContent = msg.Content
		} else {
			state.pendingContent = state.pendingContent + mergedSep + msg.Content
		}
		state.pendingIDs = append(state.pendingIDs, msg.MsgID)
		state.lastReplyTo = msg.ReplyTo
		d.msgStore.UpdateStatusBatch(context.Background(), []string{msg.MsgID}, "pending") //nolint:errcheck
	}
}

func (d *Dispatcher) executeAsync(ctx context.Context, key string, ids []string, content string, replyTo platform.InboundMessage, workerID, primaryMsgID string) {
	sess, err := d.msgStore.GetSession(ctx, replyTo.SessionKey)
	if err != nil {
		log.Printf("dispatcher: get session error: %v", err)
		select {
		case d.results <- internalResult{queueKey: key, msgIDs: ids, replyTo: replyTo, content: errorMsg}:
		case <-ctx.Done():
			return
		}
		return
	}

	var exec model.WorkerExecution
	if sess != nil && sess.LastExecutionID != "" {
		log.Printf("dispatcher: replying to execution execID=%s", sess.LastExecutionID)
		exec, err = d.manager.ReplyExecution(ctx, sess.LastExecutionID, content)
	} else {
		log.Printf("dispatcher: executing worker workerID=%s", workerID)
		exec, err = d.manager.ExecuteWorker(ctx, workerID, content)
	}
	if err != nil {
		log.Printf("dispatcher: execute error: %v", err)
		select {
		case d.results <- internalResult{queueKey: key, msgIDs: ids, replyTo: replyTo, content: errorMsg}:
		case <-ctx.Done():
			return
		}
		return
	}

	d.msgStore.SetExecution(ctx, primaryMsgID, exec.ID, exec.SessionID) //nolint:errcheck

	result := d.waitForResult(ctx, exec.ID)
	select {
	case d.results <- internalResult{queueKey: key, msgIDs: ids, replyTo: replyTo, content: result}:
	case <-ctx.Done():
		return
	}
}

func (d *Dispatcher) waitForResult(ctx context.Context, executionID string) string {
	deadline := time.Now().Add(pollTimeout)
	lastStatus := ""
	for time.Now().Before(deadline) {
		exec, err := d.manager.GetExecution(executionID)
		if err != nil {
			log.Printf("dispatcher: poll error execID=%s: %v", executionID, err)
			return errorMsg
		}
		if string(exec.Status) != lastStatus {
			log.Printf("dispatcher: polling execID=%s status=%s", executionID, exec.Status)
			lastStatus = string(exec.Status)
		}
		switch exec.Status {
		case model.ExecStatusCompleted:
			if exec.Result != "" {
				return exec.Result
			}
			return "✅ 任务已完成"
		case model.ExecStatusFailed:
			return "❌ 任务执行失败: " + exec.Result
		}
		select {
		case <-time.After(pollInterval):
		case <-ctx.Done():
			return timeoutMsg
		}
	}
	return timeoutMsg
}

func (d *Dispatcher) handleResult(res internalResult) {
	d.msgStore.MarkTerminal(context.Background(), res.msgIDs, "done") //nolint:errcheck
	select {
	case d.out <- msgsender.SenderEvent{Type: msgsender.SenderEventResult, ReplyTo: res.replyTo, Content: res.content}:
	case <-d.ctx.Done():
		return
	}

	state, ok := d.sessions[res.queueKey]
	if !ok {
		return
	}

	if state.pendingContent != "" {
		nextContent := state.pendingContent
		nextIDs := state.pendingIDs
		nextReplyTo := state.lastReplyTo
		state.pendingContent = ""
		state.pendingIDs = nil

		parts := strings.SplitN(res.queueKey, "|", 2)
		workerID := ""
		if len(parts) == 2 {
			workerID = parts[1]
		}

		d.msgStore.UpdateStatusBatch(context.Background(), nextIDs, "executing") //nolint:errcheck
		go d.executeAsync(d.ctx, res.queueKey, nextIDs, nextContent, nextReplyTo, workerID, nextIDs[0])
	} else {
		state.executing = false
		delete(d.sessions, res.queueKey)
	}
}

func (d *Dispatcher) recoverFromDB() {
	msgs, err := d.msgStore.GetUnfinished(context.Background())
	if err != nil {
		log.Printf("dispatcher: RecoverFromDB failed: %v", err)
		return
	}
	if len(msgs) == 0 {
		return
	}

	type group struct {
		content  string
		ids      []string
		replyTo  platform.InboundMessage
		workerID string
	}
	groups := make(map[string]*group)

	for _, msg := range msgs {
		key := queueKey(msg.SessionKey, msg.WorkerID)
		g, ok := groups[key]
		if !ok {
			g = &group{
				replyTo:  platform.InboundMessage{Platform: msg.Platform, SessionKey: msg.SessionKey},
				workerID: msg.WorkerID,
			}
			groups[key] = g
		}
		if g.content == "" {
			g.content = msg.Content
		} else {
			g.content = g.content + mergedSep + msg.Content
		}
		g.ids = append(g.ids, msg.ID)
	}

	for key, g := range groups {
		state := &sessionState{executing: true, workerID: g.workerID}
		d.sessions[key] = state
		d.msgStore.UpdateStatusBatch(context.Background(), g.ids, "executing") //nolint:errcheck
		go d.executeAsync(d.ctx, key, g.ids, g.content, g.replyTo, g.workerID, g.ids[0])
	}

	log.Printf("dispatcher: recovered %d session(s) from DB", len(groups))
}
