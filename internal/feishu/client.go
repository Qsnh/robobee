package feishu

import (
	"context"
	"log"

	lark "github.com/larksuite/oapi-sdk-go/v3"
	larkcore "github.com/larksuite/oapi-sdk-go/v3/core"
	"github.com/larksuite/oapi-sdk-go/v3/event/dispatcher"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
	larkws "github.com/larksuite/oapi-sdk-go/v3/ws"

	"github.com/robobee/core/internal/ai"
	"github.com/robobee/core/internal/botrouter"
	"github.com/robobee/core/internal/config"
	"github.com/robobee/core/internal/store"
	"github.com/robobee/core/internal/worker"
)

// Start connects to Feishu via WebSocket long connection and blocks until ctx is cancelled.
// Call this in a goroutine from main.go.
func Start(
	ctx context.Context,
	cfg config.FeishuConfig,
	workerStore *store.WorkerStore,
	sessionStore *store.FeishuSessionStore,
	mgr *worker.Manager,
	aiClient *ai.Client,
) error {
	larkClient := lark.NewClient(cfg.AppID, cfg.AppSecret)

	router := botrouter.NewRouter(aiClient, workerStore)
	handler := NewHandler(larkClient, router, sessionStore, mgr)

	eventHandler := dispatcher.NewEventDispatcher("", "").
		OnP2MessageReceiveV1(func(ctx context.Context, event *larkim.P2MessageReceiveV1) error {
			return handler.OnMessage(ctx, event)
		})

	wsClient := larkws.NewClient(cfg.AppID, cfg.AppSecret,
		larkws.WithEventHandler(eventHandler),
		larkws.WithLogLevel(larkcore.LogLevelInfo),
	)

	log.Println("Feishu bot starting...")
	return wsClient.Start(ctx)
}
