package types

import "time"

// VLMCacheKey identifies a cached VLM (OCR/Caption) result by content +
// model + prompt. The cache is content-addressed (no tenant_id, no doc_id):
// the same image bytes under the same VLM model and prompt version MUST
// yield the same canonical OCR/caption text regardless of who owns the
// image. Tenant isolation is enforced at the read layer (a worker only
// reads image bytes it owns), not at the cache layer.
type VLMCache struct {
	ImageHash     string    `gorm:"column:image_hash;type:varchar(64);primaryKey"`
	ModelID       string    `gorm:"column:model_id;type:varchar(64);primaryKey"`
	PromptVersion string    `gorm:"column:prompt_version;type:varchar(64);primaryKey"`
	PromptKind    string    `gorm:"column:prompt_kind;type:varchar(32);primaryKey"`
	OutputText    string    `gorm:"column:output_text;type:text"`
	CreatedAt     time.Time `gorm:"column:created_at;autoCreateTime"`
}

func (VLMCache) TableName() string { return "vlm_cache" }

// EmbeddingCache stores a vector for a (normalized text hash, model, dim)
// tuple. The key excludes doc_id and chunk_id so identical text in two
// different documents dedups to a single cached vector.
//
// Vector is serialized as a JSON array of floats (TEXT column) for full DB
// portability (sqlite / mysql / postgres). Retrieval-side vector stores
// remain untouched — this cache only short-circuits the Embed call.
type EmbeddingCache struct {
	TextHash  string    `gorm:"column:text_hash;type:varchar(128);primaryKey"`
	ModelID   string    `gorm:"column:model_id;type:varchar(64);primaryKey"`
	Dimension int       `gorm:"column:dimension;primaryKey"`
	Vector    string    `gorm:"column:vector;type:text"`
	CreatedAt time.Time `gorm:"column:created_at;autoCreateTime"`
}

func (EmbeddingCache) TableName() string { return "embedding_cache" }

// WikiMapCache stores the complete per-document map output of
// mapOneDocument (extracted entities/concepts, summary, citations,
// new-slugs) keyed by frozen doc content hash + extraction granularity +
// synthesis model + prompt version. A hit skips extract/dedup/summary/
// classify and jumps straight to reduce.
//
// Payload is an opaque JSON blob; the schema of that blob is owned by the
// wiki ingest service, not by the cache table.
type WikiMapCache struct {
	DocContentHash  string    `gorm:"column:doc_content_hash;type:varchar(128);primaryKey"`
	Granularity     string    `gorm:"column:granularity;type:varchar(32);primaryKey"`
	SynthesisModel  string    `gorm:"column:synthesis_model_id;type:varchar(64);primaryKey"`
	PromptVersion   string    `gorm:"column:prompt_version;type:varchar(64);primaryKey"`
	Payload         string    `gorm:"column:payload;type:text"`
	CreatedAt       time.Time `gorm:"column:created_at;autoCreateTime"`
}

func (WikiMapCache) TableName() string { return "wiki_map_cache" }

// GraphChunkCache stores per-chunk GraphRAG extraction output (entities +
// relationships for that chunk) keyed by frozen chunk content hash +
// extract config hash + chat model + prompt version.
type GraphChunkCache struct {
	ChunkContentHash string    `gorm:"column:chunk_content_hash;type:varchar(128);primaryKey"`
	ExtractConfigHash string   `gorm:"column:extract_config_hash;type:varchar(128);primaryKey"`
	ChatModelID      string    `gorm:"column:chat_model_id;type:varchar(64);primaryKey"`
	PromptVersion    string    `gorm:"column:prompt_version;type:varchar(64);primaryKey"`
	Payload          string    `gorm:"column:payload;type:text"`
	CreatedAt        time.Time `gorm:"column:created_at;autoCreateTime"`
}

func (GraphChunkCache) TableName() string { return "graph_chunk_cache" }

// ParseProductCache stores the parse step's full ReadResult (markdown +
// image references + image byte hashes + metadata) keyed by file bytes
// hash + parser engine + parser config + render config. A hit skips the
// CPU-hour-scale re-render of scanned documents.
//
// Image byte hashes are stored in the payload so the VLM cache (keyed on
// image bytes) still governs whether OCR/Caption re-runs.
type ParseProductCache struct {
	FileHash         string    `gorm:"column:file_hash;type:varchar(128);primaryKey"`
	ParserEngine     string    `gorm:"column:parser_engine;type:varchar(32);primaryKey"`
	ParserConfigHash string    `gorm:"column:parser_config_hash;type:varchar(128);primaryKey"`
	RenderConfigHash string    `gorm:"column:render_config_hash;type:varchar(128);primaryKey"`
	Payload          string    `gorm:"column:payload;type:text"`
	CreatedAt        time.Time `gorm:"column:created_at;autoCreateTime"`
}

func (ParseProductCache) TableName() string { return "parse_product_cache" }