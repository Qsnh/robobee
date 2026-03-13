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
