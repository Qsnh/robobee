package dingtalk

import (
	"context"
	"log"

	"github.com/open-dingtalk/dingtalk-stream-sdk-go/client"

	"github.com/robobee/core/internal/ai"
	"github.com/robobee/core/internal/botrouter"
	"github.com/robobee/core/internal/config"
	"github.com/robobee/core/internal/store"
	"github.com/robobee/core/internal/worker"
)

// Start connects to DingTalk via stream SDK and blocks until the process exits.
// Call this in a goroutine from main.go.
func Start(
	ctx context.Context,
	cfg config.DingTalkConfig,
	workerStore *store.WorkerStore,
	sessionStore *store.DingTalkSessionStore,
	mgr *worker.Manager,
	aiClient *ai.Client,
) error {
	router := botrouter.NewRouter(aiClient, workerStore)
	handler := NewHandler(router, sessionStore, mgr)

	cli := client.NewStreamClient(
		client.WithAppCredential(client.NewAppCredentialConfig(cfg.ClientID, cfg.ClientSecret)),
	)
	cli.RegisterChatBotCallbackRouter(handler.OnMessage)

	log.Println("DingTalk bot starting...")
	return cli.Start(ctx)
}
