package service

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

// TestPricingAndAggregationPostgreSQLSmoke is deliberately opt-in because it
// requires a migrated local development database. Every write is enclosed in
// an outer transaction and rolled back, including writes made by the nested
// repository transactions.
func TestPricingAndAggregationPostgreSQLSmoke(t *testing.T) {
	dsn := os.Getenv("WEKNORA_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("WEKNORA_TEST_POSTGRES_DSN is not set")
	}

	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, sqlDB.Close()) })

	tx := db.Begin()
	require.NoError(t, tx.Error)
	t.Cleanup(func() { require.NoError(t, tx.Rollback().Error) })

	ctx := context.Background()
	pricingRepo := repository.NewPricingRepository(tx)
	importer := NewPricingImporter(pricingRepo)

	provider := "fake-pg-smoke-" + uuid.NewString()
	model := "fake-model-" + uuid.NewString()
	oldID, replacementID := uuid.NewString(), uuid.NewString()
	t0 := "2026-01-01T00:00:00Z"
	t1 := "2026-02-01T00:00:00Z"

	v1 := pricingPostgresFixture(oldID, "pg-smoke-v1", provider, model, t0, nil)
	v1Path := writePricingPostgresFixture(t, v1)
	result, err := importer.ImportFile(ctx, v1Path)
	require.NoError(t, err)
	require.Equal(t, 1, result.Inserted)
	require.Zero(t, result.NoOp)
	require.Zero(t, result.Closed)

	var storedV1 types.ModelPricing
	require.NoError(t, tx.First(&storedV1, "id = ?", oldID).Error)
	require.Equal(t, types.Decimal("0.100000000000000001"), *storedV1.InputTokenPrice)
	require.Equal(t, types.Decimal("0.200000000000000002"), *storedV1.OutputTokenPrice)
	require.Equal(t, "pg-smoke-v1", storedV1.PricingVersion)
	require.Equal(t, "weknora-postgres-smoke", storedV1.SourceName)
	require.Equal(t, "https://example.invalid/pg-smoke", *storedV1.SourceReference)
	require.True(t, storedV1.EffectiveFrom.Equal(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)))
	require.Nil(t, storedV1.EffectiveTo)

	result, err = importer.ImportFile(ctx, v1Path)
	require.NoError(t, err)
	require.Zero(t, result.Inserted)
	require.Equal(t, 1, result.NoOp)
	require.Zero(t, result.Closed)

	v2 := pricingPostgresFixture(replacementID, "pg-smoke-v2", provider, model, t1, &oldID)
	result, err = importer.ImportFile(ctx, writePricingPostgresFixture(t, v2))
	require.NoError(t, err)
	require.Equal(t, 1, result.Inserted)
	require.Equal(t, 1, result.Closed)
	require.NoError(t, tx.First(&storedV1, "id = ?", oldID).Error)
	require.NotNil(t, storedV1.EffectiveTo)
	require.True(t, storedV1.EffectiveTo.Equal(time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)))

	// Replaying the original open v1 source after v2 closed it is a no-op and
	// must not reopen or otherwise mutate the persisted historical interval.
	result, err = importer.ImportFile(ctx, v1Path)
	require.NoError(t, err)
	require.Zero(t, result.Inserted)
	require.Equal(t, 1, result.NoOp)
	require.NoError(t, tx.First(&storedV1, "id = ?", oldID).Error)
	require.NotNil(t, storedV1.EffectiveTo)
	require.True(t, storedV1.EffectiveTo.Equal(time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)))

	assertPostgreSQLOverlapRollback(t, ctx, tx, pricingRepo, provider, model)
	assertPostgreSQLAggregation(t, ctx, tx)
}

