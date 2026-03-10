package botrouter

import (
	"context"
	"fmt"

	"github.com/robobee/core/internal/ai"
	"github.com/robobee/core/internal/store"
)

type Router struct {
	aiClient    *ai.Client
	workerStore *store.WorkerStore
}

func NewRouter(aiClient *ai.Client, workerStore *store.WorkerStore) *Router {
	return &Router{aiClient: aiClient, workerStore: workerStore}
}

// Route returns the worker ID best suited to handle the message.
// Uses WorkerStore.List() to fetch all workers live.
func (r *Router) Route(ctx context.Context, message string) (string, error) {
	workers, err := r.workerStore.List()
	if err != nil {
		return "", fmt.Errorf("list workers: %w", err)
	}
	if len(workers) == 0 {
		return "", fmt.Errorf("no workers available")
	}

	summaries := make([]ai.WorkerSummary, len(workers))
	for i, w := range workers {
		summaries[i] = ai.WorkerSummary{ID: w.ID, Name: w.Name, Description: w.Description}
	}

	return r.aiClient.RouteToWorker(ctx, message, summaries)
}
