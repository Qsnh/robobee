package platform

import (
	"context"
	"log"
	"strings"
	"time"

	"github.com/google/uuid"
)

// PlatformManager registers platforms and drives them concurrently.
type PlatformManager struct {
	pipeline  *Pipeline
	platforms []Platform
	queueMgr  *QueueManager
	msgStore  MessageStore
	debounce  time.Duration
}

// NewManager constructs a PlatformManager.
func NewManager(pipeline *Pipeline, msgStore MessageStore, debounce time.Duration) *PlatformManager {
	return &PlatformManager{
		pipeline: pipeline,
		msgStore: msgStore,
		debounce: debounce,
	}
}

// Register adds a platform to the manager.
func (m *PlatformManager) Register(p Platform) {
	m.platforms = append(m.platforms, p)
}

// StartAll launches each platform's receiver in its own goroutine and blocks until ctx is cancelled.
func (m *PlatformManager) StartAll(ctx context.Context) {
	// Build sender lookup by platform ID for use in executor.
	senderByPlatform := make(map[string]PlatformSenderAdapter, len(m.platforms))
	for _, p := range m.platforms {
		senderByPlatform[p.ID()] = p.Sender()
	}

	// executor: called by queue when debounce fires and slot is free.
	executor := func(sessionKey, workerID, content string, replyTo InboundMessage, primaryMsgID string) {
		mergedMsg := replyTo
		mergedMsg.Content = content

		result := m.pipeline.HandleRouted(context.Background(), mergedMsg, workerID, primaryMsgID)

		platformID := strings.SplitN(sessionKey, ":", 2)[0]
		if sender, ok := senderByPlatform[platformID]; ok {
			if err := sender.Send(context.Background(), OutboundMessage{
				SessionKey: sessionKey,
				Content:    result,
				ReplyTo:    replyTo,
			}); err != nil {
				log.Printf("platform[%s]: send error: %v", platformID, err)
			}
		}
	}

	m.queueMgr = NewQueueManager(m.msgStore, executor, m.debounce)
	m.queueMgr.RecoverFromDB()

	for _, p := range m.platforms {
		p := p
		go func() {
			sender := p.Sender()
			dispatch := func(msg InboundMessage) {
				log.Printf("platform[%s]: dispatch received sessionKey=%s contentLen=%d", p.ID(), msg.SessionKey, len(msg.Content))

				if IsClearCommand(msg.Content) {
					// Cancel any queued work for this session before clearing.
					m.queueMgr.CancelSession(msg.SessionKey)
					result := m.pipeline.Handle(context.Background(), msg)
					if err := sender.Send(context.Background(), OutboundMessage{
						SessionKey: msg.SessionKey,
						Content:    result,
						ReplyTo:    msg,
					}); err != nil {
						log.Printf("platform[%s]: send error: %v", p.ID(), err)
					}
					return
				}

				// Send ACK only for the first message in a new debounce batch.
				if !m.queueMgr.IsActiveSession(msg.SessionKey) {
					if err := sender.Send(context.Background(), OutboundMessage{
						SessionKey: msg.SessionKey,
						Content:    AckMessage,
						ReplyTo:    msg,
					}); err != nil {
						log.Printf("platform[%s]: ack error: %v", p.ID(), err)
					} else {
						log.Printf("platform[%s]: ack sent sessionKey=%s", p.ID(), msg.SessionKey)
					}
				}

				// Record message to DB (best-effort; failure does not block processing).
				msgID := uuid.New().String()
				if err := m.msgStore.Create(context.Background(), msgID, msg.SessionKey, msg.Platform, msg.Content); err != nil {
					log.Printf("platform[%s]: record message error: %v", p.ID(), err)
				}

				// Route to find worker ID.
				workerID, err := m.pipeline.Route(context.Background(), msg.Content)
				if err != nil {
					log.Printf("platform[%s]: route error: %v", p.ID(), err)
					m.msgStore.MarkTerminal(context.Background(), []string{msgID}, "failed") //nolint:errcheck
					if err2 := sender.Send(context.Background(), OutboundMessage{
						SessionKey: msg.SessionKey,
						Content:    noWorkerMsg,
						ReplyTo:    msg,
					}); err2 != nil {
						log.Printf("platform[%s]: send error: %v", p.ID(), err2)
					}
					return
				}
				m.msgStore.SetWorkerID(context.Background(), msgID, workerID) //nolint:errcheck
				log.Printf("platform[%s]: routed sessionKey=%s workerID=%s msgID=%s", p.ID(), msg.SessionKey, workerID, msgID)

				m.queueMgr.Enqueue(msg, workerID, msgID)
			}

			log.Printf("platform[%s]: starting receiver", p.ID())
			if err := p.Receiver().Start(ctx, dispatch); err != nil && ctx.Err() == nil {
				log.Printf("platform[%s]: receiver error: %v", p.ID(), err)
			}
		}()
	}
	<-ctx.Done()
}
