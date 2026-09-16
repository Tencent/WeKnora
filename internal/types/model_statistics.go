package types

import "time"

const (
	// EmbeddingCacheLookupStatusHit marks a lookup whose unique items were all cached.
	EmbeddingCacheLookupStatusHit = "hit"
	// EmbeddingCacheLookupStatusMiss marks a lookup whose unique items were all absent.
	EmbeddingCacheLookupStatusMiss = "miss"
	// EmbeddingCacheLookupStatusPartial marks a lookup containing cached and absent items.
	EmbeddingCacheLookupStatusPartial = "partial"
	// EmbeddingCacheLookupStatusBypass marks a cache repository failure followed by a provider call.
	EmbeddingCacheLookupStatusBypass = "bypass"
)

// EmbeddingCacheLookupRecord stores one cache-query outcome without source text, hashes, or vectors.
type EmbeddingCacheLookupRecord struct {
	ID             string    `json:"id" gorm:"type:varchar(36);primaryKey"`
	TenantID       uint64    `json:"tenant_id" gorm:"not null;index"`
	ModelID        string    `json:"model_id" gorm:"type:varchar(64);not null;index"`
	RequestedItems int64     `json:"requested_items" gorm:"not null"`
	UniqueItems    int64     `json:"unique_items" gorm:"not null"`
	HitItems       int64     `json:"hit_items" gorm:"not null"`
	MissItems      int64     `json:"miss_items" gorm:"not null"`
	BypassItems    int64     `json:"bypass_items" gorm:"not null"`
	Status         string    `json:"status" gorm:"type:varchar(16);not null"`
	DurationMs     int64     `json:"duration_ms" gorm:"not null"`
	OccurredAt     time.Time `json:"occurred_at" gorm:"not null;index"`
}

// TableName returns the embedding cache lookup accounting table name.
func (EmbeddingCacheLookupRecord) TableName() string { return "embedding_cache_lookup_records" }

// ModelUsageQuery selects tenant-scoped call and cache aggregates.
type ModelUsageQuery struct {
	TenantID uint64
	ModelIDs []string
	From     time.Time
	To       time.Time
}

// ModelCostTotal keeps costs separated by ISO 4217 currency code.
type ModelCostTotal struct {
	Currency       string `json:"currency"`
	CostMicrounits int64  `json:"cost_microunits"`
}

// ProviderCacheStatistics describes provider prompt-cache observations.
type ProviderCacheStatistics struct {
	ReadTokens     int64    `json:"read_tokens"`
	WriteTokens    int64    `json:"write_tokens"`
	MissTokens     int64    `json:"miss_tokens"`
	ObservedTokens int64    `json:"observed_tokens"`
	HitRate        *float64 `json:"hit_rate"`
}

// ApplicationCacheStatistics describes persistent embedding-cache key lookups.
type ApplicationCacheStatistics struct {
	LookupCount             int64    `json:"lookup_count"`
	BypassLookupCount       int64    `json:"bypass_lookup_count"`
	RequestedItems          int64    `json:"requested_items"`
	UniqueItems             int64    `json:"unique_items"`
	HitItems                int64    `json:"hit_items"`
	MissItems               int64    `json:"miss_items"`
	BypassItems             int64    `json:"bypass_items"`
	ObservedItems           int64    `json:"observed_items"`
	HitRate                 *float64 `json:"hit_rate"`
	AverageLookupDurationMs float64  `json:"average_lookup_duration_ms"`
}

// ModelLatencyStatistics describes reported provider-call latency percentiles.
type ModelLatencyStatistics struct {
	P50Ms         *float64 `json:"p50_ms"`
	P95Ms         *float64 `json:"p95_ms"`
	P99Ms         *float64 `json:"p99_ms"`
	ReportedCalls int64    `json:"reported_calls"`
}

// ModelUsageStatistics is one model's usage, latency, cost, and cache summary.
type ModelUsageStatistics struct {
	ModelID                 string                     `json:"model_id"`
	CallCount               int64                      `json:"call_count"`
	StartedCalls            int64                      `json:"started_calls"`
	SuccessCalls            int64                      `json:"success_calls"`
	ErrorCalls              int64                      `json:"error_calls"`
	CanceledCalls           int64                      `json:"canceled_calls"`
	UsageReportedCalls      int64                      `json:"usage_reported_calls"`
	UsageUnreportedCalls    int64                      `json:"usage_unreported_calls"`
	AccountingCompleteCalls int64                      `json:"accounting_complete_calls"`
	UnpricedCalls           int64                      `json:"unpriced_calls"`
	PromptTokens            int64                      `json:"prompt_tokens"`
	CompletionTokens        int64                      `json:"completion_tokens"`
	TotalTokens             int64                      `json:"total_tokens"`
	AverageDurationMs       float64                    `json:"average_duration_ms"`
	Latency                 ModelLatencyStatistics     `json:"latency"`
	Costs                   []ModelCostTotal           `json:"costs"`
	ProviderCache           ProviderCacheStatistics    `json:"provider_cache"`
	ApplicationCache        ApplicationCacheStatistics `json:"application_cache"`
}

// ModelUsageResponse is the stable single-model and multi-model response envelope.
type ModelUsageResponse struct {
	From  time.Time              `json:"from"`
	To    time.Time              `json:"to"`
	Items []ModelUsageStatistics `json:"items"`
}
