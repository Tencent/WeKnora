package repository

import (
	"context"
	"fmt"
	"math/big"
	"sort"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
)

type modelUsageAnalyticsRow struct {
	BucketStart string         `gorm:"column:bucket_start"`
	CallType    types.CallType `gorm:"column:call_type"`
	Calls       int64          `gorm:"column:calls"`

	InputTokensSum       *int64 `gorm:"column:input_tokens_sum"`
	InputTokensObserved  int64  `gorm:"column:input_tokens_observed"`
	OutputTokensSum      *int64 `gorm:"column:output_tokens_sum"`
	OutputTokensObserved int64  `gorm:"column:output_tokens_observed"`
	TotalTokensSum       *int64 `gorm:"column:total_tokens_sum"`
	TotalTokensObserved  int64  `gorm:"column:total_tokens_observed"`

	LatencySum      *int64 `gorm:"column:latency_sum"`
	LatencyMax      *int64 `gorm:"column:latency_max"`
	LatencyObserved int64  `gorm:"column:latency_observed"`

	PromptHit         int64 `gorm:"column:prompt_hit"`
	PromptMiss        int64 `gorm:"column:prompt_miss"`
	PromptUnsupported int64 `gorm:"column:prompt_unsupported"`
	PromptUnreported  int64 `gorm:"column:prompt_unreported"`
	PromptNotRecorded int64 `gorm:"column:prompt_not_recorded"`
	CacheReadTokens   int64 `gorm:"column:cache_read_tokens"`
	CacheWriteTokens  int64 `gorm:"column:cache_write_tokens"`
	CacheMissTokens   int64 `gorm:"column:cache_miss_tokens"`
	PromptTokenInput  int64 `gorm:"column:prompt_token_input"`

	EmbeddingFullHit     int64 `gorm:"column:embedding_full_hit"`
	EmbeddingPartial     int64 `gorm:"column:embedding_partial"`
	EmbeddingMiss        int64 `gorm:"column:embedding_miss"`
	EmbeddingDisabled    int64 `gorm:"column:embedding_disabled"`
	EmbeddingNotRecorded int64 `gorm:"column:embedding_not_recorded"`
	EmbeddingCacheHits   int64 `gorm:"column:embedding_cache_hits"`
	EmbeddingCacheMisses int64 `gorm:"column:embedding_cache_misses"`
}

type modelUsageAnalyticsCostRow struct {
	BucketStart   string           `gorm:"column:bucket_start"`
	CallType      types.CallType   `gorm:"column:call_type"`
	CostGroup     string           `gorm:"column:cost_group"`
	CostStatus    types.CostStatus `gorm:"column:cost_status"`
	Currency      string           `gorm:"column:cost_currency"`
	Calls         int64            `gorm:"column:calls"`
	TotalCostText string           `gorm:"column:total_cost_text"`
	KnownCostText string           `gorm:"column:known_cost_text"`
}

