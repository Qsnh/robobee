package ai

import (
	"context"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"time"
)

const (
	maxOutputBytes = 64 * 1024 // 64 KiB
	queryTimeout   = 30 * time.Second
)

// ClaudeCodeClient runs the claude CLI binary for one-shot text queries.
// It implements WorkerRouter and CronResolver.
type ClaudeCodeClient struct {
	binary string
}

// NewClaudeCodeClient creates a client using the given claude binary path.
// Returns an error if binary is empty.
func NewClaudeCodeClient(binary string) (*ClaudeCodeClient, error) {
	if binary == "" {
		return nil, fmt.Errorf("claude binary path must not be empty")
	}
	return &ClaudeCodeClient{binary: binary}, nil
}

// run executes a one-shot claude query and returns the trimmed text output.
// It wraps ctx with a 30-second deadline and caps output at 64 KiB.
// No working directory is set; the process inherits the server's cwd.
// --allowedTools is passed with no values to restrict tool use to none.
func (c *ClaudeCodeClient) run(ctx context.Context, prompt string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, queryTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, c.binary,
		"--output-format", "text",
		"-p", prompt,
		"--allowedTools", "", // empty string arg = empty tool list (no tools allowed)
	)

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return "", fmt.Errorf("stdout pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return "", fmt.Errorf("start claude: %w", err)
	}

	out, readErr := io.ReadAll(io.LimitReader(stdout, int64(maxOutputBytes)+1))
	waitErr := cmd.Wait()

	if readErr != nil {
		return "", fmt.Errorf("read output: %w", readErr)
	}
	if len(out) > maxOutputBytes {
		return "", fmt.Errorf("claude output exceeded %d bytes", maxOutputBytes)
	}
	if waitErr != nil {
		return "", fmt.Errorf("claude exited with error: %w", waitErr)
	}

	return strings.TrimSpace(string(out)), nil
}

// RouteToWorker implements WorkerRouter.
func (c *ClaudeCodeClient) RouteToWorker(ctx context.Context, message string, workers []WorkerSummary) (string, error) {
	var sb strings.Builder
	validIDs := make(map[string]bool, len(workers))
	for _, w := range workers {
		fmt.Fprintf(&sb, "- ID: %s, Name: %s, Description: %s\n", w.ID, w.Name, w.Description)
		validIDs[w.ID] = true
	}

	prompt := fmt.Sprintf(
		"You are a task router. Given a list of workers and a user message, return ONLY the ID of the most suitable worker. No explanation, no markdown, just the ID.\n\nFirst, check if the message explicitly names or refers to one of the workers by name (e.g., \"让 XX 处理\", \"请 XX 来做\", \"用 XX worker\"). If so, return that worker's ID directly — do not match by description. Only if no worker is explicitly named should you select the best match by description. If the name is partial or ambiguous, fall back to description-based matching.\n\nWorkers:\n%s\nUser message: %s",
		sb.String(), message,
	)

	workerID, err := c.run(ctx, prompt)
	if err != nil {
		return "", err
	}
	if !validIDs[workerID] {
		return "", fmt.Errorf("claude returned unknown worker ID %q", workerID)
	}
	return workerID, nil
}

// CronFromDescription implements CronResolver.
func (c *ClaudeCodeClient) CronFromDescription(ctx context.Context, description string) (string, error) {
	prompt := fmt.Sprintf(
		"You are a cron expression generator. Convert the schedule description to a valid 5-field cron expression (minute hour day month weekday). Return ONLY the cron expression, nothing else. No explanations, no markdown.\n\n%s",
		description,
	)

	cron, err := c.run(ctx, prompt)
	if err != nil {
		return "", err
	}
	if cron == "" {
		return "", fmt.Errorf("claude returned empty cron expression")
	}
	return cron, nil
}
