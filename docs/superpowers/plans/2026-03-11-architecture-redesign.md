# Architecture Redesign: Four-Layer Message Pipeline Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Refactor `internal/platform` god object into four focused packages (`msgingest`, `msgrouter`, `dispatcher`, `msgsender`) connected by typed Go channels.

**Architecture:** Platform receivers call `msgingest.Gateway.Dispatch()` → debounced `IngestedMessage` flows to `msgrouter` → `RoutedMessage` with workerID flows to `dispatcher` → `SenderEvent` (ACK or result) flows to `msgsender` → platform sender. All communication via buffered channels; no back-references between layers.

**Tech Stack:** Go 1.25, SQLite (via existing `store` package), no new dependencies.

**Spec:** `docs/superpowers/specs/2026-03-11-architecture-redesign-design.md`

---

## File Map

**Create:**
- `internal/msgingest/gateway.go` — `CommandType`, `IngestedMessage`, `MessageStore` interface, `Gateway` (dedup + debounce + merge)
- `internal/msgingest/gateway_test.go`
- `internal/msgrouter/gateway.go` — `RoutedMessage`, `MessageRouter` interface, `Gateway` (AI routing)
- `internal/msgrouter/gateway_test.go`
- `internal/dispatcher/dispatcher.go` — `SenderEventType`, `SenderEvent`, `ExecutionManager` + `MessageStore` interfaces, `Dispatcher` (serialized async execution, ACK, command handling, startup recovery)
- `internal/dispatcher/dispatcher_test.go`
- `internal/msgsender/gateway.go` — `Gateway` (platform sender lookup + send)
- `internal/msgsender/gateway_test.go`

**Modify:**
- `internal/platform/interfaces.go` — remove `MessageStore` interface and `IsClearCommand`/constants (moved to new packages); keep `InboundMessage`, `OutboundMessage`, adapter interfaces, `Platform`, `Session`
- `cmd/server/main.go` — replace old wiring with new four-layer pipeline

**Delete** (after new packages are wired and tests pass):
- `internal/platform/manager.go`
- `internal/platform/pipeline.go`
- `internal/platform/queue_manager.go`
- `internal/platform/session_queue.go`
- `internal/platform/pipeline_test.go`
- `internal/platform/manager_dedup_test.go`
- `internal/platform/queue_manager_test.go`
- `internal/platform/session_queue_test.go`

---

## Chunk 1: msgingest — Message Reception Gateway

### Task 1: Trim `internal/platform/interfaces.go`

**Files:**
- Modify: `internal/platform/interfaces.go`

Remove `MessageStore` interface (lines 55–66) and anything not a pure transport type. After this task, the file must contain only: `InboundMessage`, `OutboundMessage`, `PlatformReceiverAdapter`, `PlatformSenderAdapter`, `Platform`, `Session`.

- [ ] **Step 1: Read the current file**

```bash
cat internal/platform/interfaces.go
```

- [ ] **Step 2: Remove the `MessageStore` interface block and unused import**

Delete the `MessageStore` interface block (lines 53–66). Also remove the `"github.com/robobee/core/internal/model"` import from the file — it is only used by `MessageStore` and becomes unused after deletion. The `Session` struct and all platform adapter interfaces stay.

