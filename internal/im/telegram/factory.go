package telegram

import (
	"context"
	"fmt"

	"github.com/Tencent/WeKnora/internal/im"
	"github.com/Tencent/WeKnora/internal/logger"
)

// NewFactory returns an im.AdapterFactory for Telegram bot channels.
// Supports "webhook" and "websocket" (long polling, default).
func NewFactory() im.AdapterFactory {
	return func(factoryCtx context.Context, channel *im.IMChannel, msgHandler func(context.Context, *im.IncomingMessage) error) (im.Adapter, context.CancelFunc, error) {
		creds, err := im.ParseCredentials(channel.Credentials)
		if err != nil {
			return nil, nil, fmt.Errorf("parse telegram credentials: %w", err)
		}

		botToken := im.GetString(creds, "bot_token")

		mode := im.ResolveMode(channel, "websocket")

		switch mode {
		case "webhook":
			secretToken := im.GetString(creds, "secret_token")
			adapter := NewWebhookAdapter(botToken, secretToken)
			return adapter, nil, nil

		case "websocket":
			client := NewLongConnClient(botToken, msgHandler)

			wsCtx, wsCancel := context.WithCancel(context.Background())
			go func() {
				if err := client.Start(wsCtx); err != nil && wsCtx.Err() == nil {
					logger.Errorf(context.Background(), "[IM] Telegram long polling stopped for channel %s: %v", channel.ID, err)
				}
			}()

			adapter := NewAdapter(client, botToken)
			return adapter, wsCancel, nil

		default:
			return nil, nil, fmt.Errorf("unsupported telegram mode: %s", mode)
		}
	}
}
