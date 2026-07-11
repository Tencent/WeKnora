package types

import (
	"time"

	"gorm.io/gorm"
)

const (
	ProcessingCacheStageVLMOCR           = "vlm_ocr"
	ProcessingCacheStageVLMCaption       = "vlm_caption"
	ProcessingCacheStageWikiMap          = "wiki_map"
	ProcessingCacheStageEmbedding        = "embedding"
	ProcessingCacheStageGraphChunk       = "graph_chunk"
	ProcessingCacheStageSummary          = "summary"
	ProcessingCacheStageQuestion         = "question"
	ProcessingCacheStageParse            = "parse_artifact"
	ProcessingCacheStageWikiContribution = "wiki_contribution"
)

// ProcessingCache stores deterministic intermediate processing artifacts.
//
// CacheKey is intentionally caller-defined so each pipeline stage can include
// the exact invalidation surface it depends on: normalized content/image hash,
// model ID, prompt version, chunking config, and similar knobs.
type ProcessingCache struct {
	ID        string         `json:"id" gorm:"type:varchar(36);primaryKey"`
	TenantID  uint64         `json:"tenant_id" gorm:"uniqueIndex:idx_processing_cache_tenant_stage_key,priority:1"`
	Stage     string         `json:"stage" gorm:"type:varchar(64);uniqueIndex:idx_processing_cache_tenant_stage_key,priority:2;index:idx_processing_cache_stage_updated,priority:1"`
	CacheKey  string         `json:"cache_key" gorm:"type:varchar(128);uniqueIndex:idx_processing_cache_tenant_stage_key,priority:3"`
	Payload   JSON           `json:"payload" gorm:"type:json"`
	Metadata  JSON           `json:"metadata" gorm:"type:json"`
	LastHitAt *time.Time     `json:"last_hit_at"`
	CreatedAt time.Time      `json:"created_at"`
	UpdatedAt time.Time      `json:"updated_at" gorm:"index:idx_processing_cache_stage_updated,priority:2"`
	DeletedAt gorm.DeletedAt `json:"deleted_at" gorm:"index"`
}
