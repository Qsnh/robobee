package msgrouter_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/robobee/core/internal/msgingest"
	"github.com/robobee/core/internal/msgrouter"
)

type mockRouter struct {
	workerID string
	err      error
}

func (r *mockRouter) Route(_ context.Context, _ string) (string, error) {
	return r.workerID, r.err
}

func sendIngest(ch chan<- msgingest.IngestedMessage, msg msgingest.IngestedMessage) {
	ch <- msg
}

// TestGateway_NormalMessage_GetsWorkerID verifies that a normal message is routed.
func TestGateway_NormalMessage_GetsWorkerID(t *testing.T) {
	in := make(chan msgingest.IngestedMessage, 1)
	g := msgrouter.New(&mockRouter{workerID: "worker-42"}, in)

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

// TestGateway_RouteFailure_SetsRouteErr verifies that a routing error is propagated.
func TestGateway_RouteFailure_SetsRouteErr(t *testing.T) {
	in := make(chan msgingest.IngestedMessage, 1)
	g := msgrouter.New(&mockRouter{err: errors.New("no workers")}, in)

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
			t.Fatalf("expected empty WorkerID on error, got %q", routed.WorkerID)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("timeout waiting for error message")
	}
}

// TestGateway_CommandMessage_PassesThrough verifies that command messages bypass routing.
func TestGateway_CommandMessage_PassesThrough(t *testing.T) {
	in := make(chan msgingest.IngestedMessage, 1)
	called := false
	router := &spyRouter{fn: func() { called = true }, id: "w1"}
	g := msgrouter.New(router, in)

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
	case <-time.After(300 * time.Millisecond):
		t.Fatal("timeout")
	}

	if called {
		t.Error("Route should not be called for command messages")
	}
}

type spyRouter struct {
	fn func()
	id string
}

func (r *spyRouter) Route(_ context.Context, _ string) (string, error) {
	r.fn()
	return r.id, nil
}
