package store

import (
	"encoding/json"
	"testing"

	"github.com/robobee/core/internal/model"
)

func setupTaskTestDB(t *testing.T) (*TaskStore, *WorkerStore) {
	t.Helper()
	db, err := InitDB(t.TempDir() + "/test.db")
	if err != nil {
		t.Fatalf("InitDB: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return NewTaskStore(db), NewWorkerStore(db)
}

func TestTaskStore_CRUD(t *testing.T) {
	ts, ws := setupTaskTestDB(t)
	w, _ := ws.Create(model.Worker{Name: "Bot", Email: "bot@robobee.local", RuntimeType: model.RuntimeClaudeCode, WorkDir: "/tmp/bot"})

	recipients, _ := json.Marshal([]string{"user@example.com"})
	task := model.Task{
		WorkerID:         w.ID,
		Name:             "Daily Report",
		Plan:             "Generate daily report",
		TriggerType:      model.TriggerManual,
		Recipients:       recipients,
		RequiresApproval: true,
	}

	created, err := ts.Create(task)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if created.ID == "" {
		t.Error("expected non-empty ID")
	}

	got, err := ts.GetByID(created.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if got.Name != "Daily Report" {
		t.Errorf("expected Daily Report, got %s", got.Name)
	}

	list, err := ts.ListByWorkerID(w.ID)
	if err != nil {
		t.Fatalf("ListByWorkerID: %v", err)
	}
	if len(list) != 1 {
		t.Errorf("expected 1 task, got %d", len(list))
	}

	got.Name = "Weekly Report"
	updated, err := ts.Update(got)
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if updated.Name != "Weekly Report" {
		t.Errorf("expected Weekly Report, got %s", updated.Name)
	}

	if err := ts.Delete(created.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
}
