# Explicit Worker Routing Priority

**Date:** 2026-03-11
**Status:** Approved

## Problem

The AI router currently selects a worker by matching message content against worker descriptions. However, users sometimes explicitly name a specific worker in natural language (e.g., "让 Nova 帮我做这个", "请用代码审查 worker 处理"). In these cases, the explicit specification should take priority over description matching.

## Solution

Two changes:

1. Modify the routing prompt in `ClaudeCodeClient.RouteToWorker` to instruct the AI to check for explicit worker mentions before falling back to description-based matching.
2. Add returned-ID validation in `botrouter.Router.Route()` as defence-in-depth. `ClaudeCodeClient` already validates internally, but `Route()` should enforce a correct worker ID regardless of which `WorkerRouter` implementation is used — protecting against future implementations that do not validate.

## Scope

Four files:
- `internal/ai/claude_code_client.go` — prompt update
- `internal/botrouter/router.go` — add post-route ID validation
- `internal/botrouter/router_test.go` — new test case
- `internal/ai/claude_code_client_test.go` — no change (prompt regression testing is out of scope)

## Prompt Change

The updated prompt adds a priority rule before the existing matching instruction:

> First, check if the message explicitly names or refers to one of the workers by name (e.g., "让 XX 处理", "请 XX 来做", "用 XX worker"). If so, return that worker's **ID** directly — do not match by description. Only if no worker is explicitly named should you select the best match by description. If the name is partial or ambiguous, fall back to description-based matching.

The word **ID** is emphasised to prevent Claude from returning the worker's name instead of its UUID. The prompt already shows workers as `ID: <uuid>, Name: <name>, Description: <desc>`.

Partial/ambiguous name resolution is delegated to Claude's judgment. Non-deterministic behaviour in edge cases (e.g., "Nov" when the worker is "Nova") is an acknowledged limitation in scope.

The output contract remains unchanged: return only the worker ID, nothing else.

## Router Validation

`botrouter.Router.Route()` currently returns `r.router.RouteToWorker(...)` as a one-liner. Change to capture the returned ID and validate it against the `workers` slice already in scope (fetched earlier in the same function). No additional store query is needed.

If the ID is not found, `Route` returns `fmt.Errorf("worker %q not found", workerID)`. This wording intentionally differs from `ClaudeCodeClient`'s internal error (`"claude returned unknown worker ID %q"`), because at the `botrouter` layer the caller of the validation does not know the source of the ID.

## Error Handling

- **Routing error (any cause):** `botrouter.Router.Route()` returns an error → `platform.Manager` uses the `noWorkerMsg` constant (defined in `platform/pipeline.go`, used in `platform/manager.go`) to send `"❌ 没有找到合适的 Worker，请换个描述试试"` to the user. This message covers both "named worker not found" and "AI hallucinated an ID". Known UX limitation: if the user named a worker correctly but it does not exist, "请换个描述试试" is misleading — acceptable for this scope.
- **No explicit mention:** AI falls through to normal description-based matching — existing behaviour, unchanged.

## Testing

One new test in `botrouter/router_test.go`:

- `TestRouter_Route_UnknownWorkerID` — mock router returns an ID not in the worker list; verify `Route` returns an error. *(Tests the new ID-validation logic in `botrouter.Router`.)*

The existing `TestRouter_Route_PicksCorrectWorker` covers the happy path.

No automated test exercises the explicit-mention prompt path end-to-end. Manual verification is the only check for the new prompt behaviour.

**Manual prompt verification:** Before merging, spot-check the updated prompt with:
- `让 <worker name> 帮我做 X` → should return the named worker's ID
- `请 <worker name> 来处理` → should return the named worker's ID
- A message with no explicit name → should still route by description
- A message naming a non-existent worker → should return an unknown ID (triggering the validation error)
- A message with a partial/ambiguous name → should fall back to description matching

**Prompt regression risk:** Acknowledged. Regressions will surface as `"❌ 没有找到合适的 Worker"` errors in production. No automated prompt evaluation harness is in scope for this change.

## Files Changed

| File | Change |
|------|--------|
| `internal/ai/claude_code_client.go` | Update `RouteToWorker` prompt |
| `internal/botrouter/router.go` | Split one-liner return; validate returned ID against worker list |
| `internal/botrouter/router_test.go` | Add `TestRouter_Route_UnknownWorkerID` |
| `internal/ai/claude_code_client_test.go` | No change |
