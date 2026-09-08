package repository

import (
	"context"
	"os"
	"testing"
	"time"

	modelpricing "github.com/Tencent/WeKnora/internal/models/pricing"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func newPricingTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := "file:" + uuid.NewString() + "?mode=memory&cache=shared&_foreign_keys=on"
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	for _, path := range []string{
		"../../../migrations/sqlite/000013_evaluation_runs.up.sql",
		"../../../migrations/sqlite/000014_model_usage.up.sql",
		"../../../migrations/sqlite/000015_model_pricing.up.sql",
	} {
		ddl, err := os.ReadFile(path)
		require.NoError(t, err)
		require.NoError(t, db.Exec(string(ddl)).Error)
	}
	return db
}

func fakePricingRule(from time.Time, to *time.Time, version string) *types.ModelPricing {
	zero := types.Decimal("0")
	return &types.ModelPricing{
		ResolvedProvider: "fake", ResolvedModelName: "fake-model", CallType: types.CallTypeChat,
		Currency: "USD", BillingMode: types.BillingModeChatStandardTokens,
		InputTokenPrice: &zero, OutputTokenPrice: &zero, UnitScale: "1000",
		EffectiveFrom: from, EffectiveTo: to, PricingVersion: version, SourceName: "deterministic-test",
	}
}

func TestPricingResolverBoundariesAndOverlap(t *testing.T) {
	db := newPricingTestDB(t)
	repo := NewPricingRepository(db)
	ctx := context.Background()
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	t1 := t0.Add(24 * time.Hour)
	r1 := fakePricingRule(t0, &t1, "v1")
	r2 := fakePricingRule(t1, nil, "v2")
	require.NoError(t, repo.CreatePricing(ctx, r1))
	require.NoError(t, repo.CreatePricing(ctx, r2), "touching half-open intervals must be allowed")

	got, err := repo.ResolvePricing(ctx, "fake", "fake-model", types.CallTypeChat, t0)
	require.NoError(t, err)
	require.Equal(t, "v1", got.PricingVersion, "effective_from is inclusive")
	got, err = repo.ResolvePricing(ctx, "fake", "fake-model", types.CallTypeChat, t1)
	require.NoError(t, err)
	require.Equal(t, "v2", got.PricingVersion, "effective_to is exclusive")
	got, err = repo.ResolvePricing(ctx, "fake", "fake-model", types.CallTypeChat, t0.Add(-time.Nanosecond))
	require.NoError(t, err)
	require.Nil(t, got)

	overlapEnd := t1.Add(time.Hour)
	require.Error(t, repo.CreatePricing(ctx, fakePricingRule(t0.Add(time.Hour), &overlapEnd, "overlap")))
	require.Error(t, db.Create(fakePricingRule(t0.Add(2*time.Hour), &overlapEnd, "trigger-overlap")).Error,
		"database trigger must reject overlaps even when repository validation is bypassed")
}

func TestPricingPersistsNullAndExplicitZeroDistinctly(t *testing.T) {
	db := newPricingTestDB(t)
	repo := NewPricingRepository(db)
	rule := fakePricingRule(time.Now().UTC(), nil, "zero")
	rule.TotalTokenPrice = nil
	require.NoError(t, repo.CreatePricing(context.Background(), rule))
	var got types.ModelPricing
	require.NoError(t, db.First(&got, "id = ?", rule.ID).Error)
	require.NotNil(t, got.InputTokenPrice)
	require.Equal(t, types.Decimal("0"), *got.InputTokenPrice)
	require.Nil(t, got.TotalTokenPrice)
}

func TestSQLitePricingPreservesFixedPrecisionText(t *testing.T) {
	db := newPricingTestDB(t)
	repo := NewPricingRepository(db)
	rule := fakePricingRule(time.Now().UTC(), nil, "precise")
	tiny := types.Decimal("0.000000000000000001")
	rule.InputTokenPrice = &tiny
	require.NoError(t, repo.CreatePricing(context.Background(), rule))
	var got types.ModelPricing
	require.NoError(t, db.First(&got, "id = ?", rule.ID).Error)
	require.Equal(t, tiny, *got.InputTokenPrice)
}

