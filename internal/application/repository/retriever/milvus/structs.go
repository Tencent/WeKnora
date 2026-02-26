package milvus

import (
	"sync"

	client "github.com/milvus-io/milvus/client/v2/milvusclient"
)

type milvusRepository struct {
	filter
	client             *client.Client
	collectionBaseName string
	// Cache for initialized collections (dimension -> true)
	initializedCollections sync.Map
}

type MilvusVectorEmbedding struct {
	ID              string    `json:"id"`
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

type MilvusVectorEmbeddingWithScore struct {
	MilvusVectorEmbedding
	Score float64
}
