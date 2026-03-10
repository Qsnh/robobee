# Feishu Bot Integration Design

**Date:** 2026-03-09
**Status:** Approved

## Overview

Integrate a Feishu (Lark) bot into the RoboBee Core system so users can interact with Workers via Feishu chat. Messages are routed to the appropriate Worker using AI, with async response delivery and multi-turn conversation support.

## Requirements

- AI-based message routing: route user messages to the best-matching Worker based on Worker name + description
- Async response: immediately acknowledge receipt, deliver result when execution completes
- Multi-turn conversations: a Feishu chat maps to a session, enabling follow-up messages
- Feishu long connection (WebSocket) mode — no public webhook URL required
- Session persistence: chat-to-session mapping survives server restarts

## Architecture

### Data Flow

```
User sends Feishu message
    ↓
feishu.Handler.OnMessage()
    ↓
① Send "⏳ Processing..." immediately
    ↓
② Router.Route(message) — AI selects Worker
    ↓
③ SessionStore.GetOrCreate(chat_id) → session_id
    ↓
④ Manager.ExecuteWorker() or Manager.ReplyExecution()
    ↓ (async goroutine)
⑤ Poll execution until completed/failed → send result to Feishu
```

### New Files

```
internal/feishu/
├── client.go      # Lark client + WebSocket long connection startup
├── handler.go     # OnP2MessageReceiveV1 event handler
├── router.go      # AI routing: message → Worker selection
└── session.go     # feishu_sessions table read/write

internal/store/
└── feishu_session_store.go  # DB access layer

cmd/server/main.go           # Add: feishu.Start() alongside Scheduler
config.yaml                  # Add: feishu config block
```

### Database

New table added via additive migration in `internal/store/db.go`:

```sql
CREATE TABLE feishu_sessions (
    chat_id TEXT NOT NULL,
    worker_id TEXT NOT NULL,
    session_id TEXT NOT NULL,
    last_execution_id TEXT NOT NULL DEFAULT '',
    updated_at DATETIME NOT NULL DEFAULT (datetime('now')),
    PRIMARY KEY (chat_id)
);
```

- `chat_id`: Feishu chat ID (p2p or group)
- `worker_id`: last routed worker (for context)
- `session_id`: Claude session ID for conversation continuity
- `last_execution_id`: used to call `ReplyExecution()` for follow-up messages

## Component Design

### router.go — AI Routing

Reuses existing `ai.Client` (DeepSeek). Builds a prompt with all Worker names and descriptions fetched live from `WorkerStore.ListWorkers()`. Returns a single worker ID.

- No caching — route on every message for accuracy
- If AI returns invalid ID: reply "No suitable Worker found"

### handler.go — Message Handler

Handles `OnP2MessageReceiveV1` events:

1. Parse message text
2. Send immediate acknowledgment to Feishu chat
3. Launch goroutine:
   - Call `Router.Route(text)` to get `worker_id`
   - Check `feishu_sessions` for existing session:
     - Found → `Manager.ReplyExecution(last_execution_id, text)`
     - Not found → `Manager.ExecuteWorker(worker_id, text)`
   - Update `feishu_sessions` with new `session_id`, `last_execution_id`, `worker_id`
   - Poll execution every 2 seconds, timeout at 30 minutes
   - Send final result back to Feishu

### session.go — Session Store

Wraps DB operations for `feishu_sessions`:
- `GetSession(chatID) (*FeishuSession, error)`
- `UpsertSession(session *FeishuSession) error`

### client.go — Feishu Client Startup

```go
func Start(ctx context.Context, cfg config.FeishuConfig, handler *Handler) error
```

Creates `lark.NewClient` and `larkws.NewClient`, registers event handler, starts long connection. Called from `main.go` in a goroutine alongside the scheduler.

## Configuration

```yaml
feishu:
  enabled: true
  app_id: "cli_xxx"
  app_secret: "xxx"
```

Added to `internal/config/config.go` as `FeishuConfig` struct. When `enabled: false`, the feishu subsystem is not started.

## Error Handling

- AI routing failure: reply "No suitable Worker found, please try again"
- Worker execution failure: send error result back to Feishu chat
- Feishu API errors: log and skip (best-effort delivery)
- Feishu WebSocket disconnect: lark SDK handles automatic reconnection

## Dependencies

Add to `go.mod`:
```
github.com/larksuite/oapi-sdk-go/v3
```
