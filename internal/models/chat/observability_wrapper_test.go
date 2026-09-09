package chat

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

type observabilityFakeChat struct {
	response *types.ChatResponse
	err      error
}

func (f *observabilityFakeChat) GetModelName() string { return "test-model" }
func (f *observabilityFakeChat) GetModelID() string   { return "model-1" }
func (f *observabilityFakeChat) Chat(context.Context, []Message, *ChatOptions) (*types.ChatResponse, error) {
	return f.response, f.err
}
func (f *observabilityFakeChat) ChatStream(context.Context, []Message, *ChatOptions) (<-chan types.StreamResponse, error) {
	return nil, f.err
}

type collectingLLMObserver struct {
	mu           sync.Mutex
	observations []types.LLMCallObservation
}

func (o *collectingLLMObserver) ObserveLLMCall(observation types.LLMCallObservation) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.observations = append(o.observations, observation)
}

func TestObservableChatRecordsUsageAndMetadata(t *testing.T) {
	observer := &collectingLLMObserver{}
	ctx := types.WithLLMCallObserver(context.Background(), observer)
	ctx = types.WithLLMCallMetadata(ctx, "knowledge_qa", "prefix-hash")
	inner := &observabilityFakeChat{response: &types.ChatResponse{Usage: types.TokenUsage{
		PromptTokens: 80, CompletionTokens: 20, TotalTokens: 100,
		CacheReadTokens: 30, CacheReported: true, CacheStatus: types.PromptCacheStatusHit,
	}}}

	wrapped, err := wrapChatObservability(inner, map[string]string{
		"pricing_enabled":               "true",
		"pricing_currency":              "usd",
		"input_price_per_million":       "2",
		"output_price_per_million":      "10",
		"cache_read_price_per_million":  "1",
		"cache_write_price_per_million": "3",
	}, nil)
	require.NoError(t, err)
	response, err := wrapped.Chat(ctx, nil, nil)
	require.NoError(t, err)
	require.Equal(t, 100, response.Usage.TotalTokens)
	require.Len(t, observer.observations, 1)
	observation := observer.observations[0]
	require.Equal(t, "model-1", observation.ModelID)
	require.Equal(t, "test-model", observation.ModelName)
	require.Equal(t, "knowledge_qa", observation.Purpose)
	require.Equal(t, "prefix-hash", observation.PromptPrefixFingerprint)
	require.Equal(t, 100, observation.Usage.TotalTokens)
	require.True(t, observation.Pricing.Enabled)
	require.Equal(t, "USD", observation.Pricing.Currency)
	require.InDelta(t, 0.00033, observation.EstimatedCost, 0.0000001)
	require.True(t, observation.Success)
	require.GreaterOrEqual(t, observation.DurationMS, int64(0))
}

func TestObservableChatRecordsFailures(t *testing.T) {
	observer := &collectingLLMObserver{}
	ctx := types.WithLLMCallObserver(context.Background(), observer)
	inner := &observabilityFakeChat{err: errors.New("provider unavailable")}

	wrapped, err := wrapChatObservability(inner, nil, nil)
	require.NoError(t, err)
	_, err = wrapped.Chat(ctx, nil, nil)
	require.EqualError(t, err, "provider unavailable")
	require.Len(t, observer.observations, 1)
	require.False(t, observer.observations[0].Success)
	require.Equal(t, "provider unavailable", observer.observations[0].Error)
}
