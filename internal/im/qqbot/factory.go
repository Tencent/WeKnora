package qqbot

import (
	"context"
	"fmt"

	"github.com/Tencent/WeKnora/internal/im"
	"github.com/Tencent/WeKnora/internal/logger"
)

func NewFactory() im.AdapterFactory {
	return func(factoryCtx context.Context, channel *im.IMChannel, msgHandler func(context.Context, *im.IncomingMessage) error) (im.Adapter, context.CancelFunc, error) {
		creds, err := im.ParseCredentials(channel.Credentials)
		if err != nil {
			return nil, nil, fmt.Errorf("parse qqbot credentials: %w", err)
		}

		mode := im.ResolveMode(channel, "websocket")
		if mode != "websocket" {
			return nil, nil, fmt.Errorf("unsupported qqbot mode: %s (only websocket is supported)", mode)
		}

		client, err := NewClient(
			im.GetString(creds, "app_id"),
			im.GetString(creds, "client_secret"),
			im.GetString(creds, "api_base_url"),
			im.GetString(creds, "gateway_url"),
		)
		if err != nil {
			return nil, nil, err
		}

		longConn := NewLongConnClient(client, msgHandler)
		wsCtx, wsCancel := context.WithCancel(context.Background())
		go func() {
			if err := longConn.Start(wsCtx); err != nil && wsCtx.Err() == nil {
				logger.Errorf(context.Background(), "[IM] QQBot long connection stopped for channel %s: %v", channel.ID, err)
			}
		}()

		adapter := NewAdapter(client)
		return adapter, func() {
			wsCancel()
			longConn.Stop()
		}, nil
	}
}
