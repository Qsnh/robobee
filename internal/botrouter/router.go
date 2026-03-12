package botrouter

import (
	"context"
	"fmt"

	"github.com/robobee/core/internal/ai"
	"github.com/robobee/core/internal/store"
)

type Router struct {
	router      ai.WorkerRouter
	workerStore *store.WorkerStore
}

func NewRouter(router ai.WorkerRouter, workerStore *store.WorkerStore) *Router {
	return &Router{router: router, workerStore: workerStore}
}

// Route returns the worker ID best suited to handle the message.
func (r *Router) Route(ctx context.Context, message string) (string, error) {
	workers, err := r.workerStore.List()
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

	workerID, err := r.router.RouteToWorker(ctx, message, summaries)
	if err != nil {
		return "", err
	}
	if !validIDs[workerID] {
		return "", fmt.Errorf("worker %q not found", workerID)
	}
	return workerID, nil
}
