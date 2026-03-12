# Explicit Worker Routing Priority

**Date:** 2026-03-11
**Status:** Approved

## Problem

The AI router currently selects a worker by matching message content against worker descriptions. However, users sometimes explicitly name a specific worker in natural language (e.g., "让 Nova 帮我做这个", "请用代码审查 worker 处理"). In these cases, the explicit specification should take priority over description matching.

## Solution

Modify the routing prompt in `ClaudeCodeClient.RouteToWorker` to instruct the AI to check for explicit worker mentions before falling back to description-based matching.

## Scope

Single change: the prompt string in `internal/ai/claude_code_client.go`, `RouteToWorker` method (line 80–83).

## Prompt Change

The updated prompt adds a priority rule:

> First, check if the message explicitly names or refers to one of the workers (e.g., "让 XX 处理", "请 XX 来做", "用 XX worker"). If so, return that worker's ID directly — do not match by description. Only if no worker is explicitly named should you select the best match by description.

The output contract remains unchanged: return only the worker ID, nothing else.

## Error Handling

- **Explicit worker not found:** If the user names a worker that does not exist in the list, the AI will not be able to match it and will either return an unknown ID or pick the wrong worker. Since `RouteToWorker` already validates that the returned ID exists in `validIDs` (line 89–92), an unrecognised ID returns an error, which propagates as "no suitable worker found" to the user.
- **No explicit mention:** AI falls through to normal description-based matching — existing behaviour, unchanged.

## Testing

Add to `botrouter/router_test.go`:

1. `TestRouter_Route_ExplicitWorkerMention` — mock router returns the explicitly-named worker ID; verify routing bypasses description matching.
2. `TestRouter_Route_ExplicitWorkerNotFound` — mock router returns an ID not in the worker list; verify an error is returned.

Note: the actual AI prompt behaviour is tested by `ClaudeCodeClient` integration tests (which use a fake binary); unit tests for `botrouter` use a mock and only verify the routing contract.

## Files Changed

| File | Change |
|------|--------|
| `internal/ai/claude_code_client.go` | Update `RouteToWorker` prompt to add explicit-mention priority rule |
| `internal/botrouter/router_test.go` | Add two test cases for explicit routing scenarios |
