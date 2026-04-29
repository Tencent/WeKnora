package wechat

import (
	"context"
	"fmt"

	"github.com/Tencent/WeKnora/internal/im"
	"github.com/Tencent/WeKnora/internal/logger"
)

// NewFactory returns an im.AdapterFactory for WeChat channels (iLink bot).
// WeChat only supports a long-polling mode, so there is no mode branch.
func NewFactory() im.AdapterFactory {
	return func(factoryCtx context.Context, channel *im.IMChannel, msgHandler func(context.Context, *im.IncomingMessage) error) (im.Adapter, context.CancelFunc, error) {
		creds, err := im.ParseCredentials(channel.Credentials)
		if err != nil {
			return nil, nil, fmt.Errorf("parse wechat credentials: %w", err)
		}

		botToken := im.GetString(creds, "bot_token")
		ilinkBotID := im.GetString(creds, "ilink_bot_id")

		if botToken == "" || ilinkBotID == "" {
			return nil, nil, fmt.Errorf("wechat credentials require bot_token and ilink_bot_id")
		}

		adapter := NewAdapter(botToken, ilinkBotID)
		client := NewLongPollClient(botToken, ilinkBotID, msgHandler)

		pollCtx, pollCancel := context.WithCancel(context.Background())
		go func() {
			if err := client.Start(pollCtx); err != nil && pollCtx.Err() == nil {
				logger.Errorf(context.Background(), "[IM] WeChat long-poll stopped for channel %s: %v", channel.ID, err)
			}
		}()

		return adapter, pollCancel, nil
	}
}
