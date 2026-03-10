package feishu

import (
	"context"
	"encoding/json"
	"log"
	"time"

	lark "github.com/larksuite/oapi-sdk-go/v3"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"

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
)

type Handler struct {
	larkClient   *lark.Client
	router       *botrouter.Router
	sessionStore *store.FeishuSessionStore
	manager      *worker.Manager
}

func NewHandler(
	larkClient *lark.Client,
	router *botrouter.Router,
	sessionStore *store.FeishuSessionStore,
	manager *worker.Manager,
) *Handler {
	return &Handler{
		larkClient:   larkClient,
		router:       router,
		sessionStore: sessionStore,
		manager:      manager,
	}
}

func (h *Handler) OnMessage(ctx context.Context, event *larkim.P2MessageReceiveV1) error {
	msg := event.Event.Message
	if msg == nil || *msg.MessageType != "text" {
		return nil
	}

	var content map[string]string
	if err := json.Unmarshal([]byte(*msg.Content), &content); err != nil {
		return nil
	}
	text := content["text"]
	if text == "" {
		return nil
	}

	chatID := *msg.ChatId
	chatType := *msg.ChatType

	// Acknowledge immediately
	h.sendMessage(ctx, chatID, chatType, *msg.MessageId, ackMessage)

	// Process asynchronously.
	// Known limitation: uses context.Background() — goroutine outlives server shutdown.
	go h.process(chatID, chatType, *msg.MessageId, text)
	return nil
}

func (h *Handler) process(chatID, chatType, messageID, text string) {
	ctx := context.Background()

	workerID, err := h.router.Route(ctx, text)
	if err != nil {
		log.Printf("feishu: route error: %v", err)
		h.sendMessage(ctx, chatID, chatType, messageID, noWorkerMsg)
		return
	}

	sess, err := h.sessionStore.GetSession(chatID)
	if err != nil {
		log.Printf("feishu: get session error: %v", err)
		h.sendMessage(ctx, chatID, chatType, messageID, errorMessage)
		return
	}

	var exec model.WorkerExecution
	if sess != nil && sess.LastExecutionID != "" {
		exec, err = h.manager.ReplyExecution(ctx, sess.LastExecutionID, text)
	} else {
		exec, err = h.manager.ExecuteWorker(ctx, workerID, text)
	}
	if err != nil {
		log.Printf("feishu: execute error: %v", err)
		h.sendMessage(ctx, chatID, chatType, messageID, errorMessage)
		return
	}

	if err := h.sessionStore.UpsertSession(chatID, workerID, exec.SessionID, exec.ID); err != nil {
		log.Printf("feishu: upsert session error: %v", err)
	}

	result := h.waitForResult(exec.ID)
	h.sendMessage(ctx, chatID, chatType, messageID, result)
}

// waitForResult polls execution status every 2s until completed/failed or 30m timeout.
func (h *Handler) waitForResult(executionID string) string {
	deadline := time.Now().Add(pollTimeout)
	for time.Now().Before(deadline) {
		exec, err := h.manager.GetExecution(executionID)
		if err != nil {
			log.Printf("feishu: poll execution error: %v", err)
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

func (h *Handler) sendMessage(ctx context.Context, chatID, chatType, replyToMessageID, text string) {
	content, _ := json.Marshal(map[string]string{"text": text})

	if chatType == "p2p" {
		resp, err := h.larkClient.Im.Message.Create(ctx,
			larkim.NewCreateMessageReqBuilder().
				ReceiveIdType(larkim.ReceiveIdTypeChatId).
				Body(larkim.NewCreateMessageReqBodyBuilder().
					MsgType(larkim.MsgTypeText).
					ReceiveId(chatID).
					Content(string(content)).
					Build()).
				Build())
		if err != nil || !resp.Success() {
			log.Printf("feishu: send message error: %v, resp: %+v", err, resp)
		}
	} else {
		resp, err := h.larkClient.Im.Message.Reply(ctx,
			larkim.NewReplyMessageReqBuilder().
				MessageId(replyToMessageID).
				Body(larkim.NewReplyMessageReqBodyBuilder().
					MsgType(larkim.MsgTypeText).
					Content(string(content)).
					Build()).
				Build())
		if err != nil || !resp.Success() {
			log.Printf("feishu: reply message error: %v, resp: %+v", err, resp)
		}
	}
}
