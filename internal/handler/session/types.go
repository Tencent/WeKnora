package session

import (
	"github.com/Tencent/WeKnora/internal/types"
)

// CreateSessionRequest 세션 생성 요청을 나타냅니다.
// 세션은 이제 지식베이스와 독립적이며 대화 컨테이너 역할을 합니다.
// 모든 구성(지식베이스, 모델 설정 등)은 쿼리 시점에 사용자 정의 에이전트에서 가져옵니다.
type CreateSessionRequest struct {
	// 세션 제목 (선택 사항)
	Title string `json:"title"`
	// 세션 설명 (선택 사항)
	Description string `json:"description"`
}

// GenerateTitleRequest 세션 제목 생성을 위한 요청 구조를 정의합니다.
type GenerateTitleRequest struct {
	Messages []types.Message `json:"messages" binding:"required"` // 제목 생성 컨텍스트로 사용할 메시지
}

// MentionedItemRequest 요청에서 언급된 항목을 나타냅니다.
type MentionedItemRequest struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Type   string `json:"type"`    // "kb"는 지식베이스, "file"은 파일
	KBType string `json:"kb_type"` // "document" 또는 "faq" (kb 유형인 경우에만 해당)
}

// CreateKnowledgeQARequest 지식 QA를 위한 요청 구조를 정의합니다.
type CreateKnowledgeQARequest struct {
	Query            string                 `json:"query"              binding:"required"` // 지식베이스 검색을 위한 쿼리 텍스트
	KnowledgeBaseIDs []string               `json:"knowledge_base_ids"`                    // 이 요청에 선택된 지식베이스 ID
	KnowledgeIds     []string               `json:"knowledge_ids"`                         // 이 요청에 선택된 지식 ID
	AgentEnabled     bool                   `json:"agent_enabled"`                         // 이 요청에 대해 에이전트 모드 활성화 여부
	AgentID          string                 `json:"agent_id"`                              // 이 요청에 선택된 사용자 정의 에이전트 ID
	WebSearchEnabled bool                   `json:"web_search_enabled"`                    // 이 요청에 대해 웹 검색 활성화 여부
	SummaryModelID   string                 `json:"summary_model_id"`                      // 이 요청에 대한 선택적 요약 모델 ID (세션 기본값 재정의)
	MentionedItems   []MentionedItemRequest `json:"mentioned_items"`                       // @언급된 지식베이스 및 파일
	DisableTitle     bool                   `json:"disable_title"`                         // 자동 제목 생성 비활성화 여부
}

// SearchKnowledgeRequest LLM 요약 없이 지식을 검색하기 위한 요청 구조를 정의합니다.
type SearchKnowledgeRequest struct {
	Query            string   `json:"query"              binding:"required"` // 검색할 쿼리 텍스트
	KnowledgeBaseID  string   `json:"knowledge_base_id"`                     // 단일 지식베이스 ID (하위 호환성을 위해)
	KnowledgeBaseIDs []string `json:"knowledge_base_ids"`                    // 검색할 지식베이스 ID 목록 (다중 KB 지원)
	KnowledgeIDs     []string `json:"knowledge_ids"`                         // 검색할 특정 지식(파일) ID 목록
}

// StopSessionRequest 세션 중지 요청을 나타냅니다.
type StopSessionRequest struct {
	MessageID string `json:"message_id" binding:"required"`
}
