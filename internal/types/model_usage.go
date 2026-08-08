package types

import "time"

// Usage provenance for a model call: token counters either came from the
// provider response, were estimated locally (~runes/4), or are unavailable
// for this model kind (e.g. ASR).
const (
	ModelUsageSourceProvider      = "provider"
	ModelUsageSourceEstimated     = "estimated"
	ModelUsageSourceMissing       = "missing"
	ModelUsageSourceNotApplicable = "not_applicable"
)

// ModelUsageEvent is one recorded model invocation. It deliberately stores
// only metadata (model, tokens, latency, outcome) — never prompts, documents,
// image/audio bytes, or model outputs.
type ModelUsageEvent struct {
	ID               uint64      `json:"id" gorm:"primaryKey"`
	TenantID         uint64      `json:"tenant_id"`
	UserID           string      `json:"user_id"`
	RequestID        string      `json:"request_id"`
	ModelID          string      `json:"model_id"`
	ModelName        string      `json:"model_name"`
	ModelType        ModelType   `json:"model_type"`
	ModelSource      ModelSource `json:"model_source"`
	Provider         string      `json:"provider"`
	RequestKind      string      `json:"request_kind"`
	UsageSource      string      `json:"usage_source"`
	PromptTokens     int64       `json:"prompt_tokens"`
	CompletionTokens int64       `json:"completion_tokens"`
	CachedTokens     int64       `json:"cached_tokens"`
	TotalTokens      int64       `json:"total_tokens"`
	InputItems       int         `json:"input_items"`
	DurationMs       int64       `json:"duration_ms"`
	Success          bool        `json:"success"`
	ErrorType        string      `json:"error_type"`
	CreatedAt        time.Time   `json:"created_at"`
}

// TableName maps the event to its storage table.
func (ModelUsageEvent) TableName() string {
	return "model_usage_events"
}

// ModelUsageQuery selects the reporting window and optional filters.
type ModelUsageQuery struct {
	Range       string
	ModelType   ModelType
	ModelID     string
	Start       time.Time
	End         time.Time
	BucketSize  time.Duration
	RecentLimit int
}

// ModelUsageSummary aggregates totals over the reporting window.
type ModelUsageSummary struct {
	WindowStart      time.Time `json:"window_start"`
	WindowEnd        time.Time `json:"window_end"`
	RefreshSeconds   int       `json:"refresh_seconds"`
	TotalCalls       int64     `json:"total_calls"`
	TotalTokens      int64     `json:"total_tokens"`
	PromptTokens     int64     `json:"prompt_tokens"`
	CompletionTokens int64     `json:"completion_tokens"`
	CachedTokens     int64     `json:"cached_tokens"`
	ErrorCount       int64     `json:"error_count"`
	SuccessRate      float64   `json:"success_rate"`
}

// ModelUsageModelStats aggregates usage per model over the window.
type ModelUsageModelStats struct {
	ModelID          string      `json:"model_id"`
	ModelName        string      `json:"model_name"`
	DisplayName      string      `json:"display_name"`
	ModelType        ModelType   `json:"model_type"`
	ModelSource      ModelSource `json:"model_source"`
	Provider         string      `json:"provider"`
	Calls            int64       `json:"calls"`
	PromptTokens     int64       `json:"prompt_tokens"`
	CompletionTokens int64       `json:"completion_tokens"`
	CachedTokens     int64       `json:"cached_tokens"`
	TotalTokens      int64       `json:"total_tokens"`
	InputItems       int64       `json:"input_items"`
	DurationMs       int64       `json:"duration_ms"`
	ErrorCount       int64       `json:"error_count"`
	SuccessRate      float64     `json:"success_rate"`
	AvgTokensPerCall float64     `json:"avg_tokens_per_call"`
	LastUsedAt       *time.Time  `json:"last_used_at"`
}

// ModelUsageTimelinePoint aggregates usage for one (bucket, model) pair.
type ModelUsageTimelinePoint struct {
	BucketStart      time.Time `json:"bucket_start"`
	ModelID          string    `json:"model_id"`
	ModelName        string    `json:"model_name"`
	ModelType        ModelType `json:"model_type"`
	Calls            int64     `json:"calls"`
	PromptTokens     int64     `json:"prompt_tokens"`
	CompletionTokens int64     `json:"completion_tokens"`
	CachedTokens     int64     `json:"cached_tokens"`
	TotalTokens      int64     `json:"total_tokens"`
	ErrorCount       int64     `json:"error_count"`
}

// ModelUsageReport is the dashboard payload.
type ModelUsageReport struct {
	Summary      ModelUsageSummary         `json:"summary"`
	Models       []ModelUsageModelStats    `json:"models"`
	Timeline     []ModelUsageTimelinePoint `json:"timeline"`
	RecentEvents []ModelUsageEvent         `json:"recent_events"`
}
