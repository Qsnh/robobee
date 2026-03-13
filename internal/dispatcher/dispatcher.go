package dispatcher

import (
	"context"
	"log"
	"strings"
	"time"

	"github.com/robobee/core/internal/model"
	"github.com/robobee/core/internal/msgsender"
	"github.com/robobee/core/internal/platform"
)

const (
	pollInterval = 2 * time.Second
	pollTimeout  = 30 * time.Minute
	clearMsg     = "✅ 上下文已重置"
	errorMsg     = "❌ 处理失败，请稍后重试"
	ackMsg       = "⏳ 正在处理，请稍候…"
	timeoutMsg   = "⏰ 任务超时，请稍后通过 Web 界面查看结果"
)

// ExecutionManager manages worker executions.
type ExecutionManager interface {
	ExecuteWorker(ctx context.Context, workerID, input string) (model.WorkerExecution, error)
	// ExecuteWorkerWithSession resumes an existing Claude session identified by sessionID.
	ExecuteWorkerWithSession(ctx context.Context, workerID, input, sessionID string) (model.WorkerExecution, error)
	GetExecution(id string) (model.WorkerExecution, error)
}

// TaskStore is the subset of store.TaskStore used by the Dispatcher.
type TaskStore interface {
	SetExecution(ctx context.Context, taskID, executionID, status string) error
}

// MessageStore is the subset of store.MessageStore used by the Dispatcher.
type MessageStore interface {
	SetMessageExecution(ctx context.Context, messageID, executionID, sessionID string) error
}

// SessionStore is the subset of store.SessionStore used by the Dispatcher.
type SessionStore interface {
	GetSessionContext(ctx context.Context, sessionKey, agentID string) (string, error)
	UpsertSessionContext(ctx context.Context, sessionKey, agentID, sessionID string) error
	ClearSessionContexts(ctx context.Context, sessionKey string) error
}

type queueState struct {
	executing    bool
	pendingTasks []DispatchTask
	lastReplyTo  platform.InboundMessage
}

type internalResult struct {
	queueKey string
	task     DispatchTask
	content  string
}

// Dispatcher serializes worker executions per (SessionKey, WorkerID) and emits SenderEvents.
type Dispatcher struct {
	ctx          context.Context
	manager      ExecutionManager
	taskStore    TaskStore
	msgStore     MessageStore
	sessionStore SessionStore
	in           <-chan DispatchTask
	results      chan internalResult
	out          chan msgsender.SenderEvent
	queues       map[string]*queueState
}

// New constructs a Dispatcher.
func New(manager ExecutionManager, taskStore TaskStore, msgStore MessageStore, sessionStore SessionStore, in <-chan DispatchTask) *Dispatcher {
	return &Dispatcher{
		manager:      manager,
		taskStore:    taskStore,
		msgStore:     msgStore,
		sessionStore: sessionStore,
		in:           in,
		results:      make(chan internalResult, 64),
		out:          make(chan msgsender.SenderEvent, 64),
		queues:       make(map[string]*queueState),
	}
}

// Out returns the channel of outgoing SenderEvents.
func (d *Dispatcher) Out() <-chan msgsender.SenderEvent { return d.out }

