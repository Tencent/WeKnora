package types

import (
	"database/sql/driver"
	"encoding/json"
)

// IndexingStrategy controls which indexing pipelines are active for a knowledge base.
// Each boolean flag independently enables/disables a processing pipeline.
// When a document is uploaded, only the enabled pipelines will run.
type IndexingStrategy struct {
	// VectorEnabled enables semantic vector embedding and search
	VectorEnabled bool `yaml:"vector_enabled" json:"vector_enabled"`
	// KeywordEnabled enables keyword-based (BM25) search
	KeywordEnabled bool `yaml:"keyword_enabled" json:"keyword_enabled"`
	// WikiEnabled enables automatic wiki page generation from documents
	WikiEnabled bool `yaml:"wiki_enabled" json:"wiki_enabled"`
	// GraphEnabled enables knowledge graph entity/relation extraction
	GraphEnabled bool `yaml:"graph_enabled" json:"graph_enabled"`
	// ImageVectorEnabled embeds images themselves, so a text query can recall
	// an image without relying on its OCR text or VLM caption. It only takes
	// effect when the embedding model reports image support. Stored in the
	// existing JSON column, so rows written before it existed read back false.
	ImageVectorEnabled bool `yaml:"image_vector_enabled" json:"image_vector_enabled"`
}

// DefaultIndexingStrategy returns the default strategy matching the legacy behavior:
// vector and keyword indexing enabled, wiki and graph disabled.
func DefaultIndexingStrategy() IndexingStrategy {
	return IndexingStrategy{
		VectorEnabled:  true,
		KeywordEnabled: true,
		WikiEnabled:    false,
		GraphEnabled:   false,
	}
}

// NeedsEmbedding returns true if any pipeline that requires an embedding model is enabled.
func (s IndexingStrategy) NeedsEmbedding() bool {
	return s.VectorEnabled || s.KeywordEnabled || s.ImageVectorEnabled
}

// NeedsChunks returns true if any pipeline that requires document chunks is enabled.
// Chunks are needed for vector indexing, keyword indexing, wiki generation, and graph extraction.
func (s IndexingStrategy) NeedsChunks() bool {
	return s.VectorEnabled || s.KeywordEnabled || s.WikiEnabled || s.GraphEnabled || s.ImageVectorEnabled
}

// NeedsImageVector reports whether images should be embedded from their pixels.
// Deliberately also requires VectorEnabled: image vectors live in the vector
// collection, so with it off there is nowhere to put them. Checking here rather
// than only at write time means a row predating the validation degrades to
// text-only instead of querying in a space no stored chunk shares.
func (s IndexingStrategy) NeedsImageVector() bool {
	return s.ImageVectorEnabled && s.VectorEnabled
}

// HasAnyIndexing returns true if at least one indexing pipeline is enabled.
// ImageVectorEnabled does not count: it modifies the vector pipeline rather
// than being one, and counting it would let a KB pass this check while being
// unable to retrieve anything.
func (s IndexingStrategy) HasAnyIndexing() bool {
	return s.VectorEnabled || s.KeywordEnabled || s.WikiEnabled || s.GraphEnabled
}

// IsZero returns true if the strategy has no pipelines enabled (zero value).
// ImageVectorEnabled is excluded for the same reason as in HasAnyIndexing:
// EnsureDefaults uses this to detect an unconfigured strategy, and a flag that
// only tunes the vector pipeline does not mean the strategy was configured.
func (s IndexingStrategy) IsZero() bool {
	return !s.VectorEnabled && !s.KeywordEnabled && !s.WikiEnabled && !s.GraphEnabled
}

// Value implements the driver.Valuer interface for GORM serialization.
func (s IndexingStrategy) Value() (driver.Value, error) {
	return json.Marshal(s)
}

// Scan implements the sql.Scanner interface for GORM deserialization.
// When the database column is NULL (existing rows before migration),
// it returns DefaultIndexingStrategy() for backward compatibility.
func (s *IndexingStrategy) Scan(value interface{}) error {
	if value == nil {
		*s = DefaultIndexingStrategy()
		return nil
	}
	b, ok := value.([]byte)
	if !ok {
		*s = DefaultIndexingStrategy()
		return nil
	}
	if err := json.Unmarshal(b, s); err != nil {
		*s = DefaultIndexingStrategy()
		return nil
	}
	return nil
}
