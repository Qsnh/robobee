package worker

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/robobee/core/internal/config"
	"github.com/robobee/core/internal/model"
	"github.com/robobee/core/internal/store"
)

type Manager struct {
	cfg            config.Config
	workerStore    *store.WorkerStore
	executionStore *store.ExecutionStore
	emailStore     *store.EmailStore
	memoryStore    *store.MemoryStore

	activeRuntimes map[string]Runtime     // execution_id -> runtime
	logSubscribers map[string][]chan Output // execution_id -> subscribers
	mu             sync.RWMutex
}

func NewManager(
	cfg config.Config,
	ws *store.WorkerStore,
	es *store.ExecutionStore,
	emailS *store.EmailStore,
	ms *store.MemoryStore,
) *Manager {
	return &Manager{
		cfg:            cfg,
		workerStore:    ws,
		executionStore: es,
		emailStore:     emailS,
		memoryStore:    ms,
		activeRuntimes: make(map[string]Runtime),
		logSubscribers: make(map[string][]chan Output),
	}
}

func (m *Manager) CreateWorker(
	name, description, prompt string,
	runtimeType model.RuntimeType,
	triggerType model.TriggerType,
	cronExpression string,
	recipients []string,
	requiresApproval bool,
) (model.Worker, error) {
	email := fmt.Sprintf("%s@%s", name, m.cfg.SMTP.Domain)
	workDir := filepath.Join(m.cfg.Workers.BaseDir, name)

	if err := os.MkdirAll(workDir, 0755); err != nil {
		return model.Worker{}, fmt.Errorf("create work dir: %w", err)
	}

	// Initialize CLAUDE.md for the worker
	claudeMD := filepath.Join(workDir, "CLAUDE.md")
	initialContent := fmt.Sprintf("# %s\n\n**Role:** %s\n\n## Work Memories\n\n", name, description)
	if err := os.WriteFile(claudeMD, []byte(initialContent), 0644); err != nil {
		return model.Worker{}, fmt.Errorf("create CLAUDE.md: %w", err)
	}

	recipientsJSON, _ := json.Marshal(recipients)

	return m.workerStore.Create(model.Worker{
		Name:             name,
		Description:      description,
		Prompt:           prompt,
		Email:            email,
		RuntimeType:      runtimeType,
		WorkDir:          workDir,
		TriggerType:      triggerType,
		CronExpression:   cronExpression,
		Recipients:       recipientsJSON,
		RequiresApproval: requiresApproval,
	})
}

func (m *Manager) ExecuteWorker(ctx context.Context, workerID, triggerInput string) (model.WorkerExecution, error) {
	worker, err := m.workerStore.GetByID(workerID)
	if err != nil {
		return model.WorkerExecution{}, fmt.Errorf("get worker: %w", err)
	}

	exec, err := m.executionStore.Create(workerID, triggerInput)
	if err != nil {
		return model.WorkerExecution{}, fmt.Errorf("create execution: %w", err)
	}

	// Update worker status
	if err := m.workerStore.UpdateStatus(worker.ID, model.WorkerStatusWorking); err != nil {
		log.Printf("failed to update worker status: %v", err)
	}

	// Create runtime based on worker type
	var rt Runtime
	var timeout time.Duration
	switch worker.RuntimeType {
	case model.RuntimeClaudeCode:
		rt = NewClaudeRuntime(m.cfg.Runtime.ClaudeCode.Binary)
		timeout = m.cfg.Runtime.ClaudeCode.Timeout
	case model.RuntimeCodex:
		rt = NewCodexRuntime(m.cfg.Runtime.Codex.Binary)
		timeout = m.cfg.Runtime.Codex.Timeout
	default:
		return model.WorkerExecution{}, fmt.Errorf("unknown runtime: %s", worker.RuntimeType)
	}

	// Decouple from caller context; apply configured timeout if set
	execCtx := ctx
	if timeout > 0 {
		var cancel context.CancelFunc
		execCtx, cancel = context.WithTimeout(ctx, timeout)
		_ = cancel
	}

	// Build the prompt: base prompt + trigger input for message-triggered workers
	prompt := worker.Prompt
	if triggerInput != "" && triggerInput != "scheduled" {
		prompt = fmt.Sprintf("%s\n\n---\nMessage:\n%s", worker.Prompt, triggerInput)
	}

	// Start execution in background
	outputCh, err := rt.Execute(execCtx, worker.WorkDir, prompt)
	if err != nil {
		m.executionStore.UpdateResult(exec.ID, err.Error(), model.ExecStatusFailed)
		m.workerStore.UpdateStatus(worker.ID, model.WorkerStatusError)
		return exec, fmt.Errorf("start runtime: %w", err)
	}

	// Store active runtime
	m.mu.Lock()
	m.activeRuntimes[exec.ID] = rt
	m.mu.Unlock()

	// Update PID
	if cr, ok := rt.(*ClaudeRuntime); ok {
		m.executionStore.UpdatePID(exec.ID, cr.PID())
	} else if cx, ok := rt.(*CodexRuntime); ok {
		m.executionStore.UpdatePID(exec.ID, cx.PID())
	}

	// Monitor output in background
	go m.monitorExecution(exec, worker, outputCh)

	return exec, nil
}

func (m *Manager) monitorExecution(exec model.WorkerExecution, worker model.Worker, outputCh <-chan Output) {
	var result string

	for out := range outputCh {
		// Broadcast to WebSocket subscribers
		m.mu.RLock()
		subs := m.logSubscribers[exec.ID]
		m.mu.RUnlock()

		for _, sub := range subs {
			select {
			case sub <- out:
			default:
			}
		}

		switch out.Type {
		case OutputStdout:
			result += out.Content + "\n"
		case OutputDone:
			if worker.RequiresApproval {
				m.executionStore.UpdateResult(exec.ID, result, model.ExecStatusAwaitingApproval)
			} else {
				m.executionStore.UpdateResult(exec.ID, result, model.ExecStatusCompleted)
			}
			m.workerStore.UpdateStatus(worker.ID, model.WorkerStatusIdle)
		case OutputError:
			m.executionStore.UpdateResult(exec.ID, result+"\nERROR: "+out.Content, model.ExecStatusFailed)
			m.workerStore.UpdateStatus(worker.ID, model.WorkerStatusError)
		}
	}

	// Cleanup
	m.mu.Lock()
	delete(m.activeRuntimes, exec.ID)
	for _, sub := range m.logSubscribers[exec.ID] {
		close(sub)
	}
	delete(m.logSubscribers, exec.ID)
	m.mu.Unlock()
}

func (m *Manager) SubscribeLogs(executionID string) <-chan Output {
	m.mu.Lock()
	defer m.mu.Unlock()

	ch := make(chan Output, 100)
	m.logSubscribers[executionID] = append(m.logSubscribers[executionID], ch)
	return ch
}

func (m *Manager) StopExecution(executionID string) error {
	m.mu.RLock()
	rt, ok := m.activeRuntimes[executionID]
	m.mu.RUnlock()

	if !ok {
		return fmt.Errorf("no active runtime for execution %s", executionID)
	}
	return rt.Stop()
}
