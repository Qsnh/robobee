package mail

import (
	"strings"
	"testing"
)

func TestParseThreadID_NoReferences(t *testing.T) {
	id := parseThreadID("", "")
	if id != "" {
		t.Errorf("expected empty string for no message-id, got %q", id)
	}

	id = parseThreadID("<msg-001@example.com>", "")
	if id != "<msg-001@example.com>" {
		t.Errorf("unexpected thread id: %q", id)
	}
}

func TestParseThreadID_WithReferences(t *testing.T) {
	refs := "<root@example.com> <mid@example.com> <leaf@example.com>"
	id := parseThreadID("<leaf@example.com>", refs)
	if id != "<root@example.com>" {
		t.Errorf("expected root message-id, got %q", id)
	}
}

func TestParseThreadID_SingleReference(t *testing.T) {
	id := parseThreadID("<reply@example.com>", "<root@example.com>")
	if id != "<root@example.com>" {
		t.Errorf("expected root message-id, got %q", id)
	}
}

func TestMarkdownToHTML(t *testing.T) {
	md := "**Hello** _world_"
	html := markdownToHTML(md)
	if !strings.Contains(html, "<strong>Hello</strong>") {
		t.Errorf("expected bold tag, got: %s", html)
	}
	if !strings.Contains(html, "<em>world</em>") {
		t.Errorf("expected em tag, got: %s", html)
	}
}
