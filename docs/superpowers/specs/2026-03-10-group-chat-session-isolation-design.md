# Group Chat Session Isolation Design

**Date:** 2026-03-10
**Status:** Ready for Implementation

## Problem

In group chats on Feishu and DingTalk, all users share the same chat/conversation ID. The current `SessionKey` is derived solely from the chat ID, so every user's message maps to the same session. When multiple users @mention the bot in a group, their messages are processed under one shared session, causing context pollution and task interference.

## Goal

Each user in a group chat gets their own independent session. Private chats are unaffected in behavior (they already have one user per chat, so the change is format-only).

## Design

### Session Key Format Change

| Scenario | Before | After |
|---|---|---|
| Feishu (any) | `feishu:{chatId}` | `feishu:{chatId}:{userId}` |
| DingTalk (any) | `dingtalk:{conversationId}` | `dingtalk:{conversationId}:{senderStaffId}` |

### `InboundMessage.SenderID`

The `SenderID` field on `InboundMessage` is currently declared but never populated. Both handlers must now populate it with the user identifier used in the session key:
- Feishu: `SenderID = *event.Event.Sender.SenderId.UserId`
- DingTalk: `SenderID = data.SenderStaffId`

### Feishu (`internal/platform/feishu/handler.go`)

Use `event.Event.Sender.SenderId.UserId` as the user identifier. UserId is the enterprise-scoped user ID and is the preferred stable identifier when the app has `contact:user.employee_id:readonly` permission.

Construct the `InboundMessage` as:
```go
senderID := *event.Event.Sender.SenderId.UserId
dispatch(platform.InboundMessage{
    Platform:   "feishu",
    SenderID:   senderID,
    SessionKey: "feishu:" + *msg.ChatId + ":" + senderID,
    Content:    text,
    Raw:        event,
})
```

**Nil-guard required before constructing the message:** `msg.ChatId`, `event.Event.Sender`, `event.Event.Sender.SenderId`, and `SenderId.UserId` are all pointers and can be nil (e.g. app-originated or malformed events). If any is nil, return nil early without dispatching. Note: `msg.MessageType` and `msg.Content` are also dereferenced in the existing handler without nil-guards (lines 47 and 51) — these are pre-existing and out of scope for this change.

### DingTalk (`internal/platform/dingtalk/handler.go`)

Use `data.SenderStaffId` as the user identifier. It is the enterprise-scoped employee ID — stable and unique. `SenderNick` is a display name and must not be used as a key.

Replace the existing `dispatch(platform.InboundMessage{...})` call and the subsequent `log.Printf` line (the old log line hardcodes the pre-isolation key and must be removed) with:
```go
msg := platform.InboundMessage{
    Platform:   "dingtalk",
    SenderID:   data.SenderStaffId,
    SessionKey: "dingtalk:" + data.ConversationId + ":" + data.SenderStaffId,
    Content:    text,
    Raw:        data,
}
dispatch(msg)
log.Printf("dingtalk: dispatched message sessionKey=%s", msg.SessionKey)
```

**Empty-guard required:** `SenderStaffId` is a plain `string` and can be empty (e.g. external users or certain bot-originated callbacks). If empty, skip dispatch and return `[]byte(""), nil` to avoid creating a shared `dingtalk:CONV_ID:` session.

### `internal/platform/interfaces.go`

Update the `SessionKey` doc comment to reflect the new format:

```go
// Before
SessionKey string // platform-prefixed session key, e.g. "feishu:chatID"
// After
SessionKey string // platform-prefixed session key, e.g. "feishu:chatID:userID"
```

### Components Unchanged

- `platform.Pipeline` — no changes needed
- `platform.SessionStore` / `store.PlatformSessionStore` — no changes needed
- Clear command (`Delete(msg.SessionKey)`) — automatically scoped to the individual user after the key change

### Existing Data

Old session records (key format without userId) become orphans and are never matched. They do not cause errors. After deployment, clean up only Feishu and DingTalk keys to avoid touching other platforms:

```sql
DELETE FROM platform_sessions
WHERE (session_key LIKE 'feishu:%' OR session_key LIKE 'dingtalk:%')
  AND session_key NOT LIKE '%:%:%';
```

## Scope

Three files changed (plus optional post-deploy SQL):
- `internal/platform/feishu/handler.go` — SessionKey + SenderID + nil-guard
- `internal/platform/dingtalk/handler.go` — SessionKey + SenderID + empty-guard + replace old log line
- `internal/platform/interfaces.go` — SessionKey doc comment
- (optional post-deploy) `platform_sessions` table cleanup SQL
