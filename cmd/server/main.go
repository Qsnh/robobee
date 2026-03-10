package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/robobee/core/internal/ai"
	"github.com/robobee/core/internal/api"
	"github.com/robobee/core/internal/config"
	"github.com/robobee/core/internal/feishu"
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

	// Initialize AI client
	aiClient := ai.NewClient(cfg.AI)

	// Create worker manager
	mgr := worker.NewManager(cfg, workerStore, execStore, aiClient)

	// Start cron scheduler
	sched := scheduler.New(workerStore, mgr)
	if err := sched.Start(); err != nil {
		log.Printf("scheduler start error: %v", err)
	}

	// Start Feishu bot if enabled.
	// Known limitation: uses context.Background() so the WS client won't receive
	// a cancellation signal on shutdown — os.Exit(0) terminates it abruptly.
	if cfg.Feishu.Enabled {
		feishuSessionStore := store.NewFeishuSessionStore(db)
		go func() {
			if err := feishu.Start(context.Background(), cfg.Feishu, workerStore, feishuSessionStore, mgr, aiClient); err != nil {
				log.Printf("feishu bot error: %v", err)
			}
		}()
	}

	// Start HTTP API
	srv := api.NewServer(workerStore, execStore, mgr, sched)

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
