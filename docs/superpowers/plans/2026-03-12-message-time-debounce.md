# Platform Message Time & Debounce Fix Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Store `received_at` from the platform's native message timestamp and skip debounce for historical messages (age > debounce window) so restart recovery emits old messages immediately.

**Architecture:** Add `MessageTime int64` to `platform.InboundMessage`; each platform adapter fills it from the native timestamp field. `MessageStore.Create()` accepts this value for `received_at`. `gateway.Dispatch()` checks message age before entering the debounce state machine — messages older than the debounce window are emitted immediately without merging.

**Tech Stack:** Go, SQLite (via `database/sql`), Feishu Lark SDK (`larksuite/oapi-sdk-go`), DingTalk stream SDK (`open-dingtalk/dingtalk-stream-sdk-go`).

**Spec:** `docs/superpowers/specs/2026-03-12-message-time-debounce-design.md`

---

## File Map

| File | Change |
|------|--------|
| `internal/platform/interfaces.go` | Add `MessageTime int64` field to `InboundMessage` |
| `internal/store/message_store.go` | `Create()` gains `messageTime int64` parameter |
| `internal/store/message_store_test.go` | Update all `Create()` call sites; add two `received_at` tests |
| `internal/msgingest/gateway.go` | Update `MessageStore` interface; pass `msg.MessageTime`; add historical check |
| `internal/msgingest/gateway_test.go` | Update `mockMsgStore`; add historical/real-time debounce tests |
| `internal/platform/feishu/handler.go` | Parse `msg.CreateTime` → `MessageTime`; add `parseMillis` helper |
| `internal/platform/dingtalk/handler.go` | Set `MessageTime = data.CreateAt` |

---

## Chunk 1: Store & Interface Layer

### Task 1: Add `MessageTime` to `InboundMessage`

**Files:**
- Modify: `internal/platform/interfaces.go`

- [ ] **Step 1: Add the field**

In `internal/platform/interfaces.go`, add `MessageTime int64` as the last field of `InboundMessage`:

```go
// InboundMessage carries a parsed message from any platform.
type InboundMessage struct {
	Platform          string // "feishu" | "dingtalk"
	SenderID          string
	SessionKey        string // platform-prefixed session key, e.g. "feishu:chatID:userID"
	Content           string
	RawContent        string // original message text with formatting preserved (at-tags, markup)
	Raw               any    // original platform event, used by the sender for reply metadata
	PlatformMessageID string // platform-native dedup ID; empty string means no dedup
	MessageTime       int64  // Unix milliseconds from platform; 0 = unknown (fallback to server time)
}
```

- [ ] **Step 2: Verify it compiles**

```bash
go build ./internal/platform/...
```

Expected: no errors.

- [ ] **Step 3: Commit**

```bash
git add internal/platform/interfaces.go
git commit -m "feat: add MessageTime field to platform.InboundMessage"
```

---

### Task 2: `MessageStore.Create()` accepts external timestamp

**Files:**
- Modify: `internal/store/message_store.go`
- Modify: `internal/store/message_store_test.go`

- [ ] **Step 1: Write the failing tests first**

