package msgsender

import (
	"context"
	"log"

	"github.com/robobee/core/internal/platform"
)

// SenderEventType classifies outbound events from the dispatcher.
type SenderEventType int

const (
	SenderEventACK    SenderEventType = iota
	SenderEventResult
	SenderEventError
)

// SenderEvent is an outbound message to send to a platform user.
type SenderEvent struct {
	Type    SenderEventType
	ReplyTo platform.InboundMessage
	Content string
}

// Gateway consumes SenderEvents and sends them via the appropriate platform adapter.
// It is the only component that calls PlatformSenderAdapter.Send.
type Gateway struct {
	senders map[string]platform.PlatformSenderAdapter
	in      <-chan SenderEvent
}

// New constructs a Gateway.
func New(senders map[string]platform.PlatformSenderAdapter, in <-chan SenderEvent) *Gateway {
	return &Gateway{senders: senders, in: in}
}

// Run processes events until ctx is cancelled.
func (g *Gateway) Run(ctx context.Context) {
	for {
		select {
		case evt, ok := <-g.in:
			if !ok {
				return
			}
			g.send(ctx, evt)
		case <-ctx.Done():
			return
		}
	}
}

// Note: msgsender has no output channel to close — it is the terminal sink.

// send is called synchronously to preserve per-platform message ordering.
// A slow platform adapter will block the Run loop; callers are expected to
// size g.in appropriately to absorb burst traffic.
func (g *Gateway) send(ctx context.Context, evt SenderEvent) {
	sender, ok := g.senders[evt.ReplyTo.Platform]
	if !ok {
		log.Printf("msgsender: no sender for platform %q", evt.ReplyTo.Platform)
		return
	}
	if err := sender.Send(ctx, platform.OutboundMessage{
		SessionKey: evt.ReplyTo.SessionKey,
		Content:    evt.Content,
		ReplyTo:    evt.ReplyTo,
	}); err != nil {
		log.Printf("msgsender: send error platform=%s: %v", evt.ReplyTo.Platform, err)
	}
}
