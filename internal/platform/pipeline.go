package platform

import (
	"context"
	"log"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/robobee/core/internal/model"
)

const (
	pollInterval = 2 * time.Second
	pollTimeout  = 30 * time.Minute
	ackMessage   = "⏳ 正在处理，请稍候…"
	errorMessage = "❌ 处理失败，请稍后重试"
	noWorkerMsg  = "❌ 没有找到合适的 Worker，请换个描述试试"
	clearMessage = "✅ 上下文已重置"
	timeoutMsg   = "⏰ 任务超时，请稍后通过 Web 界面查看结果"
)

// AckMessage is the immediate acknowledgement sent before async processing.
const AckMessage = ackMessage

// MessageRouter routes a message to the best worker ID.
type MessageRouter interface {
	Route(ctx context.Context, message string) (string, error)
}

// ExecutionManager manages worker executions.
type ExecutionManager interface {
	ExecuteWorker(ctx context.Context, workerID, input string) (model.WorkerExecution, error)
	ReplyExecution(ctx context.Context, execID, input string) (model.WorkerExecution, error)
	GetExecution(execID string) (model.WorkerExecution, error)
}

// Pipeline processes inbound messages through routing, execution, and result polling.
type Pipeline struct {
	router   MessageRouter
	msgStore MessageStore
	manager  ExecutionManager
}

// NewPipeline constructs a Pipeline.
func NewPipeline(router MessageRouter, msgStore MessageStore, manager ExecutionManager) *Pipeline {
	return &Pipeline{router: router, msgStore: msgStore, manager: manager}
}

// IsClearCommand reports whether content is a "clear" command.
func IsClearCommand(content string) bool {
	return strings.EqualFold(strings.TrimSpace(content), "clear")
}

// Handle processes a clear command and returns the reply text.
// Only clear commands are routed here; all other messages go through HandleRouted.
func (p *Pipeline) Handle(ctx context.Context, msg InboundMessage) string {
	if err := p.msgStore.InsertClearSentinel(ctx, uuid.New().String(), msg.SessionKey, msg.Platform); err != nil {
		log.Printf("platform: clear session error: %v", err)
		return errorMessage
	}
	return clearMessage
}

// Route resolves the best worker ID for the given message content.
func (p *Pipeline) Route(ctx context.Context, content string) (string, error) {
	return p.router.Route(ctx, content)
}

// HandleRouted processes an already-routed message.
// workerID must be the result of a prior Route call for this content.
// msgID is the platform_messages row ID for this message (used to record execution metadata).
func (p *Pipeline) HandleRouted(ctx context.Context, msg InboundMessage, workerID, msgID string) string {
	sess, err := p.msgStore.GetSession(ctx, msg.SessionKey)
	if err != nil {
		log.Printf("platform: get session error: %v", err)
		return errorMessage
	}

	var exec model.WorkerExecution
	if sess != nil && sess.LastExecutionID != "" {
		log.Printf("platform: replying to execution execID=%s", sess.LastExecutionID)
		exec, err = p.manager.ReplyExecution(ctx, sess.LastExecutionID, msg.Content)
	} else {
		log.Printf("platform: executing worker workerID=%s", workerID)
		exec, err = p.manager.ExecuteWorker(ctx, workerID, msg.Content)
	}
	if err != nil {
		log.Printf("platform: execute error: %v", err)
		return errorMessage
	}
	log.Printf("platform: execution started execID=%s sessionID=%s", exec.ID, exec.SessionID)

	// SetExecution failure is non-fatal: the execution has already started.
	// If this write fails, the next message will start a new session rather
	// than continuing this one — degraded but not incorrect.
	if err := p.msgStore.SetExecution(ctx, msgID, exec.ID, exec.SessionID); err != nil {
		log.Printf("platform: set execution error: %v", err)
	}

	return p.waitForResult(exec.ID)
}

func (p *Pipeline) waitForResult(executionID string) string {
	deadline := time.Now().Add(pollTimeout)
	lastStatus := ""
	for time.Now().Before(deadline) {
		exec, err := p.manager.GetExecution(executionID)
		if err != nil {
			log.Printf("platform: poll execution error: %v", err)
			return errorMessage
		}
		if string(exec.Status) != lastStatus {
			log.Printf("platform: polling execID=%s status=%s", executionID, exec.Status)
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
		time.Sleep(pollInterval)
	}
	return timeoutMsg
}
