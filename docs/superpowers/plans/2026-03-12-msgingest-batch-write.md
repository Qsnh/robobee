# msgingest: Batch Write After Debounce — Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace per-message DB writes in `msgingest.Gateway` with a single batch INSERT that fires after the debounce timer, and remove the historical-message fast-path.

**Architecture:** Add `CreateBatch(ctx, []BatchMsg) (int64, error)` to `store.MessageStore`; replace the `msgingest.MessageStore` interface to expose only `CreateBatch` (using `store.BatchMsg`); rewrite `Gateway` to accumulate full `platform.InboundMessage` values during debounce instead of IDs, then write all of them atomically after the timer fires.

**Tech Stack:** Go, SQLite (`database/sql`), `github.com/google/uuid`

**Spec:** `docs/superpowers/specs/2026-03-12-msgingest-batch-write-design.md`

**Type ownership note:** `BatchMsg` is defined in the `store` package (the DB layer owns it). The `msgingest.MessageStore` interface uses `store.BatchMsg`, which means `msgingest/gateway.go` imports `internal/store`. This adds one new import but introduces no cycle.

---

## Chunk 1: Add `CreateBatch` to `store.MessageStore`

### Task 1: Write a failing test for `CreateBatch`

**Files:**
- Modify: `internal/store/message_store_test.go`

The test file is `package store` (same package as the implementation), so it can access `s.db` directly for raw queries. Use the existing `setupMessageStore(t)` helper.

- [ ] **Step 1: Add the tests at the end of `internal/store/message_store_test.go`**

```go
func TestMessageStore_CreateBatch(t *testing.T) {
	s := setupMessageStore(t)
	ctx := context.Background()

	now := time.Now().UnixMilli()
	primaryID := "primary-1"
	mergedID := "merged-1"

	msgs := []BatchMsg{
		{
			ID: mergedID, SessionKey: "s1", Platform: "test",
			Content: "first", Raw: "", PlatformMsgID: "pmsg-1",
			MessageTime: now, Status: "merged", MergedInto: primaryID,
		},
		{
			ID: primaryID, SessionKey: "s1", Platform: "test",
			Content: "first\n\n---\n\nsecond", Raw: "", PlatformMsgID: "pmsg-2",
			MessageTime: now, Status: "received", MergedInto: "",
		},
	}

	inserted, err := s.CreateBatch(ctx, msgs)
	if err != nil {
		t.Fatalf("CreateBatch error: %v", err)
	}
	if inserted != 2 {
		t.Fatalf("expected 2 rows inserted, got %d", inserted)
	}

	// Verify merged row
	var status, mergedInto string
	if err := s.db.QueryRowContext(ctx,
		`SELECT status, merged_into FROM platform_messages WHERE id = ?`, mergedID,
	).Scan(&status, &mergedInto); err != nil {
		t.Fatalf("scan merged row: %v", err)
	}
	if status != "merged" {
		t.Errorf("merged row: want status=merged, got %q", status)
	}
	if mergedInto != primaryID {
		t.Errorf("merged row: want merged_into=%q, got %q", primaryID, mergedInto)
	}

	// Verify primary row
	if err := s.db.QueryRowContext(ctx,
		`SELECT status, merged_into FROM platform_messages WHERE id = ?`, primaryID,
	).Scan(&status, &mergedInto); err != nil {
		t.Fatalf("scan primary row: %v", err)
	}
	if status != "received" {
		t.Errorf("primary row: want status=received, got %q", status)
	}
	if mergedInto != "" {
		t.Errorf("primary row: want merged_into empty, got %q", mergedInto)
	}
}

func TestMessageStore_CreateBatch_DuplicateIgnored(t *testing.T) {
	s := setupMessageStore(t)
	ctx := context.Background()

	msg := BatchMsg{
		ID: "id-1", SessionKey: "s1", Platform: "test",
		Content: "hello", Raw: "", PlatformMsgID: "pmsg-dup",
		MessageTime: time.Now().UnixMilli(), Status: "received", MergedInto: "",
	}

	// First insert: should succeed
	inserted, err := s.CreateBatch(ctx, []BatchMsg{msg})
	if err != nil {
		t.Fatalf("first CreateBatch error: %v", err)
	}
	if inserted != 1 {
		t.Fatalf("expected 1 row inserted, got %d", inserted)
	}

	// Second insert with same platform_msg_id: INSERT OR IGNORE should skip it
	msg.ID = "id-2" // different row ID but same platform_msg_id
	inserted, err = s.CreateBatch(ctx, []BatchMsg{msg})
	if err != nil {
		t.Fatalf("second CreateBatch error: %v", err)
	}
	if inserted != 0 {
		t.Fatalf("expected 0 rows inserted (duplicate ignored), got %d", inserted)
	}
}

func TestMessageStore_CreateBatch_Empty(t *testing.T) {
	s := setupMessageStore(t)
	ctx := context.Background()

	inserted, err := s.CreateBatch(ctx, nil)
	if err != nil {
		t.Fatalf("CreateBatch(nil) error: %v", err)
	}
	if inserted != 0 {
		t.Fatalf("expected 0 rows inserted for empty batch, got %d", inserted)
	}
}
```

