# .robobee.claude.md 系统规则注入 Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 将系统规则拆分到 `.robobee.claude.md`，通过 `@` 导入语法引入 CLAUDE.md，解决 bee 覆盖用户编辑和 worker 无法更新规则的问题。

**Architecture:** 新增 `internal/claudemd` 包，提供 `EnsureSystemRules(workDir, role)` 函数。该函数每次覆盖写入 `.robobee.claude.md`（确保最新规则），并检查 CLAUDE.md 是否包含 `@.robobee.claude.md` 引用（缺失则追加）。Bee 的 `WriteCLAUDEMD` 改为幂等（仅在文件不存在时创建）。

**Tech Stack:** Go 1.25, 标准库 os/filepath/strings

---

## File Structure

| File | Action | Responsibility |
|------|--------|----------------|
| `internal/claudemd/claudemd.go` | Create | `EnsureSystemRules` 函数 + 所有规则常量 |
| `internal/claudemd/claudemd_test.go` | Create | `EnsureSystemRules` 单元测试 |
| `internal/bee/bee_process.go` | Modify | `WriteCLAUDEMD` 改为幂等，移除 `clearInstructions` |
| `internal/bee/feeder.go` | Modify | tick 中增加 `EnsureSystemRules` 调用 |
| `internal/worker/manager.go` | Modify | 4 个方法中增加 `EnsureSystemRules` 调用 |

---

## Chunk 1: claudemd 包

### Task 1: 创建 `internal/claudemd/claudemd.go`

**Files:**
- Create: `internal/claudemd/claudemd.go`
- Test: `internal/claudemd/claudemd_test.go`

- [ ] **Step 1: 编写测试 — EnsureSystemRules 写入 .robobee.claude.md（bee 角色）**

