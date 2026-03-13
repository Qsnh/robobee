package store

import (
	"context"
	"testing"
	"time"

	"github.com/robobee/core/internal/model"
)

func newTaskStoreForTest(t *testing.T) (*TaskStore, func()) {
	t.Helper()
	db, err := InitDB(t.TempDir() + "/test.db")
	if err != nil {
		t.Fatalf("InitDB: %v", err)
	}
	// Insert prerequisite rows matching the actual schema (raw, platform_msg_id required)
	db.Exec(`INSERT INTO workers (id,name,work_dir,status,created_at,updated_at) VALUES ('w1','W','/','idle',1,1)`)
	db.Exec(`INSERT INTO platform_messages
        (id, session_key, platform, content, raw, platform_msg_id, received_at)
        VALUES ('m1','feishu:c:u','feishu','hi','','',1)`)
	return NewTaskStore(db), func() { db.Close() }
}

func TestTaskStore_Create_And_Get(t *testing.T) {
	ts, cleanup := newTaskStoreForTest(t)
	defer cleanup()

	now := time.Now().UnixMilli()
	task := model.Task{
		MessageID:   "m1",
		WorkerID:    "w1",
		Instruction: "do it",
		Type:        model.TaskTypeImmediate,
		Status:      model.TaskStatusPending,
		CreatedAt:   now,
		UpdatedAt:   now,
	}

	id, err := ts.Create(context.Background(), task)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if id == "" {
		t.Fatal("expected non-empty task ID")
	}

	got, err := ts.GetByID(context.Background(), id)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if got.Instruction != "do it" {
		t.Errorf("instruction: want %q got %q", "do it", got.Instruction)
	}
	if got.Type != model.TaskTypeImmediate {
		t.Errorf("type: want immediate got %q", got.Type)
	}
}

func TestTaskStore_ClaimDueTasks_ImmediateOnly(t *testing.T) {
	ts, cleanup := newTaskStoreForTest(t)
	defer cleanup()

	now := time.Now().UnixMilli()
	ts.Create(context.Background(), model.Task{
		MessageID: "m1", WorkerID: "w1", Instruction: "go",
		Type: model.TaskTypeImmediate, Status: model.TaskStatusPending,
		CreatedAt: now, UpdatedAt: now,
	})

	tasks, err := ts.ClaimDueTasks(context.Background(), now)
	if err != nil {
		t.Fatalf("ClaimDueTasks: %v", err)
	}
	if len(tasks) != 1 {
		t.Fatalf("expected 1 due task, got %d", len(tasks))
	}
	if tasks[0].Status != model.TaskStatusRunning {
		t.Errorf("claimed task should have status running, got %q", tasks[0].Status)
	}
}

func TestTaskStore_ClaimDueTasks_Idempotent(t *testing.T) {
	ts, cleanup := newTaskStoreForTest(t)
	defer cleanup()

	now := time.Now().UnixMilli()
	ts.Create(context.Background(), model.Task{
		MessageID: "m1", WorkerID: "w1", Instruction: "go",
		Type: model.TaskTypeImmediate, Status: model.TaskStatusPending,
		CreatedAt: now, UpdatedAt: now,
	})

	tasks1, _ := ts.ClaimDueTasks(context.Background(), now)
	tasks2, _ := ts.ClaimDueTasks(context.Background(), now)
	if len(tasks1) != 1 {
		t.Errorf("first claim: want 1, got %d", len(tasks1))
	}
	if len(tasks2) != 0 {
		t.Errorf("second claim should be empty (already running), got %d", len(tasks2))
	}
}

func TestTaskStore_SetExecution(t *testing.T) {
	ts, cleanup := newTaskStoreForTest(t)
	defer cleanup()

	now := time.Now().UnixMilli()
	id, _ := ts.Create(context.Background(), model.Task{
		MessageID: "m1", WorkerID: "w1", Instruction: "go",
		Type: model.TaskTypeImmediate, Status: model.TaskStatusPending,
		CreatedAt: now, UpdatedAt: now,
	})

	err := ts.SetExecution(context.Background(), id, "exec-1", model.TaskStatusCompleted)
	if err != nil {
		t.Fatalf("SetExecution: %v", err)
	}

	got, _ := ts.GetByID(context.Background(), id)
	if got.ExecutionID != "exec-1" {
		t.Errorf("execution_id: want exec-1 got %q", got.ExecutionID)
	}
	if got.Status != model.TaskStatusCompleted {
		t.Errorf("status: want completed got %q", got.Status)
	}
}

func TestTaskStore_DeleteByMessageIDs(t *testing.T) {
	ts, cleanup := newTaskStoreForTest(t)
	defer cleanup()

	now := time.Now().UnixMilli()
	ts.Create(context.Background(), model.Task{
		MessageID: "m1", WorkerID: "w1", Instruction: "go",
		Type: model.TaskTypeImmediate, Status: model.TaskStatusPending,
		CreatedAt: now, UpdatedAt: now,
	})

	err := ts.DeletePendingByMessageIDs(context.Background(), []string{"m1"})
	if err != nil {
		t.Fatalf("DeletePendingByMessageIDs: %v", err)
	}

	// Verify no pending tasks remain
	tasks, _ := ts.ClaimDueTasks(context.Background(), now)
	if len(tasks) != 0 {
		t.Errorf("expected 0 tasks after delete, got %d", len(tasks))
	}
}

func TestTaskStore_ListByMessageID(t *testing.T) {
	ts, cleanup := newTaskStoreForTest(t)
	defer cleanup()

	now := time.Now().UnixMilli()
	ts.Create(context.Background(), model.Task{
		MessageID: "m1", WorkerID: "w1", Instruction: "a",
		Type: model.TaskTypeImmediate, Status: model.TaskStatusPending,
		CreatedAt: now, UpdatedAt: now,
	})
	ts.Create(context.Background(), model.Task{
		MessageID: "m1", WorkerID: "w1", Instruction: "b",
		Type: model.TaskTypeCountdown, Status: model.TaskStatusPending,
		CreatedAt: now, UpdatedAt: now,
	})

	tasks, err := ts.ListByMessageID(context.Background(), "m1", "")
	if err != nil {
		t.Fatalf("ListByMessageID: %v", err)
	}
	if len(tasks) != 2 {
		t.Errorf("expected 2 tasks, got %d", len(tasks))
	}
}

func TestTaskStore_ResetRunningToPending(t *testing.T) {
	ts, cleanup := newTaskStoreForTest(t)
	defer cleanup()

	now := time.Now().UnixMilli()
	id, _ := ts.Create(context.Background(), model.Task{
		MessageID: "m1", WorkerID: "w1", Instruction: "go",
		Type: model.TaskTypeImmediate, Status: model.TaskStatusRunning,
		CreatedAt: now, UpdatedAt: now,
	})

	n, err := ts.ResetRunningToPending(context.Background())
	if err != nil {
		t.Fatalf("ResetRunningToPending: %v", err)
	}
	if n != 1 {
		t.Errorf("expected 1 reset, got %d", n)
	}

	got, _ := ts.GetByID(context.Background(), id)
	if got.Status != model.TaskStatusPending {
		t.Errorf("expected pending, got %q", got.Status)
	}
}