func (r *modelUsageRepository) AggregateAnalytics(
	ctx context.Context,
	tenantID uint64,
	query types.ModelUsageAnalyticsQuery,
) (*types.ModelUsageAnalyticsResult, error) {
	if tenantID == 0 {
		return nil, fmt.Errorf("model_usage analytics: tenant_id must be non-zero")
	}
	if !query.StartTime.Before(query.EndTime) {
		return nil, fmt.Errorf("model_usage analytics: start_time must be before end_time")
	}
	bucketExpression, err := modelUsageBucketExpression(r.db.Name(), query.Interval)
	if err != nil {
		return nil, err
	}

	where, args := modelUsageAnalyticsWhere(tenantID, query)
	usageSQL := fmt.Sprintf(`
		SELECT %s AS bucket_start, u.call_type, COUNT(*) AS calls,
			SUM(u.input_tokens) AS input_tokens_sum,
			COUNT(u.input_tokens) AS input_tokens_observed,
			SUM(CASE WHEN u.call_type = 'chat' THEN u.output_tokens ELSE NULL END) AS output_tokens_sum,
			COUNT(CASE WHEN u.call_type = 'chat' THEN u.output_tokens ELSE NULL END) AS output_tokens_observed,
			SUM(u.total_tokens) AS total_tokens_sum,
			COUNT(u.total_tokens) AS total_tokens_observed,
			SUM(u.latency_ms) AS latency_sum,
			MAX(u.latency_ms) AS latency_max,
			COUNT(u.latency_ms) AS latency_observed,
			SUM(CASE WHEN u.call_type = 'chat' AND u.prompt_cache_status = 'hit' THEN 1 ELSE 0 END) AS prompt_hit,
			SUM(CASE WHEN u.call_type = 'chat' AND u.prompt_cache_status = 'miss' THEN 1 ELSE 0 END) AS prompt_miss,
			SUM(CASE WHEN u.call_type = 'chat' AND u.prompt_cache_status = 'unsupported'
				THEN 1 ELSE 0 END) AS prompt_unsupported,
			SUM(CASE WHEN u.call_type = 'chat' AND u.prompt_cache_status = 'unreported'
				THEN 1 ELSE 0 END) AS prompt_unreported,
			SUM(CASE WHEN u.call_type = 'chat' AND u.prompt_cache_status IS NULL
				THEN 1 ELSE 0 END) AS prompt_not_recorded,
			SUM(CASE WHEN u.call_type = 'chat' AND u.prompt_cache_status IN ('hit', 'miss')
				AND u.input_tokens IS NOT NULL AND u.cache_read_tokens IS NOT NULL
				THEN u.cache_read_tokens ELSE 0 END) AS cache_read_tokens,
			SUM(CASE WHEN u.call_type = 'chat' AND u.prompt_cache_status IN ('hit', 'miss')
				AND u.cache_write_tokens IS NOT NULL
				THEN u.cache_write_tokens ELSE 0 END) AS cache_write_tokens,
			SUM(CASE WHEN u.call_type = 'chat' AND u.prompt_cache_status IN ('hit', 'miss')
				AND u.cache_miss_tokens IS NOT NULL
				THEN u.cache_miss_tokens ELSE 0 END) AS cache_miss_tokens,
			SUM(CASE WHEN u.call_type = 'chat' AND u.prompt_cache_status IN ('hit', 'miss')
				AND u.input_tokens IS NOT NULL AND u.cache_read_tokens IS NOT NULL
				THEN u.input_tokens ELSE 0 END) AS prompt_token_input,
			SUM(CASE WHEN u.call_type = 'embedding' AND u.embedding_cache_status = 'full_hit'
				THEN 1 ELSE 0 END) AS embedding_full_hit,
			SUM(CASE WHEN u.call_type = 'embedding' AND u.embedding_cache_status = 'partial'
				THEN 1 ELSE 0 END) AS embedding_partial,
			SUM(CASE WHEN u.call_type = 'embedding' AND u.embedding_cache_status = 'miss'
				THEN 1 ELSE 0 END) AS embedding_miss,
			SUM(CASE WHEN u.call_type = 'embedding' AND u.embedding_cache_status = 'disabled'
				THEN 1 ELSE 0 END) AS embedding_disabled,
			SUM(CASE WHEN u.call_type = 'embedding' AND u.embedding_cache_status IS NULL
				THEN 1 ELSE 0 END) AS embedding_not_recorded,
			SUM(CASE WHEN u.call_type = 'embedding'
				AND u.embedding_cache_status IN ('full_hit', 'partial', 'miss')
				THEN u.cache_hits ELSE 0 END) AS embedding_cache_hits,
			SUM(CASE WHEN u.call_type = 'embedding'
				AND u.embedding_cache_status IN ('full_hit', 'partial', 'miss')
				THEN u.cache_misses ELSE 0 END) AS embedding_cache_misses
		FROM model_usage AS u
		WHERE %s
		GROUP BY bucket_start, u.call_type
		ORDER BY bucket_start ASC, u.call_type ASC`, bucketExpression, where)

	var usageRows []modelUsageAnalyticsRow
	if err := r.db.WithContext(ctx).Raw(usageSQL, args...).Scan(&usageRows).Error; err != nil {
		return nil, fmt.Errorf("aggregate model usage analytics: %w", err)
	}

	result := &types.ModelUsageAnalyticsResult{
		TimeBasis: types.ModelUsageAnalyticsTimeBasis,
		Interval:  query.Interval,
		StartTime: query.StartTime.UTC(),
		EndTime:   query.EndTime.UTC(),
		ModelID:   query.ModelID,
		Summary:   newModelUsageAnalyticsAggregate(),
		Trend:     make([]types.ModelUsageAnalyticsBucket, 0),
	}
	bucketIndexes := make(map[string]int, len(usageRows))
	for i := range usageRows {
		row := &usageRows[i]
		bucketIndex, ok := bucketIndexes[row.BucketStart]
		if !ok {
			bucketStart, parseErr := time.Parse(time.RFC3339, row.BucketStart)
			if parseErr != nil {
				return nil, fmt.Errorf(
					"aggregate model usage analytics: invalid database bucket %q: %w",
					row.BucketStart, parseErr,
				)
			}
			bucketIndex = len(result.Trend)
			bucketIndexes[row.BucketStart] = bucketIndex
			result.Trend = append(result.Trend, types.ModelUsageAnalyticsBucket{
				BucketStart:                  bucketStart.UTC(),
				ModelUsageAnalyticsAggregate: newModelUsageAnalyticsAggregate(),
			})
		}
		if err := addModelUsageAnalyticsRow(&result.Summary, row); err != nil {
			return nil, fmt.Errorf("aggregate model usage analytics: %w", err)
		}
		if err := addModelUsageAnalyticsRow(&result.Trend[bucketIndex].ModelUsageAnalyticsAggregate, row); err != nil {
			return nil, fmt.Errorf("aggregate model usage analytics: %w", err)
		}
	}

	costsByBucket := make(map[string]map[string]*currencyCostAccumulator, len(result.Trend))
	summaryCosts := make(map[string]*currencyCostAccumulator)
	if len(usageRows) > 0 {
		costRows, costErr := r.queryModelUsageAnalyticsCosts(ctx, bucketExpression, where, args)
		if costErr != nil {
			return nil, costErr
		}
		for i := range costRows {
			row := &costRows[i]
			bucketIndex, ok := bucketIndexes[row.BucketStart]
			if !ok {
				return nil, fmt.Errorf(
					"aggregate model usage analytics: cost row references unknown bucket %q",
					row.BucketStart,
				)
			}
			bucketCosts := costsByBucket[row.BucketStart]
			if bucketCosts == nil {
				bucketCosts = make(map[string]*currencyCostAccumulator)
				costsByBucket[row.BucketStart] = bucketCosts
			}
			if err := addModelUsageAnalyticsCostRow(&result.Summary, summaryCosts, row); err != nil {
				return nil, fmt.Errorf("aggregate model usage analytics: %w", err)
			}
			if err := addModelUsageAnalyticsCostRow(
				&result.Trend[bucketIndex].ModelUsageAnalyticsAggregate, bucketCosts, row,
			); err != nil {
				return nil, fmt.Errorf("aggregate model usage analytics: %w", err)
			}
		}
	}

	if err := finishModelUsageAnalyticsAggregate(&result.Summary, summaryCosts); err != nil {
		return nil, fmt.Errorf("aggregate model usage analytics: %w", err)
	}
	for i := range result.Trend {
		bucket := &result.Trend[i]
		bucketKey := bucket.BucketStart.Format(time.RFC3339)
		if err := finishModelUsageAnalyticsAggregate(
			&bucket.ModelUsageAnalyticsAggregate, costsByBucket[bucketKey],
		); err != nil {
			return nil, fmt.Errorf("aggregate model usage analytics: %w", err)
		}
	}
	return result, nil
}

