package repository

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestModelStatisticsSeparatesProviderAndApplicationCacheDenominators(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:model-statistics?mode=memory&cache=shared"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(
		&types.ModelCallRecord{}, &types.ModelPriceVersion{}, &types.EmbeddingCacheLookupRecord{},
	))
	now := time.Date(2026, 8, 31, 12, 0, 0, 0, time.UTC)
	duration := int64(40)
	read, miss, total, cost := 25, 75, 120, int64(321)
	require.NoError(t, db.Create(&types.ModelCallRecord{
		ID: "call-1", TenantID: 7, ModelID: "model-1", ModelSnapshot: types.JSON(`{}`),
		Purpose: "general", Operation: "chat", StartedAt: now.Add(-time.Hour), DurationMs: &duration,
		Status: types.ModelCallStatusSuccess, PromptTokens: &total, TotalTokens: &total,
		ProviderCacheReadTokens: &read, ProviderCacheMissTokens: &miss,
		CostMicrounits: &cost, Currency: "USD", AccountingComplete: true,
		ProviderCacheStatus: "partial", ApplicationCacheStatus: types.ApplicationCacheStatusUnavailable,
		CreatedAt: now, UpdatedAt: now,
	}).Error)
	for index, value := range []int64{10, 100} {
		require.NoError(t, db.Create(&types.ModelCallRecord{
			ID: fmt.Sprintf("call-%d", index+2), TenantID: 7, ModelID: "model-1", ModelSnapshot: types.JSON(`{}`),
			Purpose: "general", Operation: "chat", StartedAt: now.Add(-time.Hour), DurationMs: &value,
			Status: types.ModelCallStatusSuccess, ProviderCacheStatus: "unreported",
			ApplicationCacheStatus: types.ApplicationCacheStatusUnavailable, CreatedAt: now, UpdatedAt: now,
		}).Error)
	}
	require.NoError(t, db.Create(&types.EmbeddingCacheLookupRecord{
		ID: "cache-1", TenantID: 7, ModelID: "model-1", RequestedItems: 10, UniqueItems: 10,
		HitItems: 8, MissItems: 2, Status: types.EmbeddingCacheLookupStatusPartial,
		DurationMs: 4, OccurredAt: now.Add(-time.Hour),
	}).Error)
	require.NoError(t, db.Create(&types.ModelCallRecord{
		ID: "call-started", TenantID: 7, ModelID: "model-1", ModelSnapshot: types.JSON(`{}`),
		Purpose: "general", Operation: "chat", StartedAt: now.Add(-time.Hour),
		Status: types.ModelCallStatusStarted, ProviderCacheStatus: "unreported",
		ApplicationCacheStatus: types.ApplicationCacheStatusUnavailable, CreatedAt: now, UpdatedAt: now,
	}).Error)
	require.NoError(t, db.Create(&types.EmbeddingCacheLookupRecord{
		ID: "cache-2", TenantID: 7, ModelID: "model-1", RequestedItems: 3, UniqueItems: 3,
		BypassItems: 3, Status: types.EmbeddingCacheLookupStatusBypass,
		DurationMs: 6, OccurredAt: now.Add(-time.Hour),
	}).Error)

	items, err := NewModelStatisticsRepository(db).QueryModelUsage(context.Background(), types.ModelUsageQuery{
		TenantID: 7, From: now.Add(-24 * time.Hour), To: now,
	})
	require.NoError(t, err)
	require.Len(t, items, 1)
	item := items[0]
	require.Equal(t, int64(4), item.CallCount)
	require.Equal(t, int64(1), item.StartedCalls)
	require.Equal(t, item.CallCount,
		item.StartedCalls+item.SuccessCalls+item.ErrorCalls+item.CanceledCalls)
	require.Equal(t, int64(2), item.UsageUnreportedCalls)
	require.Equal(t, int64(100), item.ProviderCache.ObservedTokens)
	require.NotNil(t, item.ProviderCache.HitRate)
	require.InDelta(t, 0.25, *item.ProviderCache.HitRate, 0.0001)
	require.Equal(t, int64(10), item.ApplicationCache.ObservedItems)
	require.Equal(t, int64(3), item.ApplicationCache.BypassItems)
	require.NotNil(t, item.ApplicationCache.HitRate)
	require.InDelta(t, 0.8, *item.ApplicationCache.HitRate, 0.0001)
	require.Equal(t, int64(2), item.ApplicationCache.LookupCount)
	require.Equal(t, int64(1), item.ApplicationCache.BypassLookupCount)
	require.Equal(t, int64(13), item.ApplicationCache.RequestedItems)
	require.InDelta(t, 5, item.ApplicationCache.AverageLookupDurationMs, 0.0001)
	require.Equal(t, int64(3), item.Latency.ReportedCalls)
	require.InDelta(t, 40, *item.Latency.P50Ms, 0.0001)
	require.InDelta(t, 94, *item.Latency.P95Ms, 0.0001)
	require.InDelta(t, 98.8, *item.Latency.P99Ms, 0.0001)
	require.Equal(t, []types.ModelCostTotal{{Currency: "USD", CostMicrounits: 321}}, item.Costs)
}

func TestModelStatisticsReturnsCacheOnlyModel(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:model-statistics-cache-only?mode=memory&cache=shared"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(
		&types.ModelCallRecord{}, &types.ModelPriceVersion{}, &types.EmbeddingCacheLookupRecord{},
	))
	now := time.Now().UTC()
	require.NoError(t, db.Create(&types.EmbeddingCacheLookupRecord{
		ID: "hot", TenantID: 7, ModelID: "embedding-1", RequestedItems: 4, UniqueItems: 4,
		HitItems: 4, Status: types.EmbeddingCacheLookupStatusHit, OccurredAt: now.Add(-time.Minute),
	}).Error)
	items, err := NewModelStatisticsRepository(db).QueryModelUsage(context.Background(), types.ModelUsageQuery{
		TenantID: 7, From: now.Add(-time.Hour), To: now,
	})
	require.NoError(t, err)
	require.Len(t, items, 1)
	require.Equal(t, int64(4), items[0].ApplicationCache.HitItems)
	require.Zero(t, items[0].CallCount)
}
