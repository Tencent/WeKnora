package repository

import (
	"context"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
	gormpostgres "gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func TestAggregateAnalyticsSQLiteSemanticsIsolationAndFilters(t *testing.T) {
	db := newModelUsageFKTestDB(t)
	repo := NewModelUsageRepository(db)
	ctx := context.Background()
	start := time.Date(2026, 9, 1, 10, 15, 0, 0, time.UTC)
	end := start.Add(2 * time.Hour)

	hit := types.PromptCacheStatusHit
	miss := types.PromptCacheStatusMiss
	unsupported := types.PromptCacheStatusUnsupported
	unreported := types.PromptCacheStatusUnreported
	chatRows := []*types.ModelUsage{
		analyticsUsage(7, "model-a", types.CallTypeChat, start),
		analyticsUsage(7, "model-a", types.CallTypeChat, start.Add(10*time.Minute)),
		analyticsUsage(7, "model-a", types.CallTypeChat, start.Add(20*time.Minute)),
		analyticsUsage(7, "model-a", types.CallTypeChat, start.Add(30*time.Minute)),
		analyticsUsage(7, "model-a", types.CallTypeChat, start.Add(40*time.Minute)),
	}
	chatRows[0].PromptCacheStatus = &hit
	chatRows[0].InputTokens = intPtr(100)
	chatRows[0].OutputTokens = intPtr(20)
	chatRows[0].TotalTokens = intPtr(120)
	chatRows[0].CacheReadTokens = intPtr(25)
	chatRows[0].CacheWriteTokens = intPtr(5)
	chatRows[0].CacheMissTokens = intPtr(75)
	chatRows[0].ProviderRequests = 7
	chatRows[0].LatencyMS = int64Ptr(100)
	chatRows[1].PromptCacheStatus = &miss
	chatRows[2].PromptCacheStatus = &unsupported
	chatRows[3].PromptCacheStatus = &unreported
	for i := 1; i < len(chatRows); i++ {
		chatRows[i].LatencyMS = nil
	}

	fullHit := types.EmbeddingCacheStatusFullHit
	partial := types.EmbeddingCacheStatusPartial
	cacheMiss := types.EmbeddingCacheStatusMiss
	disabled := types.EmbeddingCacheStatusDisabled
	embeddingRows := []*types.ModelUsage{
		analyticsUsage(7, "model-a", types.CallTypeEmbedding, start.Add(50*time.Minute)),
		analyticsUsage(7, "model-a", types.CallTypeEmbedding, start.Add(60*time.Minute)),
		analyticsUsage(7, "model-a", types.CallTypeEmbedding, start.Add(70*time.Minute)),
		analyticsUsage(7, "model-a", types.CallTypeEmbedding, start.Add(80*time.Minute)),
		analyticsUsage(7, "model-a", types.CallTypeEmbedding, start.Add(90*time.Minute)),
	}
	embeddingRows[0].EmbeddingCacheStatus, embeddingRows[0].EmbeddingInputs = &fullHit, 4
	embeddingRows[0].CacheHits = 4
	embeddingRows[1].EmbeddingCacheStatus, embeddingRows[1].EmbeddingInputs = &partial, 4
	embeddingRows[1].CacheHits, embeddingRows[1].CacheMisses = 1, 3
	embeddingRows[2].EmbeddingCacheStatus, embeddingRows[2].EmbeddingInputs = &cacheMiss, 2
	embeddingRows[2].CacheMisses = 2
	embeddingRows[3].EmbeddingCacheStatus, embeddingRows[3].EmbeddingInputs = &disabled, 3
	for _, row := range embeddingRows {
		row.LatencyMS = nil
	}
	rerank := analyticsUsage(7, "model-a", types.CallTypeRerank, start.Add(100*time.Minute))
	rerank.LatencyMS = nil

	for _, row := range append(append(chatRows, embeddingRows...), rerank) {
		require.NoError(t, repo.Create(ctx, row))
	}
	// Same tenant/model but outside [start,end), another model, and another
	// tenant must all be excluded by predicates applied in SQL.
	for _, row := range []*types.ModelUsage{
		analyticsUsage(7, "model-a", types.CallTypeChat, start.Add(-time.Nanosecond)),
		analyticsUsage(7, "model-a", types.CallTypeChat, end),
		analyticsUsage(7, "model-b", types.CallTypeChat, start),
		analyticsUsage(8, "model-a", types.CallTypeChat, start),
	} {
		require.NoError(t, repo.Create(ctx, row))
	}

	query := types.ModelUsageAnalyticsQuery{
		ModelID: "model-a", StartTime: start, EndTime: end,
		Interval: types.ModelUsageAnalyticsIntervalHour,
	}
	got, err := repo.AggregateAnalytics(ctx, 7, query)
	require.NoError(t, err)
	require.Equal(t, types.ModelUsageAnalyticsTimeBasis, got.TimeBasis)
	require.Equal(t, types.CallCounts{Total: 11, Chat: 5, Embedding: 5, Rerank: 1}, got.Summary.Calls)
	require.Equal(t, int64(11), got.Summary.InputTokens.ApplicableCalls)
	require.Equal(t, int64(1), got.Summary.InputTokens.ObservedCalls)
	require.Equal(t, int64(100), *got.Summary.InputTokens.Sum)
	require.Equal(t, int64(5), got.Summary.OutputTokens.ApplicableCalls)
	require.Equal(t, int64(1), got.Summary.OutputTokens.ObservedCalls)
	require.Equal(t, int64(20), *got.Summary.OutputTokens.Sum)
	require.Equal(t, int64(5), got.Summary.PromptCache.HitCalls+got.Summary.PromptCache.MissCalls+
		got.Summary.PromptCache.UnsupportedCalls+got.Summary.PromptCache.UnreportedCalls+
		got.Summary.PromptCache.NotRecordedCalls)
	require.Equal(t, int64(1), got.Summary.PromptCache.HitCalls)
	require.Equal(t, int64(1), got.Summary.PromptCache.MissCalls)
	require.Equal(t, int64(1), got.Summary.PromptCache.UnsupportedCalls)
	require.Equal(t, int64(1), got.Summary.PromptCache.UnreportedCalls)
	require.Equal(t, int64(1), got.Summary.PromptCache.NotRecordedCalls)
	require.Equal(t, int64(2), got.Summary.PromptCache.EligibleCalls)
	require.InDelta(t, 0.5, *got.Summary.PromptCache.CallHitRate, 0.000001)
	require.Equal(t, int64(100), got.Summary.PromptCache.TokenDenominator)
	require.InDelta(t, 0.25, *got.Summary.PromptCache.TokenCacheRatio, 0.000001)
	require.NotEqual(t, *got.Summary.PromptCache.CallHitRate, *got.Summary.PromptCache.TokenCacheRatio,
		"prompt call hit rate and prompt token cache ratio have different denominators")
	require.Equal(t, int64(25), got.Summary.PromptCache.CacheReadTokens)
	require.Equal(t, int64(5), got.Summary.EmbeddingCache.CacheHits)
	require.Equal(t, int64(5), got.Summary.EmbeddingCache.CacheMisses)
	require.Equal(t, int64(10), got.Summary.EmbeddingCache.EligibleInputs)
	require.InDelta(t, 0.5, *got.Summary.EmbeddingCache.InputHitRate, 0.000001)
	require.Equal(t, int64(1), got.Summary.EmbeddingCache.FullHitCalls)
	require.Equal(t, int64(1), got.Summary.EmbeddingCache.PartialCalls)
	require.Equal(t, int64(1), got.Summary.EmbeddingCache.MissCalls)
	require.Equal(t, int64(1), got.Summary.EmbeddingCache.DisabledCalls)
	require.Equal(t, int64(1), got.Summary.EmbeddingCache.NotRecordedCalls)
	require.Equal(t, int64(11), got.Summary.Latency.ApplicableCalls)
	require.Equal(t, int64(1), got.Summary.Latency.ObservedCalls)
	require.InDelta(t, 100, *got.Summary.Latency.AverageMS, 0.000001)
	require.Equal(t, int64(11), got.Summary.NoCostRowCalls.Total)
	require.Equal(t, int64(11), got.Summary.Calls.Total,
		"calls count model_usage rows, not provider_requests")

	wrongTenant, err := repo.AggregateAnalytics(ctx, 9, query)
	require.NoError(t, err)
	require.Zero(t, wrongTenant.Summary.Calls.Total)
	require.Empty(t, wrongTenant.Trend)
	require.Empty(t, wrongTenant.Summary.CostByCurrency)
	require.Nil(t, wrongTenant.Summary.InputTokens.Sum)
	require.Nil(t, wrongTenant.Summary.PromptCache.CallHitRate)
	require.Nil(t, wrongTenant.Summary.PromptCache.TokenCacheRatio)
	require.Nil(t, wrongTenant.Summary.EmbeddingCache.InputHitRate)
}

