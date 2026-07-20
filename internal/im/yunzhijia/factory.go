package yunzhijia

import (
	"context"
	"fmt"
	"strconv"

	"github.com/Tencent/WeKnora/internal/im"
	"github.com/Tencent/WeKnora/internal/logger"
)

// NewFactory returns an im.AdapterFactory for Yunzhijia channels.
// Webhook remains the default; WebSocket uses yzjtoken from send_msg_url.
func NewFactory() im.AdapterFactory {
	return func(factoryCtx context.Context, channel *im.IMChannel, msgHandler func(context.Context, *im.IncomingMessage) error) (im.Adapter, context.CancelFunc, error) {
		creds, err := im.ParseCredentials(channel.Credentials)
		if err != nil {
			return nil, nil, fmt.Errorf("parse yunzhijia credentials: %w", err)
		}

		sendMsgURL := im.GetString(creds, "send_msg_url")
		if sendMsgURL == "" {
			return nil, nil, fmt.Errorf("yunzhijia send_msg_url is required")
		}

		secret := im.GetString(creds, "secret")
		allowedHostSuffix := im.GetString(creds, "allowed_webhook_host_suffix")

		timeoutSeconds := positiveIntCredential(creds, "timeout_seconds", 10)

		adapter := NewAdapter(sendMsgURL, secret, timeoutSeconds, allowedHostSuffix)
		if err := adapter.validateSendURL(); err != nil {
			return nil, nil, err
		}

		mode := im.ResolveMode(channel, "webhook")
		switch mode {
		case "webhook":
			return adapter, nil, nil
		case "websocket":
			webSocketURL, err := deriveWebSocketURL(sendMsgURL, allowedHostSuffix)
			if err != nil {
				return nil, nil, err
			}
			client := NewLongConnClient(webSocketURL, msgHandler)
			wsCtx, wsCancel := context.WithCancel(context.Background())
			go func() {
				if err := client.Start(wsCtx); err != nil && wsCtx.Err() == nil {
					logger.Errorf(context.Background(), "[IM] Yunzhijia WebSocket stopped for channel %s: %v", channel.ID, err)
				}
			}()
			stop := func() {
				client.Stop()
				wsCancel()
			}
			return adapter, stop, nil
		default:
			return nil, nil, fmt.Errorf("unsupported yunzhijia mode: %s", mode)
		}
	}
}

func positiveIntCredential(creds map[string]any, key string, fallback int) int {
	switch value := creds[key].(type) {
	case float64:
		if value > 0 {
			return int(value)
		}
	case string:
		if parsed, err := strconv.Atoi(value); err == nil && parsed > 0 {
			return parsed
		}
	}
	return fallback
}