Add `"time"` to the import block in `internal/store/message_store_test.go` (it is not present yet), then add two new tests. The existing `TestMessageStore_Create_ReceivedAtMillisecondPrecision` passes `""` as the last arg — after the signature change it will need updating too, but write the new tests first (they won't compile yet due to wrong arity):

```go
func TestMessageStore_Create_ReceivedAt_FromMessageTime(t *testing.T) {
	s := setupMessageStore(t)
	ctx := context.Background()

	const wantTime int64 = 1609073151345 // fixed past timestamp
	inserted, err := s.Create(ctx, "msg-ts", "feishu:chat1:userA", "feishu", "hello", "", "", wantTime)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if !inserted {
		t.Fatal("expected inserted=true")
	}

	var receivedAt int64
	if err := s.db.QueryRowContext(ctx,
		`SELECT received_at FROM platform_messages WHERE id = ?`, "msg-ts",
	).Scan(&receivedAt); err != nil {
		t.Fatalf("scan received_at: %v", err)
	}
	if receivedAt != wantTime {
		t.Errorf("received_at: got %d, want %d", receivedAt, wantTime)
	}
}

func TestMessageStore_Create_ReceivedAt_FallbackToServerTime(t *testing.T) {
	s := setupMessageStore(t)
	ctx := context.Background()

	before := time.Now().UnixMilli()
	s.Create(ctx, "msg-zero", "feishu:chat1:userA", "feishu", "hello", "", "", 0) //nolint
	after := time.Now().UnixMilli()

	var receivedAt int64
	if err := s.db.QueryRowContext(ctx,
		`SELECT received_at FROM platform_messages WHERE id = ?`, "msg-zero",
	).Scan(&receivedAt); err != nil {
		t.Fatalf("scan received_at: %v", err)
	}
	if receivedAt < before || receivedAt > after {
		t.Errorf("received_at %d: want value between %d and %d (server time range)", receivedAt, before, after)
	}
}
```

Note: `time` import is already present in `message_store_test.go`? Check — if not, add it.

- [ ] **Step 2: Confirm the tests don't compile yet** (wrong arity)

```bash
go test ./internal/store/... 2>&1 | head -20
```

Expected: compile error about wrong number of arguments to `Create`.

- [ ] **Step 3: Update `MessageStore.Create()` signature and implementation**

In `internal/store/message_store.go`, replace the `Create` function:

```go
// Create inserts a new message record with status "received".
// Returns inserted=false (no error) when platform_msg_id is non-empty and already exists.
// If platform_msg_id is empty, the insert always proceeds (no dedup).
// messageTime is stored as received_at; pass 0 to use server time.
func (s *MessageStore) Create(ctx context.Context, id, sessionKey, platform, content, raw, platformMsgID string, messageTime int64) (bool, error) {
	if messageTime == 0 {
		messageTime = time.Now().UnixMilli()
	}
	result, err := s.db.ExecContext(ctx,
		`INSERT OR IGNORE INTO platform_messages (id, session_key, platform, content, raw, platform_msg_id, received_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		id, sessionKey, platform, content, raw, platformMsgID, messageTime,
	)
	if err != nil {
		return false, err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return false, err
	}
	return n == 1, nil
}
```

- [ ] **Step 4: Fix all existing `Create()` call sites in `message_store_test.go`**

All existing calls in `message_store_test.go` use 7 string args. Each needs a trailing `0` (use server time). Find every call with:

```bash
grep -n "\.Create(" internal/store/message_store_test.go
```

Update each one by appending `, 0)`. For example:

```go
// Before
s.Create(ctx, "msg-1", "feishu:chat1:userA", "feishu", "hello world", `{"text":"hello world"}`, "")
// After
s.Create(ctx, "msg-1", "feishu:chat1:userA", "feishu", "hello world", `{"text":"hello world"}`, "", 0)
```

Also update the existing `TestMessageStore_Create_ReceivedAtMillisecondPrecision` — it calls `s.Create(ctx, "msg-ms", ..., "")`, change to `s.Create(ctx, "msg-ms", ..., "", 0)`.

- [ ] **Step 5: Run the store tests**

```bash
go test ./internal/store/... -v -run TestMessageStore
```

Expected: all tests PASS, including the two new ones.

- [ ] **Step 6: Commit**

```bash
git add internal/store/message_store.go internal/store/message_store_test.go
git commit -m "feat: MessageStore.Create accepts messageTime parameter for received_at"
```

---

## Chunk 2: Gateway Layer

### Task 3: Update `msgingest` interface and `Dispatch()` call site

**Files:**
- Modify: `internal/msgingest/gateway.go`
- Modify: `internal/msgingest/gateway_test.go`

- [ ] **Step 1: Write the failing tests (mock + helpers + new test functions)**

First, replace the mock struct and `Create` method in `internal/msgingest/gateway_test.go`. Replace everything from `type mockMsgStore struct` through the closing `}` of the original `Create` method with:

```go
type createCall struct {
	id          string
	messageTime int64
}