```go
// internal/claudemd/claudemd_test.go
package claudemd_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/robobee/core/internal/claudemd"
)

func TestEnsureSystemRules_WritesBeeRules(t *testing.T) {
	dir := t.TempDir()
	// Create CLAUDE.md so EnsureSystemRules can check it
	os.WriteFile(filepath.Join(dir, "CLAUDE.md"), []byte("# Bee\n"), 0644)

	if err := claudemd.EnsureSystemRules(dir, claudemd.RoleBee); err != nil {
		t.Fatalf("EnsureSystemRules: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(dir, ".robobee.claude.md"))
	if err != nil {
		t.Fatalf("read .robobee.claude.md: %v", err)
	}
	content := string(data)

	// Should contain shared rules
	if !strings.Contains(content, "任务通知规范") {
		t.Error("missing shared rules (任务通知规范)")
	}
	if !strings.Contains(content, "send_message") {
		t.Error("missing send_message reference in shared rules")
	}
	// Should contain bee-specific rules
	if !strings.Contains(content, "清除上下文处理") {
		t.Error("missing bee-specific rules (清除上下文处理)")
	}
	// Should NOT contain worker-specific rules
	if strings.Contains(content, "mark_task_success") {
		t.Error("bee rules should not contain worker-specific mark_task_success")
	}
}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `cd /Users/tengyongzhi/work/robobee && go test ./internal/claudemd/ -run TestEnsureSystemRules_WritesBeeRules -v`
Expected: FAIL — package does not exist

- [ ] **Step 3: 编写测试 — EnsureSystemRules 写入 .robobee.claude.md（worker 角色）**

追加到 `internal/claudemd/claudemd_test.go`：

```go
func TestEnsureSystemRules_WritesWorkerRules(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "CLAUDE.md"), []byte("# Worker\n"), 0644)

	if err := claudemd.EnsureSystemRules(dir, claudemd.RoleWorker); err != nil {
		t.Fatalf("EnsureSystemRules: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(dir, ".robobee.claude.md"))
	if err != nil {
		t.Fatalf("read .robobee.claude.md: %v", err)
	}
	content := string(data)

	// Should contain shared rules
	if !strings.Contains(content, "任务通知规范") {
		t.Error("missing shared rules")
	}
	// Should contain worker-specific rules
	if !strings.Contains(content, "mark_task_success") {
		t.Error("missing worker-specific rules (mark_task_success)")
	}
	if !strings.Contains(content, "mark_task_failed") {
		t.Error("missing worker-specific rules (mark_task_failed)")
	}
	// Should NOT contain bee-specific rules
	if strings.Contains(content, "清除上下文处理") {
		t.Error("worker rules should not contain bee-specific 清除上下文处理")
	}
}
```

- [ ] **Step 4: 编写测试 — CLAUDE.md 不包含引用时追加**

追加到 `internal/claudemd/claudemd_test.go`：

```go
func TestEnsureSystemRules_AppendsImportWhenMissing(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "CLAUDE.md"), []byte("# My Bot\n\nSome user content\n"), 0644)

	if err := claudemd.EnsureSystemRules(dir, claudemd.RoleWorker); err != nil {
		t.Fatalf("EnsureSystemRules: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(dir, "CLAUDE.md"))
	if err != nil {
		t.Fatalf("read CLAUDE.md: %v", err)
	}
	content := string(data)

	if !strings.Contains(content, "@.robobee.claude.md") {
		t.Error("CLAUDE.md should contain @.robobee.claude.md import")
	}
	// Original content should be preserved
	if !strings.Contains(content, "# My Bot") {
		t.Error("original CLAUDE.md content should be preserved")
	}
	if !strings.Contains(content, "Some user content") {
		t.Error("user content should be preserved")
	}
}
```

- [ ] **Step 5: 编写测试 — CLAUDE.md 已包含引用时不修改**

```go
func TestEnsureSystemRules_DoesNotDuplicateImport(t *testing.T) {
	dir := t.TempDir()
	original := "# My Bot\n\n@.robobee.claude.md\n"
	os.WriteFile(filepath.Join(dir, "CLAUDE.md"), []byte(original), 0644)

	if err := claudemd.EnsureSystemRules(dir, claudemd.RoleWorker); err != nil {
		t.Fatalf("EnsureSystemRules: %v", err)
	}

	data, _ := os.ReadFile(filepath.Join(dir, "CLAUDE.md"))
	if string(data) != original {
		t.Errorf("CLAUDE.md should not be modified when import already exists.\nGot: %q\nWant: %q", string(data), original)
	}
}
```

- [ ] **Step 6: 编写测试 — CLAUDE.md 不存在时不创建**

```go
func TestEnsureSystemRules_SkipsWhenNoCLAUDEMD(t *testing.T) {
	dir := t.TempDir()
	// Do NOT create CLAUDE.md

	if err := claudemd.EnsureSystemRules(dir, claudemd.RoleWorker); err != nil {
		t.Fatalf("EnsureSystemRules: %v", err)
	}

	// .robobee.claude.md should still be written
	if _, err := os.Stat(filepath.Join(dir, ".robobee.claude.md")); err != nil {
		t.Error(".robobee.claude.md should be created even without CLAUDE.md")
	}

	// CLAUDE.md should NOT be created
	if _, err := os.Stat(filepath.Join(dir, "CLAUDE.md")); err == nil {
		t.Error("CLAUDE.md should not be created by EnsureSystemRules")
	}
}
```

- [ ] **Step 7: 编写测试 — 覆盖旧的 .robobee.claude.md**

```go
func TestEnsureSystemRules_OverwritesExistingRulesFile(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "CLAUDE.md"), []byte("# Bot\n"), 0644)
	os.WriteFile(filepath.Join(dir, ".robobee.claude.md"), []byte("old content"), 0644)

	if err := claudemd.EnsureSystemRules(dir, claudemd.RoleWorker); err != nil {
		t.Fatalf("EnsureSystemRules: %v", err)
	}

	data, _ := os.ReadFile(filepath.Join(dir, ".robobee.claude.md"))
	if string(data) == "old content" {
		t.Error(".robobee.claude.md should be overwritten with latest rules")
	}
	if !strings.Contains(string(data), "任务通知规范") {
		t.Error("overwritten file should contain latest rules")
	}
}
```

- [ ] **Step 8: 实现 `internal/claudemd/claudemd.go`**

```go
package claudemd

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const (
	RoleBee    = "bee"
	RoleWorker = "worker"

	systemRulesFile = ".robobee.claude.md"
	importLine      = "@.robobee.claude.md"
)

