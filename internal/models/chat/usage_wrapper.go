package chat

import (
	"context"
	"time"

	"github.com/Tencent/WeKnora/internal/modelusage"
	"github.com/Tencent/WeKnora/internal/types"
)

// usageChat records every chat call (tokens, latency, outcome) as a model
// usage event for the usage dashboard.
type usageChat struct {
	inner    Chat
	source   types.ModelSource
	provider string
}

func (u *usageChat) GetModelName() string { return u.inner.GetModelName() }
func (u *usageChat) GetModelID() string   { return u.inner.GetModelID() }

func (u *usageChat) Chat(ctx context.Context, messages []Message, opts *ChatOptions) (*types.ChatResponse, error) {
	start := time.Now()
	resp, err := u.inner.Chat(ctx, messages, opts)
	var usage *types.TokenUsage
	if resp != nil {
		usage = &resp.Usage
	}
	u.record(ctx, "chat.completion", usage, len(messages), time.Since(start), err == nil, modelusage.ErrorType(err))
	return resp, err
}

func (u *usageChat) ChatStream(
	ctx context.Context, messages []Message, opts *ChatOptions,
) (<-chan types.StreamResponse, error) {
	start := time.Now()
	ch, err := u.inner.ChatStream(ctx, messages, opts)
	if err != nil {
		u.record(ctx, "chat.completion.stream", nil, len(messages), time.Since(start), false, modelusage.ErrorType(err))
		return ch, err
	}
	if ch == nil {
		u.record(ctx, "chat.completion.stream", nil, len(messages), time.Since(start), true, "")
		return nil, nil
	}

	wrapped := make(chan types.StreamResponse)
	go func() {
		defer close(wrapped)
		var usage *types.TokenUsage
		success := true
		errorType := ""
		for resp := range ch {
			if resp.Usage != nil {
				usage = resp.Usage
			}
			if resp.ResponseType == types.ResponseTypeError {
				success = false
				if errorType == "" {
					errorType = "stream_error"
				}
			}
			wrapped <- resp
		}
		u.record(ctx, "chat.completion.stream", usage, len(messages), time.Since(start), success, errorType)
	}()
	return wrapped, nil
}

func (u *usageChat) record(
	ctx context.Context,
	kind string,
	usage *types.TokenUsage,
	inputItems int,
	duration time.Duration,
	success bool,
	errorType string,
) {
	event := types.ModelUsageEvent{
		ModelID:     u.inner.GetModelID(),
		ModelName:   u.inner.GetModelName(),
		ModelType:   types.ModelTypeKnowledgeQA,
		ModelSource: u.source,
		Provider:    u.provider,
		RequestKind: kind,
		UsageSource: usageSource(usage),
		InputItems:  inputItems,
		DurationMs:  duration.Milliseconds(),
		Success:     success,
		ErrorType:   errorType,
	}
	if usage != nil {
		event.PromptTokens = int64(usage.PromptTokens)
		event.CompletionTokens = int64(usage.CompletionTokens)
		event.CachedTokens = int64(usage.CachedTokens)
		event.TotalTokens = int64(usage.TotalTokens)
	}
	modelusage.Record(ctx, event)
}

func usageSource(usage *types.TokenUsage) string {
	if usage == nil {
		return types.ModelUsageSourceMissing
	}
	if usage.PromptTokens == 0 && usage.CompletionTokens == 0 && usage.TotalTokens == 0 {
		return types.ModelUsageSourceMissing
	}
	return types.ModelUsageSourceProvider
}

// wrapChatUsage applies the usage-recording decorator. Sits above the real
// provider call (below langfuse/debug) so its latency reflects the true
// upstream round-trip.
func wrapChatUsage(c Chat, err error, config *ChatConfig) (Chat, error) {
	if err != nil || c == nil {
		return c, err
	}
	source := types.ModelSource("")
	provider := ""
	if config != nil {
		source = config.Source
		provider = config.Provider
	}
	return &usageChat{inner: c, source: source, provider: provider}, nil
}