func TestMissingResolvedIdentityPersistsNoCostRow(t *testing.T) {
	db := newPricingTestDB(t)
	repo := NewPricingRepository(db)
	ctx := context.Background()
	usage := testModelUsage(1, "legacy-unknown")
	usage.ID = uuid.NewString()
	usage.ResolvedModelName = nil
	require.NoError(t, db.Create(usage).Error)
	require.NoError(t, modelpricing.NewProcessor(repo).Process(ctx, usage))
	cost, err := repo.GetCostByUsageID(ctx, 1, usage.ID)
	require.NoError(t, err)
	require.Nil(t, cost, "incomplete identity must not occupy a cost slot")
}

func TestNoPricingRulePersistsNoCostRow(t *testing.T) {
	db := newPricingTestDB(t)
	repo := NewPricingRepository(db)
	ctx := context.Background()

	start := time.Now().UTC().Truncate(time.Millisecond)
	usage := testModelUsage(1, "no-rule")
	usage.ID = uuid.NewString()
	usage.ResolvedProvider = "fake"
	resolved := "fake-model"
	usage.ResolvedModelName = &resolved
	usage.StartedAt = &start
	require.NoError(t, db.Create(usage).Error)

	require.NoError(t, modelpricing.NewProcessor(repo).Process(ctx, usage))
	cost, err := repo.GetCostByUsageID(ctx, 1, usage.ID)
	require.NoError(t, err)
	require.Nil(t, cost, "a missing pricing rule must not persist an unpriced cost row")
}

func TestLaterPricingBackfillCreatesCost(t *testing.T) {
	db := newPricingTestDB(t)
	repo := NewPricingRepository(db)
	ctx := context.Background()

	start := time.Now().UTC().Truncate(time.Millisecond)
	usage := testModelUsage(1, "backfill")
	usage.ID = uuid.NewString()
	usage.ResolvedProvider = "fake"
	resolved := "fake-model"
	usage.ResolvedModelName = &resolved
	usage.StartedAt = &start
	usage.InputTokens, usage.OutputTokens = intPtr(2), intPtr(3)
	require.NoError(t, db.Create(usage).Error)

	// No rule yet: Process must not create a cost row.
	require.NoError(t, modelpricing.NewProcessor(repo).Process(ctx, usage))
	cost, err := repo.GetCostByUsageID(ctx, 1, usage.ID)
	require.NoError(t, err)
	require.Nil(t, cost)

	// Backfill a rule that covers the usage's started_at, then re-process the
	// same usage. The previously-absent cost row must now insert cleanly.
	one := types.Decimal("1")
	rule := fakePricingRule(start.Add(-time.Hour), nil, "backfill-v1")
	rule.InputTokenPrice, rule.OutputTokenPrice, rule.UnitScale = &one, &one, "1"
	require.NoError(t, repo.CreatePricing(ctx, rule))

	require.NoError(t, modelpricing.NewProcessor(repo).Process(ctx, usage))

	cost, err = repo.GetCostByUsageID(ctx, 1, usage.ID)
	require.NoError(t, err)
	require.NotNil(t, cost)
	require.Equal(t, types.CostStatusPriced, cost.Status)
	require.Equal(t, types.Decimal("5"), *cost.TotalCost)
	require.NotNil(t, cost.PricingRuleID)
	require.Equal(t, "backfill-v1", *cost.PricingVersion)
	require.Contains(t, string(cost.PricingSnapshot), `"pricing_version":"backfill-v1"`)
}

func TestRuleExistsButMeterMissingPersistsUnpricedCostWithSnapshot(t *testing.T) {
	db := newPricingTestDB(t)
	repo := NewPricingRepository(db)
	ctx := context.Background()

	start := time.Now().UTC().Truncate(time.Millisecond)
	rule := fakePricingRule(start.Add(-time.Hour), nil, "v1")
	rule.CallType = types.CallTypeEmbedding
	rule.BillingMode = types.BillingModeEmbeddingInputToken
	rule.OutputTokenPrice = nil // not used by embedding_input_token
	require.NoError(t, repo.CreatePricing(ctx, rule))

	usage := testModelUsage(1, "meter-missing")
	usage.ID = uuid.NewString()
	usage.ResolvedProvider = "fake"
	usage.CallType = types.CallTypeEmbedding
	usage.ModelType = string(types.ModelTypeEmbedding)
	resolved := "fake-model"
	usage.ResolvedModelName = &resolved
	usage.StartedAt = &start
	// InputTokens left nil: the rule resolves but the required meter is missing.
	require.NoError(t, db.Create(usage).Error)

	require.NoError(t, modelpricing.NewProcessor(repo).Process(ctx, usage))
	cost, err := repo.GetCostByUsageID(ctx, 1, usage.ID)
	require.NoError(t, err)
	require.NotNil(t, cost, "a resolved rule with a missing meter is a finalized unpriced cost")
	require.Equal(t, types.CostStatusUnpriced, cost.Status)
	require.NotNil(t, cost.PricingRuleID)
	require.NotEqual(t, types.JSON("{}"), cost.PricingSnapshot)
	require.Nil(t, cost.TotalCost)
}

