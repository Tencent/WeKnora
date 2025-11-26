package qdrant

import "github.com/qdrant/go-client/qdrant"

type qdrantRepository struct {
	client         *qdrant.Client
	collectionName string
}

type QdrantVectorEmbedding struct {
	Content         string    `json:"content"`
	SourceID        string    `json:"source_id"`
	SourceType      int       `json:"source_type"`
	ChunkID         string    `json:"chunk_id"`
	KnowledgeID     string    `json:"knowledge_id"`
	KnowledgeBaseID string    `json:"knowledge_base_id"`
	Embedding       []float32 `json:"embedding"`
}

type QdrantVectorEmbeddingWithScore struct {
	QdrantVectorEmbedding
	Score float64
}