func modelUsageAnalyticsWhere(tenantID uint64, query types.ModelUsageAnalyticsQuery) (string, []any) {
	where := "u.tenant_id = ? AND u.created_at >= ? AND u.created_at < ?"
	args := []any{tenantID, query.StartTime.UTC(), query.EndTime.UTC()}
	if query.ModelID != "" {
		where += " AND u.model_id = ?"
		args = append(args, query.ModelID)
	}
	return where, args
}

func modelUsageBucketExpression(dialect string, interval types.ModelUsageAnalyticsInterval) (string, error) {
	switch interval {
	case types.ModelUsageAnalyticsIntervalHour, types.ModelUsageAnalyticsIntervalDay:
	default:
		return "", fmt.Errorf("model_usage analytics: unsupported interval %q", interval)
	}
	switch dialect {
	case "postgres":
		return fmt.Sprintf(
			`to_char(date_trunc('%s', u.created_at AT TIME ZONE 'UTC'), 'YYYY-MM-DD"T"HH24:MI:SS"Z"')`,
			interval,
		), nil
	case "sqlite":
		format := "%Y-%m-%dT00:00:00Z"
		if interval == types.ModelUsageAnalyticsIntervalHour {
			format = "%Y-%m-%dT%H:00:00Z"
		}
		return fmt.Sprintf("strftime('%s', u.created_at)", format), nil
	default:
		return "", fmt.Errorf("model_usage analytics: unsupported database dialect %q", dialect)
	}
}

