package mail

import (
	"context"
	"testing"

	"github.com/robobee/core/internal/platform"
)

// --- Stubs ---

type stubSMTPSender struct {
	sent []OutgoingEmail
	err  error
}

func (s *stubSMTPSender) Send(msg OutgoingEmail) error {
	s.sent = append(s.sent, msg)
	return s.err
}

// --- MailSender tests ---

func TestMailSender_Send_PlainReply(t *testing.T) {
	smtp := &stubSMTPSender{}
	sender := &MailSender{smtpSender: smtp}

	em := EmailMessage{
		MessageID: "<msg1@example.com>",
		ThreadID:  "<msg1@example.com>",
		From:      "user@example.com",
		Subject:   "Hello",
	}
	msg := platform.OutboundMessage{
		Content: "⏳ 正在处理，请稍候…",
		ReplyTo: platform.InboundMessage{Raw: em},
	}

	if err := sender.Send(context.Background(), msg); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(smtp.sent) != 1 {
		t.Fatalf("expected 1 sent email, got %d", len(smtp.sent))
	}
	got := smtp.sent[0]
	if got.To != "user@example.com" {
		t.Errorf("To: got %q, want %q", got.To, "user@example.com")
	}
	if got.Subject != "Re: Hello" {
		t.Errorf("Subject: got %q, want %q", got.Subject, "Re: Hello")
	}
	if got.InReplyTo != "<msg1@example.com>" {
		t.Errorf("InReplyTo: got %q, want %q", got.InReplyTo, "<msg1@example.com>")
	}
	if got.BodyMD != "⏳ 正在处理，请稍候…" {
		t.Errorf("BodyMD: got %q", got.BodyMD)
	}
}

func TestMailSender_Send_ThreadedReply(t *testing.T) {
	smtp := &stubSMTPSender{}
	sender := &MailSender{smtpSender: smtp}

	em := EmailMessage{
		MessageID:  "<msg2@example.com>",
		References: "<msg1@example.com>",
		ThreadID:   "<msg1@example.com>",
		From:       "user@example.com",
		Subject:    "Re: Hello",
	}
	msg := platform.OutboundMessage{
		Content: "Done!",
		ReplyTo: platform.InboundMessage{Raw: em},
	}

	if err := sender.Send(context.Background(), msg); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got := smtp.sent[0]
	if got.Subject != "Re: Hello" {
		t.Errorf("Subject: got %q (should not double-prefix)", got.Subject)
	}
	wantRefs := "<msg1@example.com> <msg2@example.com>"
	if got.References != wantRefs {
		t.Errorf("References: got %q, want %q", got.References, wantRefs)
	}
}

func TestMailSender_Send_WrongRawType(t *testing.T) {
	smtp := &stubSMTPSender{}
	sender := &MailSender{smtpSender: smtp}

	msg := platform.OutboundMessage{
		Content: "hello",
		ReplyTo: platform.InboundMessage{Raw: "not-an-email"},
	}

	if err := sender.Send(context.Background(), msg); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(smtp.sent) != 0 {
		t.Errorf("expected no sends for wrong raw type, got %d", len(smtp.sent))
	}
}

// --- subjectMatches tests ---

func TestSubjectMatches_Empty(t *testing.T) {
	if !subjectMatches("Anything", nil) {
		t.Error("empty keywords should match everything")
	}
}

func TestSubjectMatches_Hit(t *testing.T) {
	if !subjectMatches("Deploy the app", []string{"deploy"}) {
		t.Error("should match keyword")
	}
}

func TestSubjectMatches_Miss(t *testing.T) {
	if subjectMatches("Weekly newsletter", []string{"deploy", "build"}) {
		t.Error("should not match")
	}
}
