package ai_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/robobee/core/internal/ai"
)

// fakeClaude creates a shell script that prints output and exits with exitCode.
func fakeClaude(t *testing.T, output string, exitCode int) string {
	t.Helper()
	dir := t.TempDir()
	// Use printf to avoid trailing newline issues; shell %% escapes % in fmt.Sprintf
	script := fmt.Sprintf("#!/bin/sh\nprintf '%%s' %q\nexit %d\n", output, exitCode)
	path := filepath.Join(dir, "claude")
	if err := os.WriteFile(path, []byte(script), 0755); err != nil {
		t.Fatalf("write fake binary: %v", err)
	}
	return path
}

func newClient(t *testing.T, binary string) *ai.ClaudeCodeClient {
	t.Helper()
	c, err := ai.NewClaudeCodeClient(binary)
	if err != nil {
		t.Fatalf("NewClaudeCodeClient: %v", err)
	}
	return c
}

func TestNewClaudeCodeClient_EmptyBinary_ReturnsError(t *testing.T) {
	_, err := ai.NewClaudeCodeClient("")
	if err == nil {
		t.Fatal("expected error for empty binary, got nil")
	}
}

func TestClaudeCodeClient_RouteToWorker_ReturnsValidID(t *testing.T) {
	bin := fakeClaude(t, "w1", 0)
	c := newClient(t, bin)

	workers := []ai.WorkerSummary{
		{ID: "w1", Name: "analyst", Description: "market analysis"},
		{ID: "w2", Name: "coder", Description: "code review"},
	}
	id, err := c.RouteToWorker(context.Background(), "analyze sales data", workers)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if id != "w1" {
		t.Errorf("got %q, want %q", id, "w1")
	}
}

func TestClaudeCodeClient_RouteToWorker_UnknownID_ReturnsError(t *testing.T) {
	bin := fakeClaude(t, "unknown-id", 0)
	c := newClient(t, bin)

	workers := []ai.WorkerSummary{{ID: "w1", Name: "a", Description: "b"}}
	_, err := c.RouteToWorker(context.Background(), "any message", workers)
	if err == nil {
		t.Fatal("expected error for unknown worker ID, got nil")
	}
}

func TestClaudeCodeClient_CronFromDescription_ReturnsCron(t *testing.T) {
	bin := fakeClaude(t, "0 9 * * 1-5", 0)
	c := newClient(t, bin)

	cron, err := c.CronFromDescription(context.Background(), "every weekday at 9am")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cron != "0 9 * * 1-5" {
		t.Errorf("got %q, want %q", cron, "0 9 * * 1-5")
	}
}

func TestClaudeCodeClient_OutputExceedsLimit_ReturnsError(t *testing.T) {
	// Generate output larger than 64 KiB
	huge := strings.Repeat("x", 64*1024+1)
	bin := fakeClaude(t, huge, 0)
	c := newClient(t, bin)

	_, err := c.CronFromDescription(context.Background(), "daily")
	if err == nil {
		t.Fatal("expected error for oversized output, got nil")
	}
}

func TestClaudeCodeClient_NonZeroExit_ReturnsError(t *testing.T) {
	bin := fakeClaude(t, "", 1)
	c := newClient(t, bin)

	_, err := c.CronFromDescription(context.Background(), "daily")
	if err == nil {
		t.Fatal("expected error for non-zero exit, got nil")
	}
}