func (r *modelUsageRepository) queryModelUsageAnalyticsCosts(
	ctx context.Context,
	bucketExpression string,
	where string,
	args []any,
) ([]modelUsageAnalyticsCostRow, error) {
	var totalCostExpression, knownCostExpression string
	switch r.db.Name() {
	case "postgres":
		totalCostExpression = "COALESCE(SUM(c.total_cost)::text, '')"
		knownCostExpression = "COALESCE(SUM(c.known_cost)::text, '')"
	case "sqlite":
		// SQLite stores exact cost decimals as TEXT and SUM(TEXT) coerces through
		// floating point. GROUP_CONCAT keeps the exact decimal strings while the
		// database still reduces result-row cardinality by bucket/status/currency.
		totalCostExpression = "COALESCE(GROUP_CONCAT(c.total_cost, ','), '')"
		knownCostExpression = "COALESCE(GROUP_CONCAT(c.known_cost, ','), '')"
	default:
		return nil, fmt.Errorf(
			"aggregate model usage analytics costs: unsupported database dialect %q",
			r.db.Name(),
		)
	}
	costSQL := fmt.Sprintf(`
		SELECT %s AS bucket_start, u.call_type,
			CASE
				WHEN c.id IS NULL THEN 'missing'
				WHEN c.currency IS NULL OR c.currency = '' THEN 'no_currency'
				ELSE 'currency'
			END AS cost_group,
			COALESCE(c.status, '') AS cost_status,
			COALESCE(c.currency, '') AS cost_currency,
			COUNT(*) AS calls,
			%s AS total_cost_text,
			%s AS known_cost_text
		FROM model_usage AS u
		LEFT JOIN model_usage_cost AS c ON c.usage_id = u.id
		WHERE %s
		GROUP BY bucket_start, u.call_type, cost_group, cost_status, cost_currency
		ORDER BY bucket_start ASC, u.call_type ASC, cost_group ASC, cost_currency ASC, cost_status ASC`,
		bucketExpression, totalCostExpression, knownCostExpression, where)
	var rows []modelUsageAnalyticsCostRow
	if err := r.db.WithContext(ctx).Raw(costSQL, args...).Scan(&rows).Error; err != nil {
		return nil, fmt.Errorf("aggregate model usage analytics costs: %w", err)
	}
	return rows, nil
}

func newModelUsageAnalyticsAggregate() types.ModelUsageAnalyticsAggregate {
	return types.ModelUsageAnalyticsAggregate{CostByCurrency: make([]types.CurrencyCostAggregate, 0)}
}

