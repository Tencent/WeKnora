package types

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sort"
	"strings"
	"time"
)

// FAQChunkMetadata Chunk.Metadata 내의 FAQ 항목 구조를 정의합니다.
type FAQChunkMetadata struct {
	StandardQuestion  string         `json:"standard_question"`
	SimilarQuestions  []string       `json:"similar_questions,omitempty"`
	NegativeQuestions []string       `json:"negative_questions,omitempty"`
	Answers           []string       `json:"answers,omitempty"`
	AnswerStrategy    AnswerStrategy `json:"answer_strategy,omitempty"`
	Version           int            `json:"version,omitempty"`
	Source            string         `json:"source,omitempty"`
}

// GeneratedQuestion AI에 의해 생성된 단일 질문을 나타냅니다.
type GeneratedQuestion struct {
	ID       string `json:"id"`       // source_id 구성에 사용되는 고유 식별자
	Question string `json:"question"` // 질문 내용
}

// DocumentChunkMetadata 문서 청크의 메타데이터 구조를 정의합니다.
// AI 생성 질문과 같은 향상된 정보를 저장하는 데 사용됩니다.
type DocumentChunkMetadata struct {
	// GeneratedQuestions AI가 이 청크에 대해 생성한 관련 질문을 저장합니다.
	// 이러한 질문은 재현율을 높이기 위해 독립적으로 인덱싱됩니다.
	GeneratedQuestions []GeneratedQuestion `json:"generated_questions,omitempty"`
}

// GetQuestionStrings 질문 내용 문자열 목록을 반환합니다 (이전 코드 호환성).
func (m *DocumentChunkMetadata) GetQuestionStrings() []string {
	if m == nil || len(m.GeneratedQuestions) == 0 {
		return nil
	}
	result := make([]string, len(m.GeneratedQuestions))
	for i, q := range m.GeneratedQuestions {
		result[i] = q.Question
	}
	return result
}

// DocumentMetadata 청크에서 문서 메타데이터를 파싱합니다.
func (c *Chunk) DocumentMetadata() (*DocumentChunkMetadata, error) {
	if c == nil || len(c.Metadata) == 0 {
		return nil, nil
	}
	var meta DocumentChunkMetadata
	if err := json.Unmarshal(c.Metadata, &meta); err != nil {
		return nil, err
	}
	return &meta, nil
}

// SetDocumentMetadata 청크의 문서 메타데이터를 설정합니다.
func (c *Chunk) SetDocumentMetadata(meta *DocumentChunkMetadata) error {
	if c == nil {
		return nil
	}
	if meta == nil {
		c.Metadata = nil
		return nil
	}
	bytes, err := json.Marshal(meta)
	if err != nil {
		return err
	}
	c.Metadata = JSON(bytes)
	return nil
}

// Normalize 공백 및 중복 항목을 정리합니다.
func (m *FAQChunkMetadata) Normalize() {
	if m == nil {
		return
	}
	m.StandardQuestion = strings.TrimSpace(m.StandardQuestion)
	m.SimilarQuestions = normalizeStrings(m.SimilarQuestions)
	m.NegativeQuestions = normalizeStrings(m.NegativeQuestions)
	m.Answers = normalizeStrings(m.Answers)
	if m.Version <= 0 {
		m.Version = 1
	}
}

// FAQMetadata 청크에서 FAQ 메타데이터를 파싱합니다.
func (c *Chunk) FAQMetadata() (*FAQChunkMetadata, error) {
	if c == nil || len(c.Metadata) == 0 {
		return nil, nil
	}
	var meta FAQChunkMetadata
	if err := json.Unmarshal(c.Metadata, &meta); err != nil {
		return nil, err
	}
	meta.Normalize()
	return &meta, nil
}

// SetFAQMetadata 청크의 FAQ 메타데이터를 설정합니다.
func (c *Chunk) SetFAQMetadata(meta *FAQChunkMetadata) error {
	if c == nil {
		return nil
	}
	if meta == nil {
		c.Metadata = nil
		c.ContentHash = ""
		return nil
	}
	meta.Normalize()
	bytes, err := json.Marshal(meta)
	if err != nil {
		return err
	}
	c.Metadata = JSON(bytes)
	// ContentHash 계산 및 설정
	c.ContentHash = CalculateFAQContentHash(meta)
	return nil
}

