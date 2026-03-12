package msgsender_test

import (
	"context"
	"testing"
	"time"

	"github.com/robobee/core/internal/msgsender"
	"github.com/robobee/core/internal/platform"
)

type mockSender struct {
	sent []platform.OutboundMessage
}

func (s *mockSender) Send(_ context.Context, msg platform.OutboundMessage) error {
	s.sent = append(s.sent, msg)
	return nil
}

func replyTo(platformID, sessionKey string) platform.InboundMessage {
	return platform.InboundMessage{Platform: platformID, SessionKey: sessionKey}
}

// TestGateway_SendsACK verifies that ACK events are delivered to the platform sender.
func TestGateway_SendsACK(t *testing.T) {
	sender := &mockSender{}
	in := make(chan msgsender.SenderEvent, 1)
	g := msgsender.New(map[string]platform.PlatformSenderAdapter{"test": sender}, in)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go g.Run(ctx)

	in <- msgsender.SenderEvent{
		Type:    msgsender.SenderEventACK,
		ReplyTo: replyTo("test", "test:c:u"),
		Content: "⏳ 正在处理，请稍候…",
	}

	time.Sleep(100 * time.Millisecond)

	if len(sender.sent) != 1 {
		t.Fatalf("expected 1 send call, got %d", len(sender.sent))
	}
	if sender.sent[0].Content != "⏳ 正在处理，请稍候…" {
		t.Fatalf("unexpected content: %q", sender.sent[0].Content)
	}
}

// TestGateway_UnknownPlatform_DoesNotPanic verifies that unknown platform IDs are
// logged and skipped without crashing.
func TestGateway_UnknownPlatform_DoesNotPanic(t *testing.T) {
	in := make(chan msgsender.SenderEvent, 1)
	g := msgsender.New(map[string]platform.PlatformSenderAdapter{}, in) // no senders

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go g.Run(ctx)

	in <- msgsender.SenderEvent{
		Type:    msgsender.SenderEventResult,
		ReplyTo: replyTo("unknown", "unknown:c:u"),
		Content: "hello",
	}

	// Just verify no panic after a short wait
	time.Sleep(100 * time.Millisecond)
}
