# Platform Message Deduplication Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Prevent the same platform message event (Feishu/DingTalk) from being processed more than once when platforms push duplicate events due to network retries.

**Architecture:** Add a `platform_msg_id` column with a partial UNIQUE index to the `platform_messages` table. Use `INSERT OR IGNORE` in `MessageStore.Create` — returning whether the row was actually inserted — so the dispatch path can silently drop duplicates before routing or queuing. Both platform handlers populate a new `PlatformMessageID` field on `InboundMessage` from the platform-native message ID.

**Tech Stack:** Go, SQLite (mattn/go-sqlite3), Lark SDK (Feishu), DingTalk Stream SDK

---

## File Map

| File | Change |
|------|--------|
| `internal/store/db.go` | Add `platform_msg_id` column + partial UNIQUE index to schema and compat list |
| `internal/store/message_store.go` | Change `Create` to return `(bool, error)`, use `INSERT OR IGNORE` |
| `internal/store/message_store_test.go` | Fix existing `Create` calls; add 4 new dedup tests |
| `internal/platform/interfaces.go` | Add `PlatformMessageID string` to `InboundMessage`; update `MessageStore` interface |
| `internal/platform/pipeline_test.go` | Update `stubPipelineStore.Create` to match new interface |
| `internal/platform/feishu/handler.go` | Populate `PlatformMessageID: derefStr(msg.MessageId)` |
| `internal/platform/dingtalk/handler.go` | Populate `PlatformMessageID: data.MsgId` |
| `internal/platform/manager.go` | Use `(inserted, err)` from `Create`; drop duplicate messages |
| `internal/platform/manager_dedup_test.go` | New file: unit tests for dedup in dispatch |

---

## Chunk 1: Schema + Store Layer

### Task 1: Add `platform_msg_id` column and index to DB schema

**Files:**
- Modify: `internal/store/db.go`

- [ ] **Step 1: Add column to `CREATE TABLE` block**

In `db.go`, inside the `platform_messages` CREATE TABLE statement (after the `processed_at` line), add:

```sql
platform_msg_id TEXT NOT NULL DEFAULT ''
```

The full table definition ends with:
```sql
		received_at     DATETIME NOT NULL DEFAULT (datetime('now')),
		processed_at    DATETIME,
		platform_msg_id TEXT NOT NULL DEFAULT ''
	);
```

- [ ] **Step 2: Add partial UNIQUE index to main schema block**

After the existing `CREATE INDEX IF NOT EXISTS idx_platform_messages_worker_status` line, add:

```sql
	CREATE UNIQUE INDEX IF NOT EXISTS idx_platform_messages_platform_msg_id
		ON platform_messages(platform_msg_id)
		WHERE platform_msg_id != '';
```

- [ ] **Step 3: Add compat entries for existing databases**

In the idempotent `ALTER TABLE` loop in `db.go`, add two new entries to the existing slice:

```go
"ALTER TABLE platform_messages ADD COLUMN platform_msg_id TEXT NOT NULL DEFAULT ''",
"CREATE UNIQUE INDEX IF NOT EXISTS idx_platform_messages_platform_msg_id ON platform_messages(platform_msg_id) WHERE platform_msg_id != ''",
```

- [ ] **Step 4: Run store tests to verify schema migration still works**

```bash
go test ./internal/store/... -v -run TestMessageStore
```

Expected: all existing tests PASS (the new column has a default, so existing inserts are unaffected)

- [ ] **Step 5: Commit**

```bash
git add internal/store/db.go
git commit -m "feat: add platform_msg_id column with partial unique index"
```

---

### Task 2: Update `MessageStore.Create` to return `(bool, error)`

**Files:**
- Modify: `internal/store/message_store.go`
- Modify: `internal/store/message_store_test.go`

- [ ] **Step 1: Write failing dedup tests in `message_store_test.go`**

Add these four tests at the end of `message_store_test.go`:

```go
func TestMessageStore_Create_Dedup_FirstInsertReturnsTrue(t *testing.T) {
	s := setupMessageStore(t)
	ctx := context.Background()

	inserted, err := s.Create(ctx, "msg-1", "feishu:chat1:userA", "feishu", "hello", "", "feishu-msg-abc")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if !inserted {
		t.Error("first insert: want inserted=true, got false")
	}
}

func TestMessageStore_Create_Dedup_DuplicatePlatformMsgID(t *testing.T) {
	s := setupMessageStore(t)
	ctx := context.Background()

	s.Create(ctx, "msg-1", "feishu:chat1:userA", "feishu", "hello", "", "feishu-msg-abc") //nolint
	inserted, err := s.Create(ctx, "msg-2", "feishu:chat1:userA", "feishu", "hello", "", "feishu-msg-abc")
	if err != nil {
		t.Fatalf("duplicate Create: %v", err)
	}
	if inserted {
		t.Error("duplicate insert: want inserted=false, got true")
	}
}

func TestMessageStore_Create_Dedup_EmptyPlatformMsgIDNotDeduped(t *testing.T) {
	s := setupMessageStore(t)
	ctx := context.Background()

	inserted1, err := s.Create(ctx, "msg-1", "feishu:chat1:userA", "feishu", "hello", "", "")
	if err != nil || !inserted1 {
		t.Fatalf("first empty-id insert: err=%v inserted=%v", err, inserted1)
	}
	inserted2, err := s.Create(ctx, "msg-2", "feishu:chat1:userA", "feishu", "hello", "", "")
	if err != nil || !inserted2 {
		t.Fatalf("second empty-id insert: err=%v inserted=%v", err, inserted2)
	}
}

func TestMessageStore_InsertClearSentinel_UnaffectedByDedupSchema(t *testing.T) {
	s := setupMessageStore(t)
	ctx := context.Background()

	// Two clear sentinels for different sessions — both must succeed
	if err := s.InsertClearSentinel(ctx, "clear-a", "feishu:chat1:userA", "feishu"); err != nil {
		t.Fatalf("InsertClearSentinel A: %v", err)
	}
	if err := s.InsertClearSentinel(ctx, "clear-b", "feishu:chat2:userB", "feishu"); err != nil {
		t.Fatalf("InsertClearSentinel B: %v", err)
	}
}
```