- [ ] **Step 2: Verify it does NOT compile yet**

```bash
cd /Users/tengteng/work/robobee/core && go test ./internal/store/... 2>&1 | head -5
```

Expected: compile error: `BatchMsg` undefined.

---

### Task 2: Add `BatchMsg` type and `CreateBatch` to `store.MessageStore`

**Files:**
- Modify: `internal/store/message_store.go`

- [ ] **Step 1: Add `BatchMsg` struct before `type MessageStore struct`**

In `internal/store/message_store.go`, add this block immediately before `type MessageStore struct`:

```go
// BatchMsg is a single row for a bulk insert via CreateBatch.
type BatchMsg struct {
	ID            string
	SessionKey    string
	Platform      string
	Content       string
	Raw           string
	PlatformMsgID string
	MessageTime   int64
	Status        string // "received" or "merged"
	MergedInto    string // non-empty only when Status == "merged"
}
```

- [ ] **Step 2: Add `CreateBatch` method after `InsertClearSentinel`**

```go
// CreateBatch inserts multiple message rows in a single transaction using
// INSERT OR IGNORE. Returns the number of rows actually inserted.
// MessageTime is used as received_at; falls back to time.Now().UnixMilli() if zero.
func (s *MessageStore) CreateBatch(ctx context.Context, msgs []BatchMsg) (int64, error) {
	if len(msgs) == 0 {
		return 0, nil
	}

	now := time.Now().UnixMilli()
	placeholders := strings.Repeat("(?,?,?,?,?,?,?,?,?),", len(msgs))
	placeholders = placeholders[:len(placeholders)-1]

	args := make([]any, 0, len(msgs)*9)
	for _, m := range msgs {
		mt := m.MessageTime
		if mt == 0 {
			mt = now
		}
		args = append(args, m.ID, m.SessionKey, m.Platform, m.Content, m.Raw,
			m.PlatformMsgID, mt, m.Status, m.MergedInto)
	}

	result, err := s.db.ExecContext(ctx,
		fmt.Sprintf(`INSERT OR IGNORE INTO platform_messages
			(id, session_key, platform, content, raw, platform_msg_id, received_at, status, merged_into)
			VALUES %s`, placeholders),
		args...,
	)
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}
```

- [ ] **Step 3: Run the new store tests**

```bash
cd /Users/tengteng/work/robobee/core && go test ./internal/store/... -run 'TestMessageStore_CreateBatch' -v
```

Expected: all three `CreateBatch` tests PASS.

- [ ] **Step 4: Run the full store test suite**

```bash
cd /Users/tengteng/work/robobee/core && go test ./internal/store/... -v
```

Expected: all tests PASS.

- [ ] **Step 5: Commit**

```bash
cd /Users/tengteng/work/robobee/core
git add internal/store/message_store.go internal/store/message_store_test.go
git commit -m "feat(store): add BatchMsg type and CreateBatch for bulk message insert"
```

---

## Chunk 2: Rewrite `msgingest` package

### Task 3: Update `gateway_test.go` to the new interface

**Files:**
- Modify: `internal/msgingest/gateway_test.go`

