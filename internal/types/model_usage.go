package types

import "time"

// ModelUsage records one real provider response. Unknown price information is
// represented by CostCNY=nil rather than a misleading zero.
type ModelUsage struct {
	ID                uint64            `gorm:"primaryKey;autoIncrement" json:"id"`
	TenantID          uint64            `gorm:"not null;index:idx_model_usage_tenant_created" json:"tenant_id"`
	Model             string            `gorm:"type:varchar(255);not null;index" json:"model"`
	CallType          string            `gorm:"type:varchar(32);not null;default:'chat'" json:"call_type"`
	ActualCalls       int               `gorm:"not null;default:0" json:"actual_calls"`
	Purpose           string            `gorm:"type:varchar(128);not null;default:''" json:"purpose"`
	InputCount        int               `gorm:"not null;default:0" json:"input_count"`
	LocalCacheHits    int               `gorm:"not null;default:0" json:"local_cache_hits"`
	PromptFingerprint string            `gorm:"type:varchar(128);not null;default:''" json:"prompt_fingerprint,omitempty"`
	PromptTokens      int               `gorm:"not null;default:0" json:"prompt_tokens"`
	CompletionTokens  int               `gorm:"not null;default:0" json:"completion_tokens"`
	TotalTokens       int               `gorm:"not null;default:0" json:"total_tokens"`
	CacheReadTokens   int               `gorm:"not null;default:0" json:"cache_read_tokens"`
	CacheWriteTokens  int               `gorm:"not null;default:0" json:"cache_write_tokens"`
	CacheMissTokens   int               `gorm:"not null;default:0" json:"cache_miss_tokens"`
	CacheReported     bool              `gorm:"not null;default:false" json:"cache_reported"`
	CacheStatus       PromptCacheStatus `gorm:"type:varchar(32);not null;default:'unreported'" json:"cache_status"`
	CostCNY           *float64          `gorm:"type:numeric(20,8)" json:"cost_cny,omitempty"`
	Status            string            `gorm:"type:varchar(16);not null;default:'success'" json:"status"`
	ErrorMessage      string            `gorm:"type:varchar(512);not null;default:''" json:"error_message,omitempty"`
	DurationMS        int64             `gorm:"not null;default:0" json:"duration_ms"`
	CreatedAt         time.Time         `gorm:"autoCreateTime;index:idx_model_usage_tenant_created" json:"created_at"`
}

func (ModelUsage) TableName() string { return "model_usages" }

type ModelUsageSummary struct {
	Calls                 int64    `json:"calls"`
	PromptTokens          int64    `json:"prompt_tokens"`
	CompletionTokens      int64    `json:"completion_tokens"`
	TotalTokens           int64    `json:"total_tokens"`
	CacheReadTokens       int64    `json:"cache_read_tokens"`
	CacheWriteTokens      int64    `json:"cache_write_tokens"`
	CacheReportedCalls    int64    `json:"cache_reported_calls"`
	CacheReportingRate    float64  `json:"cache_reporting_rate"`
	ProviderCacheHitRate  *float64 `json:"provider_cache_hit_rate,omitempty"`
	EstimatedCostCNY      float64  `json:"estimated_cost_cny"`
	CostKnownCalls        int64    `json:"cost_known_calls"`
	SuccessfulCalls       int64    `json:"successful_calls"`
	FailedCalls           int64    `json:"failed_calls"`
	EmbeddingInputCount   int64    `json:"embedding_input_count"`
	EmbeddingCacheHits    int64    `json:"embedding_cache_hits"`
	EmbeddingCacheHitRate *float64 `json:"embedding_cache_hit_rate,omitempty"`
}
