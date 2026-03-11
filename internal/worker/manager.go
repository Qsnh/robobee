package worker

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/robobee/core/internal/ai"
	"github.com/robobee/core/internal/config"
	"github.com/robobee/core/internal/model"
	"github.com/robobee/core/internal/store"
)

type claudeStreamEvent struct {
	Type    string         `json:"type"`
	Message *claudeMessage `json:"message,omitempty"`
	Result  string         `json:"result,omitempty"`
}

type claudeMessage struct {
	Content []claudeContent `json:"content"`
}

type claudeContent struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
}

type Manager struct {
	cfg            config.Config
	workerStore    *store.WorkerStore
	executionStore *store.ExecutionStore
	cronResolver   ai.CronResolver

	activeRuntimes map[string]Runtime      // execution_id -> runtime
	logSubscribers map[string][]chan Output // execution_id -> subscribers
	mu             sync.RWMutex
}

func NewManager(
	cfg config.Config,
	ws *store.WorkerStore,
	es *store.ExecutionStore,
	cronResolver ai.CronResolver,
) *Manager {
	return &Manager{
		cfg:            cfg,
		workerStore:    ws,
		executionStore: es,
		cronResolver:   cronResolver,
		activeRuntimes: make(map[string]Runtime),
		logSubscribers: make(map[string][]chan Output),
	}
}

func (m *Manager) ResolveCron(ctx context.Context, description string) (string, error) {
	return m.cronResolver.CronFromDescription(ctx, description)
}

