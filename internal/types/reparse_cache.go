package types

import "time"

// EmbeddingCache stores reusable embeddings keyed by normalized content and
// embedding model identity. The vector itself is JSON-encoded so the same table
// works across PostgreSQL and SQLite.
type EmbeddingCache struct {
	CacheKey    string `json:"cache_key"    gorm:"type:varchar(64);primaryKey"`
	TenantID    uint64 `json:"tenant_id"    gorm:"index"`
	ContentHash string `json:"content_hash" gorm:"type:varchar(64);index"`
	ModelID     string `json:"model_id"     gorm:"type:varchar(64);index"`
	ModelName   string `json:"model_name"   gorm:"type:varchar(255)"`
	Dimensions  int    `json:"dimensions"   gorm:"index"`
	Embedding   JSON   `json:"embedding"    gorm:"type:json"`
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// ImageMultimodalCache stores OCR/caption output for a frozen image + VLM
// prompt/model tuple.
type ImageMultimodalCache struct {
	CacheKey      string `json:"cache_key"      gorm:"type:varchar(64);primaryKey"`
	TenantID      uint64 `json:"tenant_id"      gorm:"index"`
	ImageHash     string `json:"image_hash"     gorm:"type:varchar(64);index"`
	ModelID       string `json:"model_id"       gorm:"type:varchar(64);index"`
	ModelName     string `json:"model_name"     gorm:"type:varchar(255)"`
	PromptVersion string `json:"prompt_version" gorm:"type:varchar(64);index"`
	SourceType    string `json:"source_type"    gorm:"type:varchar(32);index"`
	OCRText       string `json:"ocr_text"       gorm:"type:text"`
	Caption       string `json:"caption"        gorm:"type:text"`
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

// GraphExtractionCache stores the LLM graph extraction map result for one
// chunk/content/config tuple.
type GraphExtractionCache struct {
	CacheKey      string `json:"cache_key"      gorm:"type:varchar(64);primaryKey"`
	TenantID      uint64 `json:"tenant_id"      gorm:"index"`
	ChunkHash     string `json:"chunk_hash"     gorm:"type:varchar(64);index"`
	ModelID       string `json:"model_id"       gorm:"type:varchar(64);index"`
	ModelName     string `json:"model_name"     gorm:"type:varchar(255)"`
	ConfigHash    string `json:"config_hash"    gorm:"type:varchar(64);index"`
	PromptVersion string `json:"prompt_version" gorm:"type:varchar(64);index"`
	GraphData     JSON   `json:"graph_data"     gorm:"type:json"`
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

// WikiMapCache stores the pure map-phase result for one document/content/wiki
// config tuple. Reduce remains live and is intentionally not cached.
type WikiMapCache struct {
	CacheKey      string `json:"cache_key"      gorm:"type:varchar(64);primaryKey"`
	TenantID      uint64 `json:"tenant_id"      gorm:"index"`
	KnowledgeID   string `json:"knowledge_id"   gorm:"type:varchar(36);index"`
	ContentHash   string `json:"content_hash"   gorm:"type:varchar(64);index"`
	ModelID       string `json:"model_id"       gorm:"type:varchar(64);index"`
	ModelName     string `json:"model_name"     gorm:"type:varchar(255)"`
	ConfigHash    string `json:"config_hash"    gorm:"type:varchar(64);index"`
	PromptVersion string `json:"prompt_version" gorm:"type:varchar(64);index"`
	ResultJSON    JSON   `json:"result_json"    gorm:"type:json"`
	UpdatesJSON   JSON   `json:"updates_json"   gorm:"type:json"`
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

// ReparseArtifactCache stores compressed JSON artifacts produced by pure,
// expensive stages that do not warrant a dedicated table (document parsing,
// summaries, generated questions, and future deterministic post-processing
// outputs). Cache keys are tenant-scoped and include the effective model /
// prompt / configuration fingerprints at the call site.
type ReparseArtifactCache struct {
	CacheKey      string `json:"cache_key"      gorm:"type:varchar(64);primaryKey"`
	TenantID      uint64 `json:"tenant_id"      gorm:"index"`
	ArtifactType  string `json:"artifact_type"  gorm:"type:varchar(32);index"`
	ContentHash   string `json:"content_hash"   gorm:"type:varchar(64);index"`
	ModelID       string `json:"model_id"       gorm:"type:varchar(64);index"`
	ModelName     string `json:"model_name"     gorm:"type:varchar(255)"`
	ConfigHash    string `json:"config_hash"    gorm:"type:varchar(64);index"`
	PromptVersion string `json:"prompt_version" gorm:"type:varchar(64);index"`
	ResultData    []byte `json:"result_data"`
	CreatedAt     time.Time
	UpdatedAt     time.Time
}
