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
	"github.com/robobee/core/internal/mcp"
	"github.com/robobee/core/internal/dispatcher"
	"github.com/robobee/core/internal/msgingest"
	"github.com/robobee/core/internal/msgrouter"
	"github.com/robobee/core/internal/msgsender"
	"github.com/robobee/core/internal/platform"
	"github.com/robobee/core/internal/platform/dingtalk"
	"github.com/robobee/core/internal/platform/feishu"
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
	msgStore := store.NewMessageStore(db)

	// Initialize Claude Code client for routing
	aiClient, err := ai.NewClaudeCodeClient(cfg.Runtime.ClaudeCode.Binary)
	if err != nil {
		log.Fatalf("failed to create AI client: %v", err)
	}

	// Create worker manager
	mgr := worker.NewManager(cfg, workerStore, execStore)

	// Graceful shutdown context
	ctx, cancel := context.WithCancel(context.Background())

	// Build four-layer message pipeline
	sendersByPlatform := make(map[string]platform.PlatformSenderAdapter)

	ingest := msgingest.New(msgStore, cfg.MessageQueue.DebounceWindow)
	router := msgrouter.New(aiClient, workerStore, ingest.Out())
	disp := dispatcher.New(mgr, msgStore, router.Out())
	sender := msgsender.New(sendersByPlatform, disp.Out())

	go ingest.Run(ctx)
	go router.Run(ctx)
	go disp.Run(ctx)
	go sender.Run(ctx)

	// Register enabled platforms
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

	// Start MCP server if configured
	var mcpSrv *mcp.MCPServer
	if cfg.MCP.APIKey != "" {
		mcpSrv = mcp.NewServer(workerStore, mgr)
		log.Println("MCP server enabled at /mcp/sse and /mcp/messages")
	}

	// Start HTTP API
	srv := api.NewServer(workerStore, execStore, mgr, mcpSrv, cfg.MCP.APIKey)

	// Graceful shutdown
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
