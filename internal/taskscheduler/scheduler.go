package taskscheduler

import (
	"context"
	"log"
	"time"

	"github.com/robfig/cron/v3"
	"github.com/robobee/core/internal/dispatcher"
	"github.com/robobee/core/internal/model"
	"github.com/robobee/core/internal/platform"
	"github.com/robobee/core/internal/store"
)

// Scheduler polls for due tasks and sends them to the Dispatcher.
type Scheduler struct {
	taskStore    *store.TaskStore
	dispatchCh   chan<- dispatcher.DispatchTask
	pollInterval time.Duration
}

// New creates a Scheduler.
func New(taskStore *store.TaskStore, dispatchCh chan<- dispatcher.DispatchTask, pollInterval time.Duration) *Scheduler {
	return &Scheduler{
		taskStore:    taskStore,
		dispatchCh:   dispatchCh,
		pollInterval: pollInterval,
	}
}

// RecoverRunning resets all 'running' tasks to 'pending'.
// Must be called synchronously at startup AFTER the Feeder's RecoverFeeding.
func (s *Scheduler) RecoverRunning(ctx context.Context) {
	n, err := s.taskStore.ResetRunningToPending(ctx)
	if err != nil {
		log.Printf("taskscheduler: recover running tasks: %v", err)
		return
	}
	if n > 0 {
		log.Printf("taskscheduler: reset %d running task(s) to pending", n)
	}
}

// Run polls for due tasks on each tick until ctx is cancelled.
func (s *Scheduler) Run(ctx context.Context) {
	ticker := time.NewTicker(s.pollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			s.poll(ctx)
		case <-ctx.Done():
			return
		}
	}
}

func (s *Scheduler) poll(ctx context.Context) {
	nowMS := time.Now().UnixMilli()
	tasks, err := s.taskStore.ClaimDueTasks(ctx, nowMS)
	if err != nil {
		log.Printf("taskscheduler: claim due tasks: %v", err)
		return
	}

	for _, ct := range tasks {
		// For scheduled tasks, compute the real next_run_at and update.
		if ct.Type == model.TaskTypeScheduled && ct.CronExpr != "" {
			sched, err := cron.ParseStandard(ct.CronExpr)
			if err != nil {
				log.Printf("taskscheduler: invalid cron %q for task %s: %v", ct.CronExpr, ct.ID, err)
				s.taskStore.SetExecution(ctx, ct.ID, "", model.TaskStatusFailed) //nolint:errcheck
				continue
			}
			next := sched.Next(time.Now()).UnixMilli()
			s.taskStore.UpdateNextRunAt(ctx, ct.ID, next) //nolint:errcheck
		}

		sessionKey := ct.MessageSessionKey
		replySessionKey := ct.ReplySessionKey
		effectiveSession := sessionKey
		if replySessionKey != "" {
			effectiveSession = replySessionKey
		}

		dt := dispatcher.DispatchTask{
			TaskID:          ct.ID,
			WorkerID:        ct.WorkerID,
			SessionKey:      sessionKey,
			Instruction:     ct.Instruction,
			ReplyTo:         platform.InboundMessage{Platform: ct.MessagePlatform, SessionKey: effectiveSession},
			TaskType:        ct.Type,
			MessageID:       ct.MessageID,
			ReplySessionKey: replySessionKey,
		}

		select {
		case s.dispatchCh <- dt:
		case <-ctx.Done():
			return
		}
	}
}
