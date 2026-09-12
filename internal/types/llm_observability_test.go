package types

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestEstimateLLMCallCostSeparatesCacheTokens(t *testing.T) {
	usage := TokenUsage{
		PromptTokens: 1000, CompletionTokens: 100,
		CacheReadTokens: 200, CacheWriteTokens: 300,
	}
	pricing := LLMTokenPricing{
		Enabled: true, Currency: "usd", InputPerMillion: 2,
		OutputPerMillion: 10, CacheReadPerMillion: 1, CacheWritePerMillion: 3,
	}

	require.InDelta(t, 0.0031, EstimateLLMCallCost(usage, pricing), 0.0000001)
	require.Equal(t, "USD", pricing.Normalize().Currency)
}

func TestEstimateLLMCallCostDisabled(t *testing.T) {
	usage := TokenUsage{PromptTokens: 1000, CompletionTokens: 100}
	require.Zero(t, EstimateLLMCallCost(usage, LLMTokenPricing{}))
}

func TestEstimateLLMCallCostClampsInconsistentCounters(t *testing.T) {
	usage := TokenUsage{
		PromptTokens: 100, CompletionTokens: -1,
		CacheReadTokens: 200, CacheWriteTokens: 50,
	}
	pricing := LLMTokenPricing{
		Enabled: true, InputPerMillion: 2,
		OutputPerMillion: 10, CacheReadPerMillion: 1, CacheWritePerMillion: 3,
	}
	require.InDelta(t, 0.0001, EstimateLLMCallCost(usage, pricing), 0.0000001)
}

type globalObserverStub struct{}

func (globalObserverStub) ObserveLLMCall(LLMCallObservation) {}

func TestGlobalLLMCallObserver(t *testing.T) {
	SetGlobalLLMCallObserver(globalObserverStub{})
	t.Cleanup(func() { SetGlobalLLMCallObserver(nil) })
	observer, ok := GlobalLLMCallObserver()
	require.True(t, ok)
	require.NotNil(t, observer)
	SetGlobalLLMCallObserver(nil)
	observer, ok = GlobalLLMCallObserver()
	require.False(t, ok)
	require.Nil(t, observer)
}