func TestGetCostByUsageIDTenantIsolation(t *testing.T) {
	db := newPricingTestDB(t)
	repo := NewPricingRepository(db)
	ctx := context.Background()

	start := time.Now().UTC().Truncate(time.Millisecond)
	one := types.Decimal("1")
	rule := fakePricingRule(start.Add(-time.Hour), nil, "v1")
	rule.InputTokenPrice, rule.OutputTokenPrice, rule.UnitScale = &one, &one, "1"
	require.NoError(t, repo.CreatePricing(ctx, rule))

	usage := testModelUsage(7, "tenant-a")
	usage.ID = uuid.NewString()
	usage.ResolvedProvider = "fake"
	resolved := "fake-model"
	usage.ResolvedModelName = &resolved
	usage.StartedAt = &start
	usage.InputTokens, usage.OutputTokens = intPtr(2), intPtr(3)
	require.NoError(t, db.Create(usage).Error)
	require.NoError(t, modelpricing.NewProcessor(repo).Process(ctx, usage))

	own, err := repo.GetCostByUsageID(ctx, 7, usage.ID)
	require.NoError(t, err)
	require.NotNil(t, own, "tenant A must read its own cost")

	other, err := repo.GetCostByUsageID(ctx, 8, usage.ID)
	require.NoError(t, err)
	require.Nil(t, other, "tenant B must not read tenant A cost")
}

func TestPersistedCostKeepsHistoricalSnapshot(t *testing.T) {
	db := newPricingTestDB(t)
	repo := NewPricingRepository(db)
	ctx := context.Background()
	start := time.Now().UTC().Truncate(time.Millisecond)
	one := types.Decimal("1")
	rule := fakePricingRule(start.Add(-time.Hour), nil, "v1")
	rule.InputTokenPrice, rule.OutputTokenPrice, rule.UnitScale = &one, &one, "1"
	require.NoError(t, repo.CreatePricing(ctx, rule))
	resolved := "fake-model"
	usage := testModelUsage(1, "priced")
	usage.ID, usage.ResolvedModelName, usage.StartedAt = uuid.NewString(), &resolved, &start
	usage.ResolvedProvider = "fake"
	usage.InputTokens, usage.OutputTokens = intPtr(2), intPtr(3)
	require.NoError(t, db.Create(usage).Error)
	require.NoError(t, modelpricing.NewProcessor(repo).Process(ctx, usage))

	cost, err := repo.GetCostByUsageID(ctx, 1, usage.ID)
	require.NoError(t, err)
	require.Equal(t, types.Decimal("5"), *cost.TotalCost)
	require.Contains(t, string(cost.PricingSnapshot), `"pricing_version":"v1"`)

	ten := types.Decimal("10")
	require.NoError(t, db.Model(&types.ModelPricing{}).Where("id = ?", rule.ID).Update("input_token_price", ten).Error)
	still, err := repo.GetCostByUsageID(ctx, 1, usage.ID)
	require.NoError(t, err)
	require.Equal(t, types.Decimal("5"), *still.TotalCost)
	require.Equal(t, cost.PricingSnapshot, still.PricingSnapshot)
}

