package types

// SourceType 콘텐츠 소스의 유형을 나타냅니다.
type SourceType int

const (
	ChunkSourceType   SourceType = iota // 소스가 텍스트 청크입니다.
	PassageSourceType                   // 소스가 구절입니다.
	SummarySourceType                   // 소스가 요약입니다.
)

// MatchType 매칭 알고리즘의 유형을 나타냅니다.
type MatchType int

const (
	MatchTypeEmbedding MatchType = iota
	MatchTypeKeywords
	MatchTypeNearByChunk
	MatchTypeHistory
	MatchTypeParentChunk   // 상위 청크 매칭 유형
	MatchTypeRelationChunk // 관계 청크 매칭 유형
	MatchTypeGraph
	MatchTypeWebSearch    // 웹 검색 매칭 유형
	MatchTypeDirectLoad   // 직접 로드 매칭 유형
	MatchTypeDataAnalysis // 데이터 분석 매칭 유형
)

// IndexInfo 인덱싱된 콘텐츠에 대한 정보를 포함합니다.
type IndexInfo struct {
	ID              string     // 고유 식별자
	Content         string     // 콘텐츠 텍스트
	SourceID        string     // 소스 문서 ID
	SourceType      SourceType // 소스 유형
	ChunkID         string     // 텍스트 청크 ID
	KnowledgeID     string     // 지식 ID
	KnowledgeBaseID string     // 지식베이스 ID
	KnowledgeType   string     // 지식 유형 (예: "faq", "manual")
	TagID           string     // 분류를 위한 태그 ID (FAQ 우선순위 필터링에 사용)
	IsEnabled       bool       // 검색을 위해 청크가 활성화되었는지 여부
}
