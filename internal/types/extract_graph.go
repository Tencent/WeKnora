package types

const (
	TypeChunkExtract       = "chunk:extract"
	TypeDocumentProcess    = "document:process"    // 문서 처리 작업
	TypeFAQImport          = "faq:import"          // FAQ 가져오기 작업
	TypeQuestionGeneration = "question:generation" // 질문 생성 작업
	TypeSummaryGeneration  = "summary:generation"  // 요약 생성 작업
	TypeKBClone            = "kb:clone"            // 지식베이스 복사 작업
	TypeIndexDelete        = "index:delete"        // 인덱스 삭제 작업
	TypeKBDelete           = "kb:delete"           // 지식베이스 삭제 작업
	TypeDataTableSummary   = "datatable:summary"   // 데이터 테이블 요약 작업
)

// ExtractChunkPayload 청크 추출 작업 페이로드를 나타냅니다.
type ExtractChunkPayload struct {
	TenantID uint64 `json:"tenant_id"`
	ChunkID  string `json:"chunk_id"`
	ModelID  string `json:"model_id"`
}

// DocumentProcessPayload 문서 처리 작업 페이로드를 나타냅니다.
type DocumentProcessPayload struct {
	RequestId                string   `json:"request_id"`
	TenantID                 uint64   `json:"tenant_id"`
	KnowledgeID              string   `json:"knowledge_id"`
	KnowledgeBaseID          string   `json:"knowledge_base_id"`
	FilePath                 string   `json:"file_path,omitempty"` // 파일 경로 (파일 가져오기 시 사용)
	FileName                 string   `json:"file_name,omitempty"` // 파일 이름 (파일 가져오기 시 사용)
	FileType                 string   `json:"file_type,omitempty"` // 파일 유형 (파일 가져오기 시 사용)
	URL                      string   `json:"url,omitempty"`       // URL (URL 가져오기 시 사용)
	Passages                 []string `json:"passages,omitempty"`  // 텍스트 구절 (텍스트 가져오기 시 사용)
	EnableMultimodel         bool     `json:"enable_multimodel"`
	EnableQuestionGeneration bool     `json:"enable_question_generation"` // 질문 생성 활성화 여부
	QuestionCount            int      `json:"question_count,omitempty"`   // 청크당 생성할 질문 수
}

// FAQImportPayload FAQ 가져오기 작업 페이로드를 나타냅니다.
type FAQImportPayload struct {
	TenantID    uint64            `json:"tenant_id"`
	TaskID      string            `json:"task_id"`
	KBID        string            `json:"kb_id"`
	KnowledgeID string            `json:"knowledge_id"`
	Entries     []FAQEntryPayload `json:"entries"`
	Mode        string            `json:"mode"`
}

// QuestionGenerationPayload 질문 생성 작업 페이로드를 나타냅니다.
type QuestionGenerationPayload struct {
	TenantID        uint64 `json:"tenant_id"`
	KnowledgeBaseID string `json:"knowledge_base_id"`
	KnowledgeID     string `json:"knowledge_id"`
	QuestionCount   int    `json:"question_count"`
}

// SummaryGenerationPayload 요약 생성 작업 페이로드를 나타냅니다.
type SummaryGenerationPayload struct {
	TenantID        uint64 `json:"tenant_id"`
	KnowledgeBaseID string `json:"knowledge_base_id"`
	KnowledgeID     string `json:"knowledge_id"`
}

// KBClonePayload 지식베이스 복사 작업 페이로드를 나타냅니다.
type KBClonePayload struct {
	TenantID uint64 `json:"tenant_id"`
	TaskID   string `json:"task_id"`
	SourceID string `json:"source_id"`
	TargetID string `json:"target_id"`
}

// IndexDeletePayload 인덱스 삭제 작업 페이로드를 나타냅니다.
type IndexDeletePayload struct {
	TenantID         uint64                  `json:"tenant_id"`
	KnowledgeBaseID  string                  `json:"knowledge_base_id"`
	EmbeddingModelID string                  `json:"embedding_model_id"`
	KBType           string                  `json:"kb_type"`
	ChunkIDs         []string                `json:"chunk_ids"`
	EffectiveEngines []RetrieverEngineParams `json:"effective_engines"`
}

// KBDeletePayload 지식베이스 삭제 작업 페이로드를 나타냅니다.
type KBDeletePayload struct {
	TenantID         uint64                  `json:"tenant_id"`
	KnowledgeBaseID  string                  `json:"knowledge_base_id"`
	EffectiveEngines []RetrieverEngineParams `json:"effective_engines"`
}

// KBCloneTaskStatus 지식베이스 복사 작업의 상태를 나타냅니다.
type KBCloneTaskStatus string

const (
	KBCloneStatusPending    KBCloneTaskStatus = "pending"
	KBCloneStatusProcessing KBCloneTaskStatus = "processing"
	KBCloneStatusCompleted  KBCloneTaskStatus = "completed"
	KBCloneStatusFailed     KBCloneTaskStatus = "failed"
)

// KBCloneProgress 지식베이스 복사 작업의 진행 상황을 나타냅니다.
type KBCloneProgress struct {
	TaskID    string            `json:"task_id"`
	SourceID  string            `json:"source_id"`
	TargetID  string            `json:"target_id"`
	Status    KBCloneTaskStatus `json:"status"`
	Progress  int               `json:"progress"`   // 0-100
	Total     int               `json:"total"`      // 총 지식 수
	Processed int               `json:"processed"`  // 처리된 수
	Message   string            `json:"message"`    // 상태 메시지
	Error     string            `json:"error"`      // 오류 메시지
	CreatedAt int64             `json:"created_at"` // 작업 생성 시간
	UpdatedAt int64             `json:"updated_at"` // 마지막 업데이트 시간
}

// ChunkContext 주변 문맥을 포함한 청크 내용을 나타냅니다.
type ChunkContext struct {
	ChunkID     string `json:"chunk_id"`
	Content     string `json:"content"`
	PrevContent string `json:"prev_content,omitempty"` // 문맥을 위한 이전 청크 내용
	NextContent string `json:"next_content,omitempty"` // 문맥을 위한 다음 청크 내용
}

// PromptTemplateStructured 구조화된 프롬프트 템플릿을 나타냅니다.
type PromptTemplateStructured struct {
	Description string      `json:"description"`
	Tags        []string    `json:"tags"`
	Examples    []GraphData `json:"examples"`
}

type GraphNode struct {
	Name       string   `json:"name,omitempty"`
	Chunks     []string `json:"chunks,omitempty"`
	Attributes []string `json:"attributes,omitempty"`
}

// GraphRelation 그래프의 관계를 나타냅니다.
type GraphRelation struct {
	Node1 string `json:"node1,omitempty"`
	Node2 string `json:"node2,omitempty"`
	Type  string `json:"type,omitempty"`
}

type GraphData struct {
	Text     string           `json:"text,omitempty"`
	Node     []*GraphNode     `json:"node,omitempty"`
	Relation []*GraphRelation `json:"relation,omitempty"`
}

// NameSpace 지식베이스 및 지식의 네임스페이스를 나타냅니다.
type NameSpace struct {
	KnowledgeBase string `json:"knowledge_base"`
	Knowledge     string `json:"knowledge"`
}

// Labels 네임스페이스의 레이블을 반환합니다.
func (n NameSpace) Labels() []string {
	res := make([]string, 0)
	if n.KnowledgeBase != "" {
		res = append(res, n.KnowledgeBase)
	}
	if n.Knowledge != "" {
		res = append(res, n.Knowledge)
	}
	return res
}
