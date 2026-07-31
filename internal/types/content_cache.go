package types

import (
	"crypto/sha256"
	"encoding/hex"
	"time"
)

// Content cache kind identifiers. Every cache key embeds the kind so the
// same content never collides across layers, and every key embeds the
// model id / prompt version / config that produced the payload so a model or
// config change invalidates exactly the affected layer (see ContentCacheKey).
const (
	ContentCacheKindVLM       = "vlm"
	ContentCacheKindEmbedding = "embedding"
	ContentCacheKindWikiMap   = "wiki_map"
	ContentCacheKindSummary   = "summary"
	ContentCacheKindQuestion  = "question"
	ContentCacheKindGraph     = "graph_extract"
)

// ContentCache is a content-addressed cache row for deterministic pipeline
// products (VLM OCR/Caption, embeddings, wiki per-document maps, per-chunk
// graph extractions, summaries, questions). Keys are derived from the inputs
// of the cached computation so an unchanged input always resolves to the same
// key, while any input change (content, model, prompt, config) produces a
// miss and a fresh computation.
type ContentCache struct {
	CacheKey  string    `json:"cache_key" gorm:"column:cache_key;type:varchar(128);primaryKey"`
	Kind      string    `json:"kind"      gorm:"column:kind;type:varchar(32);index"`
	Payload   string    `json:"payload"   gorm:"column:payload;type:text"`
	CreatedAt time.Time `json:"created_at" gorm:"column:created_at"`
	UpdatedAt time.Time `json:"updated_at" gorm:"column:updated_at"`
}

// TableName returns the content_cache table name.
func (ContentCache) TableName() string { return "content_cache" }

// StableChunkID derives a deterministic chunk ID from content-identifying
// parts (knowledge id, chunk type, sequence, normalized content). Two parses
// of the same content produce the same chunk ID, so vectors, wiki chunk
// references and graph edges survive a reparse. The returned value is 32 hex
// characters and fits the chunks.id VARCHAR(36) column.
func StableChunkID(parts ...string) string {
	h := sha256.New()
	for _, p := range parts {
		h.Write([]byte(p))
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))[:32]
}

// ContentCacheKey builds a content-addressed cache key for the given kind.
// Every contributing input (content hash, model id, prompt version, config
// fingerprint) is hashed into the key, so a change to any of them moves the
// key: the cache layer affected by that input invalidates precisely, while
// layers whose inputs did not change keep hitting.
func ContentCacheKey(kind string, parts ...string) string {
	h := sha256.New()
	h.Write([]byte(kind))
	for _, p := range parts {
		h.Write([]byte{0})
		h.Write([]byte(p))
	}
	return kind + ":" + hex.EncodeToString(h.Sum(nil))
}

// ContentCachePayload is the maximum payload size we are willing to persist.
// Payloads beyond this are skipped (best-effort caching) so a single oversized
// document cannot blow up the table.
const ContentCachePayloadMaxBytes = 512 * 1024