This task rewrites the test file. The new mock uses `store.BatchMsg`. The package won't compile until Task 4 (gateway.go) is also updated — that's expected.

- [ ] **Step 1: Replace `gateway_test.go` entirely**

Replace the full contents of `internal/msgingest/gateway_test.go` with:

```go
package msgingest_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/robobee/core/internal/msgingest"
	"github.com/robobee/core/internal/platform"
	"github.com/robobee/core/internal/store"
)

// mockMsgStore implements msgingest.MessageStore for testing.
type mockMsgStore struct {
	batches       [][]store.BatchMsg // one entry per CreateBatch call
	rowsToReturn  int64              // rows returned when useCustomRows is true
	errToReturn   error
	useCustomRows bool
}

func newMock() *mockMsgStore { return &mockMsgStore{} }

func (m *mockMsgStore) withPartialInsert(rows int64) *mockMsgStore {
	m.useCustomRows = true
	m.rowsToReturn = rows
	return m
}

func (m *mockMsgStore) withError(err error) *mockMsgStore {
	m.errToReturn = err
	return m
}

func (m *mockMsgStore) CreateBatch(_ context.Context, msgs []store.BatchMsg) (int64, error) {
	if m.errToReturn != nil {
		return 0, m.errToReturn
	}
	cp := make([]store.BatchMsg, len(msgs))
	copy(cp, msgs)
	m.batches = append(m.batches, cp)
	if m.useCustomRows {
		return m.rowsToReturn, nil
	}
	return int64(len(msgs)), nil
}

func inbound(sessionKey, content, platformMsgID string) platform.InboundMessage {
	return platform.InboundMessage{
		Platform:          "test",
		SessionKey:        sessionKey,
		Content:           content,
		PlatformMessageID: platformMsgID,
	}
}

// TestGateway_Dedup_InMemory verifies that two dispatches with the same
// platform_msg_id in one debounce window result in exactly one row written.
func TestGateway_Dedup_InMemory(t *testing.T) {
	st := newMock()
	g := msgingest.New(st, 150*time.Millisecond)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go g.Run(ctx)

	g.Dispatch(inbound("s1", "hello", "pmsg-1"))
	g.Dispatch(inbound("s1", "world", "pmsg-1")) // same platform_msg_id → dropped

	select {
	case <-g.Out():
	case <-time.After(500 * time.Millisecond):
		t.Fatal("timeout waiting for debounced message")
	}

	if len(st.batches) != 1 {
		t.Fatalf("expected 1 CreateBatch call, got %d", len(st.batches))
	}
	if len(st.batches[0]) != 1 {
		t.Fatalf("expected 1 row in batch (duplicate dropped), got %d", len(st.batches[0]))
	}
}

// TestGateway_Debounce_EmitsSingleMergedMessage verifies that two messages in
// one debounce window are merged into one IngestedMessage with combined content.
func TestGateway_Debounce_EmitsSingleMergedMessage(t *testing.T) {
	st := newMock()
	g := msgingest.New(st, 100*time.Millisecond)
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

	// Only one message
	select {
	case extra := <-g.Out():
		t.Fatalf("expected only one message, got extra: %+v", extra)
	case <-time.After(200 * time.Millisecond):
	}
}

// TestGateway_Debounce_BatchWrite verifies the exact structure of the
// CreateBatch call: 2 merged rows + 1 received row, correct MergedInto.
func TestGateway_Debounce_BatchWrite(t *testing.T) {
	st := newMock()
	g := msgingest.New(st, 100*time.Millisecond)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go g.Run(ctx)

	g.Dispatch(inbound("s1", "msg1", "m1"))
	g.Dispatch(inbound("s1", "msg2", "m2"))
	g.Dispatch(inbound("s1", "msg3", "m3"))

	var emitted msgingest.IngestedMessage
	select {
	case emitted = <-g.Out():
	case <-time.After(500 * time.Millisecond):
		t.Fatal("timeout waiting for debounced message")
	}

	if len(st.batches) != 1 {
		t.Fatalf("expected 1 CreateBatch call, got %d", len(st.batches))
	}
	batch := st.batches[0]
	if len(batch) != 3 {
		t.Fatalf("expected 3 rows in batch, got %d", len(batch))
	}

	// Last row is the primary (received)
	primary := batch[2]
	if primary.Status != "received" {
		t.Errorf("primary row: want status=received, got %q", primary.Status)
	}
	if primary.MergedInto != "" {
		t.Errorf("primary row: want merged_into empty, got %q", primary.MergedInto)
	}
	if primary.ID != emitted.MsgID {
		t.Errorf("primary ID %q != emitted MsgID %q", primary.ID, emitted.MsgID)
	}

	// First two rows are merged
	for i, row := range batch[:2] {
		if row.Status != "merged" {
			t.Errorf("row[%d]: want status=merged, got %q", i, row.Status)
		}
		if row.MergedInto != primary.ID {
			t.Errorf("row[%d]: want merged_into=%q, got %q", i, primary.ID, row.MergedInto)
		}
	}
}

// TestGateway_Debounce_SingleMessage verifies N=1: CreateBatch called with
// exactly one received row and no merged rows.
func TestGateway_Debounce_SingleMessage(t *testing.T) {
	st := newMock()
	g := msgingest.New(st, 100*time.Millisecond)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go g.Run(ctx)

	g.Dispatch(inbound("s1", "only", "m1"))

	select {
	case <-g.Out():
	case <-time.After(500 * time.Millisecond):
		t.Fatal("timeout")
	}

	if len(st.batches) != 1 {
		t.Fatalf("expected 1 CreateBatch call, got %d", len(st.batches))
	}
	if len(st.batches[0]) != 1 {
		t.Fatalf("expected 1 row, got %d", len(st.batches[0]))
	}
	if st.batches[0][0].Status != "received" {
		t.Errorf("want status=received, got %q", st.batches[0][0].Status)
	}
}

// TestGateway_BatchWrite_Error_NormalPath verifies that a CreateBatch error
// during debounce suppresses the emit.
func TestGateway_BatchWrite_Error_NormalPath(t *testing.T) {
	st := newMock().withError(errors.New("db down"))
	g := msgingest.New(st, 100*time.Millisecond)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go g.Run(ctx)

	g.Dispatch(inbound("s1", "hello", "m1"))

	select {
	case msg := <-g.Out():
		t.Fatalf("expected no emit on CreateBatch error, got: %+v", msg)
	case <-time.After(400 * time.Millisecond):
		// expected: nothing emitted
	}
}

// TestGateway_BatchWrite_Error_CommandPath verifies that a CreateBatch error
// during command handling suppresses the emit.
func TestGateway_BatchWrite_Error_CommandPath(t *testing.T) {
	st := newMock().withError(errors.New("db down"))
	g := msgingest.New(st, 100*time.Millisecond)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go g.Run(ctx)

	g.Dispatch(inbound("s1", "clear", "cmd-1"))

	select {
	case msg := <-g.Out():
		t.Fatalf("expected no emit on CreateBatch error, got: %+v", msg)
	case <-time.After(300 * time.Millisecond):
		// expected
	}
}

// TestGateway_BatchWrite_PartialInsert verifies that rowsInserted < N suppresses
// the emit (covers the case where the primary is ignored but merged rows succeed).
func TestGateway_BatchWrite_PartialInsert(t *testing.T) {
	// 3 messages dispatched → batch of 3; mock returns only 2 inserted
	st := newMock().withPartialInsert(2)
	g := msgingest.New(st, 100*time.Millisecond)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go g.Run(ctx)

	g.Dispatch(inbound("s1", "msg1", "m1"))
	g.Dispatch(inbound("s1", "msg2", "m2"))
	g.Dispatch(inbound("s1", "msg3", "m3"))

	select {
	case msg := <-g.Out():
		t.Fatalf("expected no emit on partial insert, got: %+v", msg)
	case <-time.After(400 * time.Millisecond):
		// expected
	}
}

// TestGateway_Command_EmitsOnOut verifies that a command message appears on Out()
// with the correct Command field.
func TestGateway_Command_EmitsOnOut(t *testing.T) {
	st := newMock()
	g := msgingest.New(st, 100*time.Millisecond)
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

// TestGateway_Command_InterruptsDebounce verifies that a clear command arriving
// during a debounce window cancels the window, discards normal messages without
// writing them to DB, and writes only the command message via CreateBatch.
func TestGateway_Command_InterruptsDebounce(t *testing.T) {
	st := newMock()
	g := msgingest.New(st, 200*time.Millisecond)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go g.Run(ctx)

	g.Dispatch(inbound("s1", "hello", "m1")) // goes into debounce
	g.Dispatch(inbound("s1", "clear", "cmd-1")) // command: interrupts

	select {
	case msg := <-g.Out():
		if msg.Command != msgingest.CommandClear {
			t.Fatalf("expected CommandClear, got %q", msg.Command)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("timeout waiting for command message")
	}

	// The debounced normal message must NOT appear
	select {
	case extra := <-g.Out():
		t.Fatalf("debounced message should have been cancelled, got: %+v", extra)
	case <-time.After(400 * time.Millisecond):
	}

	// Exactly one CreateBatch call (for the command), zero calls for normal messages
	if len(st.batches) != 1 {
		t.Fatalf("expected exactly 1 CreateBatch call (command only), got %d", len(st.batches))
	}
	if st.batches[0][0].Status != "received" {
		t.Errorf("command row: want status=received, got %q", st.batches[0][0].Status)
	}
}
```

