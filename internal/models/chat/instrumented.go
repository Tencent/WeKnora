package chat

import (
	"context"

	"github.com/Tencent/WeKnora/internal/observability"
	"github.com/Tencent/WeKnora/internal/types"
)

type instrumentedChat struct {
	inner    Chat
	provider string
}

func newInstrumentedChat(inner Chat, provider string) Chat {
	if inner == nil {
		return nil
	}
	if provider == "" {
		provider = "chat"
	}
	return &instrumentedChat{inner: inner, provider: provider}
}

func (i *instrumentedChat) Chat(ctx context.Context, messages []Message, opts *ChatOptions) (*types.ChatResponse, error) {
	done := observability.ModelCallStarted(i.provider, i.inner.GetModelName())
	resp, err := i.inner.Chat(ctx, messages, opts)
	done(err)
	return resp, err
}

func (i *instrumentedChat) ChatStream(ctx context.Context, messages []Message, opts *ChatOptions) (<-chan types.StreamResponse, error) {
	done := observability.ModelCallStarted(i.provider, i.inner.GetModelName())
	ch, err := i.inner.ChatStream(ctx, messages, opts)
	if err != nil {
		done(err)
		return nil, err
	}
	out := make(chan types.StreamResponse)
	go func() {
		defer close(out)
		defer done(nil)
		for item := range ch {
			out <- item
		}
	}()
	return out, nil
}

func (i *instrumentedChat) GetModelName() string { return i.inner.GetModelName() }
func (i *instrumentedChat) GetModelID() string   { return i.inner.GetModelID() }
