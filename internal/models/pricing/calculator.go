// Package pricing derives immutable model usage costs from pricing rules.
package pricing

import (
	"encoding/json"
	"fmt"
	"math/big"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
)

// CalculatorVersion identifies the pricing calculation algorithm.
const CalculatorVersion = "pricing-v1"

// Calculator derives model usage costs from observed meters and a pricing rule.
type Calculator struct{}

// NewCalculator creates a pricing calculator.
func NewCalculator() *Calculator { return &Calculator{} }

type component struct {
	known bool
	cost  *types.Decimal
	set   func(*types.ModelUsageCost, *types.Decimal)
}

// Calculate derives an immutable cost fact for one model usage row.
func (c *Calculator) Calculate(usage *types.ModelUsage, rule *types.ModelPricing) (*types.ModelUsageCost, error) {
	if usage == nil {
		return nil, fmt.Errorf("pricing: usage is nil")
	}
	now := time.Now().UTC()
	cost := &types.ModelUsageCost{
		UsageID: usage.ID, Status: types.CostStatusUnpriced,
		PricingSnapshot: types.JSON(`{}`), CalculatorVersion: CalculatorVersion, CalculatedAt: now,
	}
	if rule == nil || rule.Validate() != nil {
		return cost, nil
	}
	currency, ruleID, version := rule.Currency, rule.ID, rule.PricingVersion
	cost.Currency, cost.PricingRuleID, cost.PricingVersion = &currency, &ruleID, &version
	snapshot, err := pricingSnapshot(rule)
	if err != nil {
		return nil, err
	}
	cost.PricingSnapshot = snapshot

	var parts []component
	switch rule.BillingMode {
	case types.BillingModeChatStandardTokens:
		parts = []component{
			meterComponent(
				usage.InputTokens, rule.InputTokenPrice, rule.UnitScale,
				func(v *types.Decimal) { cost.InputCost = v },
			),
			meterComponent(
				usage.OutputTokens, rule.OutputTokenPrice, rule.UnitScale,
				func(v *types.Decimal) { cost.OutputCost = v },
			),
		}
	case types.BillingModeChatCacheSplitTokens:
		var uncached *int
		if usage.InputTokens != nil && usage.CacheReadTokens != nil && usage.CacheWriteTokens != nil {
			v := *usage.InputTokens - *usage.CacheReadTokens - *usage.CacheWriteTokens
			if v >= 0 {
				uncached = &v
			}
		}
		parts = []component{
			meterComponent(
				uncached, rule.InputTokenPrice, rule.UnitScale,
				func(v *types.Decimal) { cost.InputCost = v },
			),
			meterComponent(
				usage.CacheReadTokens, rule.CacheReadTokenPrice, rule.UnitScale,
				func(v *types.Decimal) { cost.CacheReadCost = v },
			),
			meterComponent(
				usage.CacheWriteTokens, rule.CacheWriteTokenPrice, rule.UnitScale,
				func(v *types.Decimal) { cost.CacheWriteCost = v },
			),
			meterComponent(
				usage.OutputTokens, rule.OutputTokenPrice, rule.UnitScale,
				func(v *types.Decimal) { cost.OutputCost = v },
			),
		}
	case types.BillingModeEmbeddingInputToken:
		parts = []component{meterComponent(
			usage.InputTokens, rule.InputTokenPrice, rule.UnitScale,
			func(v *types.Decimal) { cost.InputCost = v },
		)}
	case types.BillingModeEmbeddingTotalToken:
		parts = []component{meterComponent(
			usage.TotalTokens, rule.TotalTokenPrice, rule.UnitScale,
			func(v *types.Decimal) { cost.InputCost = v },
		)}
	case types.BillingModeEmbeddingProviderInput:
		parts = []component{meterComponent(
			embeddingProviderInputMeter(usage), rule.PerInputPrice, rule.UnitScale,
			func(v *types.Decimal) { cost.ProviderInputCost = v },
		)}
	case types.BillingModeEmbeddingProviderRequest:
		parts = []component{meterComponent(
			&usage.ProviderRequests, rule.PerRequestPrice, rule.UnitScale,
			func(v *types.Decimal) { cost.RequestCost = v },
		)}
	case types.BillingModeRerankInputToken:
		parts = []component{meterComponent(
			usage.InputTokens, rule.InputTokenPrice, rule.UnitScale,
			func(v *types.Decimal) { cost.InputCost = v },
		)}
	case types.BillingModeRerankTotalToken:
		parts = []component{meterComponent(
			usage.TotalTokens, rule.TotalTokenPrice, rule.UnitScale,
			func(v *types.Decimal) { cost.InputCost = v },
		)}
	case types.BillingModeRerankProviderPair:
		parts = []component{meterComponent(
			&usage.ProviderPairs, rule.PerPairPrice, rule.UnitScale,
			func(v *types.Decimal) { cost.ProviderPairCost = v },
		)}
	case types.BillingModeRerankProviderRequest:
		parts = []component{meterComponent(
			&usage.ProviderRequests, rule.PerRequestPrice, rule.UnitScale,
			func(v *types.Decimal) { cost.RequestCost = v },
		)}
	default:
		return cost, nil
	}

	known := 0
	total := new(big.Rat)
	for _, part := range parts {
		if !part.known {
			continue
		}
		known++
		part.set(cost, part.cost)
		r, _ := new(big.Rat).SetString(string(*part.cost))
		total.Add(total, r)
	}
	if known == 0 {
		return cost, nil
	}
	knownCost := decimalFromRat(total)
	cost.KnownCost = &knownCost
	if known == len(parts) {
		cost.Status = types.CostStatusPriced
		cost.TotalCost = &knownCost
	} else if len(parts) > 1 {
		cost.Status = types.CostStatusPartial
	}
	return cost, nil
}

