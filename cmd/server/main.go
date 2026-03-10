package main

import (
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/robobee/core/internal/ai"
	"github.com/robobee/core/internal/api"
	"github.com/robobee/core/internal/config"
	"github.com/robobee/core/internal/scheduler"
	"github.com/robobee/core/internal/store"
	"github.com/robobee/core/internal/worker"
)

func main() {
	cfgPath := "config.yaml"
	if len(os.Args) > 1 {
		cfgPath = os.Args[1]
	}

	cfg, err := config.Load(cfgPath)
	if err != nil {
		log.Fatalf("failed to load config: %v", err)
	}

	// Initialize database
	db, err := store.InitDB(cfg.Database.Path)
	if err != nil {
		log.Fatalf("failed to init database: %v", err)
	}
	defer db.Close()

	// Create stores
	workerStore := store.NewWorkerStore(db)
	execStore := store.NewExecutionStore(db)
	memoryStore := store.NewMemoryStore(db)

	// Initialize AI client
	aiClient := ai.NewClient(cfg.AI)

	// Create worker manager
	mgr := worker.NewManager(cfg, workerStore, execStore, memoryStore, aiClient)

	// Start cron scheduler
	sched := scheduler.New(workerStore, mgr)
	if err := sched.Start(); err != nil {
		log.Printf("scheduler start error: %v", err)
	}

	// Start HTTP API
	srv := api.NewServer(workerStore, execStore, memoryStore, mgr, sched)

	// Graceful shutdown
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-quit
		log.Println("Shutting down...")
		sched.Stop()
		db.Close()
		os.Exit(0)
	}()

	addr := fmt.Sprintf("%s:%d", cfg.Server.Host, cfg.Server.Port)
	log.Printf("RoboBee Core starting on %s", addr)
	if err := srv.Run(addr); err != nil {
		log.Fatalf("server error: %v", err)
	}
}
