package ai_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/robobee/core/internal/ai"
	"github.com/robobee/core/internal/config"
)

func newTestAIClient(t *testing.T, handler http.HandlerFunc) *ai.Client {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	return ai.NewClient(config.AIConfig{
		BaseURL: srv.URL,
		APIKey:  "test-key",
		Model:   "test-model",
	})
}

func TestRouteToWorker_ReturnsWorkerID(t *testing.T) {
	client := newTestAIClient(t, func(w http.ResponseWriter, r *http.Request) {
		resp := map[string]interface{}{
			"choices": []map[string]interface{}{
				{"message": map[string]string{"content": "worker-abc"}},
			},
		}
		json.NewEncoder(w).Encode(resp)
	})

	workers := []ai.WorkerSummary{
		{ID: "worker-abc", Name: "mas", Description: "market analyst"},
		{ID: "worker-xyz", Name: "nova", Description: "code reviewer"},
	}

	id, err := client.RouteToWorker(context.Background(), "analyze market data", workers)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if id != "worker-abc" {
		t.Errorf("got %q, want %q", id, "worker-abc")
	}
}

func TestRouteToWorker_InvalidID_ReturnsError(t *testing.T) {
	client := newTestAIClient(t, func(w http.ResponseWriter, r *http.Request) {
		resp := map[string]interface{}{
			"choices": []map[string]interface{}{
				{"message": map[string]string{"content": "nonexistent-id"}},
			},
		}
		json.NewEncoder(w).Encode(resp)
	})

	workers := []ai.WorkerSummary{
		{ID: "worker-abc", Name: "mas", Description: "market analyst"},
	}

	_, err := client.RouteToWorker(context.Background(), "some message", workers)
	if err == nil {
		t.Fatal("expected error for invalid worker ID, got nil")
	}
}
