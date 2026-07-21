package types

import (
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// Artifact cache type constants.
const (
	ArtifactCacheTypeChunkEmbedding = "chunk_embedding"
	ArtifactCacheTypeVLMOcr         = "vlm_ocr"
	ArtifactCacheTypeVLMCaption     = "vlm_caption"
	ArtifactCacheTypeSummary        = "summary"
	ArtifactCacheTypeQuestion       = "question"
	ArtifactCacheTypeWikiExtract    = "wiki_extract"
	ArtifactCacheTypeGraphEntity    = "graph_entity"
	ArtifactCacheTypeGraphRelation  = "graph_relation"
)

// ArtifactCache stores a single cached computation result keyed by
// (cache_key, cache_type, input_hash, config_hash). Entries are tenant-scoped
// unless a cache producer explicitly uses the reserved global tenant namespace
// (currently shared embedding vectors use tenant 0).
//
// Cached entries are best-effort: a miss falls through to the original
// compute path; a store failure is logged and swallowed.
type ArtifactCache struct {
	ID         string         `json:"id"          gorm:"type:varchar(36);primaryKey"`
	TenantID   uint64         `json:"tenant_id"   gorm:"not null;uniqueIndex:uq_artifact_cache_compound,priority:1;index:idx_artifact_caches_tenant_key"`
	CacheKey   string         `json:"cache_key"   gorm:"type:varchar(128);not null;uniqueIndex:uq_artifact_cache_compound,priority:2"`
	CacheType  string         `json:"cache_type"  gorm:"type:varchar(32);not null;uniqueIndex:uq_artifact_cache_compound,priority:3;index:idx_artifact_caches_type_input"`
	InputHash  string         `json:"input_hash"  gorm:"type:varchar(64);not null;uniqueIndex:uq_artifact_cache_compound,priority:4;index:idx_artifact_caches_type_input"`
	ConfigHash string         `json:"config_hash" gorm:"type:varchar(64);not null;default:'';uniqueIndex:uq_artifact_cache_compound,priority:5"`
	OutputJSON JSON           `json:"output_json,omitempty" gorm:"type:json"`
	OutputText string         `json:"output_text,omitempty" gorm:"type:text"`
	OutputSize int64          `json:"output_size"  gorm:"not null;default:0"`
	ComputedAt time.Time      `json:"computed_at"  gorm:"default:CURRENT_TIMESTAMP"`
	ExpiresAt  *time.Time     `json:"expires_at,omitempty"`
	CreatedAt  time.Time      `json:"created_at"`
	UpdatedAt  time.Time      `json:"updated_at"`
	DeletedAt  gorm.DeletedAt `json:"deleted_at"   gorm:"index"`
}

// BeforeCreate initialises the ID field when it is empty.
func (a *ArtifactCache) BeforeCreate(_ *gorm.DB) error {
	if a.ID == "" {
		a.ID = uuid.NewString()
	}
	return nil
}

// TableName returns the artifact cache table name.
func (ArtifactCache) TableName() string { return "artifact_caches" }
