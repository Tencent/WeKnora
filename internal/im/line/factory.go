package line

import (
	"context"
	"fmt"

	"github.com/Tencent/WeKnora/internal/im"
)

// NewFactory returns an im.AdapterFactory for LINE bot channels.
func NewFactory() im.AdapterFactory {
	return func(factoryCtx context.Context, channel *im.IMChannel, msgHandler func(context.Context, *im.IncomingMessage) error) (im.Adapter, context.CancelFunc, error) {
		creds, err := im.ParseCredentials(channel.Credentials)
		if err != nil {
			return nil, nil, fmt.Errorf("parse line credentials: %w", err)
		}

		channelAccessToken := im.GetString(creds, "channel_access_token")
		channelSecret := im.GetString(creds, "channel_secret")
		if channelAccessToken == "" {
			return nil, nil, fmt.Errorf("line credentials: channel_access_token is required")
		}
		if channelSecret == "" {
			return nil, nil, fmt.Errorf("line credentials: channel_secret is required")
		}

		adapter := NewWebhookAdapter(channelAccessToken, channelSecret)
		return adapter, nil, nil
	}
}
