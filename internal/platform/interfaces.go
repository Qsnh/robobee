package platform

import (
	"context"

	"github.com/robobee/core/internal/model"
)

// InboundMessage carries a parsed message from any platform.
type InboundMessage struct {
	Platform   string // "feishu" | "dingtalk" | "mail"
	SenderID   string
	SessionKey string // platform-prefixed session key, e.g. "feishu:chatID:userID"
	Content    string
	Raw        any // original platform event, used by the sender for reply metadata
}

// OutboundMessage carries a reply to send back on a platform.
type OutboundMessage struct {
	SessionKey string
	Content    string
	ReplyTo    InboundMessage
}

// PlatformReceiverAdapter receives inbound messages and dispatches them.
type PlatformReceiverAdapter interface {
	Start(ctx context.Context, dispatch func(InboundMessage)) error
}

// PlatformSenderAdapter sends outbound messages on a platform.
type PlatformSenderAdapter interface {
	Send(ctx context.Context, msg OutboundMessage) error
}

// Platform bundles a receiver and sender for a single messaging platform.
type Platform interface {
	ID() string
	Receiver() PlatformReceiverAdapter
	Sender() PlatformSenderAdapter
}

// Session holds the persistent state for one conversation.
type Session struct {
	Key             string
	Platform        string
	WorkerID        string
	SessionID       string
	LastExecutionID string
}

// SessionStore persists session state across restarts.
type SessionStore interface {
	Get(key string) (*Session, error)
	Upsert(session Session) error
	Delete(key string) error
}

// MessageStore is the subset of store.MessageStore operations used by the queue.
// The concrete implementation is *store.MessageStore.
type MessageStore interface {
	Create(ctx context.Context, id, sessionKey, platform, content string) error
	SetWorkerID(ctx context.Context, id, workerID string) error
	SetStatus(ctx context.Context, id, status string) error
	UpdateStatusBatch(ctx context.Context, ids []string, status string) error
	MarkMerged(ctx context.Context, primaryID string, mergedIDs []string) error
	MarkTerminal(ctx context.Context, ids []string, status string) error
	GetUnfinished(ctx context.Context) ([]model.PendingMessage, error)
}
