package dingtalk

import (
	"context"

	"github.com/open-dingtalk/dingtalk-stream-sdk-go/chatbot"
	dtsdk "github.com/open-dingtalk/dingtalk-stream-sdk-go/client"

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

// Start establishes the stream connection. The underlying SDK's Start is
// non-blocking: it dials the websocket, spawns its internal read/reconnect
// loops, and returns once the connection is established (or the attempt fails).
func (c *LongConnClient) Start(ctx context.Context) error {
	logger.Infof(ctx, "[IM] DingTalk Stream connecting...")
	return c.streamClient.Start(ctx)
}

// Close tears down the stream connection. AutoReconnect is disabled first so
// that closing the connection does not trigger the SDK's internal reconnect.
func (c *LongConnClient) Close() {
	c.streamClient.AutoReconnect = false
	c.streamClient.Close()
}

func (c *LongConnClient) onChatBotMessage(ctx context.Context, data *chatbot.BotCallbackDataModel) ([]byte, error) {
	incoming := streamToIncoming(data, c.clientID)

	if err := c.handler(ctx, incoming); err != nil {
		logger.Errorf(ctx, "[DingTalk] Handle message error: %v", err)
	}

	return nil, nil
}
