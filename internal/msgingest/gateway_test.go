package msgingest_test

import (
	"context"
	"testing"
	"time"

	"github.com/robobee/core/internal/msgingest"
	"github.com/robobee/core/internal/platform"
)

// mockMsgStore stubs the MessageStore interface for msgingest.
type mockMsgStore struct {
	insertResult bool // returned by Create
	created      []string
	failed       []string
	merged       map[string][]string // primaryID -> mergedIDs
}

func newMock(inserted bool) *mockMsgStore {
	return &mockMsgStore{insertResult: inserted, merged: make(map[string][]string)}
}

func (m *mockMsgStore) Create(_ context.Context, id, _, _, _, _, _ string) (bool, error) {
	m.created = append(m.created, id)
	return m.insertResult, nil
}
func (m *mockMsgStore) UpdateStatusBatch(_ context.Context, ids []string, _ string) error {
	return nil
}
func (m *mockMsgStore) MarkTerminal(_ context.Context, ids []string, _ string) error {
	m.failed = append(m.failed, ids...)
	return nil
}
func (m *mockMsgStore) MarkMerged(_ context.Context, primaryID string, mergedIDs []string) error {
	m.merged[primaryID] = mergedIDs
	return nil
}

func inbound(sessionKey, content, platformMsgID string) platform.InboundMessage {
	return platform.InboundMessage{
		Platform:          "test",
		SessionKey:        sessionKey,
		Content:           content,
		PlatformMessageID: platformMsgID,
	}
}

// TestGateway_Dedup_DropsKnownMessage verifies that when Create returns false (duplicate),
// nothing appears on Out().
func TestGateway_Dedup_DropsKnownMessage(t *testing.T) {
	g := msgingest.New(newMock(false), 100*time.Millisecond)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go g.Run(ctx)

	g.Dispatch(inbound("s1", "hello", "dup-1"))

	select {
	case msg := <-g.Out():
		t.Fatalf("expected nothing on Out(), got %+v", msg)
	case <-time.After(300 * time.Millisecond):
		// expected: duplicate dropped
	}
}

// TestGateway_Debounce_EmitsSingleMergedMessage verifies that two messages arriving
// within the debounce window are merged into one IngestedMessage.
func TestGateway_Debounce_EmitsSingleMergedMessage(t *testing.T) {
	g := msgingest.New(newMock(true), 100*time.Millisecond)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go g.Run(ctx)

	g.Dispatch(inbound("s1", "hello", "m1"))
	g.Dispatch(inbound("s1", "world", "m2"))

	select {
	case msg := <-g.Out():
		if msg.Command != msgingest.CommandNone {
			t.Fatalf("expected normal message, got command %q", msg.Command)
		}
		if msg.Content != "hello\n\n---\n\nworld" {
			t.Fatalf("expected merged content, got %q", msg.Content)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("timeout waiting for debounced message")
	}

	// Ensure only ONE message is emitted
	select {
	case extra := <-g.Out():
		t.Fatalf("expected only one message, got extra: %+v", extra)
	case <-time.After(200 * time.Millisecond):
		// expected
	}
}

// TestGateway_Command_InterruptsDebounce verifies that a clear command arriving
// during a debounce window cancels the window and the normal messages are marked failed.
func TestGateway_Command_InterruptsDebounce(t *testing.T) {
	store := newMock(true)
	g := msgingest.New(store, 200*time.Millisecond)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go g.Run(ctx)

	g.Dispatch(inbound("s1", "hello", "m1"))          // goes into debounce
	g.Dispatch(inbound("s1", "clear", "cmd-1"))       // command: interrupts

	select {
	case msg := <-g.Out():
		if msg.Command != msgingest.CommandClear {
			t.Fatalf("expected CommandClear, got %q", msg.Command)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("timeout waiting for command message")
	}

	// The debounced normal message should NOT appear
	select {
	case extra := <-g.Out():
		t.Fatalf("debounced message should have been cancelled, got: %+v", extra)
	case <-time.After(400 * time.Millisecond):
		// expected
	}

	// The normal message ID should have been marked terminal (failed)
	if len(store.failed) == 0 {
		t.Error("expected debounced message to be marked failed")
	}
}

// TestGateway_Command_EmitsOnOut verifies that a command message appears on Out()
// with the correct Command field.
func TestGateway_Command_EmitsOnOut(t *testing.T) {
	g := msgingest.New(newMock(true), 100*time.Millisecond)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go g.Run(ctx)

	g.Dispatch(inbound("s1", "clear", "cmd-1"))

	select {
	case msg := <-g.Out():
		if msg.Command != msgingest.CommandClear {
			t.Fatalf("expected CommandClear, got %q", msg.Command)
		}
		if msg.SessionKey != "s1" {
			t.Fatalf("expected SessionKey=s1, got %q", msg.SessionKey)
		}
	case <-time.After(300 * time.Millisecond):
		t.Fatal("timeout waiting for command message")
	}
}