func assertPostgreSQLOverlapRollback(
	t *testing.T,
	ctx context.Context,
	tx *gorm.DB,
	pricingRepo interface {
		ImportPricingBatch(context.Context, []types.PricingImportRule) (*types.PricingImportResult, error)
	},
	provider string,
	model string,
) {
	t.Helper()
	firstID, overlapID := uuid.NewString(), uuid.NewString()
	from := time.Date(2026, 2, 2, 0, 0, 0, 0, time.UTC)
	input, output := types.Decimal("0.100000000000000001"), types.Decimal("0.200000000000000002")
	newRule := func(id, ruleProvider, ruleModel string) types.PricingImportRule {
		return types.PricingImportRule{Pricing: types.ModelPricing{
			ID: id, ResolvedProvider: ruleProvider, ResolvedModelName: ruleModel,
			CallType: types.CallTypeChat, Currency: "TEST",
			BillingMode:     types.BillingModeChatStandardTokens,
			InputTokenPrice: &input, OutputTokenPrice: &output, UnitScale: types.Decimal("1"),
			EffectiveFrom: from, PricingVersion: "pg-overlap", SourceName: "weknora-postgres-smoke",
		}}
	}

	_, err := pricingRepo.ImportPricingBatch(ctx, []types.PricingImportRule{
		newRule(firstID, provider+"-batch-first", model),
		newRule(overlapID, provider, model),
	})
	require.ErrorContains(t, err, "overlapping model_pricing effective interval")

	var count int64
	require.NoError(t, tx.Model(&types.ModelPricing{}).Where("id = ?", firstID).Count(&count).Error)
	require.Zero(t, count, "the earlier insert in the failed batch must roll back")
}

func assertPostgreSQLAggregation(t *testing.T, ctx context.Context, tx *gorm.DB) {
	t.Helper()
	tenantID := uint64(991001)
	runID := uuid.NewString()
	require.NoError(t, tx.Exec(
		`
		INSERT INTO evaluation_runs
			(id, task_id, tenant_id, dataset_id, embedding_model_id, chat_model_id,
			 status, config_snapshot, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, '{}'::jsonb, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`,
		runID, "pg-smoke-"+uuid.NewString(), tenantID, "fake-dataset", "fake-embedding", "fake-chat",
		types.EvaluationStatuePending,
	).Error)

	usageRepo := repository.NewModelUsageRepository(tx)
	chat := postgresAggregationUsage(tenantID, runID, types.CallTypeChat)
	inputTokens, outputTokens, totalTokens := 1, 1, 2
	chat.InputTokens, chat.OutputTokens, chat.TotalTokens = &inputTokens, &outputTokens, &totalTokens
	embedding := postgresAggregationUsage(tenantID, runID, types.CallTypeEmbedding)
	rerank := postgresAggregationUsage(tenantID, runID, types.CallTypeRerank)
	for _, usage := range []*types.ModelUsage{chat, embedding, rerank} {
		require.NoError(t, usageRepo.Create(ctx, usage))
	}

	currency := "TEST"
	inputCost := types.Decimal("0.100000000000000001")
	outputCost := types.Decimal("0.200000000000000002")
	totalCost := types.Decimal("0.300000000000000003")
	require.NoError(t, tx.Create(&types.ModelUsageCost{
		UsageID: chat.ID, Status: types.CostStatusPriced, Currency: &currency,
		InputCost: &inputCost, OutputCost: &outputCost, TotalCost: &totalCost, KnownCost: &totalCost,
		PricingSnapshot: types.JSON(`{}`), CalculatorVersion: "pg-smoke", CalculatedAt: time.Now().UTC(),
	}).Error)
	require.NoError(t, tx.Create(&types.ModelUsageCost{
		UsageID: rerank.ID, Status: types.CostStatusUnpriced, Currency: nil,
		PricingSnapshot: types.JSON(`{}`), CalculatorVersion: "pg-smoke", CalculatedAt: time.Now().UTC(),
	}).Error)

	var storedCost types.ModelUsageCost
	require.NoError(t, tx.First(&storedCost, "usage_id = ?", chat.ID).Error)
	require.Equal(t, inputCost, *storedCost.InputCost)
	require.Equal(t, outputCost, *storedCost.OutputCost)
	require.Equal(t, totalCost, *storedCost.TotalCost)

	aggregate, err := usageRepo.AggregateEvaluationRun(ctx, tenantID, runID)
	require.NoError(t, err)
	require.Equal(t, types.CallCounts{Total: 3, Chat: 1, Embedding: 1, Rerank: 1}, aggregate.Calls)
	require.Equal(t, types.CallCounts{Total: 1, Embedding: 1}, aggregate.NoCostRowCalls)
	require.Equal(t, types.CallCounts{Total: 1, Rerank: 1}, aggregate.CostRowsWithoutCurrency)
	require.Len(t, aggregate.CostByCurrency, 1)
	require.Equal(t, "TEST", aggregate.CostByCurrency[0].Currency)
	require.Equal(t, int64(1), aggregate.CostByCurrency[0].Chat.PricedCalls)
	require.Equal(t, totalCost, aggregate.CostByCurrency[0].Chat.PricedCost)
	require.Equal(t, totalCost, aggregate.CostByCurrency[0].Total.PricedCost)

	wrongTenant, err := usageRepo.AggregateEvaluationRun(ctx, tenantID+1, runID)
	require.NoError(t, err)
	require.Zero(t, wrongTenant.Calls.Total)

	analytics, err := usageRepo.AggregateAnalytics(ctx, tenantID, types.ModelUsageAnalyticsQuery{
		StartTime: time.Now().UTC().Add(-time.Hour),
		EndTime:   time.Now().UTC().Add(time.Hour),
		Interval:  types.ModelUsageAnalyticsIntervalHour,
	})
	require.NoError(t, err)
	require.Equal(t, types.ModelUsageAnalyticsTimeBasis, analytics.TimeBasis)
	require.Equal(t, types.CallCounts{Total: 3, Chat: 1, Embedding: 1, Rerank: 1}, analytics.Summary.Calls)
	require.Equal(t, types.CallCounts{Total: 1, Embedding: 1}, analytics.Summary.NoCostRowCalls)
	require.Equal(t, types.CallCounts{Total: 1, Rerank: 1}, analytics.Summary.CostRowsWithoutCurrency)
	require.Len(t, analytics.Summary.CostByCurrency, 1)
	require.Equal(t, totalCost, analytics.Summary.CostByCurrency[0].Total.PricedCost)
}

