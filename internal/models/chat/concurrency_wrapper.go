package chat

import (
	"context"

	"github.com/Tencent/WeKnora/internal/models/limiter"
	"github.com/Tencent/WeKnora/internal/types"
)

// Model provider budgets are the real bottleneck shared by every LLM-backed
// background stage (summary / question / graph / multimodal enrichment), which
// all target the same model. This governor throttles background calls per model
// at the client layer — the one place that sees all task types — instead of at
// the asynq queue layer, whose weights are scheduling priority rather than
// throttling. Three dimensions are enforced together (see limiter.Limits):
// concurrency, requests-per-minute (RPM), and tokens-per-minute (TPM).
//
// Only background (asynq worker) calls are throttled; interactive chat is left
// untouched (see types.IsBackgroundTask), so a document-ingestion storm cannot
// exhaust the provider yet user-facing latency is never gated behind the
// limiter. The governor singleton itself lives in the limiter package so chat,
// vlm and embedding share the same limiters and per-model budget.

// concurrencyChat throttles background LLM calls through the shared per-model
// governor. It is the outermost wrapper so the reservation is held only around
// the actual provider round-trip and the wait time is excluded from the inner
// debug/langfuse timing.
type concurrencyChat struct {
	inner Chat
	// limits are this model's configured per-model background caps; a 0 in any
	// dimension falls back to the process-wide default (see limiter.Admit).
	limits limiter.Limits
}

func (w *concurrencyChat) GetModelName() string { return w.inner.GetModelName() }
func (w *concurrencyChat) GetModelID() string   { return w.inner.GetModelID() }

// estTokens sizes the TPM reservation: estimated input tokens plus the
// requested output budget (MaxCompletionTokens / MaxTokens) when set. The
// reservation is reconciled against the API's authoritative Usage on Release.
func estTokens(messages []Message, opts *ChatOptions) int {
	total := 0
	for i := range messages {
		total += limiter.EstimateTokens(messages[i].Content)
		total += limiter.EstimateTokens(messages[i].ReasoningContent)
		for _, part := range messages[i].MultiContent {
			total += limiter.EstimateTokens(part.Text)
		}
	}
	if opts != nil {
		switch {
		case opts.MaxCompletionTokens > 0:
			total += opts.MaxCompletionTokens
		case opts.MaxTokens > 0:
			total += opts.MaxTokens
		}
	}
	return total
}

func (w *concurrencyChat) Chat(ctx context.Context, messages []Message, opts *ChatOptions) (*types.ChatResponse, error) {
	res := limiter.Admit(ctx, w.inner.GetModelID(), w.limits, estTokens(messages, opts))
	resp, err := w.inner.Chat(ctx, messages, opts)
	actual := -1
	if resp != nil && resp.Usage.TotalTokens > 0 {
		actual = resp.Usage.TotalTokens
	}
	res.Release(actual)
	return resp, err
}

func (w *concurrencyChat) ChatStream(ctx context.Context, messages []Message, opts *ChatOptions) (<-chan types.StreamResponse, error) {
	res := limiter.Admit(ctx, w.inner.GetModelID(), w.limits, estTokens(messages, opts))
	ch, err := w.inner.ChatStream(ctx, messages, opts)
	if err != nil || ch == nil {
		res.Release(-1)
		return ch, err
	}
	// Hold the reservation until the stream fully drains, then release with the
	// final usage. If the consumer abandons the stream (stops reading out) we
	// would otherwise block forever on the send and never release; select on
	// ctx.Done() so a cancelled call frees promptly, and drain the inner
	// channel in the background so the upstream producer can exit.
	out := make(chan types.StreamResponse)
	go func() {
		actual := -1
		defer func() { res.Release(actual) }()
		defer close(out)
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

// wrapChatConcurrency installs the background governor as the outermost Chat
// decorator. It is always applied; when no limiter is installed or the call is
// interactive, the wrapper is a cheap passthrough.
func wrapChatConcurrency(c Chat, limits limiter.Limits, err error) (Chat, error) {
	if err != nil || c == nil {
		return c, err
	}
	return &concurrencyChat{inner: c, limits: limits}, nil
}
