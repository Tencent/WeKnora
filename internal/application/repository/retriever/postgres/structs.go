package postgres

import (
	"maps"
	"slices"
	"strconv"
	"time"

	"github.com/Tencent/WeKnora/internal/common"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/pgvector/pgvector-go"
)

// pgVector defines the database model for vector embeddings storage
type pgVector struct {
	ID              uint                `json:"id"                gorm:"primarykey"`
	CreatedAt       time.Time           `json:"created_at"        gorm:"column:created_at"`
	UpdatedAt       time.Time           `json:"updated_at"        gorm:"column:updated_at"`
	SourceID        string              `json:"source_id"         gorm:"column:source_id;not null"`
	SourceType      int                 `json:"source_type"       gorm:"column:source_type;not null"`
	ChunkID         string              `json:"chunk_id"          gorm:"column:chunk_id"`
	KnowledgeID     string              `json:"knowledge_id"      gorm:"column:knowledge_id"`
	KnowledgeBaseID string              `json:"knowledge_base_id" gorm:"column:knowledge_base_id"`
	TagID           string              `json:"tag_id"            gorm:"column:tag_id;index"`
	Content         string              `json:"content"           gorm:"column:content;not null"`
	Dimension       int                 `json:"dimension"         gorm:"column:dimension;not null"`
	Embedding       pgvector.HalfVector `json:"embedding"         gorm:"column:embedding;not null"`
	IsEnabled       bool                `json:"is_enabled"        gorm:"column:is_enabled;default:true;index"`
}

// pgVectorWithScore extends pgVector with similarity score field
type pgVectorWithScore struct {
	ID              uint                `json:"id"                gorm:"primarykey"`
	CreatedAt       time.Time           `json:"created_at"        gorm:"column:created_at"`
	UpdatedAt       time.Time           `json:"updated_at"        gorm:"column:updated_at"`
	SourceID        string              `json:"source_id"         gorm:"column:source_id;not null"`
	SourceType      int                 `json:"source_type"       gorm:"column:source_type;not null"`
	ChunkID         string              `json:"chunk_id"          gorm:"column:chunk_id"`
	KnowledgeID     string              `json:"knowledge_id"      gorm:"column:knowledge_id"`
	KnowledgeBaseID string              `json:"knowledge_base_id" gorm:"column:knowledge_base_id"`
	TagID           string              `json:"tag_id"            gorm:"column:tag_id;index"`
	Content         string              `json:"content"           gorm:"column:content;not null"`
	Dimension       int                 `json:"dimension"         gorm:"column:dimension;not null"`
	Embedding       pgvector.HalfVector `json:"embedding"         gorm:"column:embedding;not null"`
	IsEnabled       bool                `json:"is_enabled"        gorm:"column:is_enabled;default:true;index"`
	Score           float64             `json:"score"             gorm:"column:score"`
}

// TableName specifies the database table name for pgVectorWithScore
func (pgVectorWithScore) TableName() string {
	return "embeddings"
}

// TableName specifies the database table name for pgVector
func (pgVector) TableName() string {
	return "embeddings"
}

// toDBVectorEmbedding converts IndexInfo to pgVector database model
func toDBVectorEmbedding(indexInfo *types.IndexInfo, additionalParams map[string]any) *pgVector {
	pgVector := &pgVector{
		SourceID:        indexInfo.SourceID,
		SourceType:      int(indexInfo.SourceType),
		ChunkID:         indexInfo.ChunkID,
		KnowledgeID:     indexInfo.KnowledgeID,
		KnowledgeBaseID: indexInfo.KnowledgeBaseID,
		TagID:           indexInfo.TagID,
		Content:         common.CleanInvalidUTF8(indexInfo.Content),
		IsEnabled:       true, // Default to enabled
	}
	// Add embedding data if available in additionalParams
	if additionalParams != nil && slices.Contains(slices.Collect(maps.Keys(additionalParams)), "embedding") {
		if embeddingMap, ok := additionalParams["embedding"].(map[string][]float32); ok {
			pgVector.Embedding = pgvector.NewHalfVector(embeddingMap[indexInfo.SourceID])
			pgVector.Dimension = len(pgVector.Embedding.Slice())
		}
	}
	// Get is_enabled from additionalParams if available
	if additionalParams != nil {
		if chunkEnabledMap, ok := additionalParams["chunk_enabled"].(map[string]bool); ok {
			if enabled, exists := chunkEnabledMap[indexInfo.ChunkID]; exists {
				pgVector.IsEnabled = enabled
			}
		}
	}
	return pgVector
}

// fromDBVectorEmbeddingWithScore converts pgVectorWithScore to IndexWithScore domain model
func fromDBVectorEmbeddingWithScore(embedding *pgVectorWithScore, matchType types.MatchType) *types.IndexWithScore {
	return &types.IndexWithScore{
		ID:              strconv.FormatInt(int64(embedding.ID), 10),
		SourceID:        embedding.SourceID,
		SourceType:      types.SourceType(embedding.SourceType),
		ChunkID:         embedding.ChunkID,
		KnowledgeID:     embedding.KnowledgeID,
		KnowledgeBaseID: embedding.KnowledgeBaseID,
		TagID:           embedding.TagID,
		Content:         embedding.Content,
		Score:           embedding.Score,
		MatchType:       matchType,
	}
}
