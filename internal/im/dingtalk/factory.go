package dingtalk

import (
	"context"
	"fmt"

	"github.com/Tencent/WeKnora/internal/im"
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
			wsCtx, wsCancel := context.WithCancel(context.Background())
			go im.RunSupervised(wsCtx, im.SupervisorConfig{
				Name: fmt.Sprintf("DingTalk channel %s", channel.ID),
				Connect: func(ctx context.Context) (func(), error) {
					client := NewLongConnClient(clientID, clientSecret, msgHandler)
					if err := client.Start(ctx); err != nil {
						client.Close()
						return nil, err
					}
					return client.Close, nil
				},
			})

			adapter := NewAdapter(clientID, clientSecret, cardTemplateID)
			return adapter, wsCancel, nil

		default:
			return nil, nil, fmt.Errorf("unsupported dingtalk mode: %s", mode)
		}
	}
}
