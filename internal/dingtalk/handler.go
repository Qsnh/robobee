package dingtalk

import (
	"context"
	"log"
	"strings"
	"time"

	"github.com/open-dingtalk/dingtalk-stream-sdk-go/chatbot"

	"github.com/robobee/core/internal/botrouter"
	"github.com/robobee/core/internal/model"
	"github.com/robobee/core/internal/store"
	"github.com/robobee/core/internal/worker"
)

const (
	pollInterval = 2 * time.Second
	pollTimeout  = 30 * time.Minute
	ackMessage   = "⏳ 正在处理，请稍候…"
	errorMessage = "❌ 处理失败，请稍后重试"
	noWorkerMsg  = "❌ 没有找到合适的 Worker，请换个描述试试"
	clearMessage = "✅ 上下文已重置"
)

type Handler struct {
	router       *botrouter.Router
	sessionStore *store.DingTalkSessionStore
	manager      *worker.Manager
}

func NewHandler(
	router *botrouter.Router,
	sessionStore *store.DingTalkSessionStore,
	manager *worker.Manager,
) *Handler {
	return &Handler{
		router:       router,
		sessionStore: sessionStore,
		manager:      manager,
	}
}

func (h *Handler) OnMessage(ctx context.Context, data *chatbot.BotCallbackDataModel) ([]byte, error) {
	text := strings.TrimSpace(data.Text.Content)
	if text == "" {
		return []byte(""), nil
	}

	chatID := data.ConversationId
	sessionWebhook := data.SessionWebhook

	if strings.EqualFold(text, "clear") {
		if err := h.sessionStore.DeleteSession(chatID); err != nil {
			log.Printf("dingtalk: delete session error: %v", err)
			h.sendMessage(ctx, sessionWebhook, errorMessage)
			return []byte(""), nil
		}
		h.sendMessage(ctx, sessionWebhook, clearMessage)
		return []byte(""), nil
	}

	// Acknowledge immediately.
	h.sendMessage(ctx, sessionWebhook, ackMessage)

	// Process asynchronously.
	// Known limitation: uses context.Background() — goroutine outlives server shutdown.
	go h.process(chatID, sessionWebhook, text)
	return []byte(""), nil
}

func (h *Handler) process(chatID, sessionWebhook, text string) {
	ctx := context.Background()

	workerID, err := h.router.Route(ctx, text)
	if err != nil {
		log.Printf("dingtalk: route error: %v", err)
		h.sendMessage(ctx, sessionWebhook, noWorkerMsg)
		return
	}

	sess, err := h.sessionStore.GetSession(chatID)
	if err != nil {
		log.Printf("dingtalk: get session error: %v", err)
		h.sendMessage(ctx, sessionWebhook, errorMessage)
		return
	}

	var exec model.WorkerExecution
	if sess != nil && sess.LastExecutionID != "" {
		exec, err = h.manager.ReplyExecution(ctx, sess.LastExecutionID, text)
	} else {
		exec, err = h.manager.ExecuteWorker(ctx, workerID, text)
	}
	if err != nil {
		log.Printf("dingtalk: execute error: %v", err)
		h.sendMessage(ctx, sessionWebhook, errorMessage)
		return
	}

	if err := h.sessionStore.UpsertSession(chatID, workerID, exec.SessionID, exec.ID); err != nil {
		log.Printf("dingtalk: upsert session error: %v", err)
	}

	result := h.waitForResult(exec.ID)
	h.sendMessage(ctx, sessionWebhook, result)
}

// waitForResult polls execution status every 2s until completed/failed or 30m timeout.
func (h *Handler) waitForResult(executionID string) string {
	deadline := time.Now().Add(pollTimeout)
	for time.Now().Before(deadline) {
		exec, err := h.manager.GetExecution(executionID)
		if err != nil {
			log.Printf("dingtalk: poll execution error: %v", err)
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
	return "⏰ 任务超时，请稍后通过 Web 界面查看结果"
}

const markdownTitle = "RoboBee"

func (h *Handler) sendMessage(ctx context.Context, sessionWebhook, text string) {
	replier := chatbot.NewChatbotReplier()
	if err := replier.SimpleReplyMarkdown(ctx, sessionWebhook, []byte(markdownTitle), []byte(text)); err != nil {
		log.Printf("dingtalk: send message error: %v", err)
	}
}
