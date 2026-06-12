package embedding

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// EmbeddingCache provides an optional, transparent cache layer for previously
// computed embeddings. Cache keys are content-addressable: the same text +
// model + dimensions + chunk config always maps to the same cached vector.
//
// The cache is opt-in and does not affect ingestion semantics.
type EmbeddingCache struct {
	db *gorm.DB
}

// CacheEntry represents a single cached embedding.
type CacheEntry struct {
	ContentHash     string `gorm:"column:content_hash;primaryKey"`
	ModelID         string `gorm:"column:model_id;primaryKey"`
	Dimensions      int    `gorm:"column:dimensions;primaryKey"`
	ChunkConfigHash string `gorm:"column:chunk_config_hash;default:''"`
	Embedding       []byte `gorm:"column:embedding"`
	CreatedAt       int64  `gorm:"column:created_at;autoCreateTime"`
}

// TableName overrides the table name.
func (CacheEntry) TableName() string {
	return "embedding_cache"
}

// CacheResult holds the lookup result: cached embeddings indexed by their
// original position, and the indices of cache misses.
type CacheResult struct {
	Hits  map[int][]float32 // original index → cached embedding
	Misses []int            // original indices that need fresh embedding
}

// NewEmbeddingCache creates a new cache backed by the given GORM DB.
func NewEmbeddingCache(db *gorm.DB) *EmbeddingCache {
	return &EmbeddingCache{db: db}
}

// ComputeHash returns a SHA-256 hex digest that uniquely identifies an
// embedding computation. It covers the sanitized text, model identity,
// vector dimensions, chunk config, and preprocessing pipeline version.
//
// chunkConfig is expected to be a deterministic JSON serialization of the
// chunk configuration (chunk_size, overlap, strategy, etc.).
func ComputeHash(text, modelID string, dimensions int, chunkConfig []byte, preprocessingVersion string) string {
	h := sha256.New()
	h.Write([]byte(text))
	h.Write([]byte{0}) // separator
	h.Write([]byte(modelID))
	h.Write([]byte{0})

	var dimBuf [4]byte
	binary.LittleEndian.PutUint32(dimBuf[:], uint32(dimensions))
	h.Write(dimBuf[:])
	h.Write([]byte{0})

	h.Write(chunkConfig)
	h.Write([]byte{0})
	h.Write([]byte(preprocessingVersion))

	return hex.EncodeToString(h.Sum(nil))
}

// HashChunkConfig serializes chunk configuration to a deterministic JSON byte
// slice suitable for hashing.
func HashChunkConfig(chunkSize, chunkOverlap int, strategy string) []byte {
	type config struct {
		ChunkSize    int    `json:"chunk_size"`
		ChunkOverlap int    `json:"chunk_overlap"`
		Strategy     string `json:"strategy"`
	}
	b, _ := json.Marshal(config{ChunkSize: chunkSize, ChunkOverlap: chunkOverlap, Strategy: strategy})
	return b
}

// Lookup fetches cached embeddings for the given content hashes. It returns
// a CacheResult with hits (indexed by original position) and misses (indices
// that need fresh embedding via the API).
func (c *EmbeddingCache) Lookup(
	hashes []string,
	modelID string,
	dimensions int,
) (*CacheResult, error) {
	if len(hashes) == 0 {
		return &CacheResult{Hits: make(map[int][]float32)}, nil
	}

	// Deduplicate hashes to avoid redundant lookups.
	uniqueHashes := make(map[string]struct{}, len(hashes))
	for _, h := range hashes {
		uniqueHashes[h] = struct{}{}
	}
	hashList := make([]string, 0, len(uniqueHashes))
	for h := range uniqueHashes {
		hashList = append(hashList, h)
	}

	var entries []CacheEntry
	if err := c.db.Where(
		"content_hash IN ? AND model_id = ? AND dimensions = ?",
		hashList, modelID, dimensions,
	).Find(&entries).Error; err != nil {
		return nil, fmt.Errorf("embedding cache lookup: %w", err)
	}

	// Build hash → embedding map.
	cacheMap := make(map[string][]float32, len(entries))
	for _, e := range entries {
		cacheMap[e.ContentHash] = BytesToFloat32s(e.Embedding)
	}

	result := &CacheResult{Hits: make(map[int][]float32)}
	for i, h := range hashes {
		if emb, ok := cacheMap[h]; ok {
			result.Hits[i] = emb
		} else {
			result.Misses = append(result.Misses, i)
		}
	}
	return result, nil
}

// Store writes embedding entries to the cache. ON CONFLICT DO NOTHING ensures
// idempotent writes — concurrent callers or retries are safe.
func (c *EmbeddingCache) Store(entries []CacheEntry) error {
	if len(entries) == 0 {
		return nil
	}
	return c.db.Clauses(clause.OnConflict{DoNothing: true}).Create(&entries).Error
}

// BytesToFloat32s converts a BYTEA/BLOB byte slice back to []float32.
func BytesToFloat32s(b []byte) []float32 {
	if len(b)%4 != 0 {
		return nil
	}
	count := len(b) / 4
	result := make([]float32, count)
	for i := range result {
		result[i] = math.Float32frombits(binary.LittleEndian.Uint32(b[i*4 : (i+1)*4]))
	}
	return result
}

// Float32sToBytes converts a []float32 to a byte slice for BYTEA/BLOB storage.
func Float32sToBytes(v []float32) []byte {
	b := make([]byte, len(v)*4)
	for i, f := range v {
		binary.LittleEndian.PutUint32(b[i*4:], math.Float32bits(f))
	}
	return b
}