- [ ] **Step 2: Verify it does NOT compile yet (expected — gateway.go not updated)**

```bash
cd /Users/tengteng/work/robobee/core && go build ./internal/msgingest/... 2>&1 | head -10
```

Expected: compile errors.

---

### Task 4: Rewrite `gateway.go`

**Files:**
- Modify: `internal/msgingest/gateway.go`

- [ ] **Step 1: Replace `gateway.go` entirely**

Replace the full contents of `internal/msgingest/gateway.go` with:

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
	"github.com/robobee/core/internal/store"
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
	CreateBatch(ctx context.Context, msgs []store.BatchMsg) (int64, error)
}

type debounceState struct {
	timer      *time.Timer
	generation int
	msgs       []platform.InboundMessage // full message bodies, arrival order
	content    string                    // merged content string
}

// Gateway receives raw platform messages, deduplicates, debounces, and emits IngestedMessages.
type Gateway struct {
	msgStore MessageStore
	debounce time.Duration
	sessions map[string]*debounceState
	seen     map[string]struct{} // in-memory dedup set keyed by platform_msg_id
	mu       sync.Mutex
	out      chan IngestedMessage
}

// New constructs a Gateway.
func New(msgStore MessageStore, debounce time.Duration) *Gateway {
	return &Gateway{
		msgStore: msgStore,
		debounce: debounce,
		sessions: make(map[string]*debounceState),
		seen:     make(map[string]struct{}),
		out:      make(chan IngestedMessage, 64),
	}
}

