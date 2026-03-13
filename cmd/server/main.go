package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/robobee/core/internal/api"
	"github.com/robobee/core/internal/bee"
	"github.com/robobee/core/internal/config"
	"github.com/robobee/core/internal/dispatcher"
	"github.com/robobee/core/internal/mcp"
	"github.com/robobee/core/internal/msgingest"
	"github.com/robobee/core/internal/msgsender"
	"github.com/robobee/core/internal/platform"
	"github.com/robobee/core/internal/platform/dingtalk"
	"github.com/robobee/core/internal/platform/feishu"
	"github.com/robobee/core/internal/store"
	"github.com/robobee/core/internal/taskscheduler"
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

	db, err := store.InitDB(cfg.Database.Path)
	if err != nil {
		log.Fatalf("failed to init database: %v", err)
	}
	defer db.Close()

	workerStore := store.NewWorkerStore(db)
	execStore := store.NewExecutionStore(db)
	msgStore := store.NewMessageStore(db)
	taskStore := store.NewTaskStore(db)
	sessionStore := store.NewSessionStore(db)

	mgr := worker.NewManager(cfg.Workers, cfg.Runtime, workerStore, execStore)

	// MCP server (required by bee)
	if cfg.MCP.APIKey == "" {
		log.Fatal("mcp.api_key must be set — bee requires MCP to create tasks")
	}
	mcpSrv := mcp.NewServer(workerStore, mgr, taskStore)

	// Build MCP base URL for bee process
	mcpBaseURL := fmt.Sprintf("http://%s:%d", cfg.Server.Host, cfg.Server.Port)

	// Dispatch channel shared between TaskScheduler and Feeder (for clear commands)
	dispatchCh := make(chan dispatcher.DispatchTask, 128)

	// Startup recovery (synchronous, before goroutines)
	feederCfg := bee.FeederConfig{
		Interval:           cfg.Bee.Feeder.Interval,
		BatchSize:          cfg.Bee.Feeder.BatchSize,
		Timeout:            cfg.Bee.Feeder.Timeout,
		QueueWarnThreshold: cfg.Bee.Feeder.QueueWarnThreshold,
		WorkDir:            cfg.Bee.WorkDir,
		Persona:            cfg.Bee.Persona,
		Binary:             cfg.Runtime.ClaudeCode.Binary,
		MCPBaseURL:         mcpBaseURL,
		MCPAPIKey:          cfg.MCP.APIKey,
	}
	beeProcess := bee.NewBeeProcess(
		cfg.Runtime.ClaudeCode.Binary,
		mcpBaseURL+"/mcp/sse",
		cfg.MCP.APIKey,
	)
	feeder := bee.NewFeeder(msgStore, taskStore, sessionStore, beeProcess, dispatchCh, feederCfg)
	feeder.RecoverFeeding(context.Background())

	sched := taskscheduler.New(taskStore, dispatchCh, cfg.Bee.Feeder.Interval)
	sched.RecoverRunning(context.Background())

	// Pipeline
	ctx, cancel := context.WithCancel(context.Background())
	sendersByPlatform := make(map[string]platform.PlatformSenderAdapter)

	ingest := msgingest.New(msgStore, cfg.MessageQueue.DebounceWindow)
	disp := dispatcher.New(mgr, taskStore, sessionStore, dispatchCh)
	sender := msgsender.New(sendersByPlatform, disp.Out())

	go ingest.Run(ctx)
	go feeder.Run(ctx)
	go sched.Run(ctx)
	go disp.Run(ctx)
	go sender.Run(ctx)

	// Register platforms
	if cfg.Feishu.Enabled {
		p := feishu.NewPlatform(cfg.Feishu)
		sendersByPlatform[p.ID()] = p.Sender()
		go p.Receiver().Start(ctx, ingest.Dispatch)
	}
	if cfg.DingTalk.Enabled {
		p := dingtalk.NewPlatform(cfg.DingTalk)
		sendersByPlatform[p.ID()] = p.Sender()
		go p.Receiver().Start(ctx, ingest.Dispatch)
	}

	srv := api.NewServer(workerStore, execStore, mgr, mcpSrv, cfg.MCP.APIKey)

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-quit
		log.Println("Shutting down...")
		cancel()
		db.Close()
		os.Exit(0)
	}()

	addr := fmt.Sprintf("%s:%d", cfg.Server.Host, cfg.Server.Port)
	log.Printf("RoboBee Core starting on %s", addr)
	if err := srv.Run(addr); err != nil {
		log.Fatalf("server error: %v", err)
	}
}