const sharedRules = `## 任务通知规范

你在执行任何任务时，必须通过 ` + "`send_message`" + ` 工具与用户保持同步。这是强制要求，不可省略。

### 何时通知

1. **任务开始时** — 收到任务后、开始实际处理之前，立即调用 ` + "`send_message`" + ` 告知用户你已接收任务并即将开始处理
2. **阶段性进展时** — 如果任务涉及多个步骤或阶段，每完成一个阶段调用 ` + "`send_message`" + ` 汇报当前进度和下一步计划
3. **任务完成时** — 任务执行完毕后，调用 ` + "`send_message`" + ` 汇报最终结果

### 通知原则

- 简洁明了，不要冗长描述
- 包含关键信息：正在做什么、完成了什么、结果是什么
- 遇到异常或阻塞时也必须通知用户
`

const beeRules = `
## 清除上下文处理

当用户发送的消息表示想要清除/重置对话（例如"clear"、"清除"、"重置上下文"等）时：

1. 首先调用 list_tasks，传入 session_key 和 status "pending,running" 检查是否有活跃任务。

2. 如果没有活跃任务：
   - 调用 clear_session，传入 session_key
   - 调用 send_message 确认："已清除会话上下文。"

3. 如果有活跃任务：
   - 调用 send_message 告知用户："当前有 N 个任务正在处理中，清除上下文将终止这些任务。是否确认清除？"
   - 等待用户下一条消息。

4. 如果用户确认（再次发送 "clear" 或类似确认消息）：
   - 调用 clear_session（将自动取消所有任务、终止运行中的 worker 进程、清除所有会话上下文）
   - 调用 send_message 确认："已终止所有任务并清除会话上下文。"
`

const workerRules = `
## 任务状态标记

任务执行结束后，你必须根据执行结果标记任务状态：

- **成功** — 调用 ` + "`mark_task_success`" + ` 并附上结果摘要
- **失败** — 调用 ` + "`mark_task_failed`" + ` 并附上失败原因

这是每个任务的最后一步，不可遗漏。先调用 ` + "`send_message`" + ` 通知结果，再标记状态。
`

// rulesForRole returns the combined rules content for the given role.
func rulesForRole(role string) string {
	switch role {
	case RoleBee:
		return sharedRules + beeRules
	case RoleWorker:
		return sharedRules + workerRules
	default:
		return sharedRules
	}
}

