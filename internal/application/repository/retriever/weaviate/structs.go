package weaviate

import (
	"sync"

	"github.com/weaviate/weaviate-go-client/v5/weaviate"
)

type weaviateRepository struct {
	client             *weaviate.Client
	collectionBaseName string
	replicationFactor  int // 0 = use Weaviate server default
	desiredShardCount  int // 0 = use Weaviate server default
	// Cache for initialized collections (dimension -> true)
	initializedCollections sync.Map
}

// VectorEmbedding is an exported type.
type VectorEmbedding struct {
	Content         string    `json:"content"`
	SourceID        string    `json:"source_id"`
	SourceType      int       `json:"source_type"`
	ChunkID         string    `json:"chunk_id"`
	KnowledgeID     string    `json:"knowledge_id"`
	KnowledgeBaseID string    `json:"knowledge_base_id"`
	TagID           string    `json:"tag_id"`
	Embedding       []float32 `json:"embedding"`
	IsEnabled       bool      `json:"is_enabled"`
}

// VectorEmbeddingWithScore is an exported type.
type VectorEmbeddingWithScore struct {
	VectorEmbedding
	Score float64
}
