package service

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

const validPricingYAML = `schema_version: 1
pricing_version: "fixture-v1"
source:
  name: "fixture-source"
  reference: "https://example.invalid/pricing"
  retrieved_at: "2026-01-01T00:00:00Z"
rules:
  - id: "10000000-0000-4000-8000-000000000001"
    resolved_provider: "fake"
    resolved_model_name: "fake-model"
    call_type: "chat"
    billing_mode: "chat_standard_tokens"
    currency: "TEST"
    unit_scale: "1000"
    input_token_price: "0.123456789012345678"
    output_token_price: "0"
    total_token_price: null
    cache_read_token_price: null
    cache_write_token_price: null
    per_request_price: null
    per_input_price: null
    per_pair_price: null
    effective_from: "2026-01-01T00:00:00Z"
    effective_to: null
    closes_rule_id: null
`

func TestParsePricingSourcePreservesExactValuesAndProvenance(t *testing.T) {
	source, rules, err := parsePricingSource(strings.NewReader(validPricingYAML))
	require.NoError(t, err)
	require.Equal(t, "fixture-v1", source.PricingVersion)
	require.Len(t, rules, 1)
	rule := rules[0].Pricing
	require.Equal(t, types.Decimal("0.123456789012345678"), *rule.InputTokenPrice)
	require.Equal(t, types.Decimal("0"), *rule.OutputTokenPrice)
	require.Nil(t, rule.TotalTokenPrice)
	require.Equal(t, "fixture-v1", rule.PricingVersion)
	require.Equal(t, "fixture-source", rule.SourceName)
	require.Equal(t, "https://example.invalid/pricing", *rule.SourceReference)
	require.Equal(t, time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), *rule.SourceRetrievedAt)
}

func TestParsePricingSourceRejectsInvalidInput(t *testing.T) {
	replace := func(old, replacement string) string {
		return strings.Replace(validPricingYAML, old, replacement, 1)
	}
	secondRule := strings.Replace(strings.Split(validPricingYAML, "rules:\n")[1],
		"10000000-0000-4000-8000-000000000001", "10000000-0000-4000-8000-000000000002", 1)
	tests := map[string]string{
		"unknown field":  replace("pricing_version:", "unexpected: true\npricing_version:"),
		"schema version": replace("schema_version: 1", "schema_version: 2"),
		"numeric decimal": replace(
			`input_token_price: "0.123456789012345678"`, "input_token_price: 0.25",
		),
		"invalid decimal": replace(
			`input_token_price: "0.123456789012345678"`, `input_token_price: "NaN"`,
		),
		"excess decimal scale": replace(
			`input_token_price: "0.123456789012345678"`,
			`input_token_price: "0.1234567890123456789"`,
		),
		"sub-microsecond time": replace(
			`effective_from: "2026-01-01T00:00:00Z"`,
			`effective_from: "2026-01-01T00:00:00.000000001Z"`,
		),
		"whitespace currency": replace(`currency: "TEST"`, `currency: "   "`),
		"uppercase uuid": replace(
			"10000000-0000-4000-8000-000000000001", "A0000000-0000-4000-8000-00000000000A",
		),
		"compact uuid": replace(
			"10000000-0000-4000-8000-000000000001", "a000000000004000800000000000000a",
		),
		"braced uuid": replace(
			"10000000-0000-4000-8000-000000000001", "{a0000000-0000-4000-8000-00000000000a}",
		),
		"urn uuid": replace(
			"10000000-0000-4000-8000-000000000001",
			"urn:uuid:a0000000-0000-4000-8000-00000000000a",
		),
		"noncanonical closure": replace(
			"closes_rule_id: null", `closes_rule_id: "URN:UUID:a0000000-0000-4000-8000-00000000000a"`,
		),
		"duplicate id":      validPricingYAML + strings.Split(validPricingYAML, "rules:\n")[1],
		"overlapping rules": validPricingYAML + secondRule,
		"second document":   validPricingYAML + "---\nschema_version: 1\n",
	}
	for name, input := range tests {
		t.Run(name, func(t *testing.T) {
			_, _, err := parsePricingSource(strings.NewReader(input))
			require.Error(t, err)
		})
	}
}

type pricingImportStub struct {
	rules  []types.PricingImportRule
	result types.PricingImportResult
}

func (s *pricingImportStub) ImportPricingBatch(
	_ context.Context, rules []types.PricingImportRule,
) (*types.PricingImportResult, error) {
	s.rules = rules
	return &s.result, nil
}
func (*pricingImportStub) CreatePricing(context.Context, *types.ModelPricing) error { return nil }
func (*pricingImportStub) ResolvePricing(
	context.Context, string, string, types.CallType, time.Time,
) (*types.ModelPricing, error) {
	return nil, nil
}
func (*pricingImportStub) CreateCost(context.Context, *types.ModelUsageCost) error { return nil }
func (*pricingImportStub) GetCostByUsageID(context.Context, uint64, string) (*types.ModelUsageCost, error) {
	return nil, nil
}

func TestPricingImporterReportsRepositoryOutcome(t *testing.T) {
	stub := &pricingImportStub{result: types.PricingImportResult{Inserted: 1, NoOp: 2, Closed: 1}}
	path := t.TempDir() + "/pricing.yaml"
	require.NoError(t, os.WriteFile(path, []byte(validPricingYAML), 0o600))
	result, err := NewPricingImporter(stub).ImportFile(context.Background(), path)
	require.NoError(t, err)
	require.Len(t, stub.rules, 1)
	require.Equal(t, &PricingFileImportResult{
		PricingVersion: "fixture-v1", SourceName: "fixture-source", Inserted: 1, NoOp: 2, Closed: 1,
	}, result)
}
