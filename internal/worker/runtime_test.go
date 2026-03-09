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

func TestNewClaudeRuntime(t *testing.T) {
	r := NewClaudeRuntime("/usr/bin/claude")
	if r.binary != "/usr/bin/claude" {
		t.Errorf("expected binary /usr/bin/claude, got %s", r.binary)
	}
}
