package scheduler

import (
	"context"
	"log"

	"github.com/robfig/cron/v3"
	"github.com/robobee/core/internal/store"
	"github.com/robobee/core/internal/worker"
)

type Scheduler struct {
	cron      *cron.Cron
	taskStore *store.TaskStore
	manager   *worker.Manager
	entryMap  map[string]cron.EntryID // task_id -> entry_id
}

func New(ts *store.TaskStore, mgr *worker.Manager) *Scheduler {
	return &Scheduler{
		cron:      cron.New(),
		taskStore: ts,
		manager:   mgr,
		entryMap:  make(map[string]cron.EntryID),
	}
}

func (s *Scheduler) Start() error {
	// Load existing cron tasks
	tasks, err := s.taskStore.ListCronTasks()
	if err != nil {
		return err
	}

	for _, task := range tasks {
		if err := s.AddTask(task.ID, task.CronExpression); err != nil {
			log.Printf("failed to schedule task %s: %v", task.ID, err)
		}
	}

	s.cron.Start()
	log.Printf("Scheduler started with %d cron tasks", len(tasks))
	return nil
}

func (s *Scheduler) AddTask(taskID, cronExpr string) error {
	// Remove existing entry if any
	s.RemoveTask(taskID)

	id, err := s.cron.AddFunc(cronExpr, func() {
		log.Printf("Cron triggered task %s", taskID)
		if _, err := s.manager.ExecuteTask(context.Background(), taskID); err != nil {
			log.Printf("failed to execute cron task %s: %v", taskID, err)
		}
	})
	if err != nil {
		return err
	}

	s.entryMap[taskID] = id
	return nil
}

func (s *Scheduler) RemoveTask(taskID string) {
	if entryID, ok := s.entryMap[taskID]; ok {
		s.cron.Remove(entryID)
		delete(s.entryMap, taskID)
	}
}

func (s *Scheduler) Stop() {
	s.cron.Stop()
}
