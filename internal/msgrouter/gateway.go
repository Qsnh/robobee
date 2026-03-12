package msgrouter

import (
	"context"
	"fmt"
	"log"

	"github.com/robobee/core/internal/ai"
	"github.com/robobee/core/internal/msgingest"
	"github.com/robobee/core/internal/store"
)

const noWorkerMsg = "❌ 没有找到合适的 Worker，请换个描述试试"

// RoutedMessage extends IngestedMessage with a resolved worker ID.
type RoutedMessage struct {
	msgingest.IngestedMessage
	WorkerID string
	RouteErr string // user-facing error string; non-empty means routing failed
}

// Gateway reads IngestedMessages, routes normal messages, and emits RoutedMessages.
type Gateway struct {
	aiRouter    ai.WorkerRouter
	workerStore *store.WorkerStore
	in          <-chan msgingest.IngestedMessage
	out         chan RoutedMessage
}

// New constructs a Gateway.
func New(aiRouter ai.WorkerRouter, workerStore *store.WorkerStore, in <-chan msgingest.IngestedMessage) *Gateway {
	return &Gateway{
		aiRouter:    aiRouter,
		workerStore: workerStore,
		in:          in,
		out:         make(chan RoutedMessage, 64),
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
		select {
		case g.out <- RoutedMessage{IngestedMessage: msg}:
		case <-ctx.Done():
			return
		}
		return
	}

	workerID, err := g.resolveWorker(ctx, msg.Content)
	if err != nil {
		log.Printf("msgrouter: route error sessionKey=%s: %v", msg.SessionKey, err)
		select {
		case g.out <- RoutedMessage{IngestedMessage: msg, RouteErr: noWorkerMsg}:
		case <-ctx.Done():
			return
		}
		return
	}

	select {
	case g.out <- RoutedMessage{IngestedMessage: msg, WorkerID: workerID}:
	case <-ctx.Done():
		return
	}
}

func (g *Gateway) resolveWorker(ctx context.Context, content string) (string, error) {
	workers, err := g.workerStore.List()
	if err != nil {
		return "", fmt.Errorf("list workers: %w", err)
	}
	if len(workers) == 0 {
		return "", fmt.Errorf("no workers available")
	}
	summaries := make([]ai.WorkerSummary, len(workers))
	validIDs := make(map[string]bool, len(workers))
	for i, w := range workers {
		summaries[i] = ai.WorkerSummary{ID: w.ID, Name: w.Name, Description: w.Description}
		validIDs[w.ID] = true
	}
	workerID, err := g.aiRouter.RouteToWorker(ctx, content, summaries)
	if err != nil {
		return "", err
	}
	if !validIDs[workerID] {
		return "", fmt.Errorf("worker %q not found", workerID)
	}
	return workerID, nil
}
