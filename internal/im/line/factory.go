package line

import (
	"context"
	"fmt"
	"strings"

	"github.com/Tencent/WeKnora/internal/im"
)

// NewFactory returns an im.AdapterFactory for LINE bot channels.
func NewFactory() im.AdapterFactory {
	return func(
		_ context.Context,
		channel *im.IMChannel,
		_ func(context.Context, *im.IncomingMessage) error,
	) (im.Adapter, context.CancelFunc, error) {
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

		// resetKeyword is an optional, per-channel alias that clears the session
		// context. Empty means only the built-in "/clear" applies; the channel
		// form pre-fills "/reset", so a UI-created LINE channel usually has it.
		resetKeyword := im.GetString(creds, "reset_keyword")
		// enable_flex_message opts the channel into structured Flex Message
		// output; absent means plain text replies.
		enableFlexMessage := im.GetBool(creds, "enable_flex_message")

		adapter := NewWebhookAdapterWithFlex(
			channelAccessToken, channelSecret, resetKeyword, enableFlexMessage,
		)
		// Use the channel's display name as the card header so a card says which
		// bot answered instead of carrying a fixed string.
		if channel != nil {
			adapter.cardTitle = strings.TrimSpace(channel.Name)
		}
		return adapter, nil, nil
	}
}
