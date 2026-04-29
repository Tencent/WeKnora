package wecom

import (
	"context"
	"fmt"

	"github.com/Tencent/WeKnora/internal/im"
	"github.com/Tencent/WeKnora/internal/logger"
)

// NewFactory returns an im.AdapterFactory for WeCom channels.
// Supports two modes: "webhook" (HTTP callback) and "websocket" (long connection, default).
func NewFactory() im.AdapterFactory {
	return func(factoryCtx context.Context, channel *im.IMChannel, msgHandler func(context.Context, *im.IncomingMessage) error) (im.Adapter, context.CancelFunc, error) {
		creds, err := im.ParseCredentials(channel.Credentials)
		if err != nil {
			return nil, nil, fmt.Errorf("parse wecom credentials: %w", err)
		}

		mode := im.ResolveMode(channel, "websocket")

		switch mode {
		case "webhook":
			corpAgentID := 0
			if v, ok := creds["corp_agent_id"]; ok {
				switch val := v.(type) {
				case float64:
					corpAgentID = int(val)
				case int:
					corpAgentID = val
				}
			}
			adapter, err := NewWebhookAdapter(
				im.GetString(creds, "corp_id"),
				im.GetString(creds, "agent_secret"),
				im.GetString(creds, "token"),
				im.GetString(creds, "encoding_aes_key"),
				corpAgentID,
				im.GetString(creds, "api_base_url"),
			)
			if err != nil {
				return nil, nil, err
			}
			return adapter, nil, nil

		case "websocket":
			client, err := NewLongConnClient(
				im.GetString(creds, "bot_id"),
				im.GetString(creds, "bot_secret"),
				im.GetString(creds, "ws_endpoint"),
				im.GetString(creds, "bot_name"),
				msgHandler,
			)
			if err != nil {
				return nil, nil, err
			}

			wsCtx, wsCancel := context.WithCancel(context.Background())
			go func() {
				if err := client.Start(wsCtx); err != nil && wsCtx.Err() == nil {
					logger.Errorf(context.Background(), "[IM] WeCom long connection stopped for channel %s: %v", channel.ID, err)
				}
			}()

			adapter := NewWSAdapter(client)
			return adapter, wsCancel, nil

		default:
			return nil, nil, fmt.Errorf("unknown WeCom mode: %s", mode)
		}
	}
}