// Out returns the channel of outgoing IngestedMessages.
func (g *Gateway) Out() <-chan IngestedMessage { return g.out }

// Run blocks until ctx is cancelled, then closes Out().
func (g *Gateway) Run(ctx context.Context) {
	<-ctx.Done()
	close(g.out)
}

// emit sends msg to the output channel non-blocking; drops and logs if the channel is full.
func (g *Gateway) emit(msg IngestedMessage) {
	select {
	case g.out <- msg:
	default:
		log.Printf("msgingest: output channel full, dropping message sessionKey=%s", msg.SessionKey)
	}
}

// Dispatch is called by a platform receiver for each inbound message.
// All seen-map and debounce-state mutations are protected by g.mu.
func (g *Gateway) Dispatch(msg platform.InboundMessage) {
	g.mu.Lock()

	// In-memory dedup: drop if platform_msg_id already seen this process lifetime.
	if msg.PlatformMessageID != "" {
		if _, dup := g.seen[msg.PlatformMessageID]; dup {
			g.mu.Unlock()
			log.Printf("msgingest: duplicate dropped platformMsgID=%s", msg.PlatformMessageID)
			return
		}
		g.seen[msg.PlatformMessageID] = struct{}{}
	}

	// Command detection: release lock, then handle command.
	if cmd := detectCommand(msg.Content); cmd != CommandNone {
		g.mu.Unlock()
		g.handleCommand(msg, cmd)
		return
	}

	// Accumulate into debounce state.
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
	state.msgs = append(state.msgs, msg)

	if state.timer != nil {
		state.timer.Stop()
	}
	state.generation++
	gen := state.generation
	sessionKey := msg.SessionKey
	state.timer = time.AfterFunc(g.debounce, func() { g.onDebounce(sessionKey, gen) })

	g.mu.Unlock()
}