func TestAggregateAnalyticsSQLiteCostsRemainExactAndSeparateCoverage(t *testing.T) {
	db := newModelUsageFKTestDB(t)
	repo := NewModelUsageRepository(db)
	ctx := context.Background()
	start := time.Date(2026, 9, 2, 0, 0, 0, 0, time.UTC)
	end := start.Add(24 * time.Hour)
	rows := make([]*types.ModelUsage, 7)
	for i := range rows {
		rows[i] = analyticsUsage(17, "cost-model", types.CallTypeChat, start.Add(time.Duration(i)*time.Minute))
		require.NoError(t, repo.Create(ctx, rows[i]))
	}
	insertAggregationCost(
		t, db, rows[0].ID, types.CostStatusPriced, "USD",
		decPtr("0.100000000000000001"), decPtr("0.100000000000000001"),
	)
	insertAggregationCost(
		t, db, rows[1].ID, types.CostStatusPriced, "USD",
		decPtr("0.200000000000000002"), decPtr("0.200000000000000002"),
	)
	insertAggregationCost(t, db, rows[2].ID, types.CostStatusPartial, "USD", nil, decPtr("0.3"))
	insertAggregationCost(t, db, rows[3].ID, types.CostStatusUnpriced, "CNY", nil, nil)
	// rows[4] intentionally has no cost row.
	insertAggregationCostWithCurrency(t, db, rows[5].ID, types.CostStatusUnpriced, nil, nil, nil)
	insertAggregationCost(t, db, rows[6].ID, types.CostStatusPriced, "CNY", decPtr("0.4"), decPtr("0.4"))

	got, err := repo.AggregateAnalytics(ctx, 17, types.ModelUsageAnalyticsQuery{
		StartTime: start, EndTime: end, Interval: types.ModelUsageAnalyticsIntervalDay,
	})
	require.NoError(t, err)
	require.Equal(t, types.CallCounts{Total: 1, Chat: 1}, got.Summary.NoCostRowCalls)
	require.Equal(t, types.CallCounts{Total: 1, Chat: 1}, got.Summary.CostRowsWithoutCurrency)
	require.Len(t, got.Summary.CostByCurrency, 2)
	require.Equal(t, "CNY", got.Summary.CostByCurrency[0].Currency)
	require.Equal(t, types.Decimal("0.4"), got.Summary.CostByCurrency[0].Total.PricedCost)
	require.Equal(t, int64(1), got.Summary.CostByCurrency[0].Total.UnpricedCalls)
	require.Equal(t, "USD", got.Summary.CostByCurrency[1].Currency)
	require.Equal(t, types.Decimal("0.300000000000000003"), got.Summary.CostByCurrency[1].Total.PricedCost)
	require.Equal(t, types.Decimal("0.600000000000000003"), got.Summary.CostByCurrency[1].Total.KnownCost)
	require.Equal(t, int64(2), got.Summary.CostByCurrency[1].Total.PricedCalls)
	require.Equal(t, int64(1), got.Summary.CostByCurrency[1].Total.PartialCalls)
	require.Equal(t, got.Summary.CostByCurrency, got.Trend[0].CostByCurrency)
}