func addModelUsageAnalyticsRow(result *types.ModelUsageAnalyticsAggregate, row *modelUsageAnalyticsRow) error {
	if err := incrementCallCountsPointerBy(&result.Calls, row.CallType, row.Calls); err != nil {
		return err
	}
	addNullableMetricGroup(&result.InputTokens, row.InputTokensSum, row.InputTokensObserved, row.Calls)
	addNullableMetricGroup(&result.TotalTokens, row.TotalTokensSum, row.TotalTokensObserved, row.Calls)
	outputApplicable := int64(0)
	if row.CallType == types.CallTypeChat {
		outputApplicable = row.Calls
	}
	addNullableMetricGroup(&result.OutputTokens, row.OutputTokensSum, row.OutputTokensObserved, outputApplicable)
	addLatencyGroup(&result.Latency, row.LatencySum, row.LatencyMax, row.LatencyObserved, row.Calls)

	result.PromptCache.HitCalls += row.PromptHit
	result.PromptCache.MissCalls += row.PromptMiss
	result.PromptCache.UnsupportedCalls += row.PromptUnsupported
	result.PromptCache.UnreportedCalls += row.PromptUnreported
	result.PromptCache.NotRecordedCalls += row.PromptNotRecorded
	result.PromptCache.CacheReadTokens += row.CacheReadTokens
	result.PromptCache.CacheWriteTokens += row.CacheWriteTokens
	result.PromptCache.CacheMissTokens += row.CacheMissTokens
	result.PromptCache.TokenDenominator += row.PromptTokenInput
	result.EmbeddingCache.FullHitCalls += row.EmbeddingFullHit
	result.EmbeddingCache.PartialCalls += row.EmbeddingPartial
	result.EmbeddingCache.MissCalls += row.EmbeddingMiss
	result.EmbeddingCache.DisabledCalls += row.EmbeddingDisabled
	result.EmbeddingCache.NotRecordedCalls += row.EmbeddingNotRecorded
	result.EmbeddingCache.CacheHits += row.EmbeddingCacheHits
	result.EmbeddingCache.CacheMisses += row.EmbeddingCacheMisses

	return nil
}

func addNullableMetricGroup(metric *types.NullableMetricAggregate, sum *int64, observed, applicable int64) {
	metric.ApplicableCalls += applicable
	metric.ObservedCalls += observed
	if observed == 0 {
		return
	}
	if metric.Sum == nil {
		zero := int64(0)
		metric.Sum = &zero
	}
	if sum != nil {
		*metric.Sum += *sum
	}
}

func addLatencyGroup(latency *types.LatencyAggregate, sum, maximum *int64, observed, applicable int64) {
	latency.ApplicableCalls += applicable
	latency.ObservedCalls += observed
	if observed == 0 {
		return
	}
	if latency.SumMS == nil {
		zero := int64(0)
		latency.SumMS = &zero
	}
	if sum != nil {
		*latency.SumMS += *sum
	}
	if maximum != nil && (latency.MaxMS == nil || *maximum > *latency.MaxMS) {
		value := *maximum
		latency.MaxMS = &value
	}
}

func addModelUsageAnalyticsCostRow(
	result *types.ModelUsageAnalyticsAggregate,
	costs map[string]*currencyCostAccumulator,
	row *modelUsageAnalyticsCostRow,
) error {
	switch row.CostGroup {
	case "missing":
		return incrementCallCountsPointerBy(&result.NoCostRowCalls, row.CallType, row.Calls)
	case "no_currency":
		if row.CostStatus == "" {
			return fmt.Errorf("cost rows without currency have no status")
		}
		return incrementCallCountsPointerBy(&result.CostRowsWithoutCurrency, row.CallType, row.Calls)
	case "currency":
		if row.Currency == "" {
			return fmt.Errorf("currency cost group has no currency")
		}
	default:
		return fmt.Errorf("unknown cost group %q", row.CostGroup)
	}
	accumulator := costs[row.Currency]
	if accumulator == nil {
		accumulator = newCurrencyCostAccumulator()
		costs[row.Currency] = accumulator
	}
	return addGroupedCurrencyCost(
		accumulator, row.CallType, row.CostStatus,
		row.Calls, row.TotalCostText, row.KnownCostText,
	)
}

