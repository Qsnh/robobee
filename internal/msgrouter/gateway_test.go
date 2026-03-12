package msgrouter_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/robobee/core/internal/ai"
	"github.com/robobee/core/internal/model"
	"github.com/robobee/core/internal/msgingest"
	"github.com/robobee/core/internal/msgrouter"
	"github.com/robobee/core/internal/store"
)

type mockAIRouter struct {
	workerID string
	err      error
	called   bool
}

func (r *mockAIRouter) RouteToWorker(_ context.Context, _ string, _ []ai.WorkerSummary) (string, error) {
	r.called = true
	return r.workerID, r.err
}

func newTestGateway(t *testing.T, mock *mockAIRouter, workers []model.Worker, in <-chan msgingest.IngestedMessage) *msgrouter.Gateway {
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
	return msgrouter.New(mock, ws, in)
}

func TestGateway_NormalMessage_GetsWorkerID(t *testing.T) {
	in := make(chan msgingest.IngestedMessage, 1)
	workers := []model.Worker{
		{ID: "worker-42", Name: "test", Description: "test worker", WorkDir: t.TempDir()},
	}
	mock := &mockAIRouter{workerID: "worker-42"}
	g := newTestGateway(t, mock, workers, in)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go g.Run(ctx)

	in <- msgingest.IngestedMessage{MsgID: "m1", SessionKey: "s1", Content: "deploy code"}

	select {
	case routed := <-g.Out():
		if routed.WorkerID != "worker-42" {
			t.Fatalf("expected WorkerID=worker-42, got %q", routed.WorkerID)
		}
		if routed.RouteErr != "" {
			t.Fatalf("expected no RouteErr, got %q", routed.RouteErr)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("timeout waiting for routed message")
	}
}

func TestGateway_NoWorkers_SetsRouteErr(t *testing.T) {
	in := make(chan msgingest.IngestedMessage, 1)
	mock := &mockAIRouter{}
	g := newTestGateway(t, mock, []model.Worker{}, in)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go g.Run(ctx)

	in <- msgingest.IngestedMessage{MsgID: "m1", SessionKey: "s1", Content: "help"}

	select {
	case routed := <-g.Out():
		if routed.RouteErr == "" {
			t.Fatal("expected RouteErr to be set")
		}
		if routed.WorkerID != "" {
			t.Fatalf("expected empty WorkerID, got %q", routed.WorkerID)
		}
		if mock.called {
			t.Error("AI router should not be called when no workers available")
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("timeout waiting for error message")
	}
}

func TestGateway_UnknownWorkerID_SetsRouteErr(t *testing.T) {
	in := make(chan msgingest.IngestedMessage, 1)
	workers := []model.Worker{
		{ID: "w1", Name: "test", Description: "test worker", WorkDir: t.TempDir()},
	}
	mock := &mockAIRouter{workerID: "unknown-id"}
	g := newTestGateway(t, mock, workers, in)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go g.Run(ctx)

	in <- msgingest.IngestedMessage{MsgID: "m1", SessionKey: "s1", Content: "some message"}

	select {
	case routed := <-g.Out():
		if routed.RouteErr == "" {
			t.Fatal("expected RouteErr to be set for unknown worker ID")
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("timeout")
	}
}

func TestGateway_AIError_SetsRouteErr(t *testing.T) {
	in := make(chan msgingest.IngestedMessage, 1)
	workers := []model.Worker{
		{ID: "w1", Name: "test", Description: "test worker", WorkDir: t.TempDir()},
	}
	mock := &mockAIRouter{err: errors.New("AI failure")}
	g := newTestGateway(t, mock, workers, in)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go g.Run(ctx)

	in <- msgingest.IngestedMessage{MsgID: "m1", SessionKey: "s1", Content: "help"}

	select {
	case routed := <-g.Out():
		if routed.RouteErr == "" {
			t.Fatal("expected RouteErr to be set on AI error")
		}
		if routed.WorkerID != "" {
			t.Fatalf("expected empty WorkerID on error, got %q", routed.WorkerID)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("timeout waiting for error message")
	}
}

func TestGateway_CommandMessage_PassesThrough(t *testing.T) {
	in := make(chan msgingest.IngestedMessage, 1)
	mock := &mockAIRouter{workerID: "w1"}
	g := newTestGateway(t, mock, []model.Worker{}, in)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go g.Run(ctx)

	in <- msgingest.IngestedMessage{MsgID: "cmd1", SessionKey: "s1", Content: "clear", Command: msgingest.CommandClear}

	select {
	case routed := <-g.Out():
		if routed.Command != msgingest.CommandClear {
			t.Fatalf("expected CommandClear to pass through, got %q", routed.Command)
		}
		if routed.WorkerID != "" {
			t.Fatalf("command should have empty WorkerID, got %q", routed.WorkerID)
		}
		if mock.called {
			t.Error("AI router should not be called for command messages")
		}
	case <-time.After(300 * time.Millisecond):
		t.Fatal("timeout")
	}
}