func TestImportPricingBatchIdempotencyAndSemanticConflict(t *testing.T) {
	db := newPricingTestDB(t)
	repo := NewPricingRepository(db)
	ctx := context.Background()
	from := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	rule := fakePricingRule(from, nil, "import-v1")
	rule.ID = "20000000-0000-4000-8000-000000000001"
	one := types.Decimal("1")
	rule.InputTokenPrice = &one

	result, err := repo.ImportPricingBatch(ctx, []types.PricingImportRule{{Pricing: *rule}})
	require.NoError(t, err)
	require.Equal(t, &types.PricingImportResult{Inserted: 1}, result)

	equivalent := *rule
	equivalentOne := types.Decimal("1.000000000000000000")
	equivalent.InputTokenPrice = &equivalentOne
	result, err = repo.ImportPricingBatch(ctx, []types.PricingImportRule{{Pricing: equivalent}})
	require.NoError(t, err)
	require.Equal(t, &types.PricingImportResult{NoOp: 1}, result)

	changedPrice := equivalent
	two := types.Decimal("2")
	changedPrice.InputTokenPrice = &two
	_, err = repo.ImportPricingBatch(ctx, []types.PricingImportRule{{Pricing: changedPrice}})
	require.ErrorContains(t, err, "different semantic content")

	changedIdentity := equivalent
	changedIdentity.ResolvedModelName = "other-model"
	_, err = repo.ImportPricingBatch(ctx, []types.PricingImportRule{{Pricing: changedIdentity}})
	require.ErrorContains(t, err, "different semantic content")
}

func TestImportPricingBatchClosureAndReimport(t *testing.T) {
	db := newPricingTestDB(t)
	repo := NewPricingRepository(db)
	ctx := context.Background()
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	t1 := t0.Add(24 * time.Hour)
	old := fakePricingRule(t0, nil, "v1")
	old.ID = "20000000-0000-4000-8000-000000000010"
	result, err := repo.ImportPricingBatch(ctx, []types.PricingImportRule{{Pricing: *old}})
	require.NoError(t, err)
	require.Equal(t, &types.PricingImportResult{Inserted: 1}, result)

	replacement := fakePricingRule(t1, nil, "v2")
	replacement.ID = "20000000-0000-4000-8000-000000000011"
	result, err = repo.ImportPricingBatch(ctx, []types.PricingImportRule{
		{Pricing: *replacement, ClosesRuleID: &old.ID},
	})
	require.NoError(t, err)
	require.Equal(t, &types.PricingImportResult{Inserted: 1, Closed: 1}, result)

	var persistedOld types.ModelPricing
	require.NoError(t, db.First(&persistedOld, "id = ?", old.ID).Error)
	require.NotNil(t, persistedOld.EffectiveTo)
	require.True(t, persistedOld.EffectiveTo.Equal(t1))

	result, err = repo.ImportPricingBatch(ctx, []types.PricingImportRule{{Pricing: *old}})
	require.NoError(t, err, "replaying the original open-ended source after a legitimate closure must be a no-op")
	require.Equal(t, &types.PricingImportResult{NoOp: 1}, result)
	require.NoError(t, db.First(&persistedOld, "id = ?", old.ID).Error)
	require.NotNil(t, persistedOld.EffectiveTo)
	require.True(t, persistedOld.EffectiveTo.Equal(t1), "historical replay must never reopen the interval")

	mutatedOld := *old
	changed := types.Decimal("1")
	mutatedOld.InputTokenPrice = &changed
	_, err = repo.ImportPricingBatch(ctx, []types.PricingImportRule{{Pricing: mutatedOld}})
	require.ErrorContains(t, err, "different semantic content")

	result, err = repo.ImportPricingBatch(ctx, []types.PricingImportRule{
		{Pricing: *replacement, ClosesRuleID: &old.ID},
	})
	require.NoError(t, err)
	require.Equal(t, &types.PricingImportResult{NoOp: 1}, result)
}

func TestImportPricingBatchRejectsNonCanonicalUUIDAndWhitespaceCurrency(t *testing.T) {
	db := newPricingTestDB(t)
	repo := NewPricingRepository(db)
	ctx := context.Background()
	rule := fakePricingRule(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), nil, "invalid")

	rule.ID = "A0000000-0000-4000-8000-00000000000A"
	_, err := repo.ImportPricingBatch(ctx, []types.PricingImportRule{{Pricing: *rule}})
	require.ErrorContains(t, err, "canonical lowercase UUID")

	rule.ID = "a0000000-0000-4000-8000-00000000000a"
	rule.Currency = "   "
	_, err = repo.ImportPricingBatch(ctx, []types.PricingImportRule{{Pricing: *rule}})
	require.ErrorContains(t, err, "currency must not be empty or whitespace")

	rule.Currency = "TEST"
	nonCanonicalClose := "{b0000000-0000-4000-8000-00000000000b}"
	_, err = repo.ImportPricingBatch(ctx, []types.PricingImportRule{{Pricing: *rule, ClosesRuleID: &nonCanonicalClose}})
	require.ErrorContains(t, err, "canonical lowercase UUID")
}

