package botrouter_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/robobee/core/internal/ai"
	"github.com/robobee/core/internal/botrouter"
	"github.com/robobee/core/internal/config"
	"github.com/robobee/core/internal/model"
	"github.com/robobee/core/internal/store"
)

func newRouterWithWorkers(t *testing.T, aiHandler http.HandlerFunc, workers []model.Worker) *botrouter.Router {
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

	srv := httptest.NewServer(aiHandler)
	t.Cleanup(srv.Close)
	aiClient := ai.NewClient(config.AIConfig{BaseURL: srv.URL, APIKey: "test", Model: "test"})

	return botrouter.NewRouter(aiClient, ws)
}

func TestRouter_Route_PicksCorrectWorker(t *testing.T) {
	workers := []model.Worker{
		{ID: "w1", Name: "mas", Description: "market analyst", WorkDir: t.TempDir()},
		{ID: "w2", Name: "nova", Description: "code reviewer", WorkDir: t.TempDir()},
	}

	router := newRouterWithWorkers(t, func(w http.ResponseWriter, r *http.Request) {
		resp := map[string]any{
			"choices": []map[string]any{
				{"message": map[string]string{"content": "w1"}},
			},
		}
		json.NewEncoder(w).Encode(resp)
	}, workers)

	id, err := router.Route(context.Background(), "analyze sales data")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if id != "w1" {
		t.Errorf("got %q, want %q", id, "w1")
	}
}

func TestRouter_Route_NoWorkers_ReturnsError(t *testing.T) {
	router := newRouterWithWorkers(t, func(w http.ResponseWriter, r *http.Request) {}, []model.Worker{})

	_, err := router.Route(context.Background(), "some message")
	if err == nil {
		t.Fatal("expected error when no workers available")
	}
}
