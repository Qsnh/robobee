package platform

import (
	"context"
	"log"
	"strings"
	"time"

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
	sessions SessionStore
	manager  ExecutionManager
}

// NewPipeline constructs a Pipeline.
func NewPipeline(router MessageRouter, sessions SessionStore, manager ExecutionManager) *Pipeline {
	return &Pipeline{router: router, sessions: sessions, manager: manager}
}

// IsClearCommand reports whether content is a "clear" command.
func IsClearCommand(content string) bool {
	return strings.EqualFold(strings.TrimSpace(content), "clear")
}

// Handle processes an inbound message synchronously and returns the reply text.
func (p *Pipeline) Handle(ctx context.Context, msg InboundMessage) string {
	if IsClearCommand(msg.Content) {
		if err := p.sessions.Delete(msg.SessionKey); err != nil {
			log.Printf("platform: clear session error: %v", err)
			return errorMessage
		}
		return clearMessage
	}

	workerID, err := p.router.Route(ctx, msg.Content)
	if err != nil {
		log.Printf("platform: route error: %v", err)
		return noWorkerMsg
	}

	sess, err := p.sessions.Get(msg.SessionKey)
	if err != nil {
		log.Printf("platform: get session error: %v", err)
		return errorMessage
	}

	var exec model.WorkerExecution
	if sess != nil && sess.LastExecutionID != "" {
		exec, err = p.manager.ReplyExecution(ctx, sess.LastExecutionID, msg.Content)
	} else {
		exec, err = p.manager.ExecuteWorker(ctx, workerID, msg.Content)
	}
	if err != nil {
		log.Printf("platform: execute error: %v", err)
		return errorMessage
	}

	if err := p.sessions.Upsert(Session{
		Key:             msg.SessionKey,
		Platform:        msg.Platform,
		WorkerID:        workerID,
		SessionID:       exec.SessionID,
		LastExecutionID: exec.ID,
	}); err != nil {
		log.Printf("platform: upsert session error: %v", err)
	}

	return p.waitForResult(exec.ID)
}

func (p *Pipeline) waitForResult(executionID string) string {
	deadline := time.Now().Add(pollTimeout)
	for time.Now().Before(deadline) {
		exec, err := p.manager.GetExecution(executionID)
		if err != nil {
			log.Printf("platform: poll execution error: %v", err)
			return errorMessage
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
