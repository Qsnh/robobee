# Explicit Worker Routing Priority Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** When a user's message explicitly names a specific worker in natural language, route directly to that worker instead of using description-based AI matching.

**Architecture:** Modify the AI routing prompt in `ClaudeCodeClient.RouteToWorker` to check for explicit worker mentions first; add ID validation to `botrouter.Router.Route()` as defence-in-depth so any unrecognised ID from any `WorkerRouter` implementation surfaces as an error at the routing layer.

**Tech Stack:** Go 1.21+, standard `testing` package, SQLite (`:memory:`) for test DB via `store.InitDB`.

---

## Chunk 1: Prompt update and router validation

### Task 1: Update `ClaudeCodeClient.RouteToWorker` prompt

**Files:**
- Modify: `internal/ai/claude_code_client.go` (lines 80–83, the `prompt` variable in `RouteToWorker`)

**Context:** The current prompt is a one-liner that only does description matching. We prepend a priority rule that checks for explicit name mentions first.

- [ ] **Step 1: Read the current prompt**

Open `internal/ai/claude_code_client.go` and locate `RouteToWorker`. The prompt currently reads:

```
"You are a task router. Given a list of workers and a user message, return ONLY the ID of the most suitable worker. No explanation, no markdown, just the ID.\n\nWorkers:\n%s\nUser message: %s"
```

- [ ] **Step 2: Replace the prompt string**

Change the `prompt` assignment in `RouteToWorker` to:

```go
prompt := fmt.Sprintf(
    "You are a task router. Given a list of workers and a user message, return ONLY the ID of the most suitable worker. No explanation, no markdown, just the ID.\n\nFirst, check if the message explicitly names or refers to one of the workers by name (e.g., \"让 XX 处理\", \"请 XX 来做\", \"用 XX worker\"). If so, return that worker's ID directly — do not match by description. Only if no worker is explicitly named should you select the best match by description. If the name is partial or ambiguous, fall back to description-based matching.\n\nWorkers:\n%s\nUser message: %s",
    sb.String(), message,
)
```

- [ ] **Step 3: Verify the file compiles**

```bash
cd /Users/tengteng/work/robobee/core && go build ./internal/ai/...
```

Expected: no output (clean build).

- [ ] **Step 4: Commit**

```bash
git add internal/ai/claude_code_client.go
git commit -m "feat: add explicit worker mention priority to routing prompt"
```

---

### Task 2: Add ID validation to `botrouter.Router.Route()`

**Files:**
- Modify: `internal/botrouter/router.go` (the `Route` method, currently a one-liner return)

**Context:** `Route` currently returns `r.router.RouteToWorker(...)` directly. We split this into two steps: get the ID, then verify it exists in the worker list already fetched in the same function. This enforces a valid worker ID contract regardless of which `WorkerRouter` implementation is used.

- [ ] **Step 1: Read the current `Route` implementation**

Open `internal/botrouter/router.go`. The `Route` method currently ends with:

```go
return r.router.RouteToWorker(ctx, message, summaries)
```

The `workers` slice is in scope just above it.

- [ ] **Step 2: Replace the one-liner return with capture + validation**

```go
// Route returns the worker ID best suited to handle the message.
func (r *Router) Route(ctx context.Context, message string) (string, error) {
	workers, err := r.workerStore.List()
	if err != nil {
		return "", fmt.Errorf("list workers: %w", err)
	}
	if len(workers) == 0 {
		return "", fmt.Errorf("no workers available")
	}

	summaries := make([]ai.WorkerSummary, len(workers))
	validIDs := make(map[string]bool, len(workers))
	for i, w := range workers {
		summaries[i] = ai.WorkerSummary{ID: w.ID, Name: w.Name, Description: w.Description}
		validIDs[w.ID] = true
	}

	workerID, err := r.router.RouteToWorker(ctx, message, summaries)
	if err != nil {
		return "", err
	}
	if !validIDs[workerID] {
		return "", fmt.Errorf("worker %q not found", workerID)
	}
	return workerID, nil
}
```

- [ ] **Step 3: Verify the file compiles**

```bash
cd /Users/tengteng/work/robobee/core && go build ./internal/botrouter/...
```

Expected: no output (clean build).

- [ ] **Step 4: Run existing tests to ensure nothing is broken**

```bash
cd /Users/tengteng/work/robobee/core && go test ./internal/botrouter/... -v
```

Expected: all existing tests pass (`TestRouter_Route_PicksCorrectWorker`, `TestRouter_Route_NoWorkers_ReturnsError`).

- [ ] **Step 5: Commit**

```bash
git add internal/botrouter/router.go
git commit -m "feat: validate returned worker ID in botrouter.Router.Route"
```

---

### Task 3: Add `TestRouter_Route_UnknownWorkerID` test

**Files:**
- Modify: `internal/botrouter/router_test.go`

**Context:** This test verifies the new validation logic: when the underlying `WorkerRouter` returns an ID not in the worker store, `Route` must return an error. It uses the same `mockRouter` and `newTestRouter` helpers already present in the file.

- [ ] **Step 1: Write the failing test**

Add this test to `internal/botrouter/router_test.go`:

```go
func TestRouter_Route_UnknownWorkerID(t *testing.T) {
	workers := []model.Worker{
		{ID: "w1", Name: "mas", Description: "market analyst", WorkDir: t.TempDir()},
	}

	mock := &mockRouter{
		routeFunc: func(_ string, _ []ai.WorkerSummary) (string, error) {
			return "unknown-id", nil // returns an ID not in the worker store
		},
	}
	router := newTestRouter(t, mock, workers)

	_, err := router.Route(context.Background(), "some message")
	if err == nil {
		t.Fatal("expected error when router returns unknown worker ID")
	}
}
```

- [ ] **Step 2: Run the test to verify it fails before the implementation**

```bash
cd /Users/tengteng/work/robobee/core && go test ./internal/botrouter/... -run TestRouter_Route_UnknownWorkerID -v
```

Expected: FAIL — `Route` returns `nil` error currently (the one-liner passes through whatever the mock returns).

> **Note:** If you are following this plan in order, Task 2 is already complete and the test will PASS here. That is correct — continue to step 3.

- [ ] **Step 3: Run all botrouter tests to confirm everything passes**

```bash
cd /Users/tengteng/work/robobee/core && go test ./internal/botrouter/... -v
```

Expected: all three tests pass:
- `TestRouter_Route_PicksCorrectWorker` — PASS
- `TestRouter_Route_NoWorkers_ReturnsError` — PASS
- `TestRouter_Route_UnknownWorkerID` — PASS

- [ ] **Step 4: Run the full test suite**

```bash
cd /Users/tengteng/work/robobee/core && go test ./...
```

Expected: all tests pass.

- [ ] **Step 5: Commit**

```bash
git add internal/botrouter/router_test.go
git commit -m "test: add TestRouter_Route_UnknownWorkerID for botrouter ID validation"
```

---

### Manual Prompt Verification

After all tasks are complete, run the server locally and test the following scenarios (requires real workers configured in the DB):

| Input message | Expected behaviour |
|---|---|
| `让 <worker name> 帮我做 X` | Routes to the named worker directly |
| `请 <worker name> 来处理` | Routes to the named worker directly |
| A message with no worker name | Routes by description (existing behaviour) |
| A message naming a non-existent worker | Returns `"❌ 没有找到合适的 Worker，请换个描述试试"` |
| A message with a partial/ambiguous name | Falls back to description matching |
