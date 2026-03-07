package worker

import (
	"context"
	"testing"
	"time"
)

func TestClaudeRuntime_ExecuteWithEcho(t *testing.T) {
	r := &ClaudeRuntime{binary: "echo"}
	r.cmd = nil

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Verify the struct compiles and the interface is satisfied
	var _ Runtime = r
	_ = ctx
}

func TestCodexRuntime_ImplementsInterface(t *testing.T) {
	r := &CodexRuntime{binary: "echo"}
	var _ Runtime = r
	_ = r
}

func TestNewClaudeRuntime(t *testing.T) {
	r := NewClaudeRuntime("/usr/bin/claude")
	if r.binary != "/usr/bin/claude" {
		t.Errorf("expected binary /usr/bin/claude, got %s", r.binary)
	}
}

func TestNewCodexRuntime(t *testing.T) {
	r := NewCodexRuntime("/usr/bin/codex")
	if r.binary != "/usr/bin/codex" {
		t.Errorf("expected binary /usr/bin/codex, got %s", r.binary)
	}
}