func TestAggregateAnalyticsSQLiteCreatedAtRangeIsStartInclusiveEndExclusive(t *testing.T) {
	db := newModelUsageFKTestDB(t)
	repo := NewModelUsageRepository(db)
	ctx := context.Background()
	start := time.Date(2026, 9, 2, 10, 0, 0, 0, time.UTC)
	end := start.Add(time.Hour)
	for _, timestamp := range []time.Time{start.Add(-time.Nanosecond), start, end.Add(-time.Nanosecond), end} {
		require.NoError(t, repo.Create(ctx, analyticsUsage(19, "boundary-model", types.CallTypeChat, timestamp)))
	}

	got, err := repo.AggregateAnalytics(ctx, 19, types.ModelUsageAnalyticsQuery{
		StartTime: start, EndTime: end, Interval: types.ModelUsageAnalyticsIntervalHour,
	})
	require.NoError(t, err)
	require.Equal(t, int64(2), got.Summary.Calls.Total,
		"created_at == start and created_at < end are included; created_at == end is excluded")
}

func TestAggregateAnalyticsSQLiteHourDayTrendAscendingWithoutEmptyFill(t *testing.T) {
	db := newModelUsageFKTestDB(t)
	repo := NewModelUsageRepository(db)
	ctx := context.Background()
	start := time.Date(2026, 9, 3, 0, 0, 0, 0, time.UTC)
	end := start.Add(48 * time.Hour)
	for _, timestamp := range []time.Time{
		start.Add(10*time.Hour + 5*time.Minute),
		start.Add(11*time.Hour + 5*time.Minute),
		start.Add(25*time.Hour + 5*time.Minute),
	} {
		require.NoError(t, repo.Create(ctx, analyticsUsage(27, "trend-model", types.CallTypeChat, timestamp)))
	}

	hourly, err := repo.AggregateAnalytics(ctx, 27, types.ModelUsageAnalyticsQuery{
		StartTime: start, EndTime: end, Interval: types.ModelUsageAnalyticsIntervalHour,
	})
	require.NoError(t, err)
	require.Len(t, hourly.Trend, 3, "empty hours are not synthesized")
	require.Equal(t, start.Add(10*time.Hour), hourly.Trend[0].BucketStart)
	require.Equal(t, start.Add(11*time.Hour), hourly.Trend[1].BucketStart)
	require.Equal(t, start.Add(25*time.Hour), hourly.Trend[2].BucketStart)

	daily, err := repo.AggregateAnalytics(ctx, 27, types.ModelUsageAnalyticsQuery{
		StartTime: start, EndTime: end, Interval: types.ModelUsageAnalyticsIntervalDay,
	})
	require.NoError(t, err)
	require.Len(t, daily.Trend, 2)
	require.Equal(t, start, daily.Trend[0].BucketStart)
	require.Equal(t, start.Add(24*time.Hour), daily.Trend[1].BucketStart)
	require.Equal(t, int64(2), daily.Trend[0].Calls.Total)
	require.Equal(t, int64(1), daily.Trend[1].Calls.Total)
}

