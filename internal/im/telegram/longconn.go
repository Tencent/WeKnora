package telegram

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/Tencent/WeKnora/internal/im"
	"github.com/Tencent/WeKnora/internal/logger"
)

// MessageHandler is called when an IM message is received via long polling.
type MessageHandler func(ctx context.Context, msg *im.IncomingMessage) error

// LongConnClient manages a Telegram long-polling connection.
type LongConnClient struct {
	botToken   string
	handler    MessageHandler
	offset     int
	httpClient *http.Client
}

// NewLongConnClient creates a Telegram long-polling client.
func NewLongConnClient(botToken string, handler MessageHandler) *LongConnClient {
	return &LongConnClient{
		botToken:   botToken,
		handler:    handler,
		httpClient: &http.Client{Timeout: 35 * time.Second},
	}
}

// Start begins the long-polling loop. It blocks until ctx is cancelled.
func (c *LongConnClient) Start(ctx context.Context) error {
	logger.Infof(ctx, "[IM] Telegram long polling connecting...")

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		updates, err := c.getUpdates(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			logger.Errorf(ctx, "[Telegram] getUpdates error: %v", err)
			// Back off on error
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(3 * time.Second):
			}
			continue
		}

		for _, update := range updates {
			if update.UpdateID >= c.offset {
				c.offset = update.UpdateID + 1
			}

			msg := parseUpdate(&update)
			if msg == nil {
				continue
			}

			if err := c.handler(ctx, msg); err != nil {
				logger.Errorf(ctx, "[Telegram] Handle message error: %v", err)
			}
		}
	}
}

func (c *LongConnClient) getUpdates(ctx context.Context) ([]telegramUpdate, error) {
	url := fmt.Sprintf("https://api.telegram.org/bot%s/getUpdates", c.botToken)

	body := map[string]interface{}{
		"offset":          c.offset,
		"timeout":         30,
		"allowed_updates": []string{"message"},
	}
	jsonBody, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(jsonBody))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var apiResp struct {
		OK          bool             `json:"ok"`
		Description string           `json:"description"`
		Result      []telegramUpdate `json:"result"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&apiResp); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}
	if !apiResp.OK {
		return nil, fmt.Errorf("getUpdates failed: %s", apiResp.Description)
	}

	return apiResp.Result, nil
}
