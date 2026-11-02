package line

import (
	"context"
	"fmt"

	"github.com/Tencent/WeKnora/internal/im"
)

// NewFactory returns an im.AdapterFactory for LINE Official Accounts.
//
// LINE is webhook-only: there is no long-lived connection to supervise, so the
// cancel function is a no-op.
func NewFactory() im.AdapterFactory {
	return func(
		_ context.Context,
		channel *im.IMChannel,
		_ func(context.Context, *im.IncomingMessage) error,
	) (im.Adapter, context.CancelFunc, error) {
		creds, err := im.ParseCredentials(channel.Credentials)
		if err != nil {
			return nil, nil, fmt.Errorf("parse LINE credentials: %w", err)
		}

		secret := im.GetString(creds, "channel_secret")
		token := im.GetString(creds, "channel_access_token")
		if secret == "" || token == "" {
			return nil, nil, fmt.Errorf("LINE channel requires channel_secret and channel_access_token")
		}

		return NewAdapter(secret, token), func() {}, nil
	}
}
