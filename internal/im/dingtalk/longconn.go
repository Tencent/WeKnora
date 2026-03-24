package dingtalk

import (
	"context"
	"strings"

	dtsdk "github.com/open-dingtalk/dingtalk-stream-sdk-go/client"
	"github.com/open-dingtalk/dingtalk-stream-sdk-go/chatbot"

	"github.com/Tencent/WeKnora/internal/im"
	"github.com/Tencent/WeKnora/internal/logger"
)

// MessageHandler is called when an IM message is received via stream connection.
type MessageHandler func(ctx context.Context, msg *im.IncomingMessage) error

// LongConnClient manages a DingTalk Stream mode connection.
type LongConnClient struct {
	clientID     string
	clientSecret string
	handler      MessageHandler
	streamClient *dtsdk.StreamClient
}

// NewLongConnClient creates a DingTalk stream client.
func NewLongConnClient(clientID, clientSecret string, handler MessageHandler) *LongConnClient {
	cli := dtsdk.NewStreamClient(
		dtsdk.WithAppCredential(&dtsdk.AppCredentialConfig{
			ClientId:     clientID,
			ClientSecret: clientSecret,
		}),
	)

	c := &LongConnClient{
		clientID:     clientID,
		clientSecret: clientSecret,
		handler:      handler,
		streamClient: cli,
	}

	cli.RegisterChatBotCallbackRouter(c.onChatBotMessage)

	return c
}

// Start begins the stream connection. It blocks until ctx is cancelled.
func (c *LongConnClient) Start(ctx context.Context) error {
	logger.Infof(ctx, "[IM] DingTalk Stream connecting...")

	errCh := make(chan error, 1)
	go func() {
		errCh <- c.streamClient.Start(ctx)
	}()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case err := <-errCh:
		return err
	}
}

func (c *LongConnClient) onChatBotMessage(ctx context.Context, data *chatbot.BotCallbackDataModel) ([]byte, error) {
	chatType := im.ChatTypeDirect
	chatID := ""
	if data.ConversationType == "2" {
		chatType = im.ChatTypeGroup
		chatID = data.ConversationId
	}

	userID := data.SenderStaffId
	if userID == "" {
		userID = data.SenderId
	}

	content := strings.TrimSpace(data.Text.Content)

	incoming := &im.IncomingMessage{
		Platform:    im.PlatformDingtalk,
		UserID:      userID,
		UserName:    data.SenderNick,
		ChatID:      chatID,
		ChatType:    chatType,
		MessageID:   data.MsgId,
		MessageType: im.MessageTypeText,
		Content:     content,
		Extra: map[string]string{
			"session_webhook": data.SessionWebhook,
		},
	}

	if err := c.handler(ctx, incoming); err != nil {
		logger.Errorf(ctx, "[DingTalk] Handle message error: %v", err)
	}

	return nil, nil
}