// handleCommand cancels any active debounce for the session (discarding pending
// normal messages without writing them to DB), writes the command message to DB,
// then emits the command IngestedMessage.
//
// Known accepted race: between Dispatch releasing the lock and this function
// reacquiring it, another concurrent Dispatch for the same session could
// accumulate a new message. That message is silently discarded here. This race
// exists in the original codebase and is accepted — a simultaneous clear and
// normal message is an edge case with no meaningful expected outcome.
func (g *Gateway) handleCommand(msg platform.InboundMessage, cmd CommandType) {
	g.mu.Lock()
	if state, ok := g.sessions[msg.SessionKey]; ok {
		if state.timer != nil {
			state.timer.Stop()
		}
		delete(g.sessions, msg.SessionKey)
	}
	g.mu.Unlock()

	mt := msg.MessageTime
	if mt == 0 {
		mt = time.Now().UnixMilli()
	}
	batch := []store.BatchMsg{{
		ID:            uuid.New().String(),
		SessionKey:    msg.SessionKey,
		Platform:      msg.Platform,
		Content:       msg.Content,
		Raw:           msg.RawContent,
		PlatformMsgID: msg.PlatformMessageID,
		MessageTime:   mt,
		Status:        "received",
		MergedInto:    "",
	}}
	cmdID := batch[0].ID

	if _, err := g.msgStore.CreateBatch(context.Background(), batch); err != nil {
		log.Printf("msgingest: CreateBatch error for command sessionKey=%s: %v", msg.SessionKey, err)
		return
	}

	g.emit(IngestedMessage{
		MsgID:      cmdID,
		SessionKey: msg.SessionKey,
		Platform:   msg.Platform,
		Content:    msg.Content,
		ReplyTo:    msg,
		Command:    cmd,
	})
}

func (g *Gateway) onDebounce(sessionKey string, generation int) {
	g.mu.Lock()
	state, ok := g.sessions[sessionKey]
	if !ok || len(state.msgs) == 0 {
		g.mu.Unlock()
		return
	}
	if state.generation != generation {
		g.mu.Unlock()
		return
	}
	msgs := state.msgs
	content := state.content
	delete(g.sessions, sessionKey)
	g.mu.Unlock()

	n := len(msgs)
	ids := make([]string, n)
	for i := range msgs {
		ids[i] = uuid.New().String()
	}
	primaryID := ids[n-1]

	batch := make([]store.BatchMsg, n)
	for i, m := range msgs {
		mt := m.MessageTime
		if mt == 0 {
			mt = time.Now().UnixMilli()
		}
		bm := store.BatchMsg{
			ID:            ids[i],
			SessionKey:    m.SessionKey,
			Platform:      m.Platform,
			Content:       m.Content,
			Raw:           m.RawContent,
			PlatformMsgID: m.PlatformMessageID,
			MessageTime:   mt,
			MergedInto:    "",
		}
		if i < n-1 {
			bm.Status = "merged"
			bm.MergedInto = primaryID
		} else {
			bm.Status = "received"
		}
		batch[i] = bm
	}

	inserted, err := g.msgStore.CreateBatch(context.Background(), batch)
	if err != nil {
		log.Printf("msgingest: CreateBatch error sessionKey=%s: %v", sessionKey, err)
		return
	}
	if inserted != int64(n) {
		log.Printf("msgingest: CreateBatch partial insert sessionKey=%s: expected %d got %d, suppressing emit",
			sessionKey, n, inserted)
		return
	}

	g.emit(IngestedMessage{
		MsgID:      primaryID,
		SessionKey: sessionKey,
		Platform:   msgs[n-1].Platform,
		Content:    content,
		ReplyTo:    msgs[n-1],
		Command:    CommandNone,
	})
}

