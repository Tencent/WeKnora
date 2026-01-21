package types

import (
	"database/sql/driver"
	"encoding/json"
)

// SearchTargetType represents the type of search target
type SearchTargetType string

const (
	// SearchTargetTypeKnowledgeBase - search entire knowledge base
	SearchTargetTypeKnowledgeBase SearchTargetType = "knowledge_base"
	// SearchTargetTypeKnowledge - search specific knowledge files within a knowledge base
	SearchTargetTypeKnowledge SearchTargetType = "knowledge"
)

// SearchTarget represents a unified search target
// Either search an entire knowledge base, or specific knowledge files within a knowledge base
type SearchTarget struct {
	// Type of search target
	Type SearchTargetType `json:"type"`
	// KnowledgeBaseID is the ID of the knowledge base to search
	KnowledgeBaseID string `json:"knowledge_base_id"`
	// KnowledgeIDs is the list of specific knowledge IDs to search within the knowledge base
	// Only used when Type is SearchTargetTypeKnowledge
	KnowledgeIDs []string `json:"knowledge_ids,omitempty"`
}

// SearchTargets is a list of search targets, pre-computed at request entry point
type SearchTargets []*SearchTarget

// GetAllKnowledgeBaseIDs returns all unique knowledge base IDs from the search targets
func (st SearchTargets) GetAllKnowledgeBaseIDs() []string {
	seen := make(map[string]bool)
	var result []string
	for _, t := range st {
		if !seen[t.KnowledgeBaseID] {
			seen[t.KnowledgeBaseID] = true
			result = append(result, t.KnowledgeBaseID)
		}
	}
	return result
}

// SearchResult represents the search result
type SearchResult struct {
	// ID
	ID string `gorm:"column:id"              json:"id"`
	// Content
	Content string `gorm:"column:content"         json:"content"`
	// Knowledge ID
	KnowledgeID string `gorm:"column:knowledge_id"    json:"knowledge_id"`
	// Chunk index
	ChunkIndex int `gorm:"column:chunk_index"     json:"chunk_index"`
	// Knowledge title
	KnowledgeTitle string `gorm:"column:knowledge_title" json:"knowledge_title"`
	// Start at
	StartAt int `gorm:"column:start_at"        json:"start_at"`
	// End at
	EndAt int `gorm:"column:end_at"          json:"end_at"`
	// Seq
	Seq int `gorm:"column:seq"             json:"seq"`
	// Score
	Score float64 `                              json:"score"`
	// Match type
	MatchType MatchType `                              json:"match_type"`
	// SubChunkIndex
	SubChunkID []string `                              json:"sub_chunk_id"`
	// Metadata
	Metadata map[string]string `                              json:"metadata"`

	// Chunk 类型
	ChunkType string `json:"chunk_type"`
	// 父 Chunk ID
	ParentChunkID string `json:"parent_chunk_id"`
	// 图片信息 (JSON 格式)
	ImageInfo string `json:"image_info"`

	// Knowledge file name
	// Used for file type knowledge, contains the original file name
	KnowledgeFilename string `json:"knowledge_filename"`

	// Knowledge source
	// Used to indicate the source of the knowledge, such as "url"
	KnowledgeSource string `json:"knowledge_source"`

	// ChunkMetadata stores chunk-level metadata (e.g., generated questions)
	ChunkMetadata JSON `json:"chunk_metadata,omitempty"`

	// MatchedContent is the actual content that was matched in vector search
	// For FAQ: this is the matched question text (standard or similar question)
	MatchedContent string `json:"matched_content,omitempty"`
}

// SearchParams represents the search parameters
type SearchParams struct {
	QueryText            string   `json:"query_text"`
	VectorThreshold      float64  `json:"vector_threshold"`
	KeywordThreshold     float64  `json:"keyword_threshold"`
	MatchCount           int      `json:"match_count"`
	DisableKeywordsMatch bool     `json:"disable_keywords_match"`
	DisableVectorMatch   bool     `json:"disable_vector_match"`
	KnowledgeIDs         []string `json:"knowledge_ids"`
	TagIDs               []string `json:"tag_ids"` // Tag IDs for filtering (used for FAQ priority filtering)
	OnlyRecommended      bool     `json:"only_recommended"`
}

// Value implements the driver.Valuer interface, used to convert SearchResult to database value
func (c SearchResult) Value() (driver.Value, error) {
	return json.Marshal(c)
}

// Scan implements the sql.Scanner interface, used to convert database value to SearchResult
func (c *SearchResult) Scan(value interface{}) error {
	if value == nil {
		return nil
	}
	b, ok := value.([]byte)
	if !ok {
		return nil
	}
	return json.Unmarshal(b, c)
}

// Pagination represents the pagination parameters
type Pagination struct {
	// Page
	Page int `form:"page"      json:"page"      binding:"omitempty,min=1"`
	// Page size
	PageSize int `form:"page_size" json:"page_size" binding:"omitempty,min=1,max=100"`
}

// GetPage gets the page number, default is 1
func (p *Pagination) GetPage() int {
	if p.Page < 1 {
		return 1
	}
	return p.Page
}

// GetPageSize gets the page size, default is 20
func (p *Pagination) GetPageSize() int {
	if p.PageSize < 1 {
		return 20
	}
	if p.PageSize > 100 {
		return 100
	}
	return p.PageSize
}

// Offset gets the offset for database query
func (p *Pagination) Offset() int {
	return (p.GetPage() - 1) * p.GetPageSize()
}

// Limit gets the limit for database query
func (p *Pagination) Limit() int {
	return p.GetPageSize()
}

// PageResult represents the pagination query result
type PageResult struct {
	Total    int64       `json:"total"`     // Total number of records
	Page     int         `json:"page"`      // Current page number
	PageSize int         `json:"page_size"` // Page size
	Data     interface{} `json:"data"`      // Data
}

// NewPageResult creates a new pagination result
func NewPageResult(total int64, page *Pagination, data interface{}) *PageResult {
	return &PageResult{
		Total:    total,
		Page:     page.GetPage(),
		PageSize: page.GetPageSize(),
		Data:     data,
	}
}
