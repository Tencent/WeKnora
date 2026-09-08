package pricing

import (
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

func dec(v string) *types.Decimal { d := types.Decimal(v); return &d }
func meter(v int) *int            { return &v }

func pricingRule(mode types.BillingMode, callType types.CallType) *types.ModelPricing {
	return &types.ModelPricing{
		ID: "rule-1", ResolvedProvider: "fake", ResolvedModelName: "model-v1",
		CallType: callType, Currency: "USD", BillingMode: mode, UnitScale: "1",
		EffectiveFrom:  time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		PricingVersion: "fake-v1", SourceName: "deterministic-test",
	}
}

func TestChatStandardNoDoubleCountingAndReportedZero(t *testing.T) {
	rule := pricingRule(types.BillingModeChatStandardTokens, types.CallTypeChat)
	rule.UnitScale, rule.InputTokenPrice, rule.OutputTokenPrice = "100", dec("2"), dec("4")
	usage := &types.ModelUsage{
		ID: "u1", CallType: types.CallTypeChat,
		InputTokens: meter(100), OutputTokens: meter(50), TotalTokens: meter(999),
	}

	cost, err := NewCalculator().Calculate(usage, rule)
	require.NoError(t, err)
	require.Equal(t, types.CostStatusPriced, cost.Status)
	require.Equal(t, types.Decimal("4"), *cost.TotalCost)
	require.Equal(t, types.Decimal("2"), *cost.InputCost)
	require.Equal(t, types.Decimal("2"), *cost.OutputCost)

	usage.InputTokens, usage.OutputTokens = meter(0), meter(0)
	cost, err = NewCalculator().Calculate(usage, rule)
	require.NoError(t, err)
	require.Equal(t, types.CostStatusPriced, cost.Status)
	require.Equal(t, types.Decimal("0"), *cost.TotalCost, "reported zero is fully priced, not unknown")
}

func TestChatCacheSplitAndPartial(t *testing.T) {
	rule := pricingRule(types.BillingModeChatCacheSplitTokens, types.CallTypeChat)
	rule.UnitScale = "10"
	rule.InputTokenPrice, rule.CacheReadTokenPrice = dec("2"), dec("1")
	rule.CacheWriteTokenPrice, rule.OutputTokenPrice = dec("3"), dec("4")
	usage := &types.ModelUsage{
		ID: "u2", CallType: types.CallTypeChat,
		InputTokens: meter(100), CacheReadTokens: meter(20), CacheWriteTokens: meter(10), OutputTokens: meter(5),
	}

	cost, err := NewCalculator().Calculate(usage, rule)
	require.NoError(t, err)
	require.Equal(t, types.CostStatusPriced, cost.Status)
	require.Equal(t, types.Decimal("21"), *cost.TotalCost)
	require.Equal(t, types.Decimal("14"), *cost.InputCost)

	usage.OutputTokens = nil
	cost, err = NewCalculator().Calculate(usage, rule)
	require.NoError(t, err)
	require.Equal(t, types.CostStatusPartial, cost.Status)
	require.Nil(t, cost.TotalCost)
	require.Equal(t, types.Decimal("19"), *cost.KnownCost)

	usage.InputTokens = meter(10) // smaller than read + write: uncached meter invalid
	cost, err = NewCalculator().Calculate(usage, rule)
	require.NoError(t, err)
	require.Equal(t, types.CostStatusPartial, cost.Status)
	require.Nil(t, cost.InputCost)
}

func TestNullRateDiffersFromExplicitZero(t *testing.T) {
	rule := pricingRule(types.BillingModeEmbeddingInputToken, types.CallTypeEmbedding)
	usage := &types.ModelUsage{ID: "u3", CallType: types.CallTypeEmbedding, InputTokens: meter(25)}

	cost, err := NewCalculator().Calculate(usage, rule)
	require.NoError(t, err)
	require.Equal(t, types.CostStatusUnpriced, cost.Status)
	require.Nil(t, cost.TotalCost)

	rule.InputTokenPrice = dec("0")
	cost, err = NewCalculator().Calculate(usage, rule)
	require.NoError(t, err)
	require.Equal(t, types.CostStatusPriced, cost.Status)
	require.Equal(t, types.Decimal("0"), *cost.TotalCost)
}

func TestPrimaryBillingModeRejectsExtraDimensions(t *testing.T) {
	rule := pricingRule(types.BillingModeEmbeddingProviderInput, types.CallTypeEmbedding)
	rule.PerInputPrice = dec("1")
	rule.PerRequestPrice = dec("1")
	require.Error(t, rule.Validate(), "one embedding rule must not combine input and request dimensions")
}

func TestEmbeddingPrimaryBillingDimensions(t *testing.T) {
	tests := []struct {
		mode     types.BillingMode
		setRate  func(*types.ModelPricing)
		expected types.Decimal
	}{
		{types.BillingModeEmbeddingInputToken, func(r *types.ModelPricing) { r.InputTokenPrice = dec("2") }, "20"},
		{types.BillingModeEmbeddingTotalToken, func(r *types.ModelPricing) { r.TotalTokenPrice = dec("3") }, "36"},
		{types.BillingModeEmbeddingProviderInput, func(r *types.ModelPricing) { r.PerInputPrice = dec("4") }, "20"},
		{types.BillingModeEmbeddingProviderRequest, func(r *types.ModelPricing) { r.PerRequestPrice = dec("5") }, "10"},
	}
	for _, tc := range tests {
		t.Run(string(tc.mode), func(t *testing.T) {
			rule := pricingRule(tc.mode, types.CallTypeEmbedding)
			tc.setRate(rule)
			usage := &types.ModelUsage{
				ID: "embed", CallType: types.CallTypeEmbedding,
				InputTokens: meter(10), TotalTokens: meter(12), ProviderInputs: 5, ProviderRequests: 2,
			}
			cost, err := NewCalculator().Calculate(usage, rule)
			require.NoError(t, err)
			require.Equal(t, types.CostStatusPriced, cost.Status)
			require.Equal(t, tc.expected, *cost.TotalCost)
		})
	}
}

func TestFailedPooledEmbeddingUnknownProviderInputsIsUnpriced(t *testing.T) {
	rule := pricingRule(types.BillingModeEmbeddingProviderInput, types.CallTypeEmbedding)
	rule.PerInputPrice = dec("1")
	usage := &types.ModelUsage{
		ID: "failed-pool", CallType: types.CallTypeEmbedding, Status: types.UsageStatusError,
		EmbeddingInputs: 5, ProviderRequests: 1, ProviderInputs: 0,
	}
	cost, err := NewCalculator().Calculate(usage, rule)
	require.NoError(t, err)
	require.Equal(t, types.CostStatusUnpriced, cost.Status)
	require.Nil(t, cost.TotalCost)
}

func TestRerankPrimaryBillingDimensionsAndFailureStatus(t *testing.T) {
	tests := []struct {
		mode     types.BillingMode
		setRate  func(*types.ModelPricing)
		expected types.Decimal
	}{
		{types.BillingModeRerankInputToken, func(r *types.ModelPricing) { r.InputTokenPrice = dec("2") }, "20"},
		{types.BillingModeRerankTotalToken, func(r *types.ModelPricing) { r.TotalTokenPrice = dec("3") }, "36"},
		{types.BillingModeRerankProviderPair, func(r *types.ModelPricing) { r.PerPairPrice = dec("4") }, "28"},
		{types.BillingModeRerankProviderRequest, func(r *types.ModelPricing) { r.PerRequestPrice = dec("5") }, "10"},
	}
	for _, tc := range tests {
		t.Run(string(tc.mode), func(t *testing.T) {
			rule := pricingRule(tc.mode, types.CallTypeRerank)
			tc.setRate(rule)
			usage := &types.ModelUsage{
				ID: "rerank", CallType: types.CallTypeRerank, Status: types.UsageStatusError,
				InputTokens: meter(10), TotalTokens: meter(12), ProviderPairs: 7, ProviderRequests: 2,
			}
			cost, err := NewCalculator().Calculate(usage, rule)
			require.NoError(t, err)
			require.Equal(t, types.CostStatusPriced, cost.Status, "failed calls are priced from observed meters")
			require.Equal(t, tc.expected, *cost.TotalCost)
		})
	}
}

func TestMissingSinglePrimaryMeterIsUnpricedAndSnapshotStored(t *testing.T) {
	rule := pricingRule(types.BillingModeEmbeddingInputToken, types.CallTypeEmbedding)
	rule.InputTokenPrice = dec("1.25")
	cost, err := NewCalculator().Calculate(&types.ModelUsage{ID: "missing"}, rule)
	require.NoError(t, err)
	require.Equal(t, types.CostStatusUnpriced, cost.Status)
	require.Nil(t, cost.TotalCost)
	require.Contains(t, string(cost.PricingSnapshot), `"pricing_version":"fake-v1"`)
	require.Contains(t, string(cost.PricingSnapshot), `"input_token_price":"1.25"`)
}
