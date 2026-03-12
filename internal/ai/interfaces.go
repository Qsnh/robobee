package ai

import "context"

// WorkerSummary carries the minimal info needed for routing decisions.
type WorkerSummary struct {
	ID          string
	Name        string
	Description string
}

// WorkerRouter selects the most suitable worker for an incoming message.
type WorkerRouter interface {
	RouteToWorker(ctx context.Context, message string, workers []WorkerSummary) (string, error)
}

