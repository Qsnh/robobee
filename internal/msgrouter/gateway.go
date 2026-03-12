package msgrouter

import (
	"context"
	"log"

	"github.com/robobee/core/internal/msgingest"
)

const noWorkerMsg = "❌ 没有找到合适的 Worker，请换个描述试试"

// MessageRouter routes a message content to a worker ID.
type MessageRouter interface {
	Route(ctx context.Context, message string) (string, error)
}

// RoutedMessage extends IngestedMessage with a resolved worker ID.
type RoutedMessage struct {
	msgingest.IngestedMessage
	WorkerID string
	RouteErr string // user-facing error string; non-empty means routing failed
}

// Gateway reads IngestedMessages, routes normal messages, and emits RoutedMessages.
type Gateway struct {
	router MessageRouter
	in     <-chan msgingest.IngestedMessage
	out    chan RoutedMessage
}

// New constructs a Gateway.
func New(router MessageRouter, in <-chan msgingest.IngestedMessage) *Gateway {
	return &Gateway{
		router: router,
		in:     in,
		out:    make(chan RoutedMessage, 64),
	}
}

// Out returns the channel of outgoing RoutedMessages.
func (g *Gateway) Out() <-chan RoutedMessage { return g.out }

// Run processes messages until ctx is cancelled, then closes Out().
func (g *Gateway) Run(ctx context.Context) {
	defer close(g.out)
	for {
		select {
		case msg, ok := <-g.in:
			if !ok {
				return
			}
			g.route(ctx, msg)
		case <-ctx.Done():
			return
		}
	}
}

func (g *Gateway) route(ctx context.Context, msg msgingest.IngestedMessage) {
	if msg.Command != msgingest.CommandNone {
		g.out <- RoutedMessage{IngestedMessage: msg}
		return
	}

	workerID, err := g.router.Route(ctx, msg.Content)
	if err != nil {
		log.Printf("msgrouter: route error sessionKey=%s: %v", msg.SessionKey, err)
		g.out <- RoutedMessage{IngestedMessage: msg, RouteErr: noWorkerMsg}
		return
	}

	g.out <- RoutedMessage{IngestedMessage: msg, WorkerID: workerID}
}