func embeddingProviderInputMeter(usage *types.ModelUsage) *int {
	// A failed call with no outbound request has no provider-side meter, while a
	// failed pooled fan-out can observe requests without knowing how many
	// sub-batch inputs were actually sent. Pricing keeps both shapes unknown
	// rather than guessing a free or fully delivered call.
	if usage.Status != types.UsageStatusSuccess && usage.EmbeddingInputs > 0 &&
		(usage.ProviderRequests == 0 || usage.ProviderInputs == 0) {
		return nil
	}
	return &usage.ProviderInputs
}

func meterComponent(meter *int, rate *types.Decimal, unit types.Decimal, set func(*types.Decimal)) component {
	part := component{set: func(_ *types.ModelUsageCost, value *types.Decimal) { set(value) }}
	if meter == nil || rate == nil || *meter < 0 {
		return part
	}
	r, ok := new(big.Rat).SetString(string(*rate))
	if !ok {
		return part
	}
	u, ok := new(big.Rat).SetString(string(unit))
	if !ok || u.Sign() <= 0 {
		return part
	}
	r.Mul(r, new(big.Rat).SetInt64(int64(*meter)))
	r.Quo(r, u)
	v := decimalFromRat(r)
	part.known, part.cost = true, &v
	return part
}

func decimalFromRat(r *big.Rat) types.Decimal {
	scale := new(big.Int).Exp(big.NewInt(10), big.NewInt(18), nil)
	n := new(big.Int).Mul(r.Num(), scale)
	q, rem := new(big.Int), new(big.Int)
	q.QuoRem(n, r.Denom(), rem)
	if new(big.Int).Lsh(rem, 1).Cmp(r.Denom()) >= 0 {
		q.Add(q, big.NewInt(1))
	}
	whole, fraction := new(big.Int), new(big.Int)
	whole.QuoRem(q, scale, fraction)
	if fraction.Sign() == 0 {
		return types.Decimal(whole.String())
	}
	frac := fraction.String()
	frac = strings.Repeat("0", 18-len(frac)) + frac
	for len(frac) > 0 && frac[len(frac)-1] == '0' {
		frac = frac[:len(frac)-1]
	}
	return types.Decimal(whole.String() + "." + frac)
}

func pricingSnapshot(rule *types.ModelPricing) (types.JSON, error) {
	snapshot := struct {
		ResolvedProvider     string            `json:"resolved_provider"`
		ResolvedModelName    string            `json:"resolved_model_name"`
		CallType             types.CallType    `json:"call_type"`
		Currency             string            `json:"currency"`
		BillingMode          types.BillingMode `json:"billing_mode"`
		InputTokenPrice      *types.Decimal    `json:"input_token_price"`
		OutputTokenPrice     *types.Decimal    `json:"output_token_price"`
		TotalTokenPrice      *types.Decimal    `json:"total_token_price"`
		CacheReadTokenPrice  *types.Decimal    `json:"cache_read_token_price"`
		CacheWriteTokenPrice *types.Decimal    `json:"cache_write_token_price"`
		PerRequestPrice      *types.Decimal    `json:"per_request_price"`
		PerInputPrice        *types.Decimal    `json:"per_input_price"`
		PerPairPrice         *types.Decimal    `json:"per_pair_price"`
		UnitScale            types.Decimal     `json:"unit_scale"`
		EffectiveFrom        time.Time         `json:"effective_from"`
		EffectiveTo          *time.Time        `json:"effective_to"`
		PricingVersion       string            `json:"pricing_version"`
		SourceName           string            `json:"source_name"`
		SourceReference      *string           `json:"source_reference"`
		SourceRetrievedAt    *time.Time        `json:"source_retrieved_at"`
	}{
		ResolvedProvider: rule.ResolvedProvider, ResolvedModelName: rule.ResolvedModelName,
		CallType: rule.CallType, Currency: rule.Currency, BillingMode: rule.BillingMode,
		InputTokenPrice: rule.InputTokenPrice, OutputTokenPrice: rule.OutputTokenPrice,
		TotalTokenPrice: rule.TotalTokenPrice, CacheReadTokenPrice: rule.CacheReadTokenPrice,
		CacheWriteTokenPrice: rule.CacheWriteTokenPrice, PerRequestPrice: rule.PerRequestPrice,
		PerInputPrice: rule.PerInputPrice, PerPairPrice: rule.PerPairPrice, UnitScale: rule.UnitScale,
		EffectiveFrom: rule.EffectiveFrom, EffectiveTo: rule.EffectiveTo,
		PricingVersion: rule.PricingVersion, SourceName: rule.SourceName,
		SourceReference: rule.SourceReference, SourceRetrievedAt: rule.SourceRetrievedAt,
	}
	raw, err := json.Marshal(snapshot)
	return types.JSON(raw), err
}
