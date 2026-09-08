package types

import "time"

// ModelUsageAnalyticsTimeBasis identifies the timestamp used for analytics windows.
const ModelUsageAnalyticsTimeBasis = "created_at"

// ModelUsageAnalyticsInterval identifies the aggregation bucket width.
type ModelUsageAnalyticsInterval string

const (
	// ModelUsageAnalyticsIntervalHour selects hourly buckets.
	ModelUsageAnalyticsIntervalHour ModelUsageAnalyticsInterval = "hour"
	// ModelUsageAnalyticsIntervalDay selects daily buckets.
	ModelUsageAnalyticsIntervalDay ModelUsageAnalyticsInterval = "day"
)

// ModelUsageAnalyticsQuery is already tenant-free by design. The tenant scope
// is supplied separately by the application service from the authenticated
// request context.
type ModelUsageAnalyticsQuery struct {
	ModelID   string
	StartTime time.Time
	EndTime   time.Time
	Interval  ModelUsageAnalyticsInterval
}

// PromptCacheAnalytics intentionally keeps provider prompt-cache call
// coverage separate from prompt-token coverage. Cache token counters are
// descriptive subsets and are never added together to infer prompt tokens.
type PromptCacheAnalytics struct {
	HitCalls         int64    `json:"hit_calls"`
	MissCalls        int64    `json:"miss_calls"`
	UnsupportedCalls int64    `json:"unsupported_calls"`
	UnreportedCalls  int64    `json:"unreported_calls"`
	NotRecordedCalls int64    `json:"not_recorded_calls"`
	EligibleCalls    int64    `json:"eligible_calls"`
	CallHitRate      *float64 `json:"call_hit_rate"`

	CacheReadTokens  int64    `json:"cache_read_tokens"`
	CacheWriteTokens int64    `json:"cache_write_tokens"`
	CacheMissTokens  int64    `json:"cache_miss_tokens"`
	TokenDenominator int64    `json:"token_denominator"`
	TokenCacheRatio  *float64 `json:"token_cache_ratio"`
}

// EmbeddingCacheAnalytics describes the WeKnora embedding cache. It is not
// combined with provider prompt caching because their denominators differ.
type EmbeddingCacheAnalytics struct {
	FullHitCalls     int64    `json:"full_hit_calls"`
	PartialCalls     int64    `json:"partial_calls"`
	MissCalls        int64    `json:"miss_calls"`
	DisabledCalls    int64    `json:"disabled_calls"`
	NotRecordedCalls int64    `json:"not_recorded_calls"`
	CacheHits        int64    `json:"cache_hits"`
	CacheMisses      int64    `json:"cache_misses"`
	EligibleInputs   int64    `json:"eligible_inputs"`
	InputHitRate     *float64 `json:"input_hit_rate"`
}

// ModelUsageAnalyticsAggregate contains usage totals for one analytics scope.
type ModelUsageAnalyticsAggregate struct {
	Calls CallCounts `json:"calls"`

	InputTokens  NullableMetricAggregate `json:"input_tokens"`
	OutputTokens NullableMetricAggregate `json:"output_tokens"`
	TotalTokens  NullableMetricAggregate `json:"total_tokens"`

	Latency        LatencyAggregate        `json:"latency"`
	PromptCache    PromptCacheAnalytics    `json:"prompt_cache"`
	EmbeddingCache EmbeddingCacheAnalytics `json:"embedding_cache"`

	CostByCurrency          []CurrencyCostAggregate `json:"cost_by_currency"`
	NoCostRowCalls          CallCounts              `json:"no_cost_row_calls"`
	CostRowsWithoutCurrency CallCounts              `json:"cost_rows_without_currency"`
}

// ModelUsageAnalyticsBucket contains usage totals for one time bucket.
type ModelUsageAnalyticsBucket struct {
	BucketStart time.Time `json:"bucket_start"`
	ModelUsageAnalyticsAggregate
}

// ModelUsageAnalyticsResult contains the summary and time-series usage aggregates.
type ModelUsageAnalyticsResult struct {
	TimeBasis string                       `json:"time_basis"`
	Interval  ModelUsageAnalyticsInterval  `json:"interval"`
	StartTime time.Time                    `json:"start_time"`
	EndTime   time.Time                    `json:"end_time"`
	ModelID   string                       `json:"model_id,omitempty"`
	Summary   ModelUsageAnalyticsAggregate `json:"summary"`
	Trend     []ModelUsageAnalyticsBucket  `json:"trend"`
}
