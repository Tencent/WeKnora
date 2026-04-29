package feishu

import (
	"context"
	"fmt"

	"github.com/Tencent/WeKnora/internal/im"
	"github.com/Tencent/WeKnora/internal/logger"
)

// NewFactory returns an im.AdapterFactory for Feishu (Lark) channels.
// The HTTP adapter is always created (needed for SendReply in both modes);
// "websocket" mode additionally runs a long-connection event stream.
func NewFactory() im.AdapterFactory {
	return func(factoryCtx context.Context, channel *im.IMChannel, msgHandler func(context.Context, *im.IncomingMessage) error) (im.Adapter, context.CancelFunc, error) {
		creds, err := im.ParseCredentials(channel.Credentials)
		if err != nil {
			return nil, nil, fmt.Errorf("parse feishu credentials: %w", err)
		}

		appID := im.GetString(creds, "app_id")
		appSecret := im.GetString(creds, "app_secret")
		verificationToken := im.GetString(creds, "verification_token")
		encryptKey := im.GetString(creds, "encrypt_key")

		// Always create the HTTP adapter (needed for SendReply in both modes)
		adapter := NewAdapter(appID, appSecret, verificationToken, encryptKey)

		mode := im.ResolveMode(channel, "websocket")

		switch mode {
		case "webhook":
			return adapter, nil, nil

		case "websocket":
			client := NewLongConnClient(appID, appSecret, msgHandler)

			wsCtx, wsCancel := context.WithCancel(context.Background())
			go func() {
				if err := client.Start(wsCtx); err != nil && wsCtx.Err() == nil {
					logger.Errorf(context.Background(), "[IM] Feishu long connection stopped for channel %s: %v", channel.ID, err)
				}
			}()

			return adapter, wsCancel, nil

		default:
			return nil, nil, fmt.Errorf("unknown Feishu mode: %s", mode)
		}
	}
}
