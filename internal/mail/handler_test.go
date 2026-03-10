package mail

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/robobee/core/internal/model"
)

// --- Stubs ---

type stubSender struct {
	sent []OutgoingEmail
	err  error
}

func (s *stubSender) Send(msg OutgoingEmail) error {
	s.sent = append(s.sent, msg)
	return s.err
}

type stubRouter struct {
	workerID string
	err      error
}

func (r *stubRouter) Route(ctx context.Context, message string) (string, error) {
	return r.workerID, r.err
}

type stubSessionStore struct {
	sessions map[string]*stubSession
}
type stubSession struct {
	workerID        string
	sessionID       string
	lastExecutionID string
}

func newStubSessionStore() *stubSessionStore {
	return &stubSessionStore{sessions: make(map[string]*stubSession)}
}

func (s *stubSessionStore) GetSession(threadID string) (threadSession, error) {
	if sess, ok := s.sessions[threadID]; ok {
		return &mailSessionAdapter{workerID: sess.workerID, sessionID: sess.sessionID, lastExecutionID: sess.lastExecutionID}, nil
	}
	return nil, nil
}

func (s *stubSessionStore) UpsertSession(threadID, workerID, sessionID, lastExecutionID string) error {
	s.sessions[threadID] = &stubSession{workerID: workerID, sessionID: sessionID, lastExecutionID: lastExecutionID}
	return nil
}

type mailSessionAdapter struct {
	workerID        string
	sessionID       string
	lastExecutionID string
}

func (a *mailSessionAdapter) GetWorkerID() string        { return a.workerID }
func (a *mailSessionAdapter) GetSessionID() string       { return a.sessionID }
func (a *mailSessionAdapter) GetLastExecutionID() string { return a.lastExecutionID }

type stubManager struct {
	exec model.WorkerExecution
	err  error
}

func (m *stubManager) ExecuteWorker(ctx context.Context, workerID, input string) (model.WorkerExecution, error) {
	return m.exec, m.err
}

func (m *stubManager) ReplyExecution(ctx context.Context, execID, input string) (model.WorkerExecution, error) {
	return m.exec, m.err
}

func (m *stubManager) GetExecution(execID string) (model.WorkerExecution, error) {
	return m.exec, m.err
}

// --- Tests ---

// TestHandler_Ack verifies the reply helper sends to the correct address with correct body.
func TestHandler_Ack(t *testing.T) {
	sender := &stubSender{}
	h := newTestHandler(
		&stubRouter{workerID: "worker-1"},
		newStubSessionStore(),
		&stubManager{exec: model.WorkerExecution{Status: model.ExecStatusCompleted}},
		sender,
	)

	em := EmailMessage{
		MessageID: "<msg1@example.com>",
		ThreadID:  "<msg1@example.com>",
		From:      "user@example.com",
		Subject:   "Hello",
		Body:      "deploy the app",
	}
	h.reply(em, ackMessage)

	if len(sender.sent) != 1 {
		t.Fatalf("expected 1 sent email (ack), got %d", len(sender.sent))
	}
	if sender.sent[0].BodyMD != ackMessage {
		t.Errorf("expected ack body, got: %s", sender.sent[0].BodyMD)
	}
	if sender.sent[0].To != "user@example.com" {
		t.Errorf("ack sent to wrong address: %s", sender.sent[0].To)
	}
}

// TestHandler_Process_NoWorkerFound calls process() directly (synchronous) to avoid goroutine races.
func TestHandler_Process_NoWorkerFound(t *testing.T) {
	sender := &stubSender{}
	h := newTestHandler(
		&stubRouter{err: errors.New("no workers")},
		newStubSessionStore(),
		&stubManager{},
		sender,
	)

	em := EmailMessage{
		MessageID: "<msg1@example.com>",
		ThreadID:  "<msg1@example.com>",
		From:      "user@example.com",
		Subject:   "Hello",
		Body:      "deploy the app",
	}
	h.process(em)

	if len(sender.sent) != 1 {
		t.Fatalf("expected 1 sent email (error reply), got %d", len(sender.sent))
	}
	if sender.sent[0].BodyMD != noWorkerMsg {
		t.Errorf("expected noWorkerMsg, got: %s", sender.sent[0].BodyMD)
	}
	if sender.sent[0].To != "user@example.com" {
		t.Errorf("reply to wrong address: %s", sender.sent[0].To)
	}
}

// TestHandler_Process_CompletedExecution calls process() directly (synchronous).
func TestHandler_Process_CompletedExecution(t *testing.T) {
	sender := &stubSender{}
	mgr := &stubManager{
		exec: model.WorkerExecution{
			ID:        "exec-1",
			SessionID: "sess-1",
			Status:    model.ExecStatusCompleted,
			Result:    "## Done\nAll good.",
		},
	}
	h := newTestHandler(
		&stubRouter{workerID: "worker-1"},
		newStubSessionStore(),
		mgr,
		sender,
	)

	em := EmailMessage{
		MessageID: "<msg1@example.com>",
		ThreadID:  "<msg1@example.com>",
		From:      "user@example.com",
		Subject:   "Hello",
		Body:      "deploy the app",
	}
	h.process(em)

	if len(sender.sent) != 1 {
		t.Fatalf("expected 1 sent email (result), got %d", len(sender.sent))
	}
	if sender.sent[0].BodyMD != "## Done\nAll good." {
		t.Errorf("unexpected result body: %s", sender.sent[0].BodyMD)
	}
	if sender.sent[0].InReplyTo != "<msg1@example.com>" {
		t.Errorf("InReplyTo not set: %s", sender.sent[0].InReplyTo)
	}
	if sender.sent[0].Subject != "Re: Hello" {
		t.Errorf("unexpected subject: %s", sender.sent[0].Subject)
	}
}

func newTestHandler(router messageRouter, sessionStore mailSessionStoreIface, mgr executionManager, sender EmailSender) *Handler {
	return &Handler{
		router:       router,
		sessionStore: sessionStore,
		manager:      mgr,
		sender:       sender,
		pollInterval: 0,
		pollTimeout:  5 * time.Second,
	}
}