func detectCommand(content string) CommandType {
	switch strings.ToLower(strings.TrimSpace(content)) {
	case "clear":
		return CommandClear
	}
	return CommandNone
}
```

- [ ] **Step 2: Verify compilation**

```bash
cd /Users/tengteng/work/robobee/core && go build ./internal/msgingest/... 2>&1
```

Expected: no errors.

- [ ] **Step 3: Run the msgingest tests**

```bash
cd /Users/tengteng/work/robobee/core && go test ./internal/msgingest/... -v -timeout 30s
```

Expected: all tests PASS.

- [ ] **Step 4: Run the full test suite**

```bash
cd /Users/tengteng/work/robobee/core && go test ./... -timeout 60s
```

Expected: all tests PASS. If any other package that previously used `msgingest.MessageStore` (specifically `Create`, `UpdateStatusBatch`, `MarkMerged`, or `MarkTerminal` methods) fails to compile, update those call sites to use the new interface. The wiring code that passes `*store.MessageStore` to `msgingest.New()` will compile cleanly because `*store.MessageStore` now implements `msgingest.MessageStore` (both have `CreateBatch(ctx, []store.BatchMsg) (int64, error)`).

- [ ] **Step 5: Commit**

```bash
cd /Users/tengteng/work/robobee/core
git add internal/msgingest/gateway.go internal/msgingest/gateway_test.go
git commit -m "feat(msgingest): batch write after debounce, remove historical msg path"
```

---

## Chunk 3: DB Migration and Cleanup

### Task 5: Migrate stale `debouncing` rows and clean up `GetUnfinished`

**Files:**
- Modify: `internal/store/db.go` (add migration step)
- Modify: `internal/store/message_store.go` (update `GetUnfinished`)

- [ ] **Step 1: Find the current highest migration version in `db.go`**

Read `internal/store/db.go` and find the last entry in the migrations list. Note its version number — the new migration will be that number + 1.

- [ ] **Step 2: Add the new migration**

Locate the migrations slice in `db.go`. The current highest version is `9`. Append this entry after it (before the closing `}`):

```go
{
    version: 10,
    name:    "20260312000001_migrate_debouncing_to_failed",
    sql:     `UPDATE platform_messages SET status = 'failed' WHERE status = 'debouncing'`,
},
```

- [ ] **Step 3: Update `GetUnfinished` in `message_store.go`**

Find this line in `GetUnfinished`:

```go
WHERE status IN ('routed', 'debouncing', 'merged', 'executing')
```

Change it to:

```go
WHERE status IN ('routed', 'executing')
```

`debouncing` rows are now migrated to `failed` before the new code runs. `merged` rows always have `worker_id=''` and are excluded by the existing `AND worker_id != ''` clause; removing them here makes intent explicit.

- [ ] **Step 4: Run all tests**

```bash
cd /Users/tengteng/work/robobee/core && go test ./... -timeout 60s
```

Expected: all tests PASS. The existing `TestMessageStore_GetUnfinished` test does not insert any `debouncing` or `merged` rows with a `worker_id`, so it should pass unchanged.

- [ ] **Step 5: Commit**

```bash
cd /Users/tengteng/work/robobee/core
git add internal/store/db.go internal/store/message_store.go
git commit -m "feat(store): migrate debouncing rows, clean up GetUnfinished filter"
```

---

### Task 6: Final verification

- [ ] **Step 1: Build the entire module**

```bash
cd /Users/tengteng/work/robobee/core && go build ./... 2>&1
```

Expected: no errors.

- [ ] **Step 2: Run all tests with race detector**

```bash
cd /Users/tengteng/work/robobee/core && go test -race ./... -timeout 60s
```

Expected: all tests PASS with no race conditions detected. If a race is reported in `msgingest`, check that all accesses to `g.seen` and `g.sessions` happen under `g.mu`.

- [ ] **Step 3: Commit if there are any fixes from the race detector**

```bash
cd /Users/tengteng/work/robobee/core
git add -p
git commit -m "fix(msgingest): address race conditions found by race detector"
```

Skip this step if no fixes were needed.
