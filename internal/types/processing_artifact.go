package types

import (
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// ProcessingArtifactKind identifies a deterministic, reusable ingestion
// result. Values are persisted and therefore must remain stable.
type ProcessingArtifactKind string

const (
	ProcessingArtifactParser       ProcessingArtifactKind = "parser"
	ProcessingArtifactImageResolve ProcessingArtifactKind = "image_resolve"
	ProcessingArtifactVLMOCR       ProcessingArtifactKind = "vlm_ocr"
	ProcessingArtifactVLMCaption   ProcessingArtifactKind = "vlm_caption"
	ProcessingArtifactEmbedding    ProcessingArtifactKind = "embedding"
	ProcessingArtifactSummary      ProcessingArtifactKind = "summary"
	ProcessingArtifactQuestion     ProcessingArtifactKind = "question"
	ProcessingArtifactWikiExtract  ProcessingArtifactKind = "wiki_extract"
	ProcessingArtifactWikiDedup    ProcessingArtifactKind = "wiki_dedup"
	ProcessingArtifactWikiSummary  ProcessingArtifactKind = "wiki_summary"
	ProcessingArtifactWikiClassify ProcessingArtifactKind = "wiki_classify"
	ProcessingArtifactGraphExtract ProcessingArtifactKind = "graph_extract"
)

const (
	ProcessingArtifactComputing = "computing"
	ProcessingArtifactReady     = "ready"
	ProcessingArtifactFailed    = "failed"
)

// ProcessingArtifact is the durable content-addressed cache record used by
// ingestion. The cache is tenant-scoped: sharing within one tenant is useful,
// while cross-tenant reuse would create privacy and timing side channels.
type ProcessingArtifact struct {
	ID                string                 `json:"id" gorm:"type:varchar(36);primaryKey"`
	TenantID          uint64                 `json:"tenant_id" gorm:"not null;index;uniqueIndex:idx_processing_artifacts_cache_key,priority:1"`
	Kind              ProcessingArtifactKind `json:"kind" gorm:"type:varchar(32);not null;index;uniqueIndex:idx_processing_artifacts_cache_key,priority:2"`
	CacheKey          string                 `json:"cache_key" gorm:"type:varchar(64);not null;uniqueIndex:idx_processing_artifacts_cache_key,priority:3"`
	InputHash         string                 `json:"input_hash" gorm:"type:varchar(64);not null;default:''"`
	ModelFingerprint  string                 `json:"model_fingerprint" gorm:"type:varchar(64);not null;default:''"`
	PromptFingerprint string                 `json:"prompt_fingerprint" gorm:"type:varchar(64);not null;default:''"`
	ConfigFingerprint string                 `json:"config_fingerprint" gorm:"type:varchar(64);not null;default:''"`
	SchemaVersion     string                 `json:"schema_version" gorm:"type:varchar(32);not null;default:'v1'"`
	Status            string                 `json:"status" gorm:"type:varchar(16);not null;index"`
	ResultJSON        JSON                   `json:"result_json" gorm:"type:jsonb"`
	ResultSize        int64                  `json:"result_size" gorm:"not null;default:0"`
	ErrorDetail       string                 `json:"-" gorm:"type:text;not null;default:''"`
	LeaseOwner        string                 `json:"-" gorm:"type:varchar(64);not null;default:''"`
	LeaseExpiresAt    *time.Time             `json:"-" gorm:"index"`
	HitCount          int64                  `json:"hit_count" gorm:"not null;default:0"`
	LastAccessedAt    time.Time              `json:"last_accessed_at" gorm:"not null;index"`
	CreatedAt         time.Time              `json:"created_at"`
	UpdatedAt         time.Time              `json:"updated_at"`
}

func (ProcessingArtifact) TableName() string { return "processing_artifacts" }

func (a *ProcessingArtifact) BeforeCreate(_ *gorm.DB) error {
	if a.ID == "" {
		a.ID = uuid.NewString()
	}
	if a.SchemaVersion == "" {
		a.SchemaVersion = "v1"
	}
	return nil
}