func TestImportPricingBatchRejectsInvalidClosure(t *testing.T) {
	db := newPricingTestDB(t)
	repo := NewPricingRepository(db)
	ctx := context.Background()
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	t1, t2 := t0.Add(time.Hour), t0.Add(2*time.Hour)

	old := fakePricingRule(t0, nil, "open")
	old.ID = "20000000-0000-4000-8000-000000000020"
	require.NoError(t, repo.CreatePricing(ctx, old))
	wrongIdentity := fakePricingRule(t1, nil, "wrong")
	wrongIdentity.ID = "20000000-0000-4000-8000-000000000021"
	wrongIdentity.ResolvedProvider = "other-provider"
	_, err := repo.ImportPricingBatch(ctx, []types.PricingImportRule{{Pricing: *wrongIdentity, ClosesRuleID: &old.ID}})
	require.ErrorContains(t, err, "different runtime identity")

	alreadyClosed := fakePricingRule(t0, &t1, "closed")
	alreadyClosed.ID = "20000000-0000-4000-8000-000000000022"
	alreadyClosed.ResolvedModelName = "already-closed-model"
	require.NoError(t, repo.CreatePricing(ctx, alreadyClosed))
	later := fakePricingRule(t2, nil, "later")
	later.ID = "20000000-0000-4000-8000-000000000023"
	later.ResolvedModelName = "already-closed-model"
	_, err = repo.ImportPricingBatch(ctx, []types.PricingImportRule{{Pricing: *later, ClosesRuleID: &alreadyClosed.ID}})
	require.ErrorContains(t, err, "already closed at a different time")
}

func TestImportPricingBatchOverlapRollsBackWholeBatch(t *testing.T) {
	db := newPricingTestDB(t)
	repo := NewPricingRepository(db)
	ctx := context.Background()
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	existing := fakePricingRule(t0, nil, "existing")
	existing.ID = "20000000-0000-4000-8000-000000000030"
	require.NoError(t, repo.CreatePricing(ctx, existing))

	first := fakePricingRule(t0, nil, "first")
	first.ID = "20000000-0000-4000-8000-000000000031"
	first.ResolvedModelName = "other-model"
	overlap := fakePricingRule(t0.Add(time.Minute), nil, "overlap")
	overlap.ID = "20000000-0000-4000-8000-000000000032"
	_, err := repo.ImportPricingBatch(ctx, []types.PricingImportRule{{Pricing: *first}, {Pricing: *overlap}})
	require.Error(t, err)

	var count int64
	require.NoError(t, db.Model(&types.ModelPricing{}).Where("id = ?", first.ID).Count(&count).Error)
	require.Zero(t, count, "an insert before the failing rule must be rolled back")
}

func TestPricingMigrationParity(t *testing.T) {
	pg, err := os.ReadFile("../../../migrations/versioned/000093_model_pricing.up.sql")
	require.NoError(t, err)
	sq, err := os.ReadFile("../../../migrations/sqlite/000015_model_pricing.up.sql")
	require.NoError(t, err)
	for _, column := range []string{
		"resolved_provider", "resolved_model_name", "call_type", "currency", "billing_mode",
		"input_token_price", "output_token_price", "total_token_price", "cache_read_token_price",
		"cache_write_token_price", "per_request_price", "per_input_price", "per_pair_price",
		"unit_scale", "effective_from", "effective_to", "pricing_version", "source_name",
		"source_reference", "source_retrieved_at", "created_at",
	} {
		require.Contains(t, string(pg), column)
		require.Contains(t, string(sq), column)
	}
	for _, column := range []string{
		"usage_id", "status", "currency", "total_cost", "known_cost", "input_cost", "output_cost",
		"cache_read_cost", "cache_write_cost", "request_cost", "provider_input_cost",
		"provider_pair_cost", "pricing_rule_id", "pricing_version", "pricing_snapshot",
		"calculator_version", "calculated_at",
	} {
		require.Contains(t, string(pg), column)
		require.Contains(t, string(sq), column)
	}
	require.Contains(t, string(pg), "trg_model_pricing_no_overlap")
	require.Contains(t, string(sq), "trg_model_pricing_no_overlap_insert")
	require.NotContains(t, string(pg), "UPDATE model_usage SET resolved_model_name")
	require.NotContains(t, string(sq), "UPDATE model_usage SET resolved_model_name")
}
