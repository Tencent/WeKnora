package chat

import (
	"context"
	"sync"

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
// semaphore.
var (
	concurrencyMu      sync.RWMutex
	concurrencyLimiter limiter.ModelConcurrencyLimiter
	concurrencyLimit   int
)

// SetConcurrencyLimiter installs the process-wide background chat concurrency
// governor and the default per-model limit. Called once at startup (see
// container wiring). Passing a nil limiter or a non-positive limit disables
// governance (all calls pass through).
func SetConcurrencyLimiter(l limiter.ModelConcurrencyLimiter, defaultLimit int) {
	concurrencyMu.Lock()
	defer concurrencyMu.Unlock()
	concurrencyLimiter = l
	concurrencyLimit = defaultLimit
}

func getConcurrencyLimiter() (limiter.ModelConcurrencyLimiter, int) {
	concurrencyMu.RLock()
	defer concurrencyMu.RUnlock()
	return concurrencyLimiter, concurrencyLimit
}

// concurrencyChat throttles background LLM calls through a per-model
// distributed semaphore. It is the outermost wrapper so the slot is held only
// around the actual provider round-trip and the wait time is excluded from the
// inner debug/langfuse timing.
type concurrencyChat struct {
	inner Chat
}

func (w *concurrencyChat) GetModelName() string { return w.inner.GetModelName() }
func (w *concurrencyChat) GetModelID() string   { return w.inner.GetModelID() }

// gate acquires a concurrency slot when the call is a background task and a
// limiter is installed. Returns a release func (always safe to call) and
// whether the inner call may proceed — it always may; the gate never blocks a
// call permanently and fails open on any limiter error.
func (w *concurrencyChat) gate(ctx context.Context) func() {
	lim, limit := getConcurrencyLimiter()
	if lim == nil || limit <= 0 || !types.IsBackgroundTask(ctx) {
		return func() {}
	}
	release, err := lim.Acquire(ctx, w.inner.GetModelID(), limit)
	if err != nil || release == nil {
		return func() {}
	}
	return release
}

func (w *concurrencyChat) Chat(ctx context.Context, messages []Message, opts *ChatOptions) (*types.ChatResponse, error) {
	release := w.gate(ctx)
	defer release()
	return w.inner.Chat(ctx, messages, opts)
}

func (w *concurrencyChat) ChatStream(ctx context.Context, messages []Message, opts *ChatOptions) (<-chan types.StreamResponse, error) {
	release := w.gate(ctx)
	ch, err := w.inner.ChatStream(ctx, messages, opts)
	if err != nil || ch == nil {
		release()
		return ch, err
	}
	// Hold the slot until the stream fully drains, then release.
	out := make(chan types.StreamResponse)
	go func() {
		defer close(out)
		defer release()
		for resp := range ch {
			out <- resp
		}
	}()
	return out, nil
}

// wrapChatConcurrency installs the background concurrency governor as the
// outermost Chat decorator. It is always applied; when no limiter is installed
// or the call is interactive, the wrapper is a cheap passthrough.
func wrapChatConcurrency(c Chat, err error) (Chat, error) {
	if err != nil || c == nil {
		return c, err
	}
	return &concurrencyChat{inner: c}, nil
}
