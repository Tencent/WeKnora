package mattermost

import (
	"context"
	"fmt"

	"github.com/Tencent/WeKnora/internal/im"
)

// NewFactory returns an im.AdapterFactory for Mattermost channels.
// Only "webhook" mode is supported (outgoing webhook + REST API);
// the default mode is "webhook" (not "websocket" like other platforms).
func NewFactory() im.AdapterFactory {
	return func(factoryCtx context.Context, channel *im.IMChannel, msgHandler func(context.Context, *im.IncomingMessage) error) (im.Adapter, context.CancelFunc, error) {
		creds, err := im.ParseCredentials(channel.Credentials)
		if err != nil {
			return nil, nil, fmt.Errorf("parse mattermost credentials: %w", err)
		}

		mode := im.ResolveMode(channel, "webhook")
		if mode != "webhook" {
			return nil, nil, fmt.Errorf("unsupported mattermost mode: %s (only webhook is supported)", mode)
		}

		siteURL := im.GetString(creds, "site_url")
		botToken := im.GetString(creds, "bot_token")
		outgoingToken := im.GetString(creds, "outgoing_token")
		botUserID := im.GetString(creds, "bot_user_id")

		if outgoingToken == "" {
			return nil, nil, fmt.Errorf("mattermost outgoing_token is required")
		}

		client, err := NewClient(siteURL, botToken)
		if err != nil {
			return nil, nil, err
		}

		postReplyToMain := im.GetBool(creds, "post_to_main")
		adapter := NewAdapter(client, outgoingToken, botUserID, postReplyToMain)
		return adapter, func() {}, nil
	}
}
