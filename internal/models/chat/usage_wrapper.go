package chat

import (
	"context"
	"sync/atomic"
	"time"

	"github.com/Tencent/WeKnora/internal/models/usage"
	"github.com/Tencent/WeKnora/internal/types"
)

// chatUsageSpan is the per-logical-invocation accounting threaded through the
// chat decorator chain via context. The outermost usage wrapper seeds it; each
// concrete provider notes its outbound requests on it, so a logical chat call
// that fans out to a multimodal fallback or raw-HTTP retry accumulates those
// attempts instead of being hardcoded to one.
type chatUsageSpan struct {
	providerRequests atomic.Int64
}

type chatUsageSpanKey struct{}

func withChatUsageSpan(ctx context.Context) (context.Context, *chatUsageSpan) {
	span := &chatUsageSpan{}
	return context.WithValue(ctx, chatUsageSpanKey{}, span), span
}

func chatUsageSpanFromContext(ctx context.Context) *chatUsageSpan {
	span, _ := ctx.Value(chatUsageSpanKey{}).(*chatUsageSpan)
	return span
}

// noteChatProviderRequest records one outbound provider request attempt. Each
// concrete provider calls it immediately before its HTTP/SDK round-trip.
func noteChatProviderRequest(ctx context.Context) {
	if span := chatUsageSpanFromContext(ctx); span != nil {
		span.providerRequests.Add(1)
	}
}

// usageChat records one model_usage row per logical chat invocation. It is the
// OUTERMOST decorator so latency includes the concurrency wait, the full
// provider round-trip and (for streaming) complete stream consumption.
type usageChat struct {
	inner             Chat
	config            ChatConfig
	resolvedModelName *string
}

func wrapChatUsage(c Chat, config *ChatConfig, resolvedModelName *string, err error) (Chat, error) {
	if err != nil || c == nil || config == nil {
		return c, err
	}
	return &usageChat{inner: c, config: *config, resolvedModelName: resolvedModelName}, nil
}

func (w *usageChat) GetModelName() string { return w.inner.GetModelName() }
func (w *usageChat) GetModelID() string   { return w.inner.GetModelID() }

func (w *usageChat) Chat(ctx context.Context, messages []Message, opts *ChatOptions) (*types.ChatResponse, error) {
	ctx, span := withChatUsageSpan(ctx)
	start := time.Now()
	resp, err := w.inner.Chat(ctx, messages, opts)
	var tok *types.TokenUsage
	if resp != nil {
		tok = &resp.Usage
	}
	w.record(ctx, span, start, tok, err, nil)
	return resp, err
}

func (w *usageChat) ChatStream(
	ctx context.Context, messages []Message, opts *ChatOptions,
) (<-chan types.StreamResponse, error) {
	ctx, span := withChatUsageSpan(ctx)
	start := time.Now()
	ch, err := w.inner.ChatStream(ctx, messages, opts)
	if err != nil {
		w.record(ctx, span, start, nil, err, nil)
		return ch, err
	}
	if ch == nil {
		w.record(ctx, span, start, nil, nil, nil)
		return ch, nil
	}
	out := make(chan types.StreamResponse)
	go func() {
		var usage types.TokenUsage
		sawError := false
		// Terminal accounting runs exactly once via defer, regardless of how
		// the forwarding loop exits (normal close, provider error, ctx cancel or
		// deadline). record runs before close(out) so a consumer that drains out
		// to completion observes the usage row as already written.
		defer func() {
			w.record(ctx, span, start, &usage, nil, &sawError)
			close(out)
		}()
		for resp := range ch {
			if resp.ResponseType == types.ResponseTypeError {
				sawError = true
			}
			if resp.Usage != nil {
				usage.Accumulate(*resp.Usage)
			}
			// If the downstream stops consuming out (client disconnect) and the
			// context ends, stop blocking rather than leaking the goroutine.
			select {
			case out <- resp:
			case <-ctx.Done():
				return
			}
		}
	}()
	return out, nil
}

// record builds and persists the usage row. sawError, when non-nil, is how a
// stream signals a terminal provider error (there is no returned error to pass).
func (w *usageChat) record(
	ctx context.Context, span *chatUsageSpan, start time.Time,
	tok *types.TokenUsage, err error, sawError *bool,
) {
	latencyMS := time.Since(start).Milliseconds()
	mu := &types.ModelUsage{
		ModelTenantID:     w.config.TenantID,
		ModelID:           w.config.ModelID,
		ModelName:         w.config.ModelName,
		ModelType:         string(w.config.Type),
		ModelSource:       string(w.config.Source),
		ResolvedProvider:  usage.ResolveProvider(w.config.Provider, w.config.BaseURL),
		ResolvedModelName: w.resolvedModelName,
		CallType:          types.CallTypeChat,
		LatencyMS:         &latencyMS,
		StartedAt:         &start,
		// Provider requests are counted at the actual outbound request sites
		// (multimodal fallback included). SDK-internal retries are not visible.
		ProviderRequests: int(span.providerRequests.Load()),
		LogicalRequests:  1,
	}
	if sawError != nil {
		if *sawError {
			mu.Status = types.UsageStatusError
		} else {
			mu.Status = statusFromCtx(ctx)
		}
	} else {
		mu.Status = usage.StatusFromError(err)
	}
	applyChatUsage(mu, tok)
	usage.Record(ctx, mu)
}

func statusFromCtx(ctx context.Context) types.UsageStatus {
	switch ctx.Err() {
	case context.Canceled:
		return types.UsageStatusCancelled
	case context.DeadlineExceeded:
		return types.UsageStatusTimeout
	default:
		return types.UsageStatusSuccess
	}
}

// applyChatUsage maps a types.TokenUsage into the universal token counters and
// the provider prompt-cache counters. Prompt-cache status is copied verbatim
// and never inferred backwards from token values.
func applyChatUsage(mu *types.ModelUsage, u *types.TokenUsage) {
	if u == nil {
		mu.TokenProvenance = types.TokenProvenanceUnreported
		return
	}
	// Provenance is set at provider-normalization time and copied verbatim,
	// never inferred from the final token numbers.
	switch u.TokenProvenance {
	case types.TokenProvenanceProviderReported, types.TokenProvenanceDerived:
		mu.TokenProvenance = u.TokenProvenance
		mu.InputTokens = usage.IntPtr(u.PromptTokens)
		mu.OutputTokens = usage.IntPtr(u.CompletionTokens)
		mu.TotalTokens = usage.IntPtr(u.TotalTokens)
	case types.TokenProvenanceUnsupported:
		mu.TokenProvenance = types.TokenProvenanceUnsupported
	default:
		mu.TokenProvenance = types.TokenProvenanceUnreported
	}
	if u.CacheStatus != "" {
		status := u.CacheStatus
		mu.PromptCacheStatus = &status
		if u.CacheReported {
			mu.CacheReadTokens = usage.IntPtr(u.CacheReadTokens)
			mu.CacheWriteTokens = usage.IntPtr(u.CacheWriteTokens)
			mu.CacheMissTokens = usage.IntPtr(u.CacheMissTokens)
		}
	}
}
