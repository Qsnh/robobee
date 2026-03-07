package main

import (
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/robobee/core/internal/api"
	"github.com/robobee/core/internal/config"
	"github.com/robobee/core/internal/mail"
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
	taskStore := store.NewTaskStore(db)
	execStore := store.NewExecutionStore(db)
	emailStore := store.NewEmailStore(db)
	memoryStore := store.NewMemoryStore(db)

	// Create worker manager
	mgr := worker.NewManager(cfg, workerStore, taskStore, execStore, emailStore, memoryStore)

	// Create email sender
	_ = mail.NewSender(cfg.SMTP, emailStore)

	// Start SMTP server
	smtpSrv := mail.NewSMTPServer(cfg.SMTP, execStore, emailStore, workerStore)
	go func() {
		if err := smtpSrv.Start(); err != nil {
			log.Printf("SMTP server error: %v", err)
		}
	}()

	// Start cron scheduler
	sched := scheduler.New(taskStore, mgr)
	if err := sched.Start(); err != nil {
		log.Printf("scheduler start error: %v", err)
	}

	// Start HTTP API
	srv := api.NewServer(workerStore, taskStore, execStore, emailStore, memoryStore, mgr)

	// Graceful shutdown
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-quit
		log.Println("Shutting down...")
		smtpSrv.Close()
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