// EnsureSystemRules writes .robobee.claude.md with the latest system rules
// for the given role, and ensures CLAUDE.md contains the @import reference.
// It does NOT create CLAUDE.md if it doesn't exist.
func EnsureSystemRules(workDir, role string) error {
	// 1. Write .robobee.claude.md (always overwrite)
	rulesPath := filepath.Join(workDir, systemRulesFile)
	if err := os.WriteFile(rulesPath, []byte(rulesForRole(role)), 0o644); err != nil {
		return fmt.Errorf("write %s: %w", systemRulesFile, err)
	}

	// 2. Check CLAUDE.md for import reference
	claudePath := filepath.Join(workDir, "CLAUDE.md")
	data, err := os.ReadFile(claudePath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil // CLAUDE.md doesn't exist, skip
		}
		return fmt.Errorf("read CLAUDE.md: %w", err)
	}

	// 3. Append import if missing
	if !strings.Contains(string(data), importLine) {
		data = append(data, []byte("\n"+importLine+"\n")...)
		if err := os.WriteFile(claudePath, data, 0o644); err != nil {
			return fmt.Errorf("update CLAUDE.md: %w", err)
		}
	}

	return nil
}
```

- [ ] **Step 9: 运行所有测试确认通过**

Run: `cd /Users/tengyongzhi/work/robobee && go test ./internal/claudemd/ -v`
Expected: ALL PASS

- [ ] **Step 10: Commit**

```bash
git add internal/claudemd/claudemd.go internal/claudemd/claudemd_test.go
git commit -m "feat(claudemd): add EnsureSystemRules for .robobee.claude.md injection"
```

---

## Chunk 2: Bee 侧改动

### Task 2: 修改 `bee_process.go` — WriteCLAUDEMD 改为幂等

**Files:**
- Modify: `internal/bee/bee_process.go:33-62`

- [ ] **Step 1: 移除 `clearInstructions` 常量，修改 `WriteCLAUDEMD` 为幂等**

将 `internal/bee/bee_process.go` 中的 `clearInstructions` 常量（第 33-52 行）和 `WriteCLAUDEMD` 函数（第 54-62 行）替换为：

```go
// WriteCLAUDEMD creates the CLAUDE.md file in workDir with persona content
// only if it does not already exist. This preserves any user edits.
func WriteCLAUDEMD(workDir, persona string) error {
	if err := os.MkdirAll(workDir, 0o755); err != nil {
		return fmt.Errorf("mkdir bee workdir: %w", err)
	}
	path := filepath.Join(workDir, "CLAUDE.md")
	if _, err := os.Stat(path); err == nil {
		return nil // already exists, do not overwrite
	}
	return os.WriteFile(path, []byte(persona), 0o644)
}
```

- [ ] **Step 2: 编写 WriteCLAUDEMD 幂等性测试**

在 `internal/bee/feeder_test.go` 中添加（该文件使用 `package bee_test`，可访问导出函数）：

```go
func TestWriteCLAUDEMD_DoesNotOverwriteExisting(t *testing.T) {
	dir := t.TempDir()
	original := "user-edited content"
	os.WriteFile(filepath.Join(dir, "CLAUDE.md"), []byte(original), 0644)

	if err := bee.WriteCLAUDEMD(dir, "new persona"); err != nil {
		t.Fatal(err)
	}

	data, _ := os.ReadFile(filepath.Join(dir, "CLAUDE.md"))
	if string(data) != original {
		t.Error("WriteCLAUDEMD should not overwrite existing file")
	}
}

