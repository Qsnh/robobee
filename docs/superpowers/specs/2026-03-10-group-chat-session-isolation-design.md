# Group Chat Session Isolation Design

**Date:** 2026-03-10
**Status:** Approved

## Problem

In group chats on Feishu and DingTalk, all users share the same chat/conversation ID. The current `SessionKey` is derived solely from the chat ID, so every user's message maps to the same session. When multiple users @mention the bot in a group, their messages are processed under one shared session, causing context pollution and task interference.

## Goal

Each user in a group chat gets their own independent session. Private chats are unaffected in behavior (they already have one user per chat, so the change is format-only).

## Design

### Session Key Format Change

| Scenario | Before | After |
|---|---|---|
| Feishu (any) | `feishu:{chatId}` | `feishu:{chatId}:{openId}` |
| DingTalk (any) | `dingtalk:{conversationId}` | `dingtalk:{conversationId}:{senderStaffId}` |

### Feishu (`internal/platform/feishu/handler.go`)

Use `event.Event.Sender.SenderId.OpenId` as the user identifier. OpenId is stable within an app and requires no elevated permissions.

```go
SessionKey: "feishu:" + *msg.ChatId + ":" + *event.Event.Sender.SenderId.OpenId,
```

### DingTalk (`internal/platform/dingtalk/handler.go`)

Use `data.SenderStaffId` as the user identifier. It is the enterprise-scoped employee ID — stable and unique. `SenderNick` is a display name and must not be used as a key.

```go
SessionKey: "dingtalk:" + data.ConversationId + ":" + data.SenderStaffId,
```

### Components Unchanged

- `platform.Pipeline` — no changes needed
- `platform.SessionStore` / `store.PlatformSessionStore` — no changes needed
- Clear command (`Delete(msg.SessionKey)`) — automatically scoped to the individual user after the key change

### Existing Data

Old session records (key format without userId) become orphans and are never matched. They do not cause errors. After deployment, clean up with:

```sql
DELETE FROM platform_sessions WHERE session_key NOT LIKE '%:%:%';
```

## Scope

Two files changed, two lines changed (one per platform handler). No schema migration required.