func incrementCallCountsPointerBy(counts *types.CallCounts, callType types.CallType, amount int64) error {
	counts.Total += amount
	switch callType {
	case types.CallTypeChat:
		counts.Chat += amount
	case types.CallTypeEmbedding:
		counts.Embedding += amount
	case types.CallTypeRerank:
		counts.Rerank += amount
	default:
		return fmt.Errorf("unknown call_type %q", callType)
	}
	return nil
}

func addGroupedCurrencyCost(
	costs *currencyCostAccumulator,
	callType types.CallType,
	status types.CostStatus,
	calls int64,
	totalCostText, knownCostText string,
) error {
	var byType *callTypeCostAccumulator
	switch callType {
	case types.CallTypeChat:
		byType = &costs.chat
	case types.CallTypeEmbedding:
		byType = &costs.embedding
	case types.CallTypeRerank:
		byType = &costs.rerank
	default:
		return fmt.Errorf("unknown call_type %q", callType)
	}
	for _, target := range []*callTypeCostAccumulator{byType, &costs.total} {
		switch status {
		case types.CostStatusPriced:
			target.pricedCalls += calls
			if totalCostText == "" {
				return fmt.Errorf("priced costs are missing total_cost")
			}
			if err := addExactDecimalList(target.priced, totalCostText); err != nil {
				return err
			}
		case types.CostStatusPartial:
			target.partialCalls += calls
		case types.CostStatusUnpriced:
			target.unpricedCalls += calls
		default:
			return fmt.Errorf("unknown cost status %q", status)
		}
		if knownCostText != "" {
			if err := addExactDecimalList(target.known, knownCostText); err != nil {
				return err
			}
		}
	}
	return nil
}

func addExactDecimalList(total *big.Rat, values string) error {
	for _, raw := range strings.Split(values, ",") {
		if raw == "" {
			continue
		}
		if err := addExactDecimal(total, types.Decimal(raw)); err != nil {
			return err
		}
	}
	return nil
}

func finishModelUsageAnalyticsAggregate(
	result *types.ModelUsageAnalyticsAggregate,
	costs map[string]*currencyCostAccumulator,
) error {
	finishNullableMetric(&result.InputTokens)
	finishNullableMetric(&result.OutputTokens)
	finishNullableMetric(&result.TotalTokens)
	finishLatency(&result.Latency)

	result.PromptCache.EligibleCalls = result.PromptCache.HitCalls + result.PromptCache.MissCalls
	if result.PromptCache.EligibleCalls > 0 {
		rate := float64(result.PromptCache.HitCalls) / float64(result.PromptCache.EligibleCalls)
		result.PromptCache.CallHitRate = &rate
	}
	if result.PromptCache.TokenDenominator > 0 {
		ratio := float64(result.PromptCache.CacheReadTokens) / float64(result.PromptCache.TokenDenominator)
		result.PromptCache.TokenCacheRatio = &ratio
	} else {
		result.PromptCache.TokenCacheRatio = nil
	}
	result.EmbeddingCache.EligibleInputs = result.EmbeddingCache.CacheHits + result.EmbeddingCache.CacheMisses
	if result.EmbeddingCache.EligibleInputs > 0 {
		rate := float64(result.EmbeddingCache.CacheHits) / float64(result.EmbeddingCache.EligibleInputs)
		result.EmbeddingCache.InputHitRate = &rate
	}

	currencies := make([]string, 0, len(costs))
	for currency := range costs {
		currencies = append(currencies, currency)
	}
	sort.Strings(currencies)
	for _, currency := range currencies {
		item, err := costs[currency].finish(currency)
		if err != nil {
			return err
		}
		result.CostByCurrency = append(result.CostByCurrency, item)
	}
	return nil
}
