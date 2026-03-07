package store

import (
	"encoding/json"
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
	ts := NewTaskStore(db)
	es := NewExecutionStore(db)

	w, _ := ws.Create(model.Worker{Name: "Bot", Email: "bot@robobee.local", RuntimeType: model.RuntimeClaudeCode, WorkDir: "/tmp/bot"})
	recipients, _ := json.Marshal([]string{"user@example.com"})
	task, _ := ts.Create(model.Task{WorkerID: w.ID, Name: "Task1", Plan: "do stuff", TriggerType: model.TriggerManual, Recipients: recipients})

	exec, err := es.Create(task.ID)
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
	if got.TaskID != task.ID {
		t.Errorf("expected task_id %s, got %s", task.ID, got.TaskID)
	}
}

func TestExecutionStore_UpdateStatus(t *testing.T) {
	db, err := InitDB(t.TempDir() + "/test.db")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	ws := NewWorkerStore(db)
	ts := NewTaskStore(db)
	es := NewExecutionStore(db)

	w, _ := ws.Create(model.Worker{Name: "Bot", Email: "bot@robobee.local", RuntimeType: model.RuntimeClaudeCode, WorkDir: "/tmp/bot"})
	recipients, _ := json.Marshal([]string{"user@example.com"})
	task, _ := ts.Create(model.Task{WorkerID: w.ID, Name: "Task1", Plan: "do stuff", TriggerType: model.TriggerManual, Recipients: recipients})
	exec, _ := es.Create(task.ID)

	err = es.UpdateStatus(exec.ID, model.ExecStatusRunning)
	if err != nil {
		t.Fatalf("UpdateStatus: %v", err)
	}
	got, _ := es.GetByID(exec.ID)
	if got.Status != model.ExecStatusRunning {
		t.Errorf("expected running, got %s", got.Status)
	}
}

func TestExecutionStore_GetBySessionID(t *testing.T) {
	db, err := InitDB(t.TempDir() + "/test.db")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	ws := NewWorkerStore(db)
	ts := NewTaskStore(db)
	es := NewExecutionStore(db)

	w, _ := ws.Create(model.Worker{Name: "Bot", Email: "bot@robobee.local", RuntimeType: model.RuntimeClaudeCode, WorkDir: "/tmp/bot"})
	recipients, _ := json.Marshal([]string{"user@example.com"})
	task, _ := ts.Create(model.Task{WorkerID: w.ID, Name: "Task1", Plan: "do stuff", TriggerType: model.TriggerManual, Recipients: recipients})
	exec, _ := es.Create(task.ID)

	got, err := es.GetBySessionID(exec.SessionID)
	if err != nil {
		t.Fatalf("GetBySessionID: %v", err)
	}
	if got.ID != exec.ID {
		t.Errorf("expected ID %s, got %s", exec.ID, got.ID)
	}
}
