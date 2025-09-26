// Package types defines data structures and types used throughout the system
// These types are shared across different service modules to ensure data consistency
package types

import (
	"time"

	"gorm.io/gorm"
)

// ChunkType 定义了不同类型的 Chunk
type ChunkType string

const (
	// ChunkTypeText 表示普通的文本 Chunk
	ChunkTypeText ChunkType = "text"
	// ChunkTypeImageOCR 表示图片 OCR 文本的 Chunk
	ChunkTypeImageOCR ChunkType = "image_ocr"
	// ChunkTypeImageCaption 表示图片描述的 Chunk
	ChunkTypeImageCaption ChunkType = "image_caption"
	// ChunkTypeSummary 表示摘要类型的 Chunk
	ChunkTypeSummary = "summary"
	// ChunkTypeEntity 表示实体类型的 Chunk
	ChunkTypeEntity ChunkType = "entity"
	// ChunkTypeRelationship 表示关系类型的 Chunk
	ChunkTypeRelationship ChunkType = "relationship"
	// ChunkTypeExtraction 表示抽取类型的 Chunk
	ChunkTypeExtraction ChunkType = "extraction"
)

// ImageInfo 表示与 Chunk 关联的图片信息
type ImageInfo struct {
	// 图片URL（COS）
	URL string `json:"url" gorm:"type:text"`
	// 原始图片URL
	OriginalURL string `json:"original_url" gorm:"type:text"`
	// 图片在文本中的开始位置
	StartPos int `json:"start_pos"`
	// 图片在文本中的结束位置
	EndPos int `json:"end_pos"`
	// 图片描述
	Caption string `json:"caption"`
	// 图片OCR文本
	OCRText string `json:"ocr_text"`
}

// Chunk represents a document chunk
// Chunks are meaningful text segments extracted from original documents
// and are the basic units of knowledge base retrieval
// Each chunk contains a portion of the original content
// and maintains its positional relationship with the original text
// Chunks can be independently embedded as vectors and retrieved, supporting precise content localization
type Chunk struct {
	// Unique identifier of the chunk, using UUID format
	ID string `json:"id" gorm:"type:varchar(36);primaryKey"`
	// Tenant ID, used for multi-tenant isolation
	TenantID uint `json:"tenant_id"`
	// ID of the parent knowledge, associated with the Knowledge model
	KnowledgeID string `json:"knowledge_id"`
	// ID of the knowledge base, for quick location
	KnowledgeBaseID string `json:"knowledge_base_id"`
	// Actual text content of the chunk
	Content string `json:"content"`
	// Index position of the chunk in the original document
	ChunkIndex int `json:"chunk_index"`
	// Whether the chunk is enabled, can be used to temporarily disable certain chunks
	IsEnabled bool `json:"is_enabled" gorm:"default:true"`
	// Starting character position in the original text
	StartAt int `json:"start_at" `
	// Ending character position in the original text
	EndAt int `json:"end_at"`
	// Previous chunk ID
	PreChunkID string `json:"pre_chunk_id"`
	// Next chunk ID
	NextChunkID string `json:"next_chunk_id"`
	// Chunk 类型，用于区分不同类型的 Chunk
	ChunkType ChunkType `json:"chunk_type" gorm:"type:varchar(20);default:'text'"`
	// 父 Chunk ID，用于关联图片 Chunk 和原始文本 Chunk
	ParentChunkID string `json:"parent_chunk_id" gorm:"type:varchar(36);index"`
	// 关系 Chunk ID，用于关联关系 Chunk 和原始文本 Chunk
	RelationChunks JSON `json:"relation_chunks" gorm:"type:json"`
	// 间接关系 Chunk ID，用于关联间接关系 Chunk 和原始文本 Chunk
	IndirectRelationChunks JSON `json:"indirect_relation_chunks" gorm:"type:json"`
	// 图片信息，存储为 JSON
	ImageInfo string `json:"image_info" gorm:"type:text"`
	// Chunk creation time
	CreatedAt time.Time `json:"created_at"`
	// Chunk last update time
	UpdatedAt time.Time `json:"updated_at"`
	// Soft delete marker, supports data recovery
	DeletedAt gorm.DeletedAt `json:"deleted_at" gorm:"index"`
}