type mockMsgStore struct {
	insertResult bool
	creates      []createCall
	failed       []string
	merged       map[string][]string
}

func newMock(inserted bool) *mockMsgStore {
	return &mockMsgStore{insertResult: inserted, merged: make(map[string][]string)}
}

func (m *mockMsgStore) Create(_ context.Context, id, _, _, _, _, _ string, messageTime int64) (bool, error) {
	m.creates = append(m.creates, createCall{id: id, messageTime: messageTime})
	return m.insertResult, nil
}
```

Then add the `inboundWithTime` helper alongside the existing `inbound` helper:

```go
func inboundWithTime(sessionKey, content, platformMsgID string, messageTime int64) platform.InboundMessage {
	return platform.InboundMessage{
		Platform:          "test",
		SessionKey:        sessionKey,
		Content:           content,
		PlatformMessageID: platformMsgID,
		MessageTime:       messageTime,
	}
}
```

Then add the following new test functions at the end of `internal/msgingest/gateway_test.go`:

```go
// TestGateway_Historical_EmittedImmediately verifies that a message older than the
// debounce window is emitted immediately without waiting for the timer.
func TestGateway_Historical_EmittedImmediately(t *testing.T) {
	const debounce = 100 * time.Millisecond
	g := msgingest.New(newMock(true), debounce)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go g.Run(ctx)

	// Message sent 10 seconds ago — well past the debounce window.
	oldTime := time.Now().Add(-10 * time.Second).UnixMilli()
	g.Dispatch(inboundWithTime("s1", "old message", "m1", oldTime))

	select {
	case msg := <-g.Out():
		if msg.Content != "old message" {
			t.Fatalf("unexpected content: %q", msg.Content)
		}
		if msg.Command != msgingest.CommandNone {
			t.Fatalf("unexpected command: %q", msg.Command)
		}
	case <-time.After(50 * time.Millisecond):
		// Historical messages must arrive well before the debounce window expires.
		t.Fatal("timeout: historical message was not emitted immediately")
	}
}

// TestGateway_Historical_TwoMessages_EmittedIndependently verifies that two historical
// messages for the same session are each emitted as separate IngestedMessages.
func TestGateway_Historical_TwoMessages_EmittedIndependently(t *testing.T) {
	const debounce = 100 * time.Millisecond
	g := msgingest.New(newMock(true), debounce)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go g.Run(ctx)

	oldTime := time.Now().Add(-10 * time.Second).UnixMilli()
	g.Dispatch(inboundWithTime("s1", "first", "m1", oldTime))
	g.Dispatch(inboundWithTime("s1", "second", "m2", oldTime))

	received := make([]string, 0, 2)
	for i := 0; i < 2; i++ {
		select {
		case msg := <-g.Out():
			received = append(received, msg.Content)
		case <-time.After(50 * time.Millisecond):
			t.Fatalf("timeout waiting for message %d", i+1)
		}
	}
	if len(received) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(received))
	}
	// Ensure no third message appears (no merging).
	select {
	case extra := <-g.Out():
		t.Fatalf("unexpected extra message: %+v", extra)
	case <-time.After(200 * time.Millisecond):
		// expected
	}
}

