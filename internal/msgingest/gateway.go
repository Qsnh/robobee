package msgingest

import (
	"context"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/robobee/core/internal/platform"
)

const mergedSeparator = "\n\n---\n\n"

// CommandType identifies a built-in command in a message.
type CommandType string

const (
	CommandNone  CommandType = ""
	CommandClear CommandType = "clear"
)

// IngestedMessage is a deduplicated, debounced, normalized message ready for routing.
type IngestedMessage struct {
	MsgID      string
	SessionKey string
	Platform   string
	Content    string
	ReplyTo    platform.InboundMessage
	Command    CommandType
}

// MessageStore is the subset of store.MessageStore used by msgingest.
type MessageStore interface {
	Create(ctx context.Context, id, sessionKey, platform, content, raw, platformMsgID string) (bool, error)
	UpdateStatusBatch(ctx context.Context, ids []string, status string) error
	MarkTerminal(ctx context.Context, ids []string, status string) error
	MarkMerged(ctx context.Context, primaryID string, mergedIDs []string) error
}

type debounceState struct {
	timer   *time.Timer
	ids     []string
	content string
	replyTo platform.InboundMessage
}

// Gateway receives raw platform messages, deduplicates, debounces, and emits IngestedMessages.
type Gateway struct {
	msgStore MessageStore
	debounce time.Duration
	sessions map[string]*debounceState
	mu       sync.Mutex
	out      chan IngestedMessage
}

// New constructs a Gateway.
func New(msgStore MessageStore, debounce time.Duration) *Gateway {
	return &Gateway{
		msgStore: msgStore,
		debounce: debounce,
		sessions: make(map[string]*debounceState),
		out:      make(chan IngestedMessage, 64),
	}
}

// Out returns the channel of outgoing IngestedMessages.
func (g *Gateway) Out() <-chan IngestedMessage { return g.out }

// Run blocks until ctx is cancelled, then closes Out(). msgingest is driven by Dispatch() calls.
func (g *Gateway) Run(ctx context.Context) {
	<-ctx.Done()
	close(g.out)
}

// Dispatch is called by a platform receiver for each inbound message.
func (g *Gateway) Dispatch(msg platform.InboundMessage) {
	msgID := uuid.New().String()
	inserted, err := g.msgStore.Create(context.Background(), msgID, msg.SessionKey, msg.Platform, msg.Content, msg.RawContent, msg.PlatformMessageID)
	if err != nil {
		log.Printf("msgingest: store error: %v", err)
		return
	}
	if !inserted {
		log.Printf("msgingest: duplicate dropped platformMsgID=%s", msg.PlatformMessageID)
		return
	}

	if cmd := detectCommand(msg.Content); cmd != CommandNone {
		g.handleCommand(msgID, msg, cmd)
		return
	}

	g.mu.Lock()
	defer g.mu.Unlock()

	state, ok := g.sessions[msg.SessionKey]
	if !ok {
		state = &debounceState{}
		g.sessions[msg.SessionKey] = state
	}

	if state.content == "" {
		state.content = msg.Content
	} else {
		state.content = state.content + mergedSeparator + msg.Content
	}
	state.ids = append(state.ids, msgID)
	state.replyTo = msg

	g.msgStore.UpdateStatusBatch(context.Background(), state.ids, "debouncing") //nolint:errcheck

	if state.timer != nil {
		state.timer.Stop()
	}
	sessionKey := msg.SessionKey
	state.timer = time.AfterFunc(g.debounce, func() { g.onDebounce(sessionKey) })
}

func (g *Gateway) handleCommand(msgID string, msg platform.InboundMessage, cmd CommandType) {
	g.mu.Lock()
	if state, ok := g.sessions[msg.SessionKey]; ok {
		if state.timer != nil {
			state.timer.Stop()
		}
		if len(state.ids) > 0 {
			g.msgStore.MarkTerminal(context.Background(), state.ids, "failed") //nolint:errcheck
		}
		delete(g.sessions, msg.SessionKey)
	}
	g.mu.Unlock()

	g.out <- IngestedMessage{
		MsgID:      msgID,
		SessionKey: msg.SessionKey,
		Platform:   msg.Platform,
		Content:    msg.Content,
		ReplyTo:    msg,
		Command:    cmd,
	}
}

func (g *Gateway) onDebounce(sessionKey string) {
	g.mu.Lock()
	state, ok := g.sessions[sessionKey]
	if !ok || len(state.ids) == 0 {
		g.mu.Unlock()
		return
	}
	ids := state.ids
	content := state.content
	replyTo := state.replyTo
	delete(g.sessions, sessionKey)
	g.mu.Unlock()

	primaryID := ids[len(ids)-1]
	mergedIDs := ids[:len(ids)-1]
	if len(mergedIDs) > 0 {
		g.msgStore.MarkMerged(context.Background(), primaryID, mergedIDs) //nolint:errcheck
	}

	g.out <- IngestedMessage{
		MsgID:      primaryID,
		SessionKey: sessionKey,
		Platform:   replyTo.Platform,
		Content:    content,
		ReplyTo:    replyTo,
		Command:    CommandNone,
	}
}

func detectCommand(content string) CommandType {
	switch strings.ToLower(strings.TrimSpace(content)) {
	case "clear":
		return CommandClear
	}
	return CommandNone
}
