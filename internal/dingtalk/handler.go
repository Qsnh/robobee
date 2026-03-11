package dingtalk

import (
	"context"
	"log"
	"strings"

	"github.com/open-dingtalk/dingtalk-stream-sdk-go/chatbot"
	"github.com/open-dingtalk/dingtalk-stream-sdk-go/client"

	"github.com/robobee/core/internal/config"
	"github.com/robobee/core/internal/platform"
)

// DingTalkPlatform implements platform.Platform for DingTalk.
type DingTalkPlatform struct {
	receiver *DingTalkReceiver
	sender   *DingTalkSender
}

// NewPlatform constructs a DingTalkPlatform from configuration.
func NewPlatform(cfg config.DingTalkConfig) platform.Platform {
	return &DingTalkPlatform{
		receiver: &DingTalkReceiver{cfg: cfg},
		sender:   &DingTalkSender{},
	}
}

func (d *DingTalkPlatform) ID() string                                 { return "dingtalk" }
func (d *DingTalkPlatform) Receiver() platform.PlatformReceiverAdapter { return d.receiver }
func (d *DingTalkPlatform) Sender() platform.PlatformSenderAdapter     { return d.sender }

// DingTalkReceiver connects to DingTalk via the stream SDK and dispatches inbound messages.
type DingTalkReceiver struct {
	cfg config.DingTalkConfig
}

func (r *DingTalkReceiver) Start(ctx context.Context, dispatch func(platform.InboundMessage)) error {
	cli := client.NewStreamClient(
		client.WithAppCredential(client.NewAppCredentialConfig(r.cfg.ClientID, r.cfg.ClientSecret)),
	)
	cli.RegisterChatBotCallbackRouter(func(ctx context.Context, data *chatbot.BotCallbackDataModel) ([]byte, error) {
		text := strings.TrimSpace(data.Text.Content)
		if text == "" {
			return []byte(""), nil
		}
		dispatch(platform.InboundMessage{
			Platform:   "dingtalk",
			SessionKey: "dingtalk:" + data.ConversationId,
			Content:    text,
			Raw:        data,
		})
		return []byte(""), nil
	})

	log.Println("DingTalk bot starting...")
	return cli.Start(ctx)
}

// DingTalkSender sends messages via the DingTalk chatbot replier.
type DingTalkSender struct{}

const markdownTitle = "RoboBee"

func (s *DingTalkSender) Send(ctx context.Context, msg platform.OutboundMessage) error {
	data, ok := msg.ReplyTo.Raw.(*chatbot.BotCallbackDataModel)
	if !ok {
		log.Printf("dingtalk: sender: unexpected raw type %T", msg.ReplyTo.Raw)
		return nil
	}
	replier := chatbot.NewChatbotReplier()
	if err := replier.SimpleReplyMarkdown(ctx, data.SessionWebhook, []byte(markdownTitle), []byte(msg.Content)); err != nil {
		log.Printf("dingtalk: send message error: %v", err)
	}
	return nil
}

var _ platform.Platform                = (*DingTalkPlatform)(nil)
var _ platform.PlatformReceiverAdapter = (*DingTalkReceiver)(nil)
var _ platform.PlatformSenderAdapter   = (*DingTalkSender)(nil)
