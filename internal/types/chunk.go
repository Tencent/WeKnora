// Package types defines data structures and types used throughout the system
// These types are shared across different service modules to ensure data consistency
package types

import (
	"time"

	"gorm.io/gorm"
)

// ChunkType 청크 유형 정의
type ChunkType = string

const (
	// ChunkTypeText 일반 텍스트 청크
	ChunkTypeText ChunkType = "text"
	// ChunkTypeImageOCR 이미지 OCR 텍스트 청크
	ChunkTypeImageOCR ChunkType = "image_ocr"
	// ChunkTypeImageCaption 이미지 캡션 청크
	ChunkTypeImageCaption ChunkType = "image_caption"
	// ChunkTypeSummary 요약 유형 청크
	ChunkTypeSummary = "summary"
	// ChunkTypeEntity 엔티티 유형 청크
	ChunkTypeEntity ChunkType = "entity"
	// ChunkTypeRelationship 관계 유형 청크
	ChunkTypeRelationship ChunkType = "relationship"
	// ChunkTypeFAQ FAQ 항목 청크
	ChunkTypeFAQ ChunkType = "faq"
	// ChunkTypeWebSearch 웹 검색 결과 청크
	ChunkTypeWebSearch ChunkType = "web_search"
	// ChunkTypeTableSummary 데이터 테이블 요약 청크
	ChunkTypeTableSummary ChunkType = "table_summary"
	// ChunkTypeTableColumn 데이터 테이블 열 설명 청크
	ChunkTypeTableColumn ChunkType = "table_column"
)

// ChunkStatus 청크 상태 정의
type ChunkStatus int

const (
	ChunkStatusDefault ChunkStatus = 0
	// ChunkStatusStored 저장된 청크
	ChunkStatusStored ChunkStatus = 1
	// ChunkStatusIndexed 인덱싱된 청크
	ChunkStatusIndexed ChunkStatus = 2
)

// ChunkFlags 여러 부울 상태를 관리하기 위한 청크 플래그 비트 정의
type ChunkFlags int

const (
	// ChunkFlagRecommended 추천 가능 상태 (1 << 0 = 1)
	// 이 플래그가 설정되면 해당 청크를 사용자에게 추천할 수 있습니다
	ChunkFlagRecommended ChunkFlags = 1 << 0
	// 향후 확장 가능한 플래그:
	// ChunkFlagPinned ChunkFlags = 1 << 1  // 고정
	// ChunkFlagHot    ChunkFlags = 1 << 2  // 인기
)

// HasFlag 지정된 플래그가 설정되어 있는지 확인
func (f ChunkFlags) HasFlag(flag ChunkFlags) bool {
	return f&flag != 0
}

// SetFlag 지정된 플래그 설정
func (f ChunkFlags) SetFlag(flag ChunkFlags) ChunkFlags {
	return f | flag
}

// ClearFlag 지정된 플래그 해제
func (f ChunkFlags) ClearFlag(flag ChunkFlags) ChunkFlags {
	return f &^ flag
}

// ToggleFlag 지정된 플래그 전환
func (f ChunkFlags) ToggleFlag(flag ChunkFlags) ChunkFlags {
	return f ^ flag
}

// ImageInfo 청크와 관련된 이미지 정보
type ImageInfo struct {
	// 이미지 URL (COS)
	URL string `json:"url"          gorm:"type:text"`
	// 원본 이미지 URL
	OriginalURL string `json:"original_url" gorm:"type:text"`
	// 텍스트 내 이미지 시작 위치
	StartPos int `json:"start_pos"`
	// 텍스트 내 이미지 종료 위치
	EndPos int `json:"end_pos"`
	// 이미지 캡션
	Caption string `json:"caption"`
	// 이미지 OCR 텍스트
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
	// Flags 여러 부울 상태의 비트 플래그 저장 (추천 상태 등)
	// 기본값은 ChunkFlagRecommended (1)로, 기본적으로 추천 가능함을 의미
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
	// Chunk 유형, 다른 유형의 Chunk를 구분하기 위함
	ChunkType ChunkType `json:"chunk_type"               gorm:"type:varchar(20);default:'text'"`
	// 상위 Chunk ID, 이미지 Chunk와 원본 텍스트 Chunk 연결에 사용
	ParentChunkID string `json:"parent_chunk_id"          gorm:"type:varchar(36);index"`
	// 관계 Chunk ID, 관계 Chunk와 원본 텍스트 Chunk 연결에 사용
	RelationChunks JSON `json:"relation_chunks"          gorm:"type:json"`
	// 간접 관계 Chunk ID, 간접 관계 Chunk와 원본 텍스트 Chunk 연결에 사용
	IndirectRelationChunks JSON `json:"indirect_relation_chunks" gorm:"type:json"`
	// Metadata 청크 수준의 확장 정보 저장 (예: FAQ 메타데이터)
	Metadata JSON `json:"metadata"                 gorm:"type:json"`
	// ContentHash 빠른 매칭을 위한 내용 해시 값 저장 (주로 FAQ에 사용)
	ContentHash string `json:"content_hash"             gorm:"type:varchar(64);index"`
	// 이미지 정보, JSON으로 저장
	ImageInfo string `json:"image_info"               gorm:"type:text"`
	// Chunk creation time
	CreatedAt time.Time `json:"created_at"`
	// Chunk last update time
	UpdatedAt time.Time `json:"updated_at"`
	// Soft delete marker, supports data recovery
	DeletedAt gorm.DeletedAt `json:"deleted_at"               gorm:"index"`
}