func TestAggregateAnalyticsPostgreSQLUsesServerBucketsAndExactNumericSums(t *testing.T) {
	sqlDB, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherRegexp))
	require.NoError(t, err)
	t.Cleanup(func() {
		mock.ExpectClose()
		require.NoError(t, sqlDB.Close())
	})
	db, err := gorm.Open(gormpostgres.New(gormpostgres.Config{Conn: sqlDB}), &gorm.Config{DisableAutomaticPing: true})
	require.NoError(t, err)

	usageColumns := []string{
		"bucket_start", "call_type", "calls",
		"input_tokens_sum", "input_tokens_observed", "output_tokens_sum", "output_tokens_observed",
		"total_tokens_sum", "total_tokens_observed", "latency_sum", "latency_max", "latency_observed",
		"prompt_hit", "prompt_miss", "prompt_unsupported", "prompt_unreported", "prompt_not_recorded",
		"cache_read_tokens", "cache_write_tokens", "cache_miss_tokens", "prompt_token_input",
		"embedding_full_hit", "embedding_partial", "embedding_miss", "embedding_disabled", "embedding_not_recorded",
		"embedding_cache_hits", "embedding_cache_misses",
	}
	usageRows := sqlmock.NewRows(usageColumns).AddRow(
		"2026-09-04T10:00:00Z", "chat", 1,
		100, 1, 20, 1, 120, 1, 80, 80, 1,
		1, 0, 0, 0, 0, 25, 0, 75, 100,
		0, 0, 0, 0, 0, 0, 0,
	)
	mock.ExpectQuery(
		`(?s)date_trunc\('hour', u\.created_at AT TIME ZONE 'UTC'\).*FROM model_usage AS u.*`+
			`u\.tenant_id = \$1 AND u\.created_at >= \$2 AND u\.created_at < \$3 AND u\.model_id = \$4.*`+
			`GROUP BY bucket_start, u\.call_type.*ORDER BY bucket_start ASC`,
	).
		WithArgs(int64(37), sqlmock.AnyArg(), sqlmock.AnyArg(), "model-pg").WillReturnRows(usageRows)
	costRows := sqlmock.NewRows([]string{
		"bucket_start", "call_type", "cost_group", "cost_status",
		"cost_currency", "calls", "total_cost_text", "known_cost_text",
	}).AddRow(
		"2026-09-04T10:00:00Z", "chat", "currency", "priced", "USD", 1,
		"0.100000000000000001", "0.100000000000000001",
	)
	mock.ExpectQuery(
		`(?s)SUM\(c\.total_cost\)::text.*LEFT JOIN model_usage_cost AS c ON c\.usage_id = u\.id.*`+
			`u\.tenant_id = \$1.*GROUP BY bucket_start.*ORDER BY bucket_start ASC`,
	).
		WithArgs(int64(37), sqlmock.AnyArg(), sqlmock.AnyArg(), "model-pg").WillReturnRows(costRows)

	start := time.Date(2026, 9, 4, 10, 0, 0, 0, time.UTC)
	got, err := NewModelUsageRepository(db).AggregateAnalytics(context.Background(), 37, types.ModelUsageAnalyticsQuery{
		ModelID: "model-pg", StartTime: start, EndTime: start.Add(time.Hour),
		Interval: types.ModelUsageAnalyticsIntervalHour,
	})
	require.NoError(t, err)
	require.Equal(t, int64(1), got.Summary.Calls.Total)
	require.Equal(t, types.Decimal("0.100000000000000001"), got.Summary.CostByCurrency[0].Total.PricedCost)
	require.NoError(t, mock.ExpectationsWereMet())
}

func analyticsUsage(tenantID uint64, modelID string, callType types.CallType, createdAt time.Time) *types.ModelUsage {
	usage := testModelUsage(tenantID, modelID)
	usage.CallType = callType
	usage.CreatedAt = createdAt.UTC()
	usage.StartedAt = nil
	usage.LatencyMS = nil
	if callType == types.CallTypeEmbedding {
		usage.ModelType = string(types.ModelTypeEmbedding)
	}
	if callType == types.CallTypeRerank {
		usage.ModelType = string(types.ModelTypeRerank)
	}
	return usage
}
