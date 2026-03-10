# DingTalk Bot Integration Design

Date: 2026-03-10

## Overview

Add DingTalk (钉钉) chatbot support to RoboBee Core, mirroring the existing Feishu bot implementation. Users can send messages to the DingTalk bot and receive AI-routed worker execution results, with the same two-message pattern (immediate ack + async final result).

## Architecture

### Approach: Mirror + Shared Router (Option A)

Each platform remains self-contained. The only shared code is the AI-powered worker router, extracted from `feishu` into a new `botrouter` package.

### New Files

| File | Purpose |
|------|---------|
| `internal/botrouter/router.go` | Extracted from `feishu/router.go` — platform-agnostic AI worker router |
| `internal/botrouter/router_test.go` | Moved from `feishu/router_test.go` |
| `internal/dingtalk/client.go` | Starts DingTalk stream SDK client |
| `internal/dingtalk/handler.go` | Message handler: ack + async polling + reply |
| `internal/store/dingtalk_session_store.go` | Session persistence (separate table) |
| `internal/store/dingtalk_session_store_test.go` | Store tests |

### Modified Files

| File | Change |
|------|--------|
| `internal/feishu/router.go` | Replace implementation with import of `botrouter` |
| `internal/feishu/router_test.go` | Update import path |
| `internal/config/config.go` | Add `DingTalkConfig{Enabled, ClientID, ClientSecret}` |
| `internal/store/db.go` | Add `dingtalk_sessions` table DDL |
| `cmd/server/main.go` | Wire DingTalk startup (same pattern as Feishu) |
| `go.mod` | Add `github.com/open-dingtalk/dingtalk-stream-sdk-go` |

## Data Flow

1. DingTalk stream SDK calls `OnChatBotMessageReceived(ctx, data)` callback
2. Handler immediately replies via `replier.SimpleReplyText(ctx, data.SessionWebhook, ack)` — "⏳ 正在处理，请稍候…"
3. Goroutine runs `process()`:
   - Determines `chat_id` from incoming message (sender or group)
   - Looks up `dingtalk_sessions` for existing session context
   - Routes message via `botrouter.Router` → selects worker ID
   - Executes new worker or resumes existing session via `worker.Manager`
   - Updates `dingtalk_sessions` with new execution ID
   - Polls execution status every 2s, up to 30-minute timeout
4. Sends final result via `replier.SimpleReplyText(ctx, data.SessionWebhook, result)`

**Key difference from Feishu:** Replies always use `data.SessionWebhook` — the SDK abstracts p2p vs group chat, so no branching is needed in the handler.

## Database Schema

```sql
CREATE TABLE IF NOT EXISTS dingtalk_sessions (
    chat_id             TEXT NOT NULL,
    worker_id           TEXT NOT NULL,
    session_id          TEXT NOT NULL,
    last_execution_id   TEXT NOT NULL DEFAULT '',
    updated_at          DATETIME NOT NULL DEFAULT (datetime('now')),
    PRIMARY KEY (chat_id)
);
```

## Configuration

```yaml
dingtalk:
  enabled: false
  client_id: ""
  client_secret: ""
```

Go struct:
```go
type DingTalkConfig struct {
    Enabled      bool   `yaml:"enabled"`
    ClientID     string `yaml:"client_id"`
    ClientSecret string `yaml:"client_secret"`
}
```

## Error Handling

Consistent with Feishu bot:

| Condition | Message |
|-----------|---------|
| No worker found | ❌ 没有找到合适的 Worker，请换个描述试试 |
| General failure | ❌ 处理失败，请稍后重试 |
| Execution timeout (30min) | ⏰ 任务超时，请稍后通过 Web 界面查看结果 |

## Testing

- `botrouter` package: existing router tests moved from `feishu`
- `dingtalk_session_store_test.go`: mirrors Feishu session store tests (get not found, upsert + get, upsert updates)

## Dependencies

- `github.com/open-dingtalk/dingtalk-stream-sdk-go` — official DingTalk Stream SDK
  - `chatbot` sub-package: `BotCallbackDataModel`, `NewChatbotReplier`
  - `client` sub-package: `NewStreamClient`, `WithAppCredential`, `NewAppCredentialConfig`