// CalculateFAQContentHash FAQ 내용의 해시 값을 계산합니다.
// 해시 기준: 표준 질문 + 유사 질문(정렬됨) + 부정 질문(정렬됨) + 답변(정렬됨)
// 빠른 매칭 및 중복 제거에 사용됩니다.
func CalculateFAQContentHash(meta *FAQChunkMetadata) string {
	if meta == nil {
		return ""
	}

	// 복사본 생성 및 정규화
	normalized := *meta
	normalized.Normalize()

	// 배열 정렬 (동일한 내용이 동일한 해시를 생성하도록 함)
	similarQuestions := make([]string, len(normalized.SimilarQuestions))
	copy(similarQuestions, normalized.SimilarQuestions)
	sort.Strings(similarQuestions)

	negativeQuestions := make([]string, len(normalized.NegativeQuestions))
	copy(negativeQuestions, normalized.NegativeQuestions)
	sort.Strings(negativeQuestions)

	answers := make([]string, len(normalized.Answers))
	copy(answers, normalized.Answers)
	sort.Strings(answers)

	// 해시 문자열 생성: 표준 질문 + 유사 질문 + 부정 질문 + 답변
	var builder strings.Builder
	builder.WriteString(normalized.StandardQuestion)
	builder.WriteString("|")
	builder.WriteString(strings.Join(similarQuestions, ","))
	builder.WriteString("|")
	builder.WriteString(strings.Join(negativeQuestions, ","))
	builder.WriteString("|")
	builder.WriteString(strings.Join(answers, ","))

	// SHA256 해시 계산
	hash := sha256.Sum256([]byte(builder.String()))
	return hex.EncodeToString(hash[:])
}

// AnswerStrategy 답변 반환 전략을 정의합니다.
type AnswerStrategy string

const (
	// AnswerStrategyAll 모든 답변 반환
	AnswerStrategyAll AnswerStrategy = "all"
	// AnswerStrategyRandom 무작위로 하나의 답변 반환
	AnswerStrategyRandom AnswerStrategy = "random"
)

// FAQEntry 프론트엔드에 반환되는 FAQ 항목을 나타냅니다.
type FAQEntry struct {
	ID                string         `json:"id"`
	ChunkID           string         `json:"chunk_id"`
	KnowledgeID       string         `json:"knowledge_id"`
	KnowledgeBaseID   string         `json:"knowledge_base_id"`
	TagID             string         `json:"tag_id"`
	TagName           string         `json:"tag_name"`
	IsEnabled         bool           `json:"is_enabled"`
	IsRecommended     bool           `json:"is_recommended"`
	StandardQuestion  string         `json:"standard_question"`
	SimilarQuestions  []string       `json:"similar_questions"`
	NegativeQuestions []string       `json:"negative_questions"`
	Answers           []string       `json:"answers"`
	AnswerStrategy    AnswerStrategy `json:"answer_strategy"`
	IndexMode         FAQIndexMode   `json:"index_mode"`
	UpdatedAt         time.Time      `json:"updated_at"`
	CreatedAt         time.Time      `json:"created_at"`
	Score             float64        `json:"score,omitempty"`
	MatchType         MatchType      `json:"match_type,omitempty"`
	ChunkType         ChunkType      `json:"chunk_type"`
}

// FAQEntryPayload FAQ 항목 생성/업데이트를 위한 페이로드
type FAQEntryPayload struct {
	StandardQuestion  string          `json:"standard_question"    binding:"required"`
	SimilarQuestions  []string        `json:"similar_questions"`
	NegativeQuestions []string        `json:"negative_questions"`
	Answers           []string        `json:"answers"              binding:"required"`
	AnswerStrategy    *AnswerStrategy `json:"answer_strategy,omitempty"`
	TagID             string          `json:"tag_id"`
	TagName           string          `json:"tag_name"`
	IsEnabled         *bool           `json:"is_enabled,omitempty"`
	IsRecommended     *bool           `json:"is_recommended,omitempty"`
}

const (
	FAQBatchModeAppend  = "append"
	FAQBatchModeReplace = "replace"
)

// FAQBatchUpsertPayload FAQ 항목 일괄 가져오기
type FAQBatchUpsertPayload struct {
	Entries     []FAQEntryPayload `json:"entries"      binding:"required"`
	Mode        string            `json:"mode"         binding:"oneof=append replace"`
	KnowledgeID string            `json:"knowledge_id"`
}

// FAQSearchRequest FAQ 검색 요청 매개변수
type FAQSearchRequest struct {
	QueryText            string   `json:"query_text"             binding:"required"`
	VectorThreshold      float64  `json:"vector_threshold"`
	MatchCount           int      `json:"match_count"`
	FirstPriorityTagIDs  []string `json:"first_priority_tag_ids"`  // 1순위 태그 ID 목록, 검색 범위 제한, 가장 높은 우선순위
	SecondPriorityTagIDs []string `json:"second_priority_tag_ids"` // 2순위 태그 ID 목록, 검색 범위 제한, 1순위보다 낮은 우선순위
}

// UntaggedTagID 분류되지 않은 항목을 나타내는 특수 태그 ID
const UntaggedTagID = "__untagged__"

