package platform

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/robobee/core/internal/model"
)

// --- Shared test helpers ---

// dedupMockStore controls Create's inserted return and signals when Create is called.
type dedupMockStore struct {
	stubPipelineStore
	insertResult bool
	createDone   chan struct{} // closed on first Create call
}

func newDedupMockStore(inserted bool) *dedupMockStore {
	return &dedupMockStore{
		stubPipelineStore: *newStubPipelineStore(),
		insertResult:      inserted,
		createDone:        make(chan struct{}),
	}
}

func (m *dedupMockStore) Create(_ context.Context, _, _, _, _, _, _ string) (bool, error) {
	select {
	case <-m.createDone:
	default:
		close(m.createDone) // signal once
	}
	return m.insertResult, nil
}

// countingRouter counts Route calls and signals the first call via a channel.
type countingRouter struct {
	calls     atomic.Int32
	routeDone chan struct{}
	id        string
}

func newCountingRouter(id string) *countingRouter {
	return &countingRouter{id: id, routeDone: make(chan struct{})}
}

func (r *countingRouter) Route(_ context.Context, _ string) (string, error) {
	r.calls.Add(1)
	select {
	case <-r.routeDone:
	default:
		close(r.routeDone)
	}
	return r.id, nil
}

// fakePlatform injects InboundMessages into the manager's dispatch closure via a channel.
type fakePlatform struct {
	msgCh chan InboundMessage
}

func (p *fakePlatform) ID() string                            { return "test" }
func (p *fakePlatform) Receiver() PlatformReceiverAdapter    { return p }
func (p *fakePlatform) Sender() PlatformSenderAdapter        { return p }
func (p *fakePlatform) Send(_ context.Context, _ OutboundMessage) error { return nil }
func (p *fakePlatform) Start(ctx context.Context, dispatch func(InboundMessage)) error {
	for {
		select {
		case <-ctx.Done():
			return nil
		case msg, ok := <-p.msgCh:
			if !ok {
				return nil
			}
			dispatch(msg)
		}
	}
}

// --- Tests ---

// TestManagerDispatch_DuplicatePlatformMsg_NotRouted verifies that when Create
// returns inserted=false (duplicate), the manager does NOT call Route.
func TestManagerDispatch_DuplicatePlatformMsg_NotRouted(t *testing.T) {
	store := newDedupMockStore(false) // duplicate — inserted=false
	router := newCountingRouter("w1")
	pipeline := NewPipeline(router, store, &stubManager{
		exec: model.WorkerExecution{Status: model.ExecStatusCompleted},
	})
	m := NewManager(pipeline, store, 50*time.Millisecond)

	plat := &fakePlatform{msgCh: make(chan InboundMessage, 1)}
	m.Register(plat)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go m.StartAll(ctx)

	plat.msgCh <- InboundMessage{
		Platform:          "test",
		SenderID:          "user1",
		SessionKey:        "test:chat1:user1",
		Content:           "hello",
		PlatformMessageID: "plat-msg-dup",
	}

	// Wait for Create to be called (dispatch reached dedup check).
	select {
	case <-store.createDone:
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for Create to be called")
	}

	// Give a moment for Route to be called if it were going to be.
	time.Sleep(50 * time.Millisecond)

	if n := router.calls.Load(); n != 0 {
		t.Errorf("Route should not be called for duplicate: called %d time(s)", n)
	}
}

// TestManagerDispatch_FirstPlatformMsg_IsRouted verifies that when Create
// returns inserted=true (new message), the manager DOES call Route.
func TestManagerDispatch_FirstPlatformMsg_IsRouted(t *testing.T) {
	store := newDedupMockStore(true) // new message — inserted=true
	router := newCountingRouter("w1")
	pipeline := NewPipeline(router, store, &stubManager{
		exec: model.WorkerExecution{Status: model.ExecStatusCompleted},
	})
	m := NewManager(pipeline, store, 50*time.Millisecond)

	plat := &fakePlatform{msgCh: make(chan InboundMessage, 1)}
	m.Register(plat)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go m.StartAll(ctx)

	plat.msgCh <- InboundMessage{
		Platform:          "test",
		SenderID:          "user1",
		SessionKey:        "test:chat1:user1",
		Content:           "hello",
		PlatformMessageID: "plat-msg-new",
	}

	// Wait for Route to be called.
	select {
	case <-router.routeDone:
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for Route to be called")
	}

	if n := router.calls.Load(); n != 1 {
		t.Errorf("Route should be called once: called %d time(s)", n)
	}
}