func postgresAggregationUsage(tenantID uint64, runID string, callType types.CallType) *types.ModelUsage {
	resolvedModel := "fake-model"
	latencyMS := int64(10)
	modelType := "knowledge_qa"
	if callType == types.CallTypeEmbedding {
		modelType = string(types.ModelTypeEmbedding)
	} else if callType == types.CallTypeRerank {
		modelType = string(types.ModelTypeRerank)
	}
	return &types.ModelUsage{
		TenantID: tenantID, ModelTenantID: tenantID, EvaluationRunID: &runID,
		ModelID: uuid.NewString(), ModelName: "fake-model", ModelType: modelType, ModelSource: "fake",
		ResolvedProvider: "fake", ResolvedModelName: &resolvedModel,
		CallType: callType, Purpose: "pg-smoke", Status: types.UsageStatusSuccess,
		TokenProvenance: types.TokenProvenanceProviderReported,
		LatencyMS:       &latencyMS, CreatedAt: time.Now().UTC(), LogicalRequests: 1,
	}
}

func pricingPostgresFixture(id, version, provider, model, effectiveFrom string, closesRuleID *string) string {
	closure := "null"
	if closesRuleID != nil {
		closure = fmt.Sprintf("%q", *closesRuleID)
	}
	return fmt.Sprintf(`schema_version: 1
pricing_version: %q
source:
  name: "weknora-postgres-smoke"
  reference: "https://example.invalid/pg-smoke"
  retrieved_at: "2026-01-01T00:00:00Z"
rules:
  - id: %q
    resolved_provider: %q
    resolved_model_name: %q
    call_type: "chat"
    billing_mode: "chat_standard_tokens"
    currency: "TEST"
    unit_scale: "1"
    input_token_price: "0.100000000000000001"
    output_token_price: "0.200000000000000002"
    total_token_price: null
    cache_read_token_price: null
    cache_write_token_price: null
    per_request_price: null
    per_input_price: null
    per_pair_price: null
    effective_from: %q
    effective_to: null
    closes_rule_id: %s
`, version, id, provider, model, effectiveFrom, closure)
}

func writePricingPostgresFixture(t *testing.T, contents string) string {
	t.Helper()
	path := t.TempDir() + "/pricing.yaml"
	require.NoError(t, os.WriteFile(path, []byte(contents), 0o600))
	return path
}
