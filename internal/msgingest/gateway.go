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
	Create(ctx context.Context, id, sessionKey, platform, content, raw, platformMsgID string, messageTime int64) (bool, error)
	UpdateStatusBatch(ctx context.Context, ids []string, status string) error
	MarkTerminal(ctx context.Context, ids []string, status string) error
	MarkMerged(ctx context.Context, primaryID string, mergedIDs []string) error
}

type debounceState struct {
	timer      *time.Timer
	generation int
	ids        []string
	content    string
	replyTo    platform.InboundMessage
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

// emit sends msg to the output channel non-blocking; drops and logs if the channel is full.
func (g *Gateway) emit(msg IngestedMessage) {
	select {
	case g.out <- msg:
	default:
		log.Printf("msgingest: output channel full, dropping message sessionKey=%s", msg.SessionKey)
	}
}

// Dispatch is called by a platform receiver for each inbound message.
func (g *Gateway) Dispatch(msg platform.InboundMessage) {
	msgID := uuid.New().String()
	inserted, err := g.msgStore.Create(context.Background(), msgID, msg.SessionKey, msg.Platform, msg.Content, msg.RawContent, msg.PlatformMessageID, msg.MessageTime)
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

	// Resolve effective message time; zero means unknown, treat as now (never historical).
	msgTime := msg.MessageTime
	if msgTime == 0 {
		msgTime = time.Now().UnixMilli()
	}

	// Historical message: age exceeds debounce window → skip debounce, emit immediately.
	if time.Now().UnixMilli()-msgTime > g.debounce.Milliseconds() {
		g.emit(IngestedMessage{
			MsgID:      msgID,
			SessionKey: msg.SessionKey,
			Platform:   msg.Platform,
			Content:    msg.Content,
			ReplyTo:    msg,
			Command:    CommandNone,
		})
		return
	}

	g.mu.Lock()

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

	ids := make([]string, len(state.ids))
	copy(ids, state.ids)

	if state.timer != nil {
		state.timer.Stop()
	}
	state.generation++
	gen := state.generation
	sessionKey := msg.SessionKey
	state.timer = time.AfterFunc(g.debounce, func() { g.onDebounce(sessionKey, gen) })

	g.mu.Unlock()

	g.msgStore.UpdateStatusBatch(context.Background(), ids, "debouncing") //nolint:errcheck
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

	g.emit(IngestedMessage{
		MsgID:      msgID,
		SessionKey: msg.SessionKey,
		Platform:   msg.Platform,
		Content:    msg.Content,
		ReplyTo:    msg,
		Command:    cmd,
	})
}

func (g *Gateway) onDebounce(sessionKey string, generation int) {
	g.mu.Lock()
	state, ok := g.sessions[sessionKey]
	if !ok || len(state.ids) == 0 {
		g.mu.Unlock()
		return
	}
	// Bail out if a newer timer has superseded this one.
	if state.generation != generation {
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

	g.emit(IngestedMessage{
		MsgID:      primaryID,
		SessionKey: sessionKey,
		Platform:   replyTo.Platform,
		Content:    content,
		ReplyTo:    replyTo,
		Command:    CommandNone,
	})
}

func detectCommand(content string) CommandType {
	switch strings.ToLower(strings.TrimSpace(content)) {
	case "clear":
		return CommandClear
	}
	return CommandNone
}
