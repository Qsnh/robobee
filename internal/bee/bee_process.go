package bee

import (
	"bufio"
	"context"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"time"

	"github.com/robobee/core/internal/config"
)

// BeeProcess represents a single short-lived bee Claude invocation.
type BeeProcess struct {
	binary string
	mcpURL string
	apiKey string
}

// NewBeeProcess creates a BeeProcess.
func NewBeeProcess(cfg config.BeeConfig) *BeeProcess {
	return &BeeProcess{
		binary: cfg.Binary,
		mcpURL: cfg.MCPBaseURL + "/mcp/sse",
		apiKey: cfg.MCPAPIKey,
	}
}

const clearInstructions = `

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

// WriteCLAUDEMD writes (or overwrites) the CLAUDE.md file in workDir with persona content.
func WriteCLAUDEMD(workDir, persona string) error {
	if err := os.MkdirAll(workDir, 0o755); err != nil {
		return fmt.Errorf("mkdir bee workdir: %w", err)
	}
	path := filepath.Join(workDir, "CLAUDE.md")
	content := persona + clearInstructions
	return os.WriteFile(path, []byte(content), 0o644)
}

// Run spawns the bee process with the given prompt and waits for it to exit.
// If sessionID is non-empty and resume is true, passes --resume <sessionID>.
// If sessionID is non-empty and resume is false, passes --session-id <sessionID>.
// Returns nil on exit code 0, an error otherwise.
func (p *BeeProcess) Run(ctx context.Context, workDir, prompt, sessionID string, resume bool) error {
	mcpConfig := fmt.Sprintf(
		`{"mcpServers":{"robobee":{"type":"sse","url":%q}}}`,
		p.mcpURL+"?api_key="+url.QueryEscape(p.apiKey),
	)
	args := []string{
		"--dangerously-skip-permissions",
		"--verbose",
		"--output-format", "stream-json",
		"--mcp-config", mcpConfig,
		"-p", prompt,
	}
	if sessionID != "" {
		if resume {
			args = append(args, "--resume", sessionID)
		} else {
			args = append(args, "--session-id", sessionID)
		}
	}
	cmd := exec.CommandContext(ctx, p.binary, args...)
	cmd.Dir = workDir

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("stdout pipe: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return fmt.Errorf("stderr pipe: %w", err)
	}

	// Create log file for this run.
	sid := sessionID
	if sid == "" {
		sid = "nosession"
	}
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("get home dir: %w", err)
	}
	logDir := filepath.Join(homeDir, ".robobee", "bee-logs")
	if err := os.MkdirAll(logDir, 0o755); err != nil {
		return fmt.Errorf("mkdir bee-logs: %w", err)
	}
	logFileName := fmt.Sprintf("%s_%s.log", sid, time.Now().Format("20060102_150405"))
	logFile, err := os.OpenFile(filepath.Join(logDir, logFileName), os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("open bee log file: %w", err)
	}
	defer logFile.Close()

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start bee: %w", err)
	}

	// Drain stdout/stderr into log file.
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		scanner := bufio.NewScanner(stdout)
		for scanner.Scan() {
			fmt.Fprintf(logFile, "[stdout] %s\n", scanner.Text())
		}
	}()
	go func() {
		defer wg.Done()
		scanner := bufio.NewScanner(stderr)
		for scanner.Scan() {
			fmt.Fprintf(logFile, "[stderr] %s\n", scanner.Text())
		}
	}()

	wg.Wait()
	if err := cmd.Wait(); err != nil {
		return fmt.Errorf("bee exited with error: %w", err)
	}
	return nil
}
