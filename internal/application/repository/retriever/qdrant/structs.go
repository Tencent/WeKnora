package qdrant

import (
	"sync"

	"github.com/qdrant/go-client/qdrant"
)

type qdrantRepository struct {
	client             *qdrant.Client
	collectionBaseName string
	// Cache for initialized collections (dimension -> true)
	initializedCollections sync.Map
}

type QdrantVectorEmbedding struct {
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

type QdrantVectorEmbeddingWithScore struct {
	QdrantVectorEmbedding
	Score float64
}
