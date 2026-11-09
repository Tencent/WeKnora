package chat

import (
	"context"

	"github.com/Tencent/WeKnora/internal/models/limiter"
	"github.com/Tencent/WeKnora/internal/types"
)

// Model provider budgets are the real bottleneck shared by every LLM-backed
// background stage (summary / question / graph / multimodal enrichment), which
// all target the same model. This governor caps concurrent calls per model at
// the client layer — the one place that sees all task types — instead of at the
// asynq queue layer, whose weights are scheduling priority rather than
// throttling.
//
// The default governor limits background work per model. Shared admission
// additionally bounds interactive and embedding requests under one
// process-wide budget; excess calls fail before contacting the provider.
// concurrencyChat is the outermost wrapper and holds admission for the whole
// provider lifetime, including draining an abandoned stream.
type concurrencyChat struct {
	inner Chat
	// limit is this model's configured per-model background cap; 0 falls back
	// to the process-wide default (see limiter.GateN).
	limit   int
	gateKey string
}

func (w *concurrencyChat) GetModelName() string { return w.inner.GetModelName() }
func (w *concurrencyChat) GetModelID() string   { return w.inner.GetModelID() }

func (w *concurrencyChat) Chat(ctx context.Context, messages []Message, opts *ChatOptions) (*types.ChatResponse, error) {
	key := w.gateKey
	if key == "" {
		key = w.inner.GetModelID()
	}
	release, err := limiter.AcquireCall(ctx, key, w.inner.GetModelName(), w.limit)
	if err != nil {
		return nil, err
	}
	defer release()
	return w.inner.Chat(ctx, messages, opts)
}

func (w *concurrencyChat) ChatStream(ctx context.Context, messages []Message, opts *ChatOptions) (<-chan types.StreamResponse, error) {
	key := w.gateKey
	if key == "" {
		key = w.inner.GetModelID()
	}
	release, err := limiter.AcquireCall(ctx, key, w.inner.GetModelName(), w.limit)
	if err != nil {
		return nil, err
	}
	ch, err := w.inner.ChatStream(ctx, messages, opts)
	if err != nil || ch == nil {
		release()
		return ch, err
	}
	// Hold the slot until the stream fully drains, then release. If the
	// consumer abandons the stream (stops reading out) we would otherwise
	// block forever on the send and never release the slot; select on
	// ctx.Done(), stop forwarding, and drain until the provider terminates.
	// The lease remains held during that drain so cancellation cannot permit
	// another request while the previous provider is still running.
	out := make(chan types.StreamResponse)
	go func() {
		defer close(out)
		defer release()
		for resp := range ch {
			select {
			case out <- resp:
			case <-ctx.Done():
				// Keep admission until the provider has actually terminated.
				for range ch {
				}
				return
			}
		}
	}()
	return out, nil
}

// wrapChatConcurrency installs provider admission as the outermost decorator.
// Shared admission is opt-in; otherwise legacy background limits apply.
func wrapChatConcurrency(c Chat, limit int, err error, keys ...string) (Chat, error) {
	if err != nil || c == nil {
		return c, err
	}
	key := c.GetModelID()
	if len(keys) > 0 {
		key = keys[0]
	}
	return &concurrencyChat{inner: c, limit: limit, gateKey: key}, nil
}