// FAQEntryFieldsUpdate 단일 FAQ 항목의 필드 업데이트
type FAQEntryFieldsUpdate struct {
	IsEnabled     *bool   `json:"is_enabled,omitempty"`
	IsRecommended *bool   `json:"is_recommended,omitempty"`
	TagID         *string `json:"tag_id,omitempty"`
	// 향후 더 많은 필드 확장 가능
}

// FAQEntryFieldsBatchUpdate FAQ 항목 필드 일괄 업데이트 요청
// 두 가지 모드 지원:
// 1. ID별 업데이트: ByID 필드 사용
// 2. 태그별 업데이트: ByTag 필드 사용, 해당 태그의 모든 항목에 동일한 업데이트 적용
type FAQEntryFieldsBatchUpdate struct {
	// ByID 항목 ID별 업데이트, 키는 항목 ID
	ByID map[string]FAQEntryFieldsUpdate `json:"by_id,omitempty"`
	// ByTag 태그별 일괄 업데이트, 키는 TagID (__untagged__는 미분류)
	ByTag map[string]FAQEntryFieldsUpdate `json:"by_tag,omitempty"`
	// ExcludeIDs ByTag 작업에서 제외할 ID 목록
	ExcludeIDs []string `json:"exclude_ids,omitempty"`
}

// FAQImportTaskStatus 가져오기 작업 상태
type FAQImportTaskStatus string

const (
	// FAQImportStatusPending FAQ 가져오기 작업의 대기 상태를 나타냅니다
	FAQImportStatusPending FAQImportTaskStatus = "pending"
	// FAQImportStatusProcessing FAQ 가져오기 작업의 처리 중 상태를 나타냅니다
	FAQImportStatusProcessing FAQImportTaskStatus = "processing"
	// FAQImportStatusCompleted FAQ 가져오기 작업의 완료 상태를 나타냅니다
	FAQImportStatusCompleted FAQImportTaskStatus = "completed"
	// FAQImportStatusFailed FAQ 가져오기 작업의 실패 상태를 나타냅니다
	FAQImportStatusFailed FAQImportTaskStatus = "failed"
)

// FAQImportProgress Redis에 저장된 FAQ 가져오기 작업의 진행 상황을 나타냅니다
type FAQImportProgress struct {
	TaskID      string              `json:"task_id"`       // 가져오기 작업을 위한 UUID
	KBID        string              `json:"kb_id"`         // 지식베이스 ID
	KnowledgeID string              `json:"knowledge_id"`  // FAQ 지식 ID
	Status      FAQImportTaskStatus `json:"status"`        // 작업 상태
	Progress    int                 `json:"progress"`      // 0-100 퍼센트
	Total       int                 `json:"total"`         // 가져올 총 항목 수
	Processed   int                 `json:"processed"`     // 현재까지 처리된 항목 수
	Message     string              `json:"message"`       // 상태 메시지
	Error       string              `json:"error"`         // 실패 시 오류 메시지
	CreatedAt   int64               `json:"created_at"`    // 작업 생성 타임스탬프
	UpdatedAt   int64               `json:"updated_at"`    // 마지막 업데이트 타임스탬프
}

// FAQImportMetadata Knowledge.Metadata에 저장된 FAQ 가져오기 작업 정보
// Deprecated: Redis 저장소를 사용하는 FAQImportProgress를 대신 사용하세요
type FAQImportMetadata struct {
	ImportProgress  int `json:"import_progress"` // 0-100
	ImportTotal     int `json:"import_total"`
	ImportProcessed int `json:"import_processed"`
}

// ToJSON 메타데이터를 JSON 타입으로 변환합니다.
func (m *FAQImportMetadata) ToJSON() (JSON, error) {
	if m == nil {
		return nil, nil
	}
	bytes, err := json.Marshal(m)
	if err != nil {
		return nil, err
	}
	return JSON(bytes), nil
}

// ParseFAQImportMetadata Knowledge에서 FAQ 가져오기 메타데이터를 파싱합니다.
func ParseFAQImportMetadata(k *Knowledge) (*FAQImportMetadata, error) {
	if k == nil || len(k.Metadata) == 0 {
		return nil, nil
	}
	var metadata FAQImportMetadata
	if err := json.Unmarshal(k.Metadata, &metadata); err != nil {
		return nil, err
	}
	return &metadata, nil
}

func normalizeStrings(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	dedup := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, v := range values {
		trimmed := strings.TrimSpace(v)
		if trimmed == "" {
			continue
		}
		if _, exists := seen[trimmed]; exists {
			continue
		}
		seen[trimmed] = struct{}{}
		dedup = append(dedup, trimmed)
	}
	if len(dedup) == 0 {
		return nil
	}
	return dedup
}
