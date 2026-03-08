package scheduler

import (
	"context"
	"log"

	"github.com/robfig/cron/v3"
	"github.com/robobee/core/internal/store"
	"github.com/robobee/core/internal/worker"
)

type Scheduler struct {
	cron        *cron.Cron
	workerStore *store.WorkerStore
	manager     *worker.Manager
	entryMap    map[string]cron.EntryID // worker_id -> entry_id
}

func New(ws *store.WorkerStore, mgr *worker.Manager) *Scheduler {
	return &Scheduler{
		cron:        cron.New(),
		workerStore: ws,
		manager:     mgr,
		entryMap:    make(map[string]cron.EntryID),
	}
}

func (s *Scheduler) Start() error {
	workers, err := s.workerStore.ListScheduledWorkers()
	if err != nil {
		return err
	}

	for _, w := range workers {
		if err := s.AddWorker(w.ID, w.CronExpression); err != nil {
			log.Printf("failed to schedule worker %s: %v", w.ID, err)
		}
	}

	s.cron.Start()
	log.Printf("Scheduler started with %d scheduled workers", len(workers))
	return nil
}

func (s *Scheduler) AddWorker(workerID, cronExpr string) error {
	s.RemoveWorker(workerID)

	id, err := s.cron.AddFunc(cronExpr, func() {
		log.Printf("Cron triggered worker %s", workerID)
		if _, err := s.manager.ExecuteWorker(context.Background(), workerID, "scheduled"); err != nil {
			log.Printf("failed to execute cron worker %s: %v", workerID, err)
		}
	})
	if err != nil {
		return err
	}

	s.entryMap[workerID] = id
	return nil
}

func (s *Scheduler) RemoveWorker(workerID string) {
	if entryID, ok := s.entryMap[workerID]; ok {
		s.cron.Remove(entryID)
		delete(s.entryMap, workerID)
	}
}

func (s *Scheduler) Stop() {
	s.cron.Stop()
}
