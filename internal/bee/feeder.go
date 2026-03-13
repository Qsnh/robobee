package bee

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/robobee/core/internal/dispatcher"
	"github.com/robobee/core/internal/platform"
	"github.com/robobee/core/internal/store"
)

// FeederConfig holds Feeder tuning parameters.
type FeederConfig struct {
	Interval           time.Duration
	BatchSize          int
	Timeout            time.Duration
	QueueWarnThreshold int
	WorkDir            string
	Persona            string
	Binary             string // claude CLI path
	MCPBaseURL         string // e.g. "http://localhost:8080"
	MCPAPIKey          string
}

// BeeRunner abstracts the bee process invocation (real or test double).
type BeeRunner interface {
	Run(ctx context.Context, workDir, prompt string) error
}

// Feeder polls platform_messages for unprocessed messages and feeds them to bee.
type Feeder struct {
	msgStore  *store.MessageStore
	taskStore *store.TaskStore
	runner    BeeRunner
	clearCh   chan<- dispatcher.DispatchTask
	cfg       FeederConfig
}

// NewFeeder creates a Feeder.
func NewFeeder(ms *store.MessageStore, ts *store.TaskStore, runner BeeRunner, clearCh chan<- dispatcher.DispatchTask, cfg FeederConfig) *Feeder {
	return &Feeder{
		msgStore:  ms,
		taskStore: ts,
		runner:    runner,
		clearCh:   clearCh,
		cfg:       cfg,
	}
}

// RecoverFeeding resets any messages stuck in 'feeding' status back to 'received'
// and deletes their associated pending tasks.
// Must be called synchronously at startup BEFORE TaskScheduler.RecoverRunning.
func (f *Feeder) RecoverFeeding(ctx context.Context) {
	ids, err := f.msgStore.ResetFeedingToReceived(ctx)
	if err != nil {
		log.Printf("feeder: recover feeding: %v", err)
		return
	}
	if len(ids) == 0 {
		return
	}
	if err := f.taskStore.DeletePendingByMessageIDs(ctx, ids); err != nil {
		log.Printf("feeder: delete orphaned tasks: %v", err)
	}
	log.Printf("feeder: recovered %d feeding message(s)", len(ids))
}

// Run polls for unprocessed messages on each tick. Call in a goroutine.
func (f *Feeder) Run(ctx context.Context) {
	ticker := time.NewTicker(f.cfg.Interval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			f.tick(ctx)
		case <-ctx.Done():
			return
		}
	}
}

func (f *Feeder) tick(ctx context.Context) {
	// Check queue health
	count, _ := f.msgStore.CountReceived(ctx)
	if count > f.cfg.QueueWarnThreshold {
		log.Printf("feeder: WARNING: %d unprocessed messages in queue (threshold: %d)", count, f.cfg.QueueWarnThreshold)
	}

	// Claim a batch atomically
	msgs, err := f.msgStore.ClaimBatch(ctx, f.cfg.BatchSize)
	if err != nil {
		log.Printf("feeder: claim batch: %v", err)
		return
	}
	if len(msgs) == 0 {
		return
	}

	// Separate clear commands from regular messages
	var clearMsgs, regularMsgs []store.ClaimedMessage
	for _, m := range msgs {
		if detectClear(m.Content) {
			clearMsgs = append(clearMsgs, m)
		} else {
			regularMsgs = append(regularMsgs, m)
		}
	}

	// Handle clear commands directly (bypass bee)
	for _, m := range clearMsgs {
		f.msgStore.MarkBeeProcessed(ctx, []string{m.ID}) //nolint:errcheck
		select {
		case f.clearCh <- dispatcher.DispatchTask{
			TaskType:   "clear",
			SessionKey: m.SessionKey,
			ReplyTo: platform.InboundMessage{
				Platform:   m.Platform,
				SessionKey: m.SessionKey,
			},
		}:
		default:
		}
	}

	if len(regularMsgs) == 0 {
		return
	}

	// Write CLAUDE.md with persona
	if err := WriteCLAUDEMD(f.cfg.WorkDir, f.cfg.Persona); err != nil {
		log.Printf("feeder: write CLAUDE.md: %v", err)
		f.rollback(ctx, regularMsgs)
		return
	}

	prompt := buildPrompt(regularMsgs)
	msgIDs := make([]string, len(regularMsgs))
	for i, m := range regularMsgs {
		msgIDs[i] = m.ID
	}

	beeCtx, cancel := context.WithTimeout(ctx, f.cfg.Timeout)
	defer cancel()

	if err := f.runner.Run(beeCtx, f.cfg.WorkDir, prompt); err != nil {
		log.Printf("feeder: bee run failed: %v", err)
		f.rollback(ctx, regularMsgs)
		return
	}

	if err := f.msgStore.MarkBeeProcessed(ctx, msgIDs); err != nil {
		log.Printf("feeder: mark bee_processed: %v", err)
	}
}

func (f *Feeder) rollback(ctx context.Context, msgs []store.ClaimedMessage) {
	ids := make([]string, len(msgs))
	for i, m := range msgs {
		ids[i] = m.ID
	}
	if err := f.taskStore.DeletePendingByMessageIDs(ctx, ids); err != nil {
		log.Printf("feeder: rollback delete tasks: %v", err)
	}
	if err := f.msgStore.ResetFeedingBatch(ctx, ids); err != nil {
		log.Printf("feeder: rollback messages: %v", err)
	}
}

func detectClear(content string) bool {
	return strings.TrimSpace(strings.ToLower(content)) == "clear"
}

func buildPrompt(msgs []store.ClaimedMessage) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "以下是 %d 条待处理用户消息，请为每条消息创建相应的任务。\n\n", len(msgs))
	for i, m := range msgs {
		fmt.Fprintf(&sb, "--- 消息 %d ---\n来源: %s | 会话: %s | 消息ID: %s\n内容: %s\n\n",
			i+1, m.Platform, m.SessionKey, m.ID, m.Content)
	}
	sb.WriteString("请使用 create_task 工具为每条消息中的每个任务指派创建任务记录。")
	return sb.String()
}
