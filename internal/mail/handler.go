package mail

import (
	"context"
	"log"
	"time"

	"github.com/robobee/core/internal/model"
	"github.com/robobee/core/internal/store"
)

const (
	pollInterval = 2 * time.Second
	pollTimeout  = 30 * time.Minute
	ackMessage   = "⏳ 正在处理，请稍候…"
	errorMessage = "❌ 处理失败，请稍后重试"
	noWorkerMsg  = "❌ 没有找到合适的 Worker，请换个描述试试"
	timeoutMsg   = "⏰ 任务超时，请稍后通过 Web 界面查看结果"
)

// messageRouter abstracts botrouter.Router for testing.
type messageRouter interface {
	Route(ctx context.Context, message string) (string, error)
}

// threadSession abstracts a mail session record.
type threadSession interface {
	GetWorkerID() string
	GetLastExecutionID() string
}

// mailSessionStoreIface abstracts MailSessionStore for testing.
type mailSessionStoreIface interface {
	GetSession(threadID string) (threadSession, error)
	UpsertSession(threadID, workerID, sessionID, lastExecutionID string) error
}

// executionManager abstracts worker.Manager for testing.
type executionManager interface {
	ExecuteWorker(ctx context.Context, workerID, input string) (model.WorkerExecution, error)
	ReplyExecution(ctx context.Context, execID, input string) (model.WorkerExecution, error)
	GetExecution(execID string) (model.WorkerExecution, error)
}

// mailSessionStoreAdapter wraps *store.MailSessionStore to implement mailSessionStoreIface.
type mailSessionStoreAdapter struct {
	inner *store.MailSessionStore
}

func (a *mailSessionStoreAdapter) GetSession(threadID string) (threadSession, error) {
	sess, err := a.inner.GetSession(threadID)
	if err != nil || sess == nil {
		return nil, err
	}
	return sess, nil
}

func (a *mailSessionStoreAdapter) UpsertSession(threadID, workerID, sessionID, lastExecutionID string) error {
	return a.inner.UpsertSession(threadID, workerID, sessionID, lastExecutionID)
}

// Handler processes inbound emails and sends replies.
type Handler struct {
	router       messageRouter
	sessionStore mailSessionStoreIface
	manager      executionManager
	sender       EmailSender
	pollInterval time.Duration
	pollTimeout  time.Duration
}

func NewHandler(
	router messageRouter,
	sessionStore *store.MailSessionStore,
	manager executionManager,
	sender EmailSender,
) *Handler {
	return &Handler{
		router:       router,
		sessionStore: &mailSessionStoreAdapter{inner: sessionStore},
		manager:      manager,
		sender:       sender,
		pollInterval: pollInterval,
		pollTimeout:  pollTimeout,
	}
}

func (h *Handler) processEmail(em EmailMessage) {
	h.reply(em, ackMessage)
	go h.process(em)
}

func (h *Handler) process(em EmailMessage) {
	ctx := context.Background()

	workerID, err := h.router.Route(ctx, em.Body)
	if err != nil {
		log.Printf("mail: route error: %v", err)
		h.reply(em, noWorkerMsg)
		return
	}

	sess, err := h.sessionStore.GetSession(em.ThreadID)
	if err != nil {
		log.Printf("mail: get session error: %v", err)
		h.reply(em, errorMessage)
		return
	}

	var exec model.WorkerExecution
	if sess != nil && sess.GetLastExecutionID() != "" {
		exec, err = h.manager.ReplyExecution(ctx, sess.GetLastExecutionID(), em.Body)
	} else {
		exec, err = h.manager.ExecuteWorker(ctx, workerID, em.Body)
	}
	if err != nil {
		log.Printf("mail: execute error: %v", err)
		h.reply(em, errorMessage)
		return
	}

	if err := h.sessionStore.UpsertSession(em.ThreadID, workerID, exec.SessionID, exec.ID); err != nil {
		log.Printf("mail: upsert session error: %v", err)
	}

	result := h.waitForResult(exec.ID)
	h.reply(em, result)
}

func (h *Handler) waitForResult(executionID string) string {
	deadline := time.Now().Add(h.pollTimeout)
	for time.Now().Before(deadline) {
		exec, err := h.manager.GetExecution(executionID)
		if err != nil {
			log.Printf("mail: poll execution error: %v", err)
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
		if h.pollInterval > 0 {
			time.Sleep(h.pollInterval)
		}
	}
	return timeoutMsg
}

func (h *Handler) reply(em EmailMessage, bodyMD string) {
	subject := em.Subject
	if subject != "" && !hasRePrefix(subject) {
		subject = "Re: " + subject
	}

	refs := em.References
	if em.MessageID != "" {
		if refs != "" {
			refs = refs + " " + em.MessageID
		} else {
			refs = em.MessageID
		}
	}

	if err := h.sender.Send(OutgoingEmail{
		To:         em.From,
		Subject:    subject,
		BodyMD:     bodyMD,
		InReplyTo:  em.MessageID,
		References: refs,
	}); err != nil {
		log.Printf("mail: send reply error: %v", err)
	}
}

func hasRePrefix(subject string) bool {
	return len(subject) >= 3 && (subject[:3] == "Re:" || subject[:3] == "re:")
}