func TestWriteCLAUDEMD_CreatesWhenMissing(t *testing.T) {
	dir := t.TempDir()

	if err := bee.WriteCLAUDEMD(dir, "my persona"); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(filepath.Join(dir, "CLAUDE.md"))
	if err != nil {
		t.Fatalf("CLAUDE.md should be created: %v", err)
	}
	if string(data) != "my persona" {
		t.Errorf("unexpected content: %q", string(data))
	}
}
```

- [ ] **Step 3: 确认编译和测试通过**

Run: `cd /Users/tengyongzhi/work/robobee && go test ./internal/bee/ -run TestWriteCLAUDEMD -v`
Expected: ALL PASS

- [ ] **Step 4: Commit**

```bash
git add internal/bee/bee_process.go internal/bee/feeder_test.go
git commit -m "refactor(bee): make WriteCLAUDEMD idempotent, move clearInstructions to claudemd"
```

### Task 3: 修改 `feeder.go` — tick 中增加 EnsureSystemRules

**Files:**
- Modify: `internal/bee/feeder.go:1-14,88-92`

- [ ] **Step 1: 添加 import 并在 tick 中调用 EnsureSystemRules**

在 `internal/bee/feeder.go` 的 import 块中添加：

```go
"github.com/robobee/core/internal/claudemd"
```

将 `tick()` 中第 88-92 行的：

```go
if err := WriteCLAUDEMD(f.cfg.WorkDir, f.cfg.Persona); err != nil {
    log.Printf("feeder: write CLAUDE.md: %v", err)
    f.rollback(ctx, msgs)
    return
}
```

替换为：

```go
if err := WriteCLAUDEMD(f.cfg.WorkDir, f.cfg.Persona); err != nil {
    log.Printf("feeder: write CLAUDE.md: %v", err)
    f.rollback(ctx, msgs)
    return
}
if err := claudemd.EnsureSystemRules(f.cfg.WorkDir, claudemd.RoleBee); err != nil {
    log.Printf("feeder: ensure system rules: %v", err)
    // non-fatal: continue even if system rules update fails
}
```

- [ ] **Step 2: 运行现有 feeder 测试确认不破坏**

Run: `cd /Users/tengyongzhi/work/robobee && go test ./internal/bee/ -v`
Expected: ALL PASS

- [ ] **Step 3: Commit**

```bash
git add internal/bee/feeder.go
git commit -m "feat(bee): call EnsureSystemRules in feeder tick"
```

---

## Chunk 3: Worker 侧改动

### Task 4: 修改 `manager.go` — 4 个方法增加 EnsureSystemRules

**Files:**
- Modify: `internal/worker/manager.go:62-91,93-129,131-160,273-305`

- [ ] **Step 1: 添加 import**

在 `internal/worker/manager.go` 的 import 块中添加：

```go
"github.com/robobee/core/internal/claudemd"
```

- [ ] **Step 2: 修改 `CreateWorker` — 初始内容加引用 + 调用 EnsureSystemRules**

将 `CreateWorker` 中第 77-81 行的 CLAUDE.md 创建逻辑：

```go
if _, err := os.Stat(claudeMD); os.IsNotExist(err) {
    initialContent := fmt.Sprintf("# %s\n\n**Role:** %s\n\n## Work Memories\n\n", name, description)
    if err := os.WriteFile(claudeMD, []byte(initialContent), 0644); err != nil {
        return model.Worker{}, fmt.Errorf("create CLAUDE.md: %w", err)
    }
}
```

替换为：

```go
if _, err := os.Stat(claudeMD); os.IsNotExist(err) {
    initialContent := fmt.Sprintf("# %s\n\n**Role:** %s\n\n## Work Memories\n\n", name, description)
    if err := os.WriteFile(claudeMD, []byte(initialContent), 0644); err != nil {
        return model.Worker{}, fmt.Errorf("create CLAUDE.md: %w", err)
    }
}
if err := claudemd.EnsureSystemRules(workDir, claudemd.RoleWorker); err != nil {
    log.Printf("create worker: ensure system rules: %v", err)
}
```

- [ ] **Step 3: 修改 `ExecuteWorker` — 执行前调用 EnsureSystemRules**

在 `ExecuteWorker` 方法中，第 109 行 `rt := NewClaudeRuntime(...)` 之前插入：

```go
if err := claudemd.EnsureSystemRules(worker.WorkDir, claudemd.RoleWorker); err != nil {
    log.Printf("execute worker: ensure system rules: %v", err)
}
```

- [ ] **Step 4: 修改 `ExecuteWorkerWithSession` — 执行前调用 EnsureSystemRules**

在 `ExecuteWorkerWithSession` 方法中，第 148 行 `rt := NewClaudeRuntime(...)` 之前插入：

```go
if err := claudemd.EnsureSystemRules(worker.WorkDir, claudemd.RoleWorker); err != nil {
    log.Printf("execute worker with session: ensure system rules: %v", err)
}
```

- [ ] **Step 5: 修改 `ReplyExecution` — 执行前调用 EnsureSystemRules**

在 `ReplyExecution` 方法中，第 295 行 `rt := NewClaudeRuntime(...)` 之前插入：

```go
if err := claudemd.EnsureSystemRules(worker.WorkDir, claudemd.RoleWorker); err != nil {
    log.Printf("reply execution: ensure system rules: %v", err)
}
```

- [ ] **Step 6: 确认编译通过**

Run: `cd /Users/tengyongzhi/work/robobee && go build ./internal/worker/`
Expected: SUCCESS

- [ ] **Step 7: 运行全部测试**

Run: `cd /Users/tengyongzhi/work/robobee && go test ./... -v`
Expected: ALL PASS

- [ ] **Step 8: Commit**

```bash
git add internal/worker/manager.go
git commit -m "feat(worker): call EnsureSystemRules before worker execution"
```