// TestGateway_Historical_DoesNotCancelRunningTimer verifies that a historical message
// arriving while a debounce timer is active does not cancel the timer.
func TestGateway_Historical_DoesNotCancelRunningTimer(t *testing.T) {
	const debounce = 150 * time.Millisecond
	g := msgingest.New(newMock(true), debounce)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go g.Run(ctx)

	// Start a normal debounce for "real" message.
	g.Dispatch(inbound("s1", "real message", "m1"))

	// Immediately send a historical message for the same session.
	oldTime := time.Now().Add(-10 * time.Second).UnixMilli()
	g.Dispatch(inboundWithTime("s1", "old message", "m2", oldTime))

	// Historical message should arrive first (immediately).
	select {
	case msg := <-g.Out():
		if msg.Content != "old message" {
			t.Fatalf("expected historical message first, got %q", msg.Content)
		}
	case <-time.After(50 * time.Millisecond):
		t.Fatal("timeout waiting for historical message")
	}

	// Real message should still arrive after the debounce timer fires.
	select {
	case msg := <-g.Out():
		if msg.Content != "real message" {
			t.Fatalf("expected real message after debounce, got %q", msg.Content)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("timeout: debounce timer appears to have been cancelled")
	}
}

// TestGateway_RealTime_EntersDebounceNormally verifies that a message with age < debounce
// enters the debounce state machine and is not emitted before the timer fires.
func TestGateway_RealTime_EntersDebounceNormally(t *testing.T) {
	const debounce = 150 * time.Millisecond
	g := msgingest.New(newMock(true), debounce)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go g.Run(ctx)

	// Message sent just now — age ≈ 0, well within debounce window.
	g.Dispatch(inboundWithTime("s1", "fresh", "m1", time.Now().UnixMilli()))

	// Should NOT arrive before debounce fires.
	select {
	case msg := <-g.Out():
		t.Fatalf("message arrived too early (before debounce): %+v", msg)
	case <-time.After(50 * time.Millisecond):
		// expected: still in debounce window
	}

	// Should arrive after debounce fires.
	select {
	case msg := <-g.Out():
		if msg.Content != "fresh" {
			t.Fatalf("unexpected content: %q", msg.Content)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("timeout waiting for debounced message")
	}
}

// TestGateway_FutureTimestamp_TreatedAsRealTime verifies that a message with a future
// MessageTime falls through to the normal debounce path (not emitted immediately).
func TestGateway_FutureTimestamp_TreatedAsRealTime(t *testing.T) {
	const debounce = 150 * time.Millisecond
	g := msgingest.New(newMock(true), debounce)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go g.Run(ctx)

	futureTime := time.Now().Add(10 * time.Second).UnixMilli()
	g.Dispatch(inboundWithTime("s1", "future", "m1", futureTime))

	// Must NOT arrive immediately (goes to normal debounce).
	select {
	case msg := <-g.Out():
		t.Fatalf("future-timestamp message arrived immediately (should debounce): %+v", msg)
	case <-time.After(50 * time.Millisecond):
		// expected
	}
}
```

- [ ] **Step 2: Confirm the tests don't compile yet**

```bash
go test ./internal/msgingest/... 2>&1 | head -20
```

Expected: compile error — `mockMsgStore` does not implement `MessageStore` interface (wrong `Create` arity, since `gateway.go` interface still has 7 params).

- [ ] **Step 3: Update the `MessageStore` interface and `Dispatch()` in `gateway.go`**

In `internal/msgingest/gateway.go`:

**3a.** Change the `Create` line in the `MessageStore` interface:

```go
Create(ctx context.Context, id, sessionKey, platform, content, raw, platformMsgID string, messageTime int64) (bool, error)
```

**3b.** Update the `Create()` call in `Dispatch()` to pass `msg.MessageTime`:

```go
inserted, err := g.msgStore.Create(context.Background(), msgID, msg.SessionKey, msg.Platform, msg.Content, msg.RawContent, msg.PlatformMessageID, msg.MessageTime)
```

**3c.** After the command check block and before `g.mu.Lock()`, insert the historical message check. The full updated `Dispatch()` should be:

```go
func (g *Gateway) Dispatch(msg platform.InboundMessage) {
	msgID := uuid.New().String()
	inserted, err := g.msgStore.Create(context.Background(), msgID, msg.SessionKey, msg.Platform, msg.Content, msg.RawContent, msg.PlatformMessageID, msg.MessageTime)
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

	// Resolve effective message time; zero means unknown, treat as now (never historical).
	msgTime := msg.MessageTime
	if msgTime == 0 {
		msgTime = time.Now().UnixMilli()
	}

	// Historical message: age exceeds debounce window → skip debounce, emit immediately.
	if time.Now().UnixMilli()-msgTime > g.debounce.Milliseconds() {
		g.emit(IngestedMessage{
			MsgID:      msgID,
			SessionKey: msg.SessionKey,
			Platform:   msg.Platform,
			Content:    msg.Content,
			ReplyTo:    msg,
			Command:    CommandNone,
		})
		return
	}

	g.mu.Lock()

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

	ids := make([]string, len(state.ids))
	copy(ids, state.ids)

	if state.timer != nil {
		state.timer.Stop()
	}
	state.generation++
	gen := state.generation
	sessionKey := msg.SessionKey
	state.timer = time.AfterFunc(g.debounce, func() { g.onDebounce(sessionKey, gen) })

	g.mu.Unlock()

	g.msgStore.UpdateStatusBatch(context.Background(), ids, "debouncing") //nolint:errcheck
}
```

- [ ] **Step 4: Run all gateway tests**

```bash
go test ./internal/msgingest/... -v
```

Expected: all tests PASS (existing + new).

- [ ] **Step 5: Commit**

```bash
git add internal/msgingest/gateway.go internal/msgingest/gateway_test.go
git commit -m "feat: skip debounce for historical messages in msgingest.Gateway"
```

---

## Chunk 3: Platform Adapters

### Task 4: Feishu — parse `CreateTime` into `MessageTime`

**Files:**
- Modify: `internal/platform/feishu/handler.go`

- [ ] **Step 1: Add `strconv` to imports and `parseMillis` helper**

In `internal/platform/feishu/handler.go`, add `"strconv"` to the import block, then add the helper function at the bottom of the file (before the closing `var _` assertions):

```go
// parseMillis converts a *string millisecond timestamp (e.g. "1609073151345") to int64.
// Returns 0 for nil or unparseable input.
func parseMillis(s *string) int64 {
	if s == nil {
		return 0
	}
	v, err := strconv.ParseInt(*s, 10, 64)
	if err != nil {
		return 0
	}
	return v
}
```

- [ ] **Step 2: Set `MessageTime` in the `dispatch()` call**

In the `OnP2MessageReceiveV1` handler inside `Start()`, add `MessageTime` to the `dispatch` call:

```go
dispatch(platform.InboundMessage{
	Platform:          "feishu",
	SenderID:          senderID,
	SessionKey:        "feishu:" + *msg.ChatId + ":" + senderID,
	Content:           text,
	RawContent:        *msg.Content,
	Raw:               event,
	PlatformMessageID: feishuMsgID(msg.MessageId),
	MessageTime:       parseMillis(msg.CreateTime),
})
```

- [ ] **Step 3: Verify compile**

```bash
go build ./internal/platform/feishu/...
```

Expected: no errors.

- [ ] **Step 4: Commit**

```bash
git add internal/platform/feishu/handler.go
git commit -m "feat: set MessageTime from Feishu message CreateTime"
```

---

### Task 5: DingTalk — set `MessageTime` from `data.CreateAt`

**Files:**
- Modify: `internal/platform/dingtalk/handler.go`

- [ ] **Step 1: Set `MessageTime` in the `dispatch` call**

In `internal/platform/dingtalk/handler.go`, update the `msg` construction inside `RegisterChatBotCallbackRouter`:

```go
msg := platform.InboundMessage{
	Platform:          "dingtalk",
	SenderID:          data.SenderStaffId,
	SessionKey:        "dingtalk:" + data.ConversationId + ":" + data.SenderStaffId,
	Content:           text,
	RawContent:        rawContent,
	Raw:               data,
	PlatformMessageID: data.MsgId,
	MessageTime:       data.CreateAt, // int64 Unix ms per DingTalk open platform docs
}
```

- [ ] **Step 2: Verify compile**

```bash
go build ./internal/platform/dingtalk/...
```

Expected: no errors.

- [ ] **Step 3: Run the full test suite**

```bash
go test ./...
```

Expected: all tests PASS.

- [ ] **Step 4: Commit**

```bash
git add internal/platform/dingtalk/handler.go
git commit -m "feat: set MessageTime from DingTalk message CreateAt"
```

---

## Done

All tasks complete. Verify the full suite one final time:

```bash
go test ./...
```

Expected: all tests PASS, no compilation errors.