- [ ] **Step 2: Run new tests to confirm they fail (compile error expected)**

```bash
go test ./internal/store/... -v -run TestMessageStore_Create_Dedup
```

Expected: FAIL — `Create` currently returns `error` not `(bool, error)`

- [ ] **Step 3: Update `MessageStore.Create` in `message_store.go`**

Replace the existing `Create` method:

```go
// Create inserts a new message record with status "received".
// Returns inserted=false (no error) when platform_msg_id is non-empty and already exists.
// If platform_msg_id is empty, the insert always proceeds (no dedup).
func (s *MessageStore) Create(ctx context.Context, id, sessionKey, platform, content, raw, platformMsgID string) (bool, error) {
	result, err := s.db.ExecContext(ctx,
		`INSERT OR IGNORE INTO platform_messages (id, session_key, platform, content, raw, platform_msg_id)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		id, sessionKey, platform, content, raw, platformMsgID,
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

- [ ] **Step 4: Fix existing `Create` calls in `message_store_test.go`**

All existing test calls pass only 6 arguments (no `platformMsgID`) and ignore the return. Update every existing call site in `message_store_test.go` to add `""` as the last argument and discard the bool with `_`:

Pattern to find: `s.Create(ctx,`
Pattern to apply: add `""` before the closing `)`, prefix with `_, _ =` or just call it bare since errors are ignored in helpers.

The easiest approach is to update each call like this:

```go
// Before:
s.Create(ctx, "msg-1", "feishu:chat1:userA", "feishu", "hello world", `{"text":"hello world"}`)

// After:
s.Create(ctx, "msg-1", "feishu:chat1:userA", "feishu", "hello world", `{"text":"hello world"}`, "") //nolint
```

Update all such calls in the file (search for `s.Create(ctx,` and update every occurrence). For `TestMessageStore_Create` which checks the return value:

```go
func TestMessageStore_Create(t *testing.T) {
	s := setupMessageStore(t)
	ctx := context.Background()

	if _, err := s.Create(ctx, "msg-1", "feishu:chat1:userA", "feishu", "hello world", `{"text":"hello world"}`, ""); err != nil {
		t.Fatalf("Create: %v", err)
	}
	// ... rest unchanged
}
```

- [ ] **Step 5: Run all store tests to confirm they pass**

```bash
go test ./internal/store/... -v
```

Expected: ALL PASS including the 4 new dedup tests

- [ ] **Step 6: Commit**

```bash
git add internal/store/message_store.go internal/store/message_store_test.go
git commit -m "feat: make MessageStore.Create return (bool, error) for dedup"
```

---

## Chunk 2: Interface + Domain + Handlers

### Task 3: Update `MessageStore` interface and `InboundMessage`

**Files:**
- Modify: `internal/platform/interfaces.go`
- Modify: `internal/platform/pipeline_test.go`

- [ ] **Step 1: Update `MessageStore` interface in `interfaces.go`**

Change the `Create` line in the `MessageStore` interface:

```go
// Before:
Create(ctx context.Context, id, sessionKey, platform, content, raw string) error

// After:
Create(ctx context.Context, id, sessionKey, platform, content, raw, platformMsgID string) (bool, error)
```

- [ ] **Step 2: Add `PlatformMessageID` field to `InboundMessage`**

In `interfaces.go`, update `InboundMessage`:

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
}
```

- [ ] **Step 3: Update `stubPipelineStore.Create` in `pipeline_test.go`**

Change the Create stub signature to match the new interface:

```go
// Before:
func (s *stubPipelineStore) Create(_ context.Context, _, _, _, _, _ string) error { return nil }

