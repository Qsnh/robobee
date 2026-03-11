# Group Chat Session Isolation Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make each user in a group chat have their own independent bot session by including the sender's user ID in the session key.

**Architecture:** Three files change. The two platform handlers (`feishu/handler.go`, `dingtalk/handler.go`) populate `SenderID` and construct a per-user `SessionKey` (`platform:chatId:userId`). The `interfaces.go` comment is updated to reflect the new format. Pipeline, SessionStore, and database schema are untouched.

**Tech Stack:** Go, Feishu Lark SDK (`github.com/larksuite/oapi-sdk-go/v3`), DingTalk Stream SDK (`github.com/open-dingtalk/dingtalk-stream-sdk-go`), SQLite

**Spec:** `docs/superpowers/specs/2026-03-10-group-chat-session-isolation-design.md`

---

## Chunk 1: Pipeline test for per-user isolation + interfaces.go comment

### Task 1: Add pipeline test for per-user session isolation

This test lives in the existing test file `internal/platform/pipeline_test.go`. It verifies that two messages with the same chat but different sender IDs each get their own independent session — i.e., the pipeline treats session keys as opaque strings and stores them independently.

**Files:**
- Modify: `internal/platform/pipeline_test.go`

- [ ] **Step 1: Write the failing… wait, this test should already pass** (the pipeline already treats `SessionKey` as an opaque string). Write it to document and guard the behavior.

Add to `internal/platform/pipeline_test.go`:

```go
func TestPipeline_GroupChat_TwoUsersGetIndependentSessions(t *testing.T) {
	store := newStubSessionStore()
	mgr := &stubManager{exec: model.WorkerExecution{
		ID:        "exec-1",
		SessionID: "sess-1",
		Status:    model.ExecStatusCompleted,
		Result:    "done",
	}}
	p := newPipeline(&stubRouter{workerID: "w1"}, store, mgr)

	// Simulate two different users in the same group chat
	msgUserA := InboundMessage{
		Platform:   "feishu",
		SenderID:   "userA",
		SessionKey: "feishu:chat123:userA",
		Content:    "deploy the app",
	}
	msgUserB := InboundMessage{
		Platform:   "feishu",
		SenderID:   "userB",
		SessionKey: "feishu:chat123:userB",
		Content:    "run tests",
	}

	p.Handle(context.Background(), msgUserA)
	p.Handle(context.Background(), msgUserB)

	sessA := store.sessions["feishu:chat123:userA"]
	sessB := store.sessions["feishu:chat123:userB"]

	if sessA == nil {
		t.Fatal("session for userA should exist")
	}
	if sessB == nil {
		t.Fatal("session for userB should exist")
	}
	if sessA == sessB {
		t.Error("userA and userB should have independent sessions")
	}
}
```

- [ ] **Step 2: Run the test to verify it passes**

```bash
cd /Users/tengteng/work/robobee/core
go test ./internal/platform/... -run TestPipeline_GroupChat_TwoUsersGetIndependentSessions -v
```

Expected: `PASS`

- [ ] **Step 3: Commit**

```bash
git add internal/platform/pipeline_test.go
git commit -m "test: add per-user session isolation test for group chat"
```

---

### Task 2: Update interfaces.go SessionKey doc comment

**Files:**
- Modify: `internal/platform/interfaces.go`

- [ ] **Step 1: Update the comment on the `SessionKey` field**

In `internal/platform/interfaces.go`, find:
```go
SessionKey string // platform-prefixed session key, e.g. "feishu:chatID"
```
Replace with:
```go
SessionKey string // platform-prefixed session key, e.g. "feishu:chatID:userID"
```

- [ ] **Step 2: Build to verify no compilation errors**

```bash
cd /Users/tengteng/work/robobee/core
go build ./...
```

Expected: no output (clean build)

- [ ] **Step 3: Commit**

```bash
git add internal/platform/interfaces.go
git commit -m "docs: update SessionKey comment to reflect per-user key format"
```

---

## Chunk 2: DingTalk handler

### Task 3: Update DingTalk handler with per-user session key

**Files:**
- Modify: `internal/platform/dingtalk/handler.go`

The current dispatch block (lines 42–55 of `dingtalk/handler.go`) looks like:
```go
cli.RegisterChatBotCallbackRouter(func(ctx context.Context, data *chatbot.BotCallbackDataModel) ([]byte, error) {
    text := strings.TrimSpace(data.Text.Content)
    log.Printf("dingtalk: received message conversationId=%s sender=%s text=%q", data.ConversationId, data.SenderNick, text)
    if text == "" {
        return []byte(""), nil
    }
    dispatch(platform.InboundMessage{
        Platform:   "dingtalk",
        SessionKey: "dingtalk:" + data.ConversationId,
        Content:    text,
        Raw:        data,
    })
    log.Printf("dingtalk: dispatched message sessionKey=%s", "dingtalk:"+data.ConversationId)
    return []byte(""), nil
})
```

