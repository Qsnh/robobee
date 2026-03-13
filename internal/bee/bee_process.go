package bee

import (
	"bufio"
	"context"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
)

// BeeProcess represents a single short-lived bee Claude invocation.
type BeeProcess struct {
	binary string
	mcpURL string
	apiKey string
}

// NewBeeProcess creates a BeeProcess.
func NewBeeProcess(binary, mcpURL, apiKey string) *BeeProcess {
	return &BeeProcess{binary: binary, mcpURL: mcpURL, apiKey: apiKey}
}

// WriteCLAUDEMD writes (or overwrites) the CLAUDE.md file in workDir with persona content.
func WriteCLAUDEMD(workDir, persona string) error {
	if err := os.MkdirAll(workDir, 0o755); err != nil {
		return fmt.Errorf("mkdir bee workdir: %w", err)
	}
	path := filepath.Join(workDir, "CLAUDE.md")
	return os.WriteFile(path, []byte(persona), 0o644)
}

// Run spawns the bee process with the given prompt and waits for it to exit.
// If sessionID is non-empty and resume is true, passes --resume <sessionID>.
// If sessionID is non-empty and resume is false, passes --session-id <sessionID>.
// Returns nil on exit code 0, an error otherwise.
func (p *BeeProcess) Run(ctx context.Context, workDir, prompt, sessionID string, resume bool) error {
	mcpConfig := fmt.Sprintf(
		`{"mcpServers":{"robobee":{"url":%q,"headers":{"Authorization":"Bearer %s"}}}}`,
		p.mcpURL, p.apiKey,
	)
	args := []string{
		"--dangerously-skip-permissions",
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

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start bee: %w", err)
	}

	// Drain stdout/stderr to prevent pipe buffer from blocking
	go func() {
		scanner := bufio.NewScanner(stdout)
		for scanner.Scan() {
			log.Printf("bee: %s", scanner.Text())
		}
	}()
	go func() {
		scanner := bufio.NewScanner(stderr)
		for scanner.Scan() {
			log.Printf("bee stderr: %s", scanner.Text())
		}
	}()

	if err := cmd.Wait(); err != nil {
		return fmt.Errorf("bee exited with error: %w", err)
	}
	return nil
}
