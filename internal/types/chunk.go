// Package types defines data structures and types used throughout the system
// These types are shared across different service modules to ensure data consistency
package types

import (
	"time"

	"gorm.io/gorm"
)

// ChunkType 定义了不同类型的 Chunk
type ChunkType = string

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
	// ChunkTypeFAQ 表示 FAQ 条目 Chunk
	ChunkTypeFAQ ChunkType = "faq"
	// ChunkTypeWebSearch 表示 Web 搜索结果的 Chunk
	ChunkTypeWebSearch ChunkType = "web_search"
	// ChunkTypeTableSummary 表示数据表摘要的 Chunk
	ChunkTypeTableSummary ChunkType = "table_summary"
	// ChunkTypeTableColumn 表示数据表列描述的 Chunk
	ChunkTypeTableColumn ChunkType = "table_column"
)

// ChunkStatus 定义了不同状态的 Chunk
type ChunkStatus int

const (
	ChunkStatusDefault ChunkStatus = 0
	// ChunkStatusStored 表示已存储的 Chunk
	ChunkStatusStored ChunkStatus = 1
	// ChunkStatusIndexed 表示已索引的 Chunk
	ChunkStatusIndexed ChunkStatus = 2
)

// ChunkFlags 定义 Chunk 的标志位，用于管理多个布尔状态
type ChunkFlags int

const (
	// ChunkFlagRecommended 表示可推荐状态（1 << 0 = 1）
	// 当设置此标志时，该 Chunk 可以被推荐给用户
	ChunkFlagRecommended ChunkFlags = 1 << 0
	// 未来可扩展更多标志位：
	// ChunkFlagPinned ChunkFlags = 1 << 1  // 置顶
	// ChunkFlagHot    ChunkFlags = 1 << 2  // 热门
)

// HasFlag 检查是否设置了指定标志
func (f ChunkFlags) HasFlag(flag ChunkFlags) bool {
	return f&flag != 0
}

// SetFlag 设置指定标志
func (f ChunkFlags) SetFlag(flag ChunkFlags) ChunkFlags {
	return f | flag
}

// ClearFlag 清除指定标志
func (f ChunkFlags) ClearFlag(flag ChunkFlags) ChunkFlags {
	return f &^ flag
}

// ToggleFlag 切换指定标志
func (f ChunkFlags) ToggleFlag(flag ChunkFlags) ChunkFlags {
	return f ^ flag
}

// ImageInfo 表示与 Chunk 关联的图片信息
type ImageInfo struct {
	// 图片URL（COS）
	URL string `json:"url"          gorm:"type:text"`
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
	ID string `json:"id"                       gorm:"type:varchar(36);primaryKey"`
	// Tenant ID, used for multi-tenant isolation
	TenantID uint64 `json:"tenant_id"`
	// ID of the parent knowledge, associated with the Knowledge model
	KnowledgeID string `json:"knowledge_id"`
	// ID of the knowledge base, for quick location
	KnowledgeBaseID string `json:"knowledge_base_id"`
	// Optional tag ID for categorization within a knowledge base (used for FAQ)
	TagID string `json:"tag_id"                   gorm:"type:varchar(36);index"`
	// Actual text content of the chunk
	Content string `json:"content"`
	// Index position of the chunk in the original document
	ChunkIndex int `json:"chunk_index"`
	// Whether the chunk is enabled, can be used to temporarily disable certain chunks
	IsEnabled bool `json:"is_enabled"               gorm:"default:true"`
	// Flags 存储多个布尔状态的位标志（如推荐状态等）
	// 默认值为 ChunkFlagRecommended (1)，表示默认可推荐
	Flags ChunkFlags `json:"flags"                    gorm:"default:1"`
	// Status of the chunk
	Status int `json:"status"                   gorm:"default:0"`
	// Starting character position in the original text
	StartAt int `json:"start_at"`
	// Ending character position in the original text
	EndAt int `json:"end_at"`
	// Previous chunk ID
	PreChunkID string `json:"pre_chunk_id"`
	// Next chunk ID
	NextChunkID string `json:"next_chunk_id"`
	// Chunk 类型，用于区分不同类型的 Chunk
	ChunkType ChunkType `json:"chunk_type"               gorm:"type:varchar(20);default:'text'"`
	// 父 Chunk ID，用于关联图片 Chunk 和原始文本 Chunk
	ParentChunkID string `json:"parent_chunk_id"          gorm:"type:varchar(36);index"`
	// 关系 Chunk ID，用于关联关系 Chunk 和原始文本 Chunk
	RelationChunks JSON `json:"relation_chunks"          gorm:"type:json"`
	// 间接关系 Chunk ID，用于关联间接关系 Chunk 和原始文本 Chunk
	IndirectRelationChunks JSON `json:"indirect_relation_chunks" gorm:"type:json"`
	// Metadata 存储 chunk 级别的扩展信息，例如 FAQ 元数据
	Metadata JSON `json:"metadata"                 gorm:"type:json"`
	// ContentHash 存储内容的 hash 值，用于快速匹配（主要用于 FAQ）
	ContentHash string `json:"content_hash"             gorm:"type:varchar(64);index"`
	// 图片信息，存储为 JSON
	ImageInfo string `json:"image_info"               gorm:"type:text"`
	// Chunk creation time
	CreatedAt time.Time `json:"created_at"`
	// Chunk last update time
	UpdatedAt time.Time `json:"updated_at"`
	// Soft delete marker, supports data recovery
	DeletedAt gorm.DeletedAt `json:"deleted_at"               gorm:"index"`
}
