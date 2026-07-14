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
// Only background (asynq worker) calls are throttled; interactive chat is left
// untouched (see types.IsBackgroundTask), so a document-ingestion storm cannot
// exhaust the provider yet user-facing latency is never gated behind the
// semaphore. The governor singleton itself lives in the limiter package so chat
// and vlm share the same limiter and per-model budget.

// concurrencyChat throttles background LLM calls through a per-model
// distributed semaphore. It is the outermost wrapper so the slot is held only
// around the actual provider round-trip and the wait time is excluded from the
// inner debug/langfuse timing.
type concurrencyChat struct {
	inner Chat
	// limit is this model's configured per-model background cap; 0 falls back
	// to the process-wide default (see limiter.GateN).
	limit int
}

func (w *concurrencyChat) GetModelName() string { return w.inner.GetModelName() }
func (w *concurrencyChat) GetModelID() string   { return w.inner.GetModelID() }

func (w *concurrencyChat) Chat(ctx context.Context, messages []Message, opts *ChatOptions) (*types.ChatResponse, error) {
	release := limiter.GateNamedN(ctx, w.inner.GetModelID(), w.inner.GetModelName(), w.limit)
	defer release()
	return w.inner.Chat(ctx, messages, opts)
}

func (w *concurrencyChat) ChatStream(ctx context.Context, messages []Message, opts *ChatOptions) (<-chan types.StreamResponse, error) {
	release := limiter.GateNamedN(ctx, w.inner.GetModelID(), w.inner.GetModelName(), w.limit)
	ch, err := w.inner.ChatStream(ctx, messages, opts)
	if err != nil || ch == nil {
		release()
		return ch, err
	}
	// Hold the slot until the stream fully drains, then release. If the
	// consumer abandons the stream (stops reading out) we would otherwise
	// block forever on the send and never release the slot; select on
	// ctx.Done() so a cancelled call frees its slot promptly, and drain the
	// inner channel in the background so the upstream producer can exit.
	out := make(chan types.StreamResponse)
	go func() {
		defer close(out)
		defer release()
		for resp := range ch {
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

// wrapChatConcurrency installs the background concurrency governor as the
// outermost Chat decorator. It is always applied; when no limiter is installed
// or the call is interactive, the wrapper is a cheap passthrough.
func wrapChatConcurrency(c Chat, limit int, err error) (Chat, error) {
	if err != nil || c == nil {
		return c, err
	}
	return &concurrencyChat{inner: c, limit: limit}, nil
}