func (m *Manager) CreateWorker(
	name, description, prompt string,
	scheduleDescription string,
	scheduleEnabled bool,
	workDir string,
) (model.Worker, error) {
	id := uuid.New().String()
	if workDir == "" {
		workDir = filepath.Join(m.cfg.Workers.BaseDir, id)
	}

	if err := os.MkdirAll(workDir, 0755); err != nil {
		return model.Worker{}, fmt.Errorf("create work dir: %w", err)
	}

	// Initialize CLAUDE.md only if it doesn't already exist
	claudeMD := filepath.Join(workDir, "CLAUDE.md")
	if _, err := os.Stat(claudeMD); os.IsNotExist(err) {
		initialContent := fmt.Sprintf("# %s\n\n**Role:** %s\n\n## Work Memories\n\n", name, description)
		if err := os.WriteFile(claudeMD, []byte(initialContent), 0644); err != nil {
			return model.Worker{}, fmt.Errorf("create CLAUDE.md: %w", err)
		}
	}

	var cronExpression string
	if scheduleEnabled && scheduleDescription != "" {
		var err error
		cronExpression, err = m.cronResolver.CronFromDescription(context.Background(), scheduleDescription)
		if err != nil {
			return model.Worker{}, fmt.Errorf("generate cron expression: %w", err)
		}
	}

	return m.workerStore.Create(model.Worker{
		ID:                  id,
		Name:                name,
		Description:         description,
		Prompt:              prompt,
		WorkDir:             workDir,
		CronExpression:      cronExpression,
		ScheduleDescription: scheduleDescription,
		ScheduleEnabled:     scheduleEnabled,
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

	rt := NewClaudeRuntime(m.cfg.Runtime.ClaudeCode.Binary)
	timeout := m.cfg.Runtime.ClaudeCode.Timeout

	// Build the prompt: base prompt + trigger input
	prompt := worker.Prompt
	if triggerInput != "" && triggerInput != "scheduled" {
		if worker.Prompt != "" {
			prompt = fmt.Sprintf("%s\n\n---\nMessage:\n%s", worker.Prompt, triggerInput)
		} else {
			prompt = triggerInput
		}
	}

	if err := m.launchRuntime(exec, worker, rt, timeout, prompt, false); err != nil {
		m.executionStore.UpdateResult(exec.ID, err.Error(), model.ExecStatusFailed)
		m.workerStore.UpdateStatus(worker.ID, model.WorkerStatusError)
		return exec, fmt.Errorf("start runtime: %w", err)
	}

	return exec, nil
}

// launchRuntime applies timeout, starts the runtime, registers it, updates PID, and launches monitoring.
// The execution context is always derived from context.Background() to decouple from the caller's request.
func (m *Manager) launchRuntime(exec model.WorkerExecution, worker model.Worker, rt Runtime, timeout time.Duration, prompt string, resume bool) error {
	var execCtx context.Context
	var cancel context.CancelFunc
	if timeout > 0 {
		execCtx, cancel = context.WithTimeout(context.Background(), timeout)
	} else {
		execCtx, cancel = context.WithCancel(context.Background())
	}

	outputCh, err := rt.Execute(execCtx, worker.WorkDir, prompt, ExecuteOptions{SessionID: exec.SessionID, Resume: resume})
	if err != nil {
		cancel()
		return err
	}

	m.mu.Lock()
	m.activeRuntimes[exec.ID] = rt
	m.mu.Unlock()

	m.executionStore.UpdatePID(exec.ID, rt.PID())
	go m.monitorExecution(exec, worker, outputCh, cancel)
	return nil
}

func (m *Manager) monitorExecution(exec model.WorkerExecution, worker model.Worker, outputCh <-chan Output, cancel context.CancelFunc) {
	defer cancel()
	var rawLogs string
	var lastAssistantText string
	var streamResult string

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
			rawLogs += out.Content + "\n"
			// Parse stream-json to extract assistant text and result
			line := strings.TrimSpace(out.Content)
			if strings.HasPrefix(line, "{") {
				var event claudeStreamEvent
				if err := json.Unmarshal([]byte(line), &event); err == nil {
					switch event.Type {
					case "assistant":
						if event.Message != nil && len(event.Message.Content) > 0 {
							if event.Message.Content[0].Type == "text" && event.Message.Content[0].Text != "" {
								lastAssistantText = event.Message.Content[0].Text
							}
						}
					case "result":
						if event.Result != "" {
							streamResult = event.Result
						}
					}
				}
			}
		case OutputDone:
			// Save raw stdout logs
			m.executionStore.UpdateLogs(exec.ID, rawLogs)
			// Determine result with priority: file > streamResult > lastAssistantText > rawLogs
			result := rawLogs
			if lastAssistantText != "" {
				result = lastAssistantText
			}
			if streamResult != "" {
				result = streamResult
			}
			resultFilePath := filepath.Join(worker.WorkDir, ".robobee_result.txt")
			if data, err := os.ReadFile(resultFilePath); err == nil && len(data) > 0 {
				result = string(data)
				os.Remove(resultFilePath)
			}
			m.executionStore.UpdateResult(exec.ID, result, model.ExecStatusCompleted)
			m.workerStore.UpdateStatus(worker.ID, model.WorkerStatusIdle)
		case OutputError:
			m.executionStore.UpdateLogs(exec.ID, rawLogs)
			m.executionStore.UpdateResult(exec.ID, rawLogs+"\nERROR: "+out.Content, model.ExecStatusFailed)
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

func (m *Manager) ReplyExecution(ctx context.Context, executionID string, message string) (model.WorkerExecution, error) {
	srcExec, err := m.executionStore.GetByID(executionID)
	if err != nil {
		return model.WorkerExecution{}, fmt.Errorf("get execution: %w", err)
	}
	if srcExec.Status == model.ExecStatusRunning || srcExec.Status == model.ExecStatusPending {
		return model.WorkerExecution{}, fmt.Errorf("execution is still running")
	}

	worker, err := m.workerStore.GetByID(srcExec.WorkerID)
	if err != nil {
		return model.WorkerExecution{}, fmt.Errorf("get worker: %w", err)
	}
	newExec, err := m.executionStore.CreateWithSessionID(srcExec.WorkerID, message, srcExec.SessionID)
	if err != nil {
		return model.WorkerExecution{}, fmt.Errorf("create reply execution: %w", err)
	}

	if err := m.workerStore.UpdateStatus(worker.ID, model.WorkerStatusWorking); err != nil {
		log.Printf("failed to update worker status: %v", err)
	}

	rt := NewClaudeRuntime(m.cfg.Runtime.ClaudeCode.Binary)
	timeout := m.cfg.Runtime.ClaudeCode.Timeout

	if err := m.launchRuntime(newExec, worker, rt, timeout, message, true); err != nil {
		m.executionStore.UpdateResult(newExec.ID, err.Error(), model.ExecStatusFailed)
		m.workerStore.UpdateStatus(worker.ID, model.WorkerStatusError)
		return newExec, fmt.Errorf("start runtime: %w", err)
	}

	return newExec, nil
}

func (m *Manager) DeleteWorker(id string, deleteWorkDir bool) error {
	if deleteWorkDir {
		worker, err := m.workerStore.GetByID(id)
		if err != nil {
			return fmt.Errorf("get worker: %w", err)
		}
		if worker.WorkDir != "" {
			if err := os.RemoveAll(worker.WorkDir); err != nil {
				return fmt.Errorf("remove work dir: %w", err)
			}
		}
	}
	return m.workerStore.Delete(id)
}

// GetExecution returns the current state of an execution by ID.
func (m *Manager) GetExecution(id string) (model.WorkerExecution, error) {
	return m.executionStore.GetByID(id)
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
