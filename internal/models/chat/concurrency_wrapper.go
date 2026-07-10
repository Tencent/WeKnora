package chat

import (
	"context"

	"github.com/Tencent/WeKnora/internal/models/limiter"
	"github.com/Tencent/WeKnora/internal/types"
)

type quotaChat struct {
	inner  Chat
	key    string
	limits limiter.Limits
}

func (w *quotaChat) GetModelName() string { return w.inner.GetModelName() }
func (w *quotaChat) GetModelID() string   { return w.inner.GetModelID() }

func (w *quotaChat) Chat(ctx context.Context, messages []Message, opts *ChatOptions) (*types.ChatResponse, error) {
	estimate := estimateChatRequestTokens(w.inner.GetModelName(), messages, opts)
	permit, err := limiter.Admit(ctx, w.key, w.limits, estimate)
	if err != nil {
		return nil, err
	}
	resp, err := w.inner.Chat(ctx, messages, opts)
	actual := 0
	if resp != nil {
		actual = resp.Usage.TotalTokens
	}
	permit.Complete(actual)
	return resp, err
}

func (w *quotaChat) ChatStream(ctx context.Context, messages []Message, opts *ChatOptions) (<-chan types.StreamResponse, error) {
	estimate := estimateChatRequestTokens(w.inner.GetModelName(), messages, opts)
	permit, err := limiter.Admit(ctx, w.key, w.limits, estimate)
	if err != nil {
		return nil, err
	}
	ch, err := w.inner.ChatStream(ctx, messages, opts)
	if err != nil || ch == nil {
		permit.Release()
		return ch, err
	}
	out := make(chan types.StreamResponse)
	go func() {
		defer close(out)
		actual := 0
		defer func() { permit.Complete(actual) }()
		for resp := range ch {
			if resp.Usage != nil && resp.Usage.TotalTokens > 0 {
				actual = resp.Usage.TotalTokens
			}
			select {
			case out <- resp:
			case <-ctx.Done():
				go func() {
					for range ch {
					}
				}()
				return
			}
		}
	}()
	return out, nil
}

func wrapChatConcurrency(c Chat, config *ChatConfig, err error) (Chat, error) {
	if err != nil || c == nil {
		return c, err
	}
	return &quotaChat{
		inner: c,
		key:   limiter.QuotaKey(config.TenantID, c.GetModelID(), config.QuotaGroup),
		limits: limiter.Limits{
			MaxConcurrency:                config.MaxConcurrency,
			RequestsPerMinute:             config.RequestsPerMinute,
			TokensPerMinute:               config.TokensPerMinute,
			InteractiveConcurrencyReserve: config.InteractiveConcurrencyReserve,
		},
	}, nil
}
