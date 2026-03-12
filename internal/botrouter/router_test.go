package botrouter_test

import (
	"context"
	"testing"

	"github.com/robobee/core/internal/ai"
	"github.com/robobee/core/internal/botrouter"
	"github.com/robobee/core/internal/model"
	"github.com/robobee/core/internal/store"
)

// mockRouter is a test double for ai.WorkerRouter.
type mockRouter struct {
	routeFunc func(message string, workers []ai.WorkerSummary) (string, error)
}

func (m *mockRouter) RouteToWorker(_ context.Context, message string, workers []ai.WorkerSummary) (string, error) {
	return m.routeFunc(message, workers)
}

func newTestRouter(t *testing.T, mock ai.WorkerRouter, workers []model.Worker) *botrouter.Router {
	t.Helper()
	db, err := store.InitDB(":memory:")
	if err != nil {
		t.Fatalf("init db: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	ws := store.NewWorkerStore(db)
	for _, w := range workers {
		if _, err := ws.Create(w); err != nil {
			t.Fatalf("create worker: %v", err)
		}
	}
	return botrouter.NewRouter(mock, ws)
}

func TestRouter_Route_PicksCorrectWorker(t *testing.T) {
	workers := []model.Worker{
		{ID: "w1", Name: "mas", Description: "market analyst", WorkDir: t.TempDir()},
		{ID: "w2", Name: "nova", Description: "code reviewer", WorkDir: t.TempDir()},
	}

	mock := &mockRouter{
		routeFunc: func(_ string, _ []ai.WorkerSummary) (string, error) {
			return "w1", nil
		},
	}
	router := newTestRouter(t, mock, workers)

	id, err := router.Route(context.Background(), "analyze sales data")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if id != "w1" {
		t.Errorf("got %q, want %q", id, "w1")
	}
}

func TestRouter_Route_NoWorkers_ReturnsError(t *testing.T) {
	mock := &mockRouter{
		routeFunc: func(_ string, _ []ai.WorkerSummary) (string, error) {
			return "", nil
		},
	}
	router := newTestRouter(t, mock, []model.Worker{})

	_, err := router.Route(context.Background(), "some message")
	if err == nil {
		t.Fatal("expected error when no workers available")
	}
}

func TestRouter_Route_UnknownWorkerID(t *testing.T) {
	workers := []model.Worker{
		{ID: "w1", Name: "mas", Description: "market analyst", WorkDir: t.TempDir()},
	}

	mock := &mockRouter{
		routeFunc: func(_ string, _ []ai.WorkerSummary) (string, error) {
			return "unknown-id", nil // returns an ID not in the worker store
		},
	}
	router := newTestRouter(t, mock, workers)

	_, err := router.Route(context.Background(), "some message")
	if err == nil {
		t.Fatal("expected error when router returns unknown worker ID")
	}
}
