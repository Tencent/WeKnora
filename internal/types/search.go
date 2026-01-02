package types

import (
	"database/sql/driver"
	"encoding/json"
)

// SearchTargetType 검색 대상의 유형을 나타냅니다.
type SearchTargetType string

const (
	// SearchTargetTypeKnowledgeBase - 전체 지식베이스 검색
	SearchTargetTypeKnowledgeBase SearchTargetType = "knowledge_base"
	// SearchTargetTypeKnowledge - 지식베이스 내 특정 지식 파일 검색
	SearchTargetTypeKnowledge SearchTargetType = "knowledge"
)

// SearchTarget 통합 검색 대상을 나타냅니다.
// 전체 지식베이스 검색 또는 지식베이스 내 특정 지식 파일 검색
type SearchTarget struct {
	// 검색 대상 유형
	Type SearchTargetType `json:"type"`
	// KnowledgeBaseID는 검색할 지식베이스의 ID입니다.
	KnowledgeBaseID string `json:"knowledge_base_id"`
	// KnowledgeIDs는 지식베이스 내에서 검색할 특정 지식 ID의 목록입니다.
	// Type이 SearchTargetTypeKnowledge일 때만 사용됩니다.
	KnowledgeIDs []string `json:"knowledge_ids,omitempty"`
}

// SearchTargets 요청 진입 시 미리 계산된 검색 대상 목록입니다.
type SearchTargets []*SearchTarget

// GetAllKnowledgeBaseIDs 검색 대상에서 모든 고유 지식베이스 ID를 반환합니다.
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

// SearchResult 검색 결과를 나타냅니다.
type SearchResult struct {
	// ID
	ID string `gorm:"column:id"              json:"id"`
	// 내용
	Content string `gorm:"column:content"         json:"content"`
	// 지식 ID
	KnowledgeID string `gorm:"column:knowledge_id"    json:"knowledge_id"`
	// 청크 인덱스
	ChunkIndex int `gorm:"column:chunk_index"     json:"chunk_index"`
	// 지식 제목
	KnowledgeTitle string `gorm:"column:knowledge_title" json:"knowledge_title"`
	// 시작 위치
	StartAt int `gorm:"column:start_at"        json:"start_at"`
	// 종료 위치
	EndAt int `gorm:"column:end_at"          json:"end_at"`
	// 순서
	Seq int `gorm:"column:seq"             json:"seq"`
	// 점수
	Score float64 `                              json:"score"`
	// 매칭 유형
	MatchType MatchType `                              json:"match_type"`
	// 하위 청크 ID
	SubChunkID []string `                              json:"sub_chunk_id"`
	// 메타데이터
	Metadata map[string]string `                              json:"metadata"`

	// Chunk 유형
	ChunkType string `json:"chunk_type"`
	// 상위 Chunk ID
	ParentChunkID string `json:"parent_chunk_id"`
	// 이미지 정보 (JSON 형식)
	ImageInfo string `json:"image_info"`

	// 지식 파일 이름
	// 파일 유형 지식에 사용되며 원본 파일 이름을 포함합니다.
	KnowledgeFilename string `json:"knowledge_filename"`

	// 지식 소스
	// 지식의 출처를 나타내는 데 사용됩니다 (예: "url").
	KnowledgeSource string `json:"knowledge_source"`

	// ChunkMetadata 청크 수준 메타데이터 (예: 생성된 질문) 저장
	ChunkMetadata JSON `json:"chunk_metadata,omitempty"`
}

// SearchParams 검색 매개변수를 나타냅니다.
type SearchParams struct {
	QueryText            string   `json:"query_text"`
	VectorThreshold      float64  `json:"vector_threshold"`
	KeywordThreshold     float64  `json:"keyword_threshold"`
	MatchCount           int      `json:"match_count"`
	DisableKeywordsMatch bool     `json:"disable_keywords_match"`
	DisableVectorMatch   bool     `json:"disable_vector_match"`
	KnowledgeIDs         []string `json:"knowledge_ids"`
	TagIDs               []string `json:"tag_ids"` // 필터링을 위한 태그 ID (FAQ 우선순위 필터링에 사용)
}

// Value SearchResult를 데이터베이스 값으로 변환하는 driver.Valuer 인터페이스 구현
func (c SearchResult) Value() (driver.Value, error) {
	return json.Marshal(c)
}

// Scan 데이터베이스 값을 SearchResult로 변환하는 sql.Scanner 인터페이스 구현
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

// Pagination 페이징 매개변수를 나타냅니다.
type Pagination struct {
	// 페이지
	Page int `form:"page"      json:"page"      binding:"omitempty,min=1"`
	// 페이지 크기
	PageSize int `form:"page_size" json:"page_size" binding:"omitempty,min=1,max=100"`
}

// GetPage 페이지 번호를 가져옵니다. 기본값은 1입니다.
func (p *Pagination) GetPage() int {
	if p.Page < 1 {
		return 1
	}
	return p.Page
}

// GetPageSize 페이지 크기를 가져옵니다. 기본값은 20입니다.
func (p *Pagination) GetPageSize() int {
	if p.PageSize < 1 {
		return 20
	}
	if p.PageSize > 100 {
		return 100
	}
	return p.PageSize
}

// Offset 데이터베이스 쿼리를 위한 오프셋을 가져옵니다.
func (p *Pagination) Offset() int {
	return (p.GetPage() - 1) * p.GetPageSize()
}

// Limit 데이터베이스 쿼리를 위한 제한을 가져옵니다.
func (p *Pagination) Limit() int {
	return p.GetPageSize()
}

// PageResult 페이징 쿼리 결과를 나타냅니다.
type PageResult struct {
	Total    int64       `json:"total"`     // 총 레코드 수
	Page     int         `json:"page"`      // 현재 페이지 번호
	PageSize int         `json:"page_size"` // 페이지 크기
	Data     interface{} `json:"data"`      // 데이터
}

// NewPageResult 새로운 페이징 결과를 생성합니다.
func NewPageResult(total int64, page *Pagination, data interface{}) *PageResult {
	return &PageResult{
		Total:    total,
		Page:     page.GetPage(),
		PageSize: page.GetPageSize(),
		Data:     data,
	}
}
