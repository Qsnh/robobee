package msgingest_test

import (
	"context"
	"testing"
	"time"

	"github.com/robobee/core/internal/msgingest"
	"github.com/robobee/core/internal/platform"
)

type createCall struct {
	id          string
	messageTime int64
}

type mockMsgStore struct {
	insertResult bool
	creates      []createCall
	failed       []string
	merged       map[string][]string
}

func newMock(inserted bool) *mockMsgStore {
	return &mockMsgStore{insertResult: inserted, merged: make(map[string][]string)}
}

func (m *mockMsgStore) Create(_ context.Context, id, _, _, _, _, _ string, messageTime int64) (bool, error) {
	m.creates = append(m.creates, createCall{id: id, messageTime: messageTime})
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

func inboundWithTime(sessionKey, content, platformMsgID string, messageTime int64) platform.InboundMessage {
	return platform.InboundMessage{
		Platform:          "test",
		SessionKey:        sessionKey,
		Content:           content,
		PlatformMessageID: platformMsgID,
		MessageTime:       messageTime,
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

// TestGateway_Historical_EmittedImmediately verifies that a message older than the
// debounce window is emitted immediately without waiting for the timer.
func TestGateway_Historical_EmittedImmediately(t *testing.T) {
	const debounce = 100 * time.Millisecond
	g := msgingest.New(newMock(true), debounce)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go g.Run(ctx)

	// Message sent 10 seconds ago — well past the debounce window.
	oldTime := time.Now().Add(-10 * time.Second).UnixMilli()
	g.Dispatch(inboundWithTime("s1", "old message", "m1", oldTime))

	select {
	case msg := <-g.Out():
		if msg.Content != "old message" {
			t.Fatalf("unexpected content: %q", msg.Content)
		}
		if msg.Command != msgingest.CommandNone {
			t.Fatalf("unexpected command: %q", msg.Command)
		}
	case <-time.After(50 * time.Millisecond):
		// Historical messages must arrive well before the debounce window expires.
		t.Fatal("timeout: historical message was not emitted immediately")
	}
}

// TestGateway_Historical_TwoMessages_EmittedIndependently verifies that two historical
// messages for the same session are each emitted as separate IngestedMessages.
func TestGateway_Historical_TwoMessages_EmittedIndependently(t *testing.T) {
	const debounce = 100 * time.Millisecond
	g := msgingest.New(newMock(true), debounce)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go g.Run(ctx)

	oldTime := time.Now().Add(-10 * time.Second).UnixMilli()
	g.Dispatch(inboundWithTime("s1", "first", "m1", oldTime))
	g.Dispatch(inboundWithTime("s1", "second", "m2", oldTime))

	received := make([]string, 0, 2)
	for i := 0; i < 2; i++ {
		select {
		case msg := <-g.Out():
			received = append(received, msg.Content)
		case <-time.After(50 * time.Millisecond):
			t.Fatalf("timeout waiting for message %d", i+1)
		}
	}
	if len(received) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(received))
	}
	// Ensure no third message appears (no merging).
	select {
	case extra := <-g.Out():
		t.Fatalf("unexpected extra message: %+v", extra)
	case <-time.After(200 * time.Millisecond):
		// expected
	}
}

// TestGateway_Historical_DoesNotCancelRunningTimer verifies that a historical message
// arriving while a debounce timer is active does not cancel the timer.
func TestGateway_Historical_DoesNotCancelRunningTimer(t *testing.T) {
	const debounce = 150 * time.Millisecond
	g := msgingest.New(newMock(true), debounce)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go g.Run(ctx)

	// Start a normal debounce for "real" message.
	g.Dispatch(inbound("s1", "real message", "m1"))

	// Immediately send a historical message for the same session.
	oldTime := time.Now().Add(-10 * time.Second).UnixMilli()
	g.Dispatch(inboundWithTime("s1", "old message", "m2", oldTime))

	// Historical message should arrive first (immediately).
	select {
	case msg := <-g.Out():
		if msg.Content != "old message" {
			t.Fatalf("expected historical message first, got %q", msg.Content)
		}
	case <-time.After(50 * time.Millisecond):
		t.Fatal("timeout waiting for historical message")
	}

	// Real message should still arrive after the debounce timer fires.
	select {
	case msg := <-g.Out():
		if msg.Content != "real message" {
			t.Fatalf("expected real message after debounce, got %q", msg.Content)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("timeout: debounce timer appears to have been cancelled")
	}
}

// TestGateway_RealTime_EntersDebounceNormally verifies that a message with age < debounce
// enters the debounce state machine and is not emitted before the timer fires.
func TestGateway_RealTime_EntersDebounceNormally(t *testing.T) {
	const debounce = 150 * time.Millisecond
	g := msgingest.New(newMock(true), debounce)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go g.Run(ctx)

	// Message sent just now — age ≈ 0, well within debounce window.
	g.Dispatch(inboundWithTime("s1", "fresh", "m1", time.Now().UnixMilli()))

	// Should NOT arrive before debounce fires.
	select {
	case msg := <-g.Out():
		t.Fatalf("message arrived too early (before debounce): %+v", msg)
	case <-time.After(50 * time.Millisecond):
		// expected: still in debounce window
	}

	// Should arrive after debounce fires.
	select {
	case msg := <-g.Out():
		if msg.Content != "fresh" {
			t.Fatalf("unexpected content: %q", msg.Content)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("timeout waiting for debounced message")
	}
}

// TestGateway_FutureTimestamp_TreatedAsRealTime verifies that a message with a future
// MessageTime falls through to the normal debounce path (not emitted immediately).
func TestGateway_FutureTimestamp_TreatedAsRealTime(t *testing.T) {
	const debounce = 150 * time.Millisecond
	g := msgingest.New(newMock(true), debounce)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go g.Run(ctx)

	futureTime := time.Now().Add(10 * time.Second).UnixMilli()
	g.Dispatch(inboundWithTime("s1", "future", "m1", futureTime))

	// Must NOT arrive immediately (goes to normal debounce).
	select {
	case msg := <-g.Out():
		t.Fatalf("future-timestamp message arrived immediately (should debounce): %+v", msg)
	case <-time.After(50 * time.Millisecond):
		// expected
	}
}
