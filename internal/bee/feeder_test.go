package bee_test

import (
	"context"
	"database/sql"
	"fmt"
	"testing"
	"time"

	"github.com/robobee/core/internal/bee"
	"github.com/robobee/core/internal/dispatcher"
	"github.com/robobee/core/internal/store"
)

func setupFeederDB(t *testing.T) (*sql.DB, *store.MessageStore, *store.TaskStore) {
	t.Helper()
	db, err := store.InitDB(t.TempDir() + "/test.db")
	if err != nil {
		t.Fatalf("InitDB: %v", err)
	}
	db.Exec(`INSERT INTO platform_messages (id, session_key, platform, content, status, received_at, created_at, updated_at)
             VALUES ('m1', 'feishu:c:u', 'feishu', 'hello', 'received', 1, 1, 1)`)
	return db, store.NewMessageStore(db), store.NewTaskStore(db)
}

// mockBeeRunner is a test double that records the prompt it was called with.
type mockBeeRunner struct {
	calledWithPrompt string
	err              error
}

func (m *mockBeeRunner) Run(_ context.Context, _, prompt string) error {
	m.calledWithPrompt = prompt
	return m.err
}

func TestFeeder_ClaimsMessages_And_InvokesBee(t *testing.T) {
	db, ms, ts := setupFeederDB(t)
	defer db.Close()

	runner := &mockBeeRunner{}
	clearCh := make(chan dispatcher.DispatchTask, 10)
	cfg := bee.FeederConfig{
		Interval:           50 * time.Millisecond,
		BatchSize:          5,
		Timeout:            5 * time.Second,
		QueueWarnThreshold: 100,
		WorkDir:            t.TempDir(),
	}

	f := bee.NewFeeder(ms, ts, runner, clearCh, cfg)
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()

	go f.Run(ctx)
	time.Sleep(200 * time.Millisecond)

	if runner.calledWithPrompt == "" {
		t.Error("expected bee runner to be called with a prompt")
	}

	// Message should now be bee_processed
	var status string
	db.QueryRow(`SELECT status FROM platform_messages WHERE id='m1'`).Scan(&status)
	if status != "bee_processed" {
		t.Errorf("expected bee_processed, got %q", status)
	}
}

func TestFeeder_RollsBack_OnBeeFailure(t *testing.T) {
	db, ms, ts := setupFeederDB(t)
	defer db.Close()

	runner := &mockBeeRunner{err: fmt.Errorf("bee crashed")}
	clearCh := make(chan dispatcher.DispatchTask, 10)
	cfg := bee.FeederConfig{
		Interval:           50 * time.Millisecond,
		BatchSize:          5,
		Timeout:            5 * time.Second,
		QueueWarnThreshold: 100,
		WorkDir:            t.TempDir(),
	}

	f := bee.NewFeeder(ms, ts, runner, clearCh, cfg)
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	go f.Run(ctx)
	time.Sleep(150 * time.Millisecond)

	var status string
	db.QueryRow(`SELECT status FROM platform_messages WHERE id='m1'`).Scan(&status)
	if status != "received" {
		t.Errorf("expected rollback to received, got %q", status)
	}
}
