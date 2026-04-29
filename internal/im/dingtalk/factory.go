package dingtalk

import (
	"context"
	"fmt"

	"github.com/Tencent/WeKnora/internal/im"
	"github.com/Tencent/WeKnora/internal/logger"
)

// NewFactory returns an im.AdapterFactory for DingTalk channels.
// Supports "webhook" and "websocket" (stream mode, default).
func NewFactory() im.AdapterFactory {
	return func(factoryCtx context.Context, channel *im.IMChannel, msgHandler func(context.Context, *im.IncomingMessage) error) (im.Adapter, context.CancelFunc, error) {
		creds, err := im.ParseCredentials(channel.Credentials)
		if err != nil {
			return nil, nil, fmt.Errorf("parse dingtalk credentials: %w", err)
		}

		clientID := im.GetString(creds, "client_id")
		clientSecret := im.GetString(creds, "client_secret")
		cardTemplateID := im.GetString(creds, "card_template_id")

		mode := im.ResolveMode(channel, "websocket")

		switch mode {
		case "webhook":
			adapter := NewWebhookAdapter(clientID, clientSecret, cardTemplateID)
			return adapter, nil, nil

		case "websocket":
			client := NewLongConnClient(clientID, clientSecret, msgHandler)

			wsCtx, wsCancel := context.WithCancel(context.Background())
			go func() {
				if err := client.Start(wsCtx); err != nil && wsCtx.Err() == nil {
					logger.Errorf(context.Background(), "[IM] DingTalk stream stopped for channel %s: %v", channel.ID, err)
				}
			}()

			adapter := NewAdapter(client, clientID, clientSecret, cardTemplateID)
			return adapter, wsCancel, nil

		default:
			return nil, nil, fmt.Errorf("unsupported dingtalk mode: %s", mode)
		}
	}
}