- [ ] **Step 1: Add empty-guard and update dispatch block**

Replace the block above with:
```go
cli.RegisterChatBotCallbackRouter(func(ctx context.Context, data *chatbot.BotCallbackDataModel) ([]byte, error) {
    text := strings.TrimSpace(data.Text.Content)
    log.Printf("dingtalk: received message conversationId=%s sender=%s text=%q", data.ConversationId, data.SenderNick, text)
    if text == "" {
        return []byte(""), nil
    }
    if data.SenderStaffId == "" {
        log.Printf("dingtalk: skipping message with empty SenderStaffId conversationId=%s", data.ConversationId)
        return []byte(""), nil
    }
    msg := platform.InboundMessage{
        Platform:   "dingtalk",
        SenderID:   data.SenderStaffId,
        SessionKey: "dingtalk:" + data.ConversationId + ":" + data.SenderStaffId,
        Content:    text,
        Raw:        data,
    }
    dispatch(msg)
    log.Printf("dingtalk: dispatched message sessionKey=%s", msg.SessionKey)
    return []byte(""), nil
})
```

- [ ] **Step 2: Build to verify no compilation errors**

```bash
cd /Users/tengteng/work/robobee/core
go build ./...
```

Expected: no output (clean build)

- [ ] **Step 3: Run all tests**

```bash
cd /Users/tengteng/work/robobee/core
go test ./...
```

Expected: all pass

- [ ] **Step 4: Commit**

```bash
git add internal/platform/dingtalk/handler.go
git commit -m "feat: per-user session key in DingTalk group chats"
```

---

## Chunk 3: Feishu handler

### Task 4: Update Feishu handler with per-user session key

**Files:**
- Modify: `internal/platform/feishu/handler.go`

The current dispatch block (inside `Start`, lines 45–65 of `feishu/handler.go`) looks like:
```go
OnP2MessageReceiveV1(func(ctx context.Context, event *larkim.P2MessageReceiveV1) error {
    msg := event.Event.Message
    if msg == nil || *msg.MessageType != "text" {
        return nil
    }
    var content map[string]string
    if err := json.Unmarshal([]byte(*msg.Content), &content); err != nil {
        return nil
    }
    text := content["text"]
    if text == "" {
        return nil
    }
    dispatch(platform.InboundMessage{
        Platform:   "feishu",
        SessionKey: "feishu:" + *msg.ChatId,
        Content:    text,
        Raw:        event,
    })
    return nil
})
```

- [ ] **Step 1: Add nil-guards and update dispatch block**

Replace the `dispatch(...)` call (and anything after `if text == ""`) with:

```go
    if text == "" {
        return nil
    }
    sender := event.Event.Sender
    if sender == nil || sender.SenderId == nil || sender.SenderId.UserId == nil {
        log.Printf("feishu: skipping message with nil sender or UserId")
        return nil
    }
    if msg.ChatId == nil {
        log.Printf("feishu: skipping message with nil ChatId")
        return nil
    }
    senderID := *sender.SenderId.UserId
    dispatch(platform.InboundMessage{
        Platform:   "feishu",
        SenderID:   senderID,
        SessionKey: "feishu:" + *msg.ChatId + ":" + senderID,
        Content:    text,
        Raw:        event,
    })
    return nil
```

Note: `event.Event.Sender` is of type `*larkim.EventSender`. Access path: `event.Event.Sender.SenderId.UserId` where `SenderId` is `*larkim.UserId` and `UserId` is `*string`.

- [ ] **Step 2: Build to verify no compilation errors**

```bash
cd /Users/tengteng/work/robobee/core
go build ./...
```

Expected: no output (clean build). If the field path is wrong for the SDK version in use, check with:
```bash
grep -r "UserId\|SenderId\|EventSender" $(go env GOPATH)/pkg/mod/github.com/larksuite/oapi-sdk-go/ 2>/dev/null | grep "type EventSender\|type UserId" | head -20
```

- [ ] **Step 3: Run all tests**

```bash
cd /Users/tengteng/work/robobee/core
go test ./...
```

Expected: all pass

- [ ] **Step 4: Commit**

```bash
git add internal/platform/feishu/handler.go
git commit -m "feat: per-user session key in Feishu group chats"
```

---

## Chunk 4: Post-deploy cleanup (optional)

### Task 5: Clean up orphaned session records

Run this SQL against the production database after deploying the above changes. Old session records with the two-segment key format become unreachable but harmless. This step is optional and can be skipped if preferred.

- [ ] **Step 1: Run cleanup SQL**

```sql
DELETE FROM platform_sessions
WHERE (session_key LIKE 'feishu:%' OR session_key LIKE 'dingtalk:%')
  AND session_key NOT LIKE '%:%:%';
```

Connect to the SQLite database file (typically `data/robobee.db` or as configured in `config.yaml`) and run the query.