// After:
func (s *stubPipelineStore) Create(_ context.Context, _, _, _, _, _, _ string) (bool, error) {
	return true, nil
}
```

- [ ] **Step 4: Run platform package tests to confirm they compile and pass**

```bash
go test ./internal/platform/... -v
```

Expected: ALL PASS

- [ ] **Step 5: Commit**

```bash
git add internal/platform/interfaces.go internal/platform/pipeline_test.go
git commit -m "feat: add PlatformMessageID to InboundMessage and update MessageStore interface"
```

---

### Task 4: Update Feishu and DingTalk handlers

**Files:**
- Modify: `internal/platform/feishu/handler.go`
- Modify: `internal/platform/dingtalk/handler.go`

- [ ] **Step 1: Update Feishu handler to populate `PlatformMessageID`**

In `feishu/handler.go`, in the `dispatch(platform.InboundMessage{...})` call (around line 80), add the field:

```go
dispatch(platform.InboundMessage{
    Platform:          "feishu",
    SenderID:          senderID,
    SessionKey:        "feishu:" + *msg.ChatId + ":" + senderID,
    Content:           text,
    RawContent:        *msg.Content,
    Raw:               event,
    PlatformMessageID: feishuMsgID(msg.MessageId),
})
```

**Do NOT use `derefStr`** — it returns the string `"<nil>"` for nil pointers, not `""`. A non-empty `"<nil>"` would be treated as a valid platform ID and incorrectly deduplicate future messages.

Instead, add this private helper below `derefStr` in the same file:

```go
// feishuMsgID safely dereferences a *string message ID.
// Returns "" (not "<nil>") for nil so dedup is skipped when MessageId is absent.
func feishuMsgID(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
```

- [ ] **Step 2: Update DingTalk handler to populate `PlatformMessageID`**

In `dingtalk/handler.go`, in the `platform.InboundMessage{...}` struct literal (around line 60), add:

```go
msg := platform.InboundMessage{
    Platform:          "dingtalk",
    SenderID:          data.SenderStaffId,
    SessionKey:        "dingtalk:" + data.ConversationId + ":" + data.SenderStaffId,
    Content:           text,
    RawContent:        rawContent,
    Raw:               data,
    PlatformMessageID: data.MsgId,
}
```

- [ ] **Step 3: Build to confirm no compilation errors**

```bash
go build ./...
```

Expected: success, no errors

- [ ] **Step 4: Commit**

```bash
git add internal/platform/feishu/handler.go internal/platform/dingtalk/handler.go
git commit -m "feat: populate PlatformMessageID in Feishu and DingTalk handlers"
```

---

## Chunk 3: Dispatch Layer + Tests

### Task 5: Update manager.go dispatch to drop duplicates

**Files:**
- Modify: `internal/platform/manager.go`

- [ ] **Step 1: Update `msgStore.Create` call in `manager.go`**

In `manager.go`, find the `dispatch` closure (around line 100). Replace:

```go
// Record message to DB (best-effort; failure does not block processing).
msgID := uuid.New().String()
if err := m.msgStore.Create(context.Background(), msgID, msg.SessionKey, msg.Platform, msg.Content, msg.RawContent); err != nil {
    log.Printf("platform[%s]: record message error: %v", p.ID(), err)
}
```

With:

```go
// Record message to DB. Returns inserted=false if platform already pushed this message (dedup).
msgID := uuid.New().String()
inserted, err := m.msgStore.Create(context.Background(), msgID, msg.SessionKey, msg.Platform, msg.Content, msg.RawContent, msg.PlatformMessageID)
if err != nil {
    log.Printf("platform[%s]: record message error: %v", p.ID(), err)
}
if !inserted {
    log.Printf("platform[%s]: duplicate message dropped platformMsgID=%s", p.ID(), msg.PlatformMessageID)
    return
}
```

- [ ] **Step 2: Build and run all tests**

```bash
go build ./... && go test ./...
```

Expected: ALL PASS

- [ ] **Step 3: Commit**

```bash
git add internal/platform/manager.go
git commit -m "feat: drop duplicate platform messages using INSERT OR IGNORE dedup"
```

---

### Task 6: Add manager-level dedup unit tests

**Files:**
- Create: `internal/platform/manager_dedup_test.go`

- [ ] **Step 1: Write the failing test file**

Create `internal/platform/manager_dedup_test.go`. These tests wire a fake `Platform` that injects messages through the real `StartAll` goroutine / `dispatch` closure in `manager.go`, so the dedup guard in `manager.go` is actually exercised.

```go
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
	calls    atomic.Int32
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

func (p *fakePlatform) ID() string { return "test" }
func (p *fakePlatform) Receiver() PlatformReceiverAdapter { return p }
func (p *fakePlatform) Sender() PlatformSenderAdapter     { return p }
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
```

- [ ] **Step 2: Run the new tests**

```bash
go test ./internal/platform/... -v -run TestManagerDispatch
```

Expected: PASS

- [ ] **Step 3: Run full test suite**

```bash
go test ./...
```

Expected: ALL PASS

- [ ] **Step 4: Commit**

```bash
git add internal/platform/manager_dedup_test.go
git commit -m "test: add manager dispatch dedup unit tests"
```

---

## Done

All tasks complete. The feature is fully implemented and tested. Verify the full suite one final time:

```bash
go test ./... -count=1
```

Expected output: all packages PASS.
