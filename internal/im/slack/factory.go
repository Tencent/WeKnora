package slack

import (
	"context"
	"fmt"

	slackpkg "github.com/slack-go/slack"

	"github.com/Tencent/WeKnora/internal/im"
	"github.com/Tencent/WeKnora/internal/logger"
)

// NewFactory returns an im.AdapterFactory for Slack channels.
// Supports "webhook" (Events API) and "websocket" (Socket Mode, default).
func NewFactory() im.AdapterFactory {
	return func(factoryCtx context.Context, channel *im.IMChannel, msgHandler func(context.Context, *im.IncomingMessage) error) (im.Adapter, context.CancelFunc, error) {
		creds, err := im.ParseCredentials(channel.Credentials)
		if err != nil {
			return nil, nil, fmt.Errorf("parse slack credentials: %w", err)
		}

		mode := im.ResolveMode(channel, "websocket")

		switch mode {
		case "webhook":
			api := slackpkg.New(im.GetString(creds, "bot_token"))
			adapter := NewWebhookAdapter(api, im.GetString(creds, "signing_secret"))
			return adapter, func() {}, nil

		case "websocket":
			client := NewLongConnClient(
				im.GetString(creds, "app_token"),
				im.GetString(creds, "bot_token"),
				msgHandler,
			)

			adapter := NewAdapter(client, client.GetAPI())

			wsCtx, wsCancel := context.WithCancel(context.Background())
			go func() {
				if err := client.Start(wsCtx); err != nil && wsCtx.Err() == nil {
					logger.Errorf(context.Background(), "[IM] Slack long connection stopped for channel %s: %v", channel.ID, err)
				}
			}()

			return adapter, wsCancel, nil

		default:
			return nil, nil, fmt.Errorf("unsupported slack mode: %s", mode)
		}
	}
}
