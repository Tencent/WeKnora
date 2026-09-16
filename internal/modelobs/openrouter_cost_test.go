package modelobs

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

func TestOpenRouterReportedCostKeepsEstimateAndCurrencyBoundary(t *testing.T) {
	for _, tc := range []struct {
		provider, currency, reported string
		want                         int64
		actual                       bool
	}{
		{"openrouter", "USD", "0.0001235", 124, true},
		{"openrouter", "USD", "0", 0, true},
		{"openrouter", "CNY", "0.0001235", 15, false},
		{"generic", "USD", "0.0001235", 15, false},
		{"openrouter", "USD", "-1", 15, false},
	} {
		store := &recorderStore{price: &types.ModelPriceVersion{
			ID: "price", Currency: tc.currency,
			InputMicrounitsPerMillion: 1_000_000, OutputMicrounitsPerMillion: 1_000_000,
		}}
		model := &types.Model{ID: "m", TenantID: 1, Parameters: types.ModelParameters{Provider: tc.provider}}
		ctx := WithPurpose(context.Background(), PurposeEvaluation, true)
		active, _, err := NewRecorder(store).start(ctx, model, "chat")
		require.NoError(t, err)
		u := &types.TokenUsage{
			UsageReported: true, PromptTokens: 10, CompletionTokens: 5,
			TotalTokens: 15, ReportedCost: &tc.reported,
		}
		require.NoError(t, active.finish(ctx, types.ModelCallStatusSuccess, nil, u))
		done := store.completions[0]
		require.Equal(t, tc.want, *done.CostMicrounits)
		var snapshot map[string]any
		require.NoError(t, json.Unmarshal(done.ModelSnapshot, &snapshot))
		billing := snapshot["billing_usage"].(map[string]any)
		if tc.actual {
			require.Equal(t, "openrouter_usage_cost", billing["cost_source"])
			require.Equal(t, float64(15), billing["price_estimate_microunits"])
		} else {
			require.NotContains(t, billing, "cost_source")
		}
		require.NotContains(t, snapshot, "cost_source")
	}
}
