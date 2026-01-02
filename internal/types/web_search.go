package types

import (
	"database/sql/driver"
	"encoding/json"
	"time"
)

// WebSearchConfig 테넌트의 웹 검색 구성을 나타냅니다
type WebSearchConfig struct {
	Provider          string   `json:"provider"`           // 검색 엔진 공급자 ID
	APIKey            string   `json:"api_key"`            // API 키 (필요한 경우)
	MaxResults        int      `json:"max_results"`        // 최대 검색 결과 수
	IncludeDate       bool     `json:"include_date"`       // 날짜 포함 여부
	CompressionMethod string   `json:"compression_method"` // 압축 방법: none, summary, extract, rag
	Blacklist         []string `json:"blacklist"`          // 블랙리스트 규칙 목록
	// RAG 압축 관련 구성
	EmbeddingModelID   string `json:"embedding_model_id,omitempty"`  // 임베딩 모델 ID (RAG 압축용)
	EmbeddingDimension int    `json:"embedding_dimension,omitempty"` // 임베딩 차원 (RAG 압축용)
	RerankModelID      string `json:"rerank_model_id,omitempty"`     // 재순위 모델 ID (RAG 압축용)
	DocumentFragments  int    `json:"document_fragments,omitempty"`  // 문서 조각 수 (RAG 압축용)
}

// Value WebSearchConfig를 데이터베이스 값으로 변환하는 driver.Valuer 인터페이스 구현
func (c WebSearchConfig) Value() (driver.Value, error) {
	return json.Marshal(c)
}

// Scan 데이터베이스 값을 WebSearchConfig로 변환하는 sql.Scanner 인터페이스 구현
func (c *WebSearchConfig) Scan(value interface{}) error {
	if value == nil {
		return nil
	}
	b, ok := value.([]byte)
	if !ok {
		return nil
	}
	return json.Unmarshal(b, c)
}

// WebSearchResult 단일 웹 검색 결과를 나타냅니다
type WebSearchResult struct {
	Title       string     `json:"title"`                  // 검색 결과 제목
	URL         string     `json:"url"`                    // 결과 URL
	Snippet     string     `json:"snippet"`                // 요약 스니펫
	Content     string     `json:"content"`                // 전체 내용 (선택 사항, 추가 크롤링 필요)
	Source      string     `json:"source"`                 // 소스 (예: duckduckgo 등)
	PublishedAt *time.Time `json:"published_at,omitempty"` // 게시 시간 (있는 경우)
}

// WebSearchProviderInfo 웹 검색 공급자에 대한 정보를 나타냅니다
type WebSearchProviderInfo struct {
	ID             string `json:"id"`                // 공급자 ID
	Name           string `json:"name"`              // 공급자 이름
	Free           bool   `json:"free"`              // 무료 여부
	RequiresAPIKey bool   `json:"requires_api_key"`  // API 키 필요 여부
	Description    string `json:"description"`       // 설명
	APIURL         string `json:"api_url,omitempty"` // API 주소 (선택 사항)
}