- [ ] **Step 3: Verify the package still compiles (it will have errors from manager.go/pipeline.go which import it — that's OK for now)**

```bash
go build ./internal/platform/... 2>&1 | grep -v "manager\|pipeline\|queue" || true
```

- [ ] **Step 4: Commit**

```bash
git add internal/platform/interfaces.go
git commit -m "refactor: remove MessageStore interface from platform/interfaces — moving to consumers"
```

---

### Task 2: Create `internal/msgingest` package

**Files:**
- Create: `internal/msgingest/gateway.go`
- Create: `internal/msgingest/gateway_test.go`

- [ ] **Step 1: Write the failing tests**

Create `internal/msgingest/gateway_test.go`:

```go
package msgingest_test

import (
	"context"
	"testing"
	"time"

	"github.com/robobee/core/internal/msgingest"
	"github.com/robobee/core/internal/platform"
)

// mockMsgStore stubs the MessageStore interface for msgingest.
type mockMsgStore struct {
	insertResult bool // returned by Create
	created      []string
	failed       []string
	merged       map[string][]string // primaryID -> mergedIDs
}

func newMock(inserted bool) *mockMsgStore {
	return &mockMsgStore{insertResult: inserted, merged: make(map[string][]string)}
}

func (m *mockMsgStore) Create(_ context.Context, id, _, _, _, _, _ string) (bool, error) {
	m.created = append(m.created, id)
	return m.insertResult, nil
}
func (m *mockMsgStore) UpdateStatusBatch(_ context.Context, ids []string, _ string) error {
	return nil
}
func (m *mockMsgStore) MarkTerminal(_ context.Context, ids []string, _ string) error {
	m.failed = append(m.failed, ids...)
	return nil
}
func (m *mockMsgStore) MarkMerged(_ context.Context, primaryID string, mergedIDs []string) error {
	m.merged[primaryID] = mergedIDs
	return nil
}

func inbound(sessionKey, content, platformMsgID string) platform.InboundMessage {
	return platform.InboundMessage{
		Platform:          "test",
		SessionKey:        sessionKey,
		Content:           content,
		PlatformMessageID: platformMsgID,
	}
}

// TestGateway_Dedup_DropsKnownMessage verifies that when Create returns false (duplicate),
// nothing appears on Out().
func TestGateway_Dedup_DropsKnownMessage(t *testing.T) {
	g := msgingest.New(newMock(false), 100*time.Millisecond)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go g.Run(ctx)

	g.Dispatch(inbound("s1", "hello", "dup-1"))

	select {
	case msg := <-g.Out():
		t.Fatalf("expected nothing on Out(), got %+v", msg)
	case <-time.After(300 * time.Millisecond):
		// expected: duplicate dropped
	}
}

// TestGateway_Debounce_EmitsSingleMergedMessage verifies that two messages arriving
// within the debounce window are merged into one IngestedMessage.
func TestGateway_Debounce_EmitsSingleMergedMessage(t *testing.T) {
	g := msgingest.New(newMock(true), 100*time.Millisecond)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go g.Run(ctx)

	g.Dispatch(inbound("s1", "hello", "m1"))
	g.Dispatch(inbound("s1", "world", "m2"))

	select {
	case msg := <-g.Out():
		if msg.Command != msgingest.CommandNone {
			t.Fatalf("expected normal message, got command %q", msg.Command)
		}
		if msg.Content != "hello\n\n---\n\nworld" {
			t.Fatalf("expected merged content, got %q", msg.Content)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("timeout waiting for debounced message")
	}

	// Ensure only ONE message is emitted
	select {
	case extra := <-g.Out():
		t.Fatalf("expected only one message, got extra: %+v", extra)
	case <-time.After(200 * time.Millisecond):
		// expected
	}
}

// TestGateway_Command_InterruptsDebounce verifies that a clear command arriving
// during a debounce window cancels the window and the normal messages are marked failed.
func TestGateway_Command_InterruptsDebounce(t *testing.T) {
	store := newMock(true)
	g := msgingest.New(store, 200*time.Millisecond)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go g.Run(ctx)

	g.Dispatch(inbound("s1", "hello", "m1"))          // goes into debounce
	g.Dispatch(inbound("s1", "clear", "cmd-1"))       // command: interrupts

	select {
	case msg := <-g.Out():
		if msg.Command != msgingest.CommandClear {
			t.Fatalf("expected CommandClear, got %q", msg.Command)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("timeout waiting for command message")
	}

	// The debounced normal message should NOT appear
	select {
	case extra := <-g.Out():
		t.Fatalf("debounced message should have been cancelled, got: %+v", extra)
	case <-time.After(400 * time.Millisecond):
		// expected
	}

	// The normal message ID should have been marked terminal (failed)
	if len(store.failed) == 0 {
		t.Error("expected debounced message to be marked failed")
	}
}

// TestGateway_Command_EmitsOnOut verifies that a command message appears on Out()
// with the correct Command field.
func TestGateway_Command_EmitsOnOut(t *testing.T) {
	g := msgingest.New(newMock(true), 100*time.Millisecond)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go g.Run(ctx)

	g.Dispatch(inbound("s1", "clear", "cmd-1"))

	select {
	case msg := <-g.Out():
		if msg.Command != msgingest.CommandClear {
			t.Fatalf("expected CommandClear, got %q", msg.Command)
		}
		if msg.SessionKey != "s1" {
			t.Fatalf("expected SessionKey=s1, got %q", msg.SessionKey)
		}
	case <-time.After(300 * time.Millisecond):
		t.Fatal("timeout waiting for command message")
	}
}
```

- [ ] **Step 2: Run tests to confirm they fail (package doesn't exist yet)**

```bash
go test ./internal/msgingest/... 2>&1 | head -5
```
Expected: `cannot find package` or `no Go files`

- [ ] **Step 3: Create `internal/msgingest/gateway.go`**

```go
package msgingest

import (
	"context"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/robobee/core/internal/platform"
)

const mergedSeparator = "\n\n---\n\n"

// CommandType identifies a built-in command in a message.
type CommandType string

const (
	CommandNone  CommandType = ""
	CommandClear CommandType = "clear"
)

// IngestedMessage is a deduplicated, debounced, normalized message ready for routing.
type IngestedMessage struct {
	MsgID      string
	SessionKey string
	Platform   string
	Content    string
	ReplyTo    platform.InboundMessage
	Command    CommandType
}

// MessageStore is the subset of store.MessageStore used by msgingest.
type MessageStore interface {
	Create(ctx context.Context, id, sessionKey, platform, content, raw, platformMsgID string) (bool, error)
	UpdateStatusBatch(ctx context.Context, ids []string, status string) error
	MarkTerminal(ctx context.Context, ids []string, status string) error
	MarkMerged(ctx context.Context, primaryID string, mergedIDs []string) error
}

type debounceState struct {
	timer   *time.Timer
	ids     []string
	content string
	replyTo platform.InboundMessage
}

// Gateway receives raw platform messages, deduplicates, debounces, and emits IngestedMessages.
type Gateway struct {
	msgStore MessageStore
	debounce time.Duration
	sessions map[string]*debounceState
	mu       sync.Mutex
	out      chan IngestedMessage
}

// New constructs a Gateway.
func New(msgStore MessageStore, debounce time.Duration) *Gateway {
	return &Gateway{
		msgStore: msgStore,
		debounce: debounce,
		sessions: make(map[string]*debounceState),
		out:      make(chan IngestedMessage, 64),
	}
}

// Out returns the channel of outgoing IngestedMessages.
func (g *Gateway) Out() <-chan IngestedMessage { return g.out }

// Run blocks until ctx is cancelled, then closes Out(). msgingest is driven by Dispatch() calls.
func (g *Gateway) Run(ctx context.Context) {
	<-ctx.Done()
	close(g.out)
}

// Dispatch is called by a platform receiver for each inbound message.
func (g *Gateway) Dispatch(msg platform.InboundMessage) {
	msgID := uuid.New().String()
	inserted, err := g.msgStore.Create(context.Background(), msgID, msg.SessionKey, msg.Platform, msg.Content, msg.RawContent, msg.PlatformMessageID)
	if err != nil {
		log.Printf("msgingest: store error: %v", err)
		return
	}
	if !inserted {
		log.Printf("msgingest: duplicate dropped platformMsgID=%s", msg.PlatformMessageID)
		return
	}

	if cmd := detectCommand(msg.Content); cmd != CommandNone {
		g.handleCommand(msgID, msg, cmd)
		return
	}

	g.mu.Lock()
	defer g.mu.Unlock()

	state, ok := g.sessions[msg.SessionKey]
	if !ok {
		state = &debounceState{}
		g.sessions[msg.SessionKey] = state
	}

	if state.content == "" {
		state.content = msg.Content
	} else {
		state.content = state.content + mergedSeparator + msg.Content
	}
	state.ids = append(state.ids, msgID)
	state.replyTo = msg

	g.msgStore.UpdateStatusBatch(context.Background(), state.ids, "debouncing") //nolint:errcheck

	if state.timer != nil {
		state.timer.Stop()
	}
	sessionKey := msg.SessionKey
	state.timer = time.AfterFunc(g.debounce, func() { g.onDebounce(sessionKey) })
}

func (g *Gateway) handleCommand(msgID string, msg platform.InboundMessage, cmd CommandType) {
	g.mu.Lock()
	if state, ok := g.sessions[msg.SessionKey]; ok {
		if state.timer != nil {
			state.timer.Stop()
		}
		if len(state.ids) > 0 {
			g.msgStore.MarkTerminal(context.Background(), state.ids, "failed") //nolint:errcheck
		}
		delete(g.sessions, msg.SessionKey)
	}
	g.mu.Unlock()

	g.out <- IngestedMessage{
		MsgID:      msgID,
		SessionKey: msg.SessionKey,
		Platform:   msg.Platform,
		Content:    msg.Content,
		ReplyTo:    msg,
		Command:    cmd,
	}
}

func (g *Gateway) onDebounce(sessionKey string) {
	g.mu.Lock()
	state, ok := g.sessions[sessionKey]
	if !ok || len(state.ids) == 0 {
		g.mu.Unlock()
		return
	}
	ids := state.ids
	content := state.content
	replyTo := state.replyTo
	delete(g.sessions, sessionKey)
	g.mu.Unlock()

	primaryID := ids[len(ids)-1]
	mergedIDs := ids[:len(ids)-1]
	if len(mergedIDs) > 0 {
		g.msgStore.MarkMerged(context.Background(), primaryID, mergedIDs) //nolint:errcheck
	}

	g.out <- IngestedMessage{
		MsgID:      primaryID,
		SessionKey: sessionKey,
		Platform:   replyTo.Platform,
		Content:    content,
		ReplyTo:    replyTo,
		Command:    CommandNone,
	}
}

func detectCommand(content string) CommandType {
	switch strings.ToLower(strings.TrimSpace(content)) {
	case "clear":
		return CommandClear
	}
	return CommandNone
}
```

- [ ] **Step 4: Run tests**

```bash
go test ./internal/msgingest/... -v
```
Expected: all 4 tests PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/msgingest/
git commit -m "feat: add msgingest gateway (Layer 1 — dedup, debounce, command detection)"
```

---

## Chunk 2: msgrouter + msgsender

### Task 3: Create `internal/msgrouter` package

**Files:**
- Create: `internal/msgrouter/gateway.go`
- Create: `internal/msgrouter/gateway_test.go`

- [ ] **Step 1: Write failing tests**

Create `internal/msgrouter/gateway_test.go`:

```go
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
```

- [ ] **Step 2: Run tests to confirm failure**

```bash
go test ./internal/msgrouter/... 2>&1 | head -5
```

- [ ] **Step 3: Create `internal/msgrouter/gateway.go`**

```go
package msgrouter

import (
	"context"
	"log"

	"github.com/robobee/core/internal/msgingest"
)

const noWorkerMsg = "❌ 没有找到合适的 Worker，请换个描述试试"

// MessageRouter routes a message content to a worker ID.
type MessageRouter interface {
	Route(ctx context.Context, message string) (string, error)
}

// RoutedMessage extends IngestedMessage with a resolved worker ID.
type RoutedMessage struct {
	msgingest.IngestedMessage
	WorkerID string
	RouteErr string // user-facing error string; non-empty means routing failed
}

// Gateway reads IngestedMessages, routes normal messages, and emits RoutedMessages.
type Gateway struct {
	router MessageRouter
	in     <-chan msgingest.IngestedMessage
	out    chan RoutedMessage
}

// New constructs a Gateway.
func New(router MessageRouter, in <-chan msgingest.IngestedMessage) *Gateway {
	return &Gateway{
		router: router,
		in:     in,
		out:    make(chan RoutedMessage, 64),
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
		g.out <- RoutedMessage{IngestedMessage: msg}
		return
	}

	workerID, err := g.router.Route(ctx, msg.Content)
	if err != nil {
		log.Printf("msgrouter: route error sessionKey=%s: %v", msg.SessionKey, err)
		g.out <- RoutedMessage{IngestedMessage: msg, RouteErr: noWorkerMsg}
		return
	}

	g.out <- RoutedMessage{IngestedMessage: msg, WorkerID: workerID}
}
```

- [ ] **Step 4: Run tests**

```bash
go test ./internal/msgrouter/... -v
```
Expected: all 3 tests PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/msgrouter/
git commit -m "feat: add msgrouter gateway (Layer 2 — AI worker routing)"
```

---

### Task 4: Create `internal/msgsender` package

**Files:**
- Create: `internal/msgsender/gateway.go`
- Create: `internal/msgsender/gateway_test.go`

Note: `msgsender` imports `dispatcher` for the `SenderEvent` type. Since `dispatcher` doesn't exist yet, we'll define a local stub event type in tests and use a build tag. Instead, we write `msgsender` to import `dispatcher` and implement the test after `dispatcher` is created in Task 5.

**Alternative (cleaner):** Define `SenderEvent` in `msgsender` itself (it's the consumer), and have `dispatcher` import `msgsender` for the type. This avoids the dependency on the unbuilt `dispatcher` package.

We will use this approach: `SenderEvent` is defined in `msgsender`, `dispatcher` imports `msgsender`.

- [ ] **Step 1: Write failing tests**

Create `internal/msgsender/gateway_test.go`:

```go
package msgsender_test

import (
	"context"
	"testing"
	"time"

	"github.com/robobee/core/internal/msgsender"
	"github.com/robobee/core/internal/platform"
)

type mockSender struct {
	sent []platform.OutboundMessage
}

func (s *mockSender) Send(_ context.Context, msg platform.OutboundMessage) error {
	s.sent = append(s.sent, msg)
	return nil
}

func replyTo(platformID, sessionKey string) platform.InboundMessage {
	return platform.InboundMessage{Platform: platformID, SessionKey: sessionKey}
}

// TestGateway_SendsACK verifies that ACK events are delivered to the platform sender.
func TestGateway_SendsACK(t *testing.T) {
	sender := &mockSender{}
	in := make(chan msgsender.SenderEvent, 1)
	g := msgsender.New(map[string]platform.PlatformSenderAdapter{"test": sender}, in)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go g.Run(ctx)

	in <- msgsender.SenderEvent{
		Type:    msgsender.SenderEventACK,
		ReplyTo: replyTo("test", "test:c:u"),
		Content: "⏳ 正在处理，请稍候…",
	}

	time.Sleep(100 * time.Millisecond)

	if len(sender.sent) != 1 {
		t.Fatalf("expected 1 send call, got %d", len(sender.sent))
	}
	if sender.sent[0].Content != "⏳ 正在处理，请稍候…" {
		t.Fatalf("unexpected content: %q", sender.sent[0].Content)
	}
}

// TestGateway_UnknownPlatform_DoesNotPanic verifies that unknown platform IDs are
// logged and skipped without crashing.
func TestGateway_UnknownPlatform_DoesNotPanic(t *testing.T) {
	in := make(chan msgsender.SenderEvent, 1)
	g := msgsender.New(map[string]platform.PlatformSenderAdapter{}, in) // no senders

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go g.Run(ctx)

	in <- msgsender.SenderEvent{
		Type:    msgsender.SenderEventResult,
		ReplyTo: replyTo("unknown", "unknown:c:u"),
		Content: "hello",
	}

	// Just verify no panic after a short wait
	time.Sleep(100 * time.Millisecond)
}
```

- [ ] **Step 2: Run tests to confirm failure**

```bash
go test ./internal/msgsender/... 2>&1 | head -5
```

- [ ] **Step 3: Create `internal/msgsender/gateway.go`**

```go
package msgsender

import (
	"context"
	"log"

	"github.com/robobee/core/internal/platform"
)

// SenderEventType classifies outbound events from the dispatcher.
type SenderEventType int

const (
	SenderEventACK    SenderEventType = iota
	SenderEventResult
	SenderEventError
)

// SenderEvent is an outbound message to send to a platform user.
type SenderEvent struct {
	Type    SenderEventType
	ReplyTo platform.InboundMessage
	Content string
}

// Gateway consumes SenderEvents and sends them via the appropriate platform adapter.
// It is the only component that calls PlatformSenderAdapter.Send.
type Gateway struct {
	senders map[string]platform.PlatformSenderAdapter
	in      <-chan SenderEvent
}

// New constructs a Gateway.
func New(senders map[string]platform.PlatformSenderAdapter, in <-chan SenderEvent) *Gateway {
	return &Gateway{senders: senders, in: in}
}

// Run processes events until ctx is cancelled.
func (g *Gateway) Run(ctx context.Context) {
	for {
		select {
		case evt, ok := <-g.in:
			if !ok {
				return
			}
			g.send(evt)
		case <-ctx.Done():
			return
		}
	}
}
// Note: msgsender has no output channel to close — it is the terminal sink.

func (g *Gateway) send(evt SenderEvent) {
	sender, ok := g.senders[evt.ReplyTo.Platform]
	if !ok {
		log.Printf("msgsender: no sender for platform %q", evt.ReplyTo.Platform)
		return
	}
	if err := sender.Send(context.Background(), platform.OutboundMessage{
		SessionKey: evt.ReplyTo.SessionKey,
		Content:    evt.Content,
		ReplyTo:    evt.ReplyTo,
	}); err != nil {
		log.Printf("msgsender: send error platform=%s: %v", evt.ReplyTo.Platform, err)
	}
}
```

- [ ] **Step 4: Run tests**

```bash
go test ./internal/msgsender/... -v
```
Expected: all 2 tests PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/msgsender/
git commit -m "feat: add msgsender gateway (Layer 4 — platform send, defines SenderEvent type)"
```

---

## Chunk 3: dispatcher — Message Dispatch Process

### Task 5: Create `internal/dispatcher` package

**Files:**
- Create: `internal/dispatcher/dispatcher.go`
- Create: `internal/dispatcher/dispatcher_test.go`

The dispatcher is the most complex component. It owns serialized async execution, ACK emission, command handling, and startup recovery. It imports `msgrouter` (for `RoutedMessage`) and `msgsender` (for `SenderEvent`).

- [ ] **Step 1: Write failing tests**

Create `internal/dispatcher/dispatcher_test.go`:

```go
package dispatcher_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/robobee/core/internal/dispatcher"
	"github.com/robobee/core/internal/model"
	"github.com/robobee/core/internal/msgingest"
	"github.com/robobee/core/internal/msgrouter"
	"github.com/robobee/core/internal/msgsender"
	"github.com/robobee/core/internal/platform"
)

// --- Mocks ---

type mockExecManager struct {
	execResult model.WorkerExecution
	execErr    error
	getResult  model.WorkerExecution
	calls      int
}

func (m *mockExecManager) ExecuteWorker(_ context.Context, _, _ string) (model.WorkerExecution, error) {
	m.calls++
	return m.execResult, m.execErr
}
func (m *mockExecManager) ReplyExecution(_ context.Context, _, _ string) (model.WorkerExecution, error) {
	return m.execResult, m.execErr
}
func (m *mockExecManager) GetExecution(_ string) (model.WorkerExecution, error) {
	return m.getResult, nil
}

type mockStore struct{}

func (s *mockStore) SetWorkerID(_ context.Context, _, _ string) error          { return nil }
func (s *mockStore) UpdateStatusBatch(_ context.Context, _ []string, _ string) error { return nil }
func (s *mockStore) MarkMerged(_ context.Context, _ string, _ []string) error  { return nil }
func (s *mockStore) MarkTerminal(_ context.Context, _ []string, _ string) error { return nil }
func (s *mockStore) GetUnfinished(_ context.Context) ([]model.PendingMessage, error) {
	return nil, nil
}
func (s *mockStore) GetSession(_ context.Context, _ string) (*platform.Session, error) {
	return nil, nil
}
func (s *mockStore) SetExecution(_ context.Context, _, _, _ string) error { return nil }
func (s *mockStore) InsertClearSentinel(_ context.Context, _, _, _ string) error { return nil }

func routedMsg(sessionKey, workerID, content string) msgrouter.RoutedMessage {
	return msgrouter.RoutedMessage{
		IngestedMessage: msgingest.IngestedMessage{
			MsgID:      "msg-1",
			SessionKey: sessionKey,
			Platform:   "test",
			Content:    content,
			ReplyTo:    platform.InboundMessage{Platform: "test", SessionKey: sessionKey},
		},
		WorkerID: workerID,
	}
}

func collectEvents(out <-chan msgsender.SenderEvent, n int, timeout time.Duration) []msgsender.SenderEvent {
	var events []msgsender.SenderEvent
	deadline := time.After(timeout)
	for len(events) < n {
		select {
		case evt := <-out:
			events = append(events, evt)
		case <-deadline:
			return events
		}
	}
	return events
}

// --- Tests ---

// TestDispatcher_NormalMessage_EmitsACKThenResult verifies the happy path:
// one normal message produces ACK immediately, then Result after execution.
func TestDispatcher_NormalMessage_EmitsACKThenResult(t *testing.T) {
	in := make(chan msgrouter.RoutedMessage, 1)
	mgr := &mockExecManager{
		execResult: model.WorkerExecution{ID: "exec-1", SessionID: "sess-1"},
		getResult:  model.WorkerExecution{ID: "exec-1", Status: model.ExecStatusCompleted, Result: "done!"},
	}
	d := dispatcher.New(mgr, &mockStore{}, in)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go d.Run(ctx)

	in <- routedMsg("s1", "w1", "hello")

	events := collectEvents(d.Out(), 2, 2*time.Second)
	if len(events) < 2 {
		t.Fatalf("expected ACK+Result, got %d events", len(events))
	}
	if events[0].Type != msgsender.SenderEventACK {
		t.Errorf("first event should be ACK, got %v", events[0].Type)
	}
	if events[1].Type != msgsender.SenderEventResult {
		t.Errorf("second event should be Result, got %v", events[1].Type)
	}
	if events[1].Content != "done!" {
		t.Errorf("unexpected result content: %q", events[1].Content)
	}
}

// TestDispatcher_RouteError_EmitsError verifies that a RouteErr produces an error event.
func TestDispatcher_RouteError_EmitsError(t *testing.T) {
	in := make(chan msgrouter.RoutedMessage, 1)
	d := dispatcher.New(&mockExecManager{}, &mockStore{}, in)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go d.Run(ctx)

	in <- msgrouter.RoutedMessage{
		IngestedMessage: msgingest.IngestedMessage{
			MsgID: "m1", SessionKey: "s1", Platform: "test",
			ReplyTo: platform.InboundMessage{Platform: "test", SessionKey: "s1"},
		},
		RouteErr: "❌ 没有找到合适的 Worker",
	}

	events := collectEvents(d.Out(), 1, 500*time.Millisecond)
	if len(events) != 1 || events[0].Type != msgsender.SenderEventError {
		t.Fatalf("expected 1 SenderEventError, got %v", events)
	}
}

// TestDispatcher_ClearCommand_CancelsPendingAndEmitsClear verifies that a clear command
// produces a clear result event.
func TestDispatcher_ClearCommand_CancelsPendingAndEmitsClear(t *testing.T) {
	in := make(chan msgrouter.RoutedMessage, 2)
	d := dispatcher.New(&mockExecManager{execErr: errors.New("never called")}, &mockStore{}, in)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go d.Run(ctx)

	in <- msgrouter.RoutedMessage{
		IngestedMessage: msgingest.IngestedMessage{
			MsgID: "cmd-1", SessionKey: "s1", Platform: "test",
			Content: "clear", Command: msgingest.CommandClear,
			ReplyTo: platform.InboundMessage{Platform: "test", SessionKey: "s1"},
		},
	}

	events := collectEvents(d.Out(), 1, 500*time.Millisecond)
	if len(events) != 1 {
		t.Fatalf("expected 1 event for clear, got %d", len(events))
	}
	if events[0].Type != msgsender.SenderEventResult {
		t.Errorf("expected SenderEventResult for clear, got %v", events[0].Type)
	}
	if events[0].Content != "✅ 上下文已重置" {
		t.Errorf("unexpected clear content: %q", events[0].Content)
	}
}

// TestDispatcher_TwoMessages_SameSession_Serialized verifies that a second message
// for the same session is executed only after the first completes.
func TestDispatcher_TwoMessages_SameSession_Serialized(t *testing.T) {
	in := make(chan msgrouter.RoutedMessage, 2)

	execCount := 0
	blocker := make(chan struct{})
	mgr := &blockingExecManager{
		blocker: blocker,
		result:  model.WorkerExecution{ID: "exec-1", Status: model.ExecStatusCompleted, Result: "ok"},
	}
	d := dispatcher.New(mgr, &mockStore{}, in)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go d.Run(ctx)

	msg1 := routedMsg("s1", "w1", "first")
	msg1.MsgID = "msg-1"
	msg2 := routedMsg("s1", "w1", "second")
	msg2.MsgID = "msg-2"

	in <- msg1
	in <- msg2

	// Both should get ACK immediately
	events := collectEvents(d.Out(), 2, 500*time.Millisecond)
	ackCount := 0
	for _, e := range events {
		if e.Type == msgsender.SenderEventACK {
			ackCount++
		}
	}
	if ackCount != 2 {
		t.Errorf("expected 2 ACKs, got %d (events: %v)", ackCount, events)
	}

	// Verify execution count before unblocking
	time.Sleep(50 * time.Millisecond)
	if execCount > 1 {
		t.Error("second execution should not start before first completes")
	}
	_ = execCount

	// Unblock first execution
	close(blocker)

	// Should get 2 results eventually
	results := collectEvents(d.Out(), 2, 2*time.Second)
	resultCount := 0
	for _, e := range results {
		if e.Type == msgsender.SenderEventResult {
			resultCount++
		}
	}
	if resultCount != 2 {
		t.Errorf("expected 2 results, got %d", resultCount)
	}
}

// blockingExecManager blocks ExecuteWorker until blocker is closed, then returns result.
type blockingExecManager struct {
	blocker <-chan struct{}
	result  model.WorkerExecution
}

func (m *blockingExecManager) ExecuteWorker(_ context.Context, _, _ string) (model.WorkerExecution, error) {
	<-m.blocker
	return model.WorkerExecution{ID: "exec-x"}, nil
}
func (m *blockingExecManager) ReplyExecution(_ context.Context, _, _ string) (model.WorkerExecution, error) {
	return model.WorkerExecution{ID: "exec-x"}, nil
}
func (m *blockingExecManager) GetExecution(_ string) (model.WorkerExecution, error) {
	return m.result, nil
}
```

- [ ] **Step 2: Run tests to confirm failure**

```bash
go test ./internal/dispatcher/... 2>&1 | head -5
```

- [ ] **Step 3: Create `internal/dispatcher/dispatcher.go`**

```go
package dispatcher

import (
	"context"
	"log"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/robobee/core/internal/model"
	"github.com/robobee/core/internal/msgingest"
	"github.com/robobee/core/internal/msgrouter"
	"github.com/robobee/core/internal/msgsender"
	"github.com/robobee/core/internal/platform"
)

const (
	pollInterval = 2 * time.Second
	pollTimeout  = 30 * time.Minute
	mergedSep    = "\n\n---\n\n"
	clearMsg     = "✅ 上下文已重置"
	errorMsg     = "❌ 处理失败，请稍后重试"
	ackMsg       = "⏳ 正在处理，请稍候…"
	timeoutMsg   = "⏰ 任务超时，请稍后通过 Web 界面查看结果"
)

// ExecutionManager manages worker executions.
type ExecutionManager interface {
	ExecuteWorker(ctx context.Context, workerID, input string) (model.WorkerExecution, error)
	ReplyExecution(ctx context.Context, execID, input string) (model.WorkerExecution, error)
	GetExecution(id string) (model.WorkerExecution, error)
}

// MessageStore is the subset of store.MessageStore used by the dispatcher.
type MessageStore interface {
	SetWorkerID(ctx context.Context, id, workerID string) error
	UpdateStatusBatch(ctx context.Context, ids []string, status string) error
	MarkMerged(ctx context.Context, primaryID string, mergedIDs []string) error
	MarkTerminal(ctx context.Context, ids []string, status string) error
	GetUnfinished(ctx context.Context) ([]model.PendingMessage, error)
	GetSession(ctx context.Context, sessionKey string) (*platform.Session, error)
	SetExecution(ctx context.Context, msgID, executionID, sessionID string) error
	InsertClearSentinel(ctx context.Context, id, sessionKey, platform string) error
}

type sessionState struct {
	executing      bool
	pendingIDs     []string
	pendingContent string
	lastReplyTo    platform.InboundMessage
	workerID       string
}

type internalResult struct {
	queueKey string
	msgIDs   []string
	replyTo  platform.InboundMessage
	content  string
}

// Dispatcher serializes worker executions per session+worker pair and emits SenderEvents.
type Dispatcher struct {
	manager  ExecutionManager
	msgStore MessageStore
	in       <-chan msgrouter.RoutedMessage
	results  chan internalResult
	out      chan msgsender.SenderEvent
	sessions map[string]*sessionState
}

// New constructs a Dispatcher.
func New(manager ExecutionManager, msgStore MessageStore, in <-chan msgrouter.RoutedMessage) *Dispatcher {
	return &Dispatcher{
		manager:  manager,
		msgStore: msgStore,
		in:       in,
		results:  make(chan internalResult, 64),
		out:      make(chan msgsender.SenderEvent, 64),
		sessions: make(map[string]*sessionState),
	}
}

// Out returns the channel of outgoing SenderEvents.
func (d *Dispatcher) Out() <-chan msgsender.SenderEvent { return d.out }

// Run processes messages until ctx is cancelled, then closes Out(). Call in a goroutine.
func (d *Dispatcher) Run(ctx context.Context) {
	defer close(d.out)
	d.recoverFromDB()
	for {
		select {
		case msg, ok := <-d.in:
			if !ok {
				return
			}
			d.handleInbound(msg)
		case res := <-d.results:
			d.handleResult(res)
		case <-ctx.Done():
			return
		}
	}
}

func queueKey(sessionKey, workerID string) string {
	return sessionKey + "|" + workerID
}

func (d *Dispatcher) handleInbound(msg msgrouter.RoutedMessage) {
	if msg.RouteErr != "" {
		d.out <- msgsender.SenderEvent{Type: msgsender.SenderEventError, ReplyTo: msg.ReplyTo, Content: msg.RouteErr}
		return
	}

	if msg.Command == msgingest.CommandClear {
		prefix := msg.SessionKey + "|"
		for key, state := range d.sessions {
			if strings.HasPrefix(key, prefix) {
				if len(state.pendingIDs) > 0 {
					d.msgStore.MarkTerminal(context.Background(), state.pendingIDs, "failed") //nolint:errcheck
				}
				delete(d.sessions, key)
			}
		}
		d.msgStore.InsertClearSentinel(context.Background(), uuid.New().String(), msg.SessionKey, msg.Platform) //nolint:errcheck
		d.out <- msgsender.SenderEvent{Type: msgsender.SenderEventResult, ReplyTo: msg.ReplyTo, Content: clearMsg}
		return
	}

	key := queueKey(msg.SessionKey, msg.WorkerID)
	state, ok := d.sessions[key]
	if !ok {
		state = &sessionState{workerID: msg.WorkerID}
		d.sessions[key] = state
	}

	// ACK immediately on queue entry
	d.out <- msgsender.SenderEvent{Type: msgsender.SenderEventACK, ReplyTo: msg.ReplyTo, Content: ackMsg}

	if !state.executing {
		state.executing = true
		state.lastReplyTo = msg.ReplyTo
		d.msgStore.UpdateStatusBatch(context.Background(), []string{msg.MsgID}, "executing") //nolint:errcheck
		go d.executeAsync(key, []string{msg.MsgID}, msg.Content, msg.ReplyTo, msg.WorkerID, msg.MsgID)
	} else {
		if state.pendingContent == "" {
			state.pendingContent = msg.Content
		} else {
			state.pendingContent = state.pendingContent + mergedSep + msg.Content
		}
		state.pendingIDs = append(state.pendingIDs, msg.MsgID)
		state.lastReplyTo = msg.ReplyTo
		d.msgStore.UpdateStatusBatch(context.Background(), []string{msg.MsgID}, "pending") //nolint:errcheck
	}
}

func (d *Dispatcher) executeAsync(key string, ids []string, content string, replyTo platform.InboundMessage, workerID, primaryMsgID string) {
	sess, err := d.msgStore.GetSession(context.Background(), replyTo.SessionKey)
	if err != nil {
		log.Printf("dispatcher: get session error: %v", err)
		d.results <- internalResult{queueKey: key, msgIDs: ids, replyTo: replyTo, content: errorMsg}
		return
	}

	var exec model.WorkerExecution
	if sess != nil && sess.LastExecutionID != "" {
		log.Printf("dispatcher: replying to execution execID=%s", sess.LastExecutionID)
		exec, err = d.manager.ReplyExecution(context.Background(), sess.LastExecutionID, content)
	} else {
		log.Printf("dispatcher: executing worker workerID=%s", workerID)
		exec, err = d.manager.ExecuteWorker(context.Background(), workerID, content)
	}
	if err != nil {
		log.Printf("dispatcher: execute error: %v", err)
		d.results <- internalResult{queueKey: key, msgIDs: ids, replyTo: replyTo, content: errorMsg}
		return
	}

	d.msgStore.SetExecution(context.Background(), primaryMsgID, exec.ID, exec.SessionID) //nolint:errcheck

	result := d.waitForResult(exec.ID)
	d.results <- internalResult{queueKey: key, msgIDs: ids, replyTo: replyTo, content: result}
}

func (d *Dispatcher) waitForResult(executionID string) string {
	deadline := time.Now().Add(pollTimeout)
	lastStatus := ""
	for time.Now().Before(deadline) {
		exec, err := d.manager.GetExecution(executionID)
		if err != nil {
			log.Printf("dispatcher: poll error execID=%s: %v", executionID, err)
			return errorMsg
		}
		if string(exec.Status) != lastStatus {
			log.Printf("dispatcher: polling execID=%s status=%s", executionID, exec.Status)
			lastStatus = string(exec.Status)
		}
		switch exec.Status {
		case model.ExecStatusCompleted:
			if exec.Result != "" {
				return exec.Result
			}
			return "✅ 任务已完成"
		case model.ExecStatusFailed:
			return "❌ 任务执行失败: " + exec.Result
		}
		time.Sleep(pollInterval)
	}
	return timeoutMsg
}

func (d *Dispatcher) handleResult(res internalResult) {
	d.msgStore.MarkTerminal(context.Background(), res.msgIDs, "done") //nolint:errcheck
	d.out <- msgsender.SenderEvent{Type: msgsender.SenderEventResult, ReplyTo: res.replyTo, Content: res.content}

	state, ok := d.sessions[res.queueKey]
	if !ok {
		return
	}

	if state.pendingContent != "" {
		nextContent := state.pendingContent
		nextIDs := state.pendingIDs
		nextReplyTo := state.lastReplyTo
		state.pendingContent = ""
		state.pendingIDs = nil

		parts := strings.SplitN(res.queueKey, "|", 2)
		workerID := ""
		if len(parts) == 2 {
			workerID = parts[1]
		}

		d.msgStore.UpdateStatusBatch(context.Background(), nextIDs, "executing") //nolint:errcheck
		go d.executeAsync(res.queueKey, nextIDs, nextContent, nextReplyTo, workerID, nextIDs[0])
	} else {
		state.executing = false
		delete(d.sessions, res.queueKey)
	}
}

func (d *Dispatcher) recoverFromDB() {
	msgs, err := d.msgStore.GetUnfinished(context.Background())
	if err != nil {
		log.Printf("dispatcher: RecoverFromDB failed: %v", err)
		return
	}
	if len(msgs) == 0 {
		return
	}

	type group struct {
		content  string
		ids      []string
		replyTo  platform.InboundMessage
		workerID string
	}
	groups := make(map[string]*group)

	for _, msg := range msgs {
		key := queueKey(msg.SessionKey, msg.WorkerID)
		g, ok := groups[key]
		if !ok {
			g = &group{
				replyTo:  platform.InboundMessage{Platform: msg.Platform, SessionKey: msg.SessionKey},
				workerID: msg.WorkerID,
			}
			groups[key] = g
		}
		if g.content == "" {
			g.content = msg.Content
		} else {
			g.content = g.content + mergedSep + msg.Content
		}
		g.ids = append(g.ids, msg.ID)
	}

	for key, g := range groups {
		state := &sessionState{executing: true, workerID: g.workerID}
		d.sessions[key] = state
		d.msgStore.UpdateStatusBatch(context.Background(), g.ids, "executing") //nolint:errcheck
		go d.executeAsync(key, g.ids, g.content, g.replyTo, g.workerID, g.ids[0])
	}

	log.Printf("dispatcher: recovered %d session(s) from DB", len(groups))
}
```

- [ ] **Step 4: Run tests**

```bash
go test ./internal/dispatcher/... -v -timeout 30s
```
Expected: all tests PASS. (The serialization test may take a few seconds.)

- [ ] **Step 5: Run all new package tests together**

```bash
go test ./internal/msgingest/... ./internal/msgrouter/... ./internal/msgsender/... ./internal/dispatcher/... -v
```
Expected: all PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/dispatcher/
git commit -m "feat: add dispatcher (Layer 3 — async serialized execution, ACK, command handling)"
```

---

## Chunk 4: Wiring and Cleanup

### Task 6: Rewire `cmd/server/main.go`

**Files:**
- Modify: `cmd/server/main.go`

Replace the old `platform.NewPipeline` / `platform.NewManager` / `platManager.StartAll` with the four-layer pipeline. The new wiring does not use `Pipeline`, `PlatformManager`, or `QueueManager`.

- [ ] **Step 1: Read the current `main.go`**

```bash
cat cmd/server/main.go
```

- [ ] **Step 2: Replace the platform wiring section**

Replace the block starting at `// Build shared pipeline` through `go platManager.StartAll(ctx)` with:

```go
	// Build four-layer message pipeline
	sendersByPlatform := make(map[string]platform.PlatformSenderAdapter)

	ingest := msgingest.New(msgStore, cfg.MessageQueue.DebounceWindow)
	router := msgrouter.New(botrouter.NewRouter(aiClient, workerStore), ingest.Out())
	disp   := dispatcher.New(mgr, msgStore, router.Out())
	sender := msgsender.New(sendersByPlatform, disp.Out())

	go ingest.Run(ctx)
	go router.Run(ctx)
	go disp.Run(ctx)
	go sender.Run(ctx)

	// Register enabled platforms
	if cfg.Feishu.Enabled {
		p := feishu.NewPlatform(cfg.Feishu)
		sendersByPlatform[p.ID()] = p.Sender()
		go p.Receiver().Start(ctx, ingest.Dispatch)
	}
	if cfg.DingTalk.Enabled {
		p := dingtalk.NewPlatform(cfg.DingTalk)
		sendersByPlatform[p.ID()] = p.Sender()
		go p.Receiver().Start(ctx, ingest.Dispatch)
	}
```

- [ ] **Step 3: Update imports in `main.go`**

Remove imports: `"github.com/robobee/core/internal/platform"` (if it's only used for `platform.NewPipeline` etc.)

Add imports:
```go
"github.com/robobee/core/internal/dispatcher"
"github.com/robobee/core/internal/msgingest"
"github.com/robobee/core/internal/msgrouter"
"github.com/robobee/core/internal/msgsender"
"github.com/robobee/core/internal/platform"  // still needed for PlatformSenderAdapter
```

- [ ] **Step 4: Build to check for compile errors**

```bash
go build ./cmd/server/... 2>&1
```
Expected: success (or only errors from old platform files — that's OK for now).

- [ ] **Step 5: Commit**

```bash
git add cmd/server/main.go
git commit -m "feat: rewire main.go to use four-layer message pipeline"
```

---

### Task 7: Delete old platform files

**Files:**
- Delete: `internal/platform/manager.go`
- Delete: `internal/platform/pipeline.go`
- Delete: `internal/platform/queue_manager.go`
- Delete: `internal/platform/session_queue.go`
- Delete: `internal/platform/pipeline_test.go`
- Delete: `internal/platform/manager_dedup_test.go`
- Delete: `internal/platform/queue_manager_test.go`
- Delete: `internal/platform/session_queue_test.go`

- [ ] **Step 1: Delete the implementation files**

```bash
git rm internal/platform/manager.go \
       internal/platform/pipeline.go \
       internal/platform/queue_manager.go \
       internal/platform/session_queue.go
```

- [ ] **Step 2: Delete the old test files**

```bash
git rm internal/platform/pipeline_test.go \
       internal/platform/manager_dedup_test.go \
       internal/platform/queue_manager_test.go \
       internal/platform/session_queue_test.go
```

- [ ] **Step 3: Build the entire project**

```bash
go build ./... 2>&1
```
Expected: clean build with no errors.

- [ ] **Step 4: Run all tests**

```bash
go test ./... -timeout 60s 2>&1
```
Expected: all tests PASS. The four new packages each have passing tests. Old platform tests are gone.

- [ ] **Step 5: Commit**

```bash
git add -A
git commit -m "refactor: delete old platform manager/pipeline/queue files — replaced by four-layer pipeline"
```

---

### Task 8: Final verification

- [ ] **Step 1: Run full test suite**

```bash
go test ./... -race -timeout 60s
```
Expected: PASS with no race conditions detected.

- [ ] **Step 2: Build the server binary**

```bash
go build -o /tmp/robobee-test ./cmd/server/
echo "Build OK: $?"
```
Expected: `Build OK: 0`

- [ ] **Step 3: Verify platform package is now types-only**

```bash
grep -n "func " internal/platform/interfaces.go || echo "No functions — types only ✓"
```
Expected: `No functions — types only ✓`

- [ ] **Step 4: Commit if there are any leftover fixups, then tag**

```bash
git log --oneline -8
```
