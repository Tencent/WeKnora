package types

import "time"

// EmbeddingCache stores a content-addressed embedding result.
// The unique key is tenant + model + dimension + input hash, so changing an
// embedding model or dimension invalidates only the vector layer.
type EmbeddingCache struct {
	ID        string    `json:"id" gorm:"type:varchar(36);primaryKey"`
	TenantID  uint64    `json:"tenant_id" gorm:"uniqueIndex:idx_embedding_cache_key,priority:1;index"`
	ModelID   string    `json:"model_id" gorm:"type:varchar(128);uniqueIndex:idx_embedding_cache_key,priority:2"`
	Dimension int       `json:"dimension" gorm:"uniqueIndex:idx_embedding_cache_key,priority:3"`
	InputHash string    `json:"input_hash" gorm:"type:varchar(64);uniqueIndex:idx_embedding_cache_key,priority:4;index"`
	Vector    JSON      `json:"vector" gorm:"type:json"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// GenerationCache stores deterministic LLM/VLM artifacts such as OCR/caption,
// Wiki per-document map results, and GraphRAG per-chunk extraction results.
type GenerationCache struct {
	ID            string    `json:"id" gorm:"type:varchar(36);primaryKey"`
	TenantID      uint64    `json:"tenant_id" gorm:"uniqueIndex:idx_generation_cache_key,priority:1;index"`
	Namespace     string    `json:"namespace" gorm:"type:varchar(64);uniqueIndex:idx_generation_cache_key,priority:2;index"`
	ScopeID       string    `json:"scope_id" gorm:"type:varchar(128);uniqueIndex:idx_generation_cache_key,priority:3;index"`
	ModelID       string    `json:"model_id" gorm:"type:varchar(128);uniqueIndex:idx_generation_cache_key,priority:4"`
	InputHash     string    `json:"input_hash" gorm:"type:varchar(64);uniqueIndex:idx_generation_cache_key,priority:5;index"`
	PromptVersion string    `json:"prompt_version" gorm:"type:varchar(32);uniqueIndex:idx_generation_cache_key,priority:6"`
	PromptHash    string    `json:"prompt_hash" gorm:"type:varchar(64);uniqueIndex:idx_generation_cache_key,priority:7"`
	Output        JSON      `json:"output" gorm:"type:json"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
}