// Run processes tasks until ctx is cancelled. Call in a goroutine.
func (d *Dispatcher) Run(ctx context.Context) {
	defer close(d.out)
	d.ctx = ctx
	for {
		select {
		case task, ok := <-d.in:
			if !ok {
				return
			}
			d.handleInbound(task)
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

func (d *Dispatcher) handleInbound(task DispatchTask) {
	// Handle clear command: evict all session state for this session prefix.
	if task.TaskType == "clear" {
		if err := d.sessionStore.ClearSessionContexts(d.ctx, task.SessionKey); err != nil {
			log.Printf("dispatcher: clear session contexts for %s: %v", task.SessionKey, err)
		}
		prefix := task.SessionKey + "|"
		for key := range d.queues {
			if strings.HasPrefix(key, prefix) {
				delete(d.queues, key)
			}
		}
		replyTo := task.ReplyTo
		if task.ReplySessionKey != "" {
			replyTo.SessionKey = task.ReplySessionKey
		}
		select {
		case d.out <- msgsender.SenderEvent{Type: msgsender.SenderEventResult, ReplyTo: replyTo, Content: clearMsg}:
		case <-d.ctx.Done():
		}
		return
	}

	key := queueKey(task.SessionKey, task.WorkerID)
	state, ok := d.queues[key]
	if !ok {
		state = &queueState{}
		d.queues[key] = state
	}

	// Determine effective reply target.
	replyTo := task.ReplyTo
	if task.ReplySessionKey != "" {
		replyTo.SessionKey = task.ReplySessionKey
	}

	// ACK immediately.
	select {
	case d.out <- msgsender.SenderEvent{Type: msgsender.SenderEventACK, ReplyTo: replyTo, Content: ackMsg}:
	case <-d.ctx.Done():
		return
	}

	if !state.executing {
		state.executing = true
		state.lastReplyTo = replyTo
		go d.executeAsync(d.ctx, key, task, replyTo)
	} else {
		state.pendingTasks = append(state.pendingTasks, task)
		state.lastReplyTo = replyTo
	}
}

func (d *Dispatcher) executeAsync(ctx context.Context, key string, task DispatchTask, replyTo platform.InboundMessage) {
	var exec model.WorkerExecution
	var err error

	// For immediate tasks, attempt --resume if a prior session exists.
	if task.TaskType == model.TaskTypeImmediate {
		sessionID, sessErr := d.sessionStore.GetSessionContext(ctx, task.SessionKey, task.WorkerID)
		if sessErr != nil {
			log.Printf("dispatcher: get session context error: %v", sessErr)
		}
		if sessionID != "" {
			log.Printf("dispatcher: resuming session=%s for task %s", sessionID, task.TaskID)
			exec, err = d.manager.ExecuteWorkerWithSession(ctx, task.WorkerID, task.Instruction, sessionID)
			if err != nil {
				log.Printf("dispatcher: resume error (falling back to fresh): %v", err)
				exec, err = d.manager.ExecuteWorker(ctx, task.WorkerID, task.Instruction)
			}
			goto execStarted
		}
	}

	log.Printf("dispatcher: executing worker %s for task %s", task.WorkerID, task.TaskID)
	exec, err = d.manager.ExecuteWorker(ctx, task.WorkerID, task.Instruction)

execStarted:
	if err != nil {
		log.Printf("dispatcher: execute error: %v", err)
		select {
		case d.results <- internalResult{queueKey: key, task: task, content: errorMsg}:
		case <-ctx.Done():
		}
		return
	}

	// Write execution back to task and message records.
	if task.TaskID != "" {
		d.taskStore.SetExecution(ctx, task.TaskID, exec.ID, model.TaskStatusRunning) //nolint:errcheck
	}
	if task.TaskType == model.TaskTypeImmediate && task.MessageID != "" {
		d.msgStore.SetMessageExecution(ctx, task.MessageID, exec.ID, exec.SessionID) //nolint:errcheck
	}

	result := d.waitForResult(ctx, exec.ID, task.TaskID, task.SessionKey, task.WorkerID)
	select {
	case d.results <- internalResult{queueKey: key, task: task, content: result}:
	case <-ctx.Done():
	}
}

func (d *Dispatcher) waitForResult(ctx context.Context, executionID, taskID, sessionKey, workerID string) string {
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
			if taskID != "" {
				d.taskStore.SetExecution(ctx, taskID, executionID, model.TaskStatusCompleted) //nolint:errcheck
			}
			// Persist session_id for future resume (only on success)
			if sessionKey != "" && workerID != "" {
				if err := d.sessionStore.UpsertSessionContext(ctx, sessionKey, workerID, exec.SessionID); err != nil {
					log.Printf("dispatcher: upsert session context: %v", err)
				}
			}
			if exec.Result != "" {
				return exec.Result
			}
			return "✅ 任务已完成"
		case model.ExecStatusFailed:
			if taskID != "" {
				d.taskStore.SetExecution(ctx, taskID, executionID, model.TaskStatusFailed) //nolint:errcheck
			}
			// Do NOT update session_contexts on failure
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
	replyTo := res.task.ReplyTo
	if res.task.ReplySessionKey != "" {
		replyTo.SessionKey = res.task.ReplySessionKey
	}
	select {
	case d.out <- msgsender.SenderEvent{Type: msgsender.SenderEventResult, ReplyTo: replyTo, Content: res.content}:
	case <-d.ctx.Done():
		return
	}

	state, ok := d.queues[res.queueKey]
	if !ok {
		return
	}

	if len(state.pendingTasks) > 0 {
		next := state.pendingTasks[0]
		state.pendingTasks = state.pendingTasks[1:]
		nextReplyTo := next.ReplyTo
		if next.ReplySessionKey != "" {
			nextReplyTo.SessionKey = next.ReplySessionKey
		}
		state.lastReplyTo = nextReplyTo
		go d.executeAsync(d.ctx, res.queueKey, next, nextReplyTo)
	} else {
		state.executing = false
		delete(d.queues, res.queueKey)
	}
}
