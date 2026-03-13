package dispatcher

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/robobee/core/internal/model"
	"github.com/robobee/core/internal/platform"
)

const (
	pollInterval = 2 * time.Second
	pollTimeout  = 30 * time.Minute
)

// ExecutionManager manages worker executions.
type ExecutionManager interface {
	ExecuteWorker(ctx context.Context, workerID, input string) (model.WorkerExecution, error)
	ExecuteWorkerWithSession(ctx context.Context, workerID, input, sessionID string) (model.WorkerExecution, error)
	GetExecution(id string) (model.WorkerExecution, error)
}

// TaskStore is the subset of store.TaskStore used by the Dispatcher.
type TaskStore interface {
	SetExecution(ctx context.Context, taskID, executionID, status string) error
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
}

// Dispatcher serializes worker executions per (SessionKey, WorkerID).
type Dispatcher struct {
	ctx          context.Context
	manager      ExecutionManager
	taskStore    TaskStore
	sessionStore SessionStore
	in           <-chan DispatchTask
	results      chan internalResult
	queues       map[string]*queueState
}

// New constructs a Dispatcher.
func New(manager ExecutionManager, taskStore TaskStore, sessionStore SessionStore, in <-chan DispatchTask) *Dispatcher {
	return &Dispatcher{
		manager:      manager,
		taskStore:    taskStore,
		sessionStore: sessionStore,
		in:           in,
		results:      make(chan internalResult, 64),
		queues:       make(map[string]*queueState),
	}
}

// Run processes tasks until ctx is cancelled. Call in a goroutine.
func (d *Dispatcher) Run(ctx context.Context) {
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
		return
	}

	key := queueKey(task.SessionKey, task.WorkerID)
	state, ok := d.queues[key]
	if !ok {
		state = &queueState{}
		d.queues[key] = state
	}

	replyTo := task.ReplyTo
	if task.ReplySessionKey != "" {
		replyTo.SessionKey = task.ReplySessionKey
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

// buildInstruction prepends task metadata to the instruction so workers
// can call mark_task_success/failed and send_message via MCP.
func buildInstruction(task DispatchTask) string {
	if task.TaskID == "" {
		return task.Instruction
	}
	return fmt.Sprintf("[系统元数据] task_id=%s message_id=%s\n\n%s",
		task.TaskID, task.MessageID, task.Instruction)
}

func (d *Dispatcher) executeAsync(ctx context.Context, key string, task DispatchTask, replyTo platform.InboundMessage) {
	var exec model.WorkerExecution
	var err error

	instruction := buildInstruction(task)

	if task.TaskType == model.TaskTypeImmediate {
		sessionID, sessErr := d.sessionStore.GetSessionContext(ctx, task.SessionKey, task.WorkerID)
		if sessErr != nil {
			log.Printf("dispatcher: get session context error: %v", sessErr)
		}
		if sessionID != "" {
			log.Printf("dispatcher: resuming session=%s for task %s", sessionID, task.TaskID)
			exec, err = d.manager.ExecuteWorkerWithSession(ctx, task.WorkerID, instruction, sessionID)
			if err != nil {
				log.Printf("dispatcher: resume error (falling back to fresh): %v", err)
				if clearErr := d.sessionStore.ClearSessionContexts(ctx, task.SessionKey); clearErr != nil {
					log.Printf("dispatcher: clear stale session contexts for %s: %v", task.SessionKey, clearErr)
				}
				exec, err = d.manager.ExecuteWorker(ctx, task.WorkerID, instruction)
			}
			goto execStarted
		}
	}

	log.Printf("dispatcher: executing worker %s for task %s", task.WorkerID, task.TaskID)
	exec, err = d.manager.ExecuteWorker(ctx, task.WorkerID, instruction)

execStarted:
	if err != nil {
		log.Printf("dispatcher: execute error: %v", err)
		select {
		case d.results <- internalResult{queueKey: key, task: task}:
		case <-ctx.Done():
		}
		return
	}

	if task.TaskID != "" {
		d.taskStore.SetExecution(ctx, task.TaskID, exec.ID, model.TaskStatusRunning) //nolint:errcheck
	}
	d.waitForResult(ctx, exec.ID, task.TaskID, task.SessionKey, task.WorkerID)
	select {
	case d.results <- internalResult{queueKey: key, task: task}:
	case <-ctx.Done():
	}
}

func (d *Dispatcher) waitForResult(ctx context.Context, executionID, taskID, sessionKey, workerID string) {
	deadline := time.Now().Add(pollTimeout)
	lastStatus := ""
	for time.Now().Before(deadline) {
		exec, err := d.manager.GetExecution(executionID)
		if err != nil {
			log.Printf("dispatcher: poll error execID=%s: %v", executionID, err)
			return
		}
		if string(exec.Status) != lastStatus {
			log.Printf("dispatcher: polling execID=%s status=%s", executionID, exec.Status)
			lastStatus = string(exec.Status)
		}
		switch exec.Status {
		case model.ExecStatusCompleted:
			// Persist session_id for future resume (only on success).
			// Terminal task status is set by the worker via mark_task_success.
			if sessionKey != "" && workerID != "" {
				if err := d.sessionStore.UpsertSessionContext(ctx, sessionKey, workerID, exec.SessionID); err != nil {
					log.Printf("dispatcher: upsert session context: %v", err)
				}
			}
			return
		case model.ExecStatusFailed:
			// Terminal task status is set by the worker via mark_task_failed.
			return
		}
		select {
		case <-time.After(pollInterval):
		case <-ctx.Done():
			return
		}
	}
}

func (d *Dispatcher) handleResult(res internalResult) {
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
