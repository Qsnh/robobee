package store

import (
	"testing"

	"github.com/robobee/core/internal/model"
)

func TestExecutionStore_CreateAndGet(t *testing.T) {
	db, err := InitDB(t.TempDir() + "/test.db")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	ws := NewWorkerStore(db)
	es := NewExecutionStore(db)

	w, _ := ws.Create(model.Worker{Name: "Bot", WorkDir: "/tmp/bot"})

	exec, err := es.Create(w.ID, "test message")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if exec.Status != model.ExecStatusPending {
		t.Errorf("expected pending, got %s", exec.Status)
	}
	if exec.SessionID == "" {
		t.Error("expected non-empty session_id")
	}

	got, err := es.GetByID(exec.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if got.WorkerID != w.ID {
		t.Errorf("expected worker_id %s, got %s", w.ID, got.WorkerID)
	}
}

func TestExecutionStore_UpdateStatus(t *testing.T) {
	db, err := InitDB(t.TempDir() + "/test.db")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	ws := NewWorkerStore(db)
	es := NewExecutionStore(db)

	w, _ := ws.Create(model.Worker{Name: "Bot", WorkDir: "/tmp/bot"})
	exec, _ := es.Create(w.ID, "test message")

	err = es.UpdateStatus(exec.ID, model.ExecStatusRunning)
	if err != nil {
		t.Fatalf("UpdateStatus: %v", err)
	}
	got, _ := es.GetByID(exec.ID)
	if got.Status != model.ExecStatusRunning {
		t.Errorf("expected running, got %s", got.Status)
	}
}

func TestExecutionStore_Create_StartedAtMillisecondPrecision(t *testing.T) {
	db, err := InitDB(t.TempDir() + "/test.db")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	ws := NewWorkerStore(db)
	es := NewExecutionStore(db)

	w, _ := ws.Create(model.Worker{Name: "Bot", WorkDir: "/tmp/bot"})
	exec, err := es.Create(w.ID, "test")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Verify the raw DB value has millisecond precision
	var startedAt string
	err = db.QueryRow(`SELECT started_at FROM worker_executions WHERE id = ?`, exec.ID).Scan(&startedAt)
	if err != nil {
		t.Fatalf("scan started_at: %v", err)
	}
	if len(startedAt) < 24 || startedAt[19] != '.' || startedAt[len(startedAt)-1] != 'Z' {
		t.Errorf("started_at %q: want millisecond format like 2026-03-11T10:30:00.123Z", startedAt)
	}

	// Verify the returned struct still has a valid *time.Time
	if exec.StartedAt == nil {
		t.Error("exec.StartedAt must not be nil")
	}
}

func TestExecutionStore_UpdateResult_CompletedAtMillisecondPrecision(t *testing.T) {
	db, err := InitDB(t.TempDir() + "/test.db")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	ws := NewWorkerStore(db)
	es := NewExecutionStore(db)

	w, _ := ws.Create(model.Worker{Name: "Bot", WorkDir: "/tmp/bot"})
	exec, _ := es.Create(w.ID, "test")

	if err := es.UpdateResult(exec.ID, "output", model.ExecStatusCompleted); err != nil {
		t.Fatalf("UpdateResult: %v", err)
	}

	var completedAt string
	err = db.QueryRow(`SELECT completed_at FROM worker_executions WHERE id = ?`, exec.ID).Scan(&completedAt)
	if err != nil {
		t.Fatalf("scan completed_at: %v", err)
	}
	if len(completedAt) < 24 || completedAt[19] != '.' || completedAt[len(completedAt)-1] != 'Z' {
		t.Errorf("completed_at %q: want millisecond format like 2026-03-11T10:30:00.123Z", completedAt)
	}
}

func TestExecutionStore_GetBySessionID(t *testing.T) {
	db, err := InitDB(t.TempDir() + "/test.db")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	ws := NewWorkerStore(db)
	es := NewExecutionStore(db)

	w, _ := ws.Create(model.Worker{Name: "Bot", WorkDir: "/tmp/bot"})
	exec, _ := es.Create(w.ID, "test message")

	got, err := es.GetBySessionID(exec.SessionID)
	if err != nil {
		t.Fatalf("GetBySessionID: %v", err)
	}
	if got.ID != exec.ID {
		t.Errorf("expected ID %s, got %s", exec.ID, got.ID)
	}
}
