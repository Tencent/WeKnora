package whatsapp

import (
	"context"
	"fmt"

	"github.com/Tencent/WeKnora/internal/im"
)

// NewFactory returns an im.AdapterFactory for WhatsApp Cloud API channels.
// Only "webhook" mode is supported: Meta delivers events exclusively over
// HTTPS callbacks.
func NewFactory() im.AdapterFactory {
	return func(
		_ context.Context,
		channel *im.IMChannel,
		_ func(context.Context, *im.IncomingMessage) error,
	) (im.Adapter, context.CancelFunc, error) {
		creds, err := im.ParseCredentials(channel.Credentials)
		if err != nil {
			return nil, nil, fmt.Errorf("parse whatsapp credentials: %w", err)
		}

		mode := im.ResolveMode(channel, "webhook")
		if mode != "webhook" {
			return nil, nil, fmt.Errorf("unsupported whatsapp mode: %s (only webhook is supported)", mode)
		}

		adapter := NewAdapter(
			im.GetString(creds, "access_token"),
			im.GetString(creds, "phone_number_id"),
			im.GetString(creds, "app_secret"),
			im.GetString(creds, "verify_token"),
		)
		for _, field := range []struct{ name, value string }{
			{"access_token", adapter.accessToken},
			{"phone_number_id", adapter.phoneNumberID},
			{"app_secret", adapter.appSecret},
			{"verify_token", adapter.verifyToken},
		} {
			if field.value == "" {
				return nil, nil, fmt.Errorf("whatsapp %s is required", field.name)
			}
		}

		return adapter, nil, nil
	}
}
