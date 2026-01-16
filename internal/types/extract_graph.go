package types

const (
	TypeChunkExtract        = "chunk:extract"
	TypeDocumentProcess     = "document:process"      // 文档处理任务
	TypeFAQImport           = "faq:import"            // FAQ导入任务（包含dry run模式）
	TypeQuestionGeneration  = "question:generation"   // 问题生成任务
	TypeSummaryGeneration   = "summary:generation"    // 摘要生成任务
	TypeKBClone             = "kb:clone"              // 知识库复制任务
	TypeIndexDelete         = "index:delete"          // 索引删除任务
	TypeKBDelete            = "kb:delete"             // 知识库删除任务
	TypeKnowledgeListDelete = "knowledge:list_delete" // 批量删除知识任务
	TypeDataTableSummary    = "datatable:summary"     // 表格摘要任务
)

// ExtractChunkPayload represents the extract chunk task payload
type ExtractChunkPayload struct {
	TenantID uint64 `json:"tenant_id"`
	ChunkID  string `json:"chunk_id"`
	ModelID  string `json:"model_id"`
}

// DocumentProcessPayload represents the document process task payload
type DocumentProcessPayload struct {
	RequestId                string   `json:"request_id"`
	TenantID                 uint64   `json:"tenant_id"`
	KnowledgeID              string   `json:"knowledge_id"`
	KnowledgeBaseID          string   `json:"knowledge_base_id"`
	FilePath                 string   `json:"file_path,omitempty"` // 文件路径（文件导入时使用）
	FileName                 string   `json:"file_name,omitempty"` // 文件名（文件导入时使用）
	FileType                 string   `json:"file_type,omitempty"` // 文件类型（文件导入时使用）
	URL                      string   `json:"url,omitempty"`       // URL（URL导入时使用）
	Passages                 []string `json:"passages,omitempty"`  // 文本段落（文本导入时使用）
	EnableMultimodel         bool     `json:"enable_multimodel"`
	EnableQuestionGeneration bool     `json:"enable_question_generation"` // 是否启用问题生成
	QuestionCount            int      `json:"question_count,omitempty"`   // 每个chunk生成的问题数量
}

// FAQImportPayload represents the FAQ import task payload (including dry run mode)
type FAQImportPayload struct {
	TenantID    uint64            `json:"tenant_id"`
	TaskID      string            `json:"task_id"`
	KBID        string            `json:"kb_id"`
	KnowledgeID string            `json:"knowledge_id,omitempty"` // 仅非 dry run 模式需要
	Entries     []FAQEntryPayload `json:"entries,omitempty"`      // 小数据量时直接存储在 payload 中
	EntriesURL  string            `json:"entries_url,omitempty"`  // 大数据量时存储到对象存储，这里存储 URL
	EntryCount  int               `json:"entry_count,omitempty"`  // 条目总数（使用 EntriesURL 时需要）
	Mode        string            `json:"mode"`
	DryRun      bool              `json:"dry_run"`     // dry run 模式只验证不导入
	EnqueuedAt  int64             `json:"enqueued_at"` // 任务入队时间戳，用于区分同一 TaskID 的不同次提交
}

// QuestionGenerationPayload represents the question generation task payload
type QuestionGenerationPayload struct {
	TenantID        uint64 `json:"tenant_id"`
	KnowledgeBaseID string `json:"knowledge_base_id"`
	KnowledgeID     string `json:"knowledge_id"`
	QuestionCount   int    `json:"question_count"`
}

// SummaryGenerationPayload represents the summary generation task payload
type SummaryGenerationPayload struct {
	TenantID        uint64 `json:"tenant_id"`
	KnowledgeBaseID string `json:"knowledge_base_id"`
	KnowledgeID     string `json:"knowledge_id"`
}

// KBClonePayload represents the knowledge base clone task payload
type KBClonePayload struct {
	TenantID uint64 `json:"tenant_id"`
	TaskID   string `json:"task_id"`
	SourceID string `json:"source_id"`
	TargetID string `json:"target_id"`
}

// IndexDeletePayload represents the index delete task payload
type IndexDeletePayload struct {
	TenantID         uint64                  `json:"tenant_id"`
	KnowledgeBaseID  string                  `json:"knowledge_base_id"`
	EmbeddingModelID string                  `json:"embedding_model_id"`
	KBType           string                  `json:"kb_type"`
	ChunkIDs         []string                `json:"chunk_ids"`
	EffectiveEngines []RetrieverEngineParams `json:"effective_engines"`
}

// KBDeletePayload represents the knowledge base delete task payload
type KBDeletePayload struct {
	TenantID         uint64                  `json:"tenant_id"`
	KnowledgeBaseID  string                  `json:"knowledge_base_id"`
	EffectiveEngines []RetrieverEngineParams `json:"effective_engines"`
}

// KnowledgeListDeletePayload represents the batch knowledge delete task payload
type KnowledgeListDeletePayload struct {
	TenantID     uint64   `json:"tenant_id"`
	KnowledgeIDs []string `json:"knowledge_ids"`
}

// KBCloneTaskStatus represents the status of a knowledge base clone task
type KBCloneTaskStatus string

const (
	KBCloneStatusPending    KBCloneTaskStatus = "pending"
	KBCloneStatusProcessing KBCloneTaskStatus = "processing"
	KBCloneStatusCompleted  KBCloneTaskStatus = "completed"
	KBCloneStatusFailed     KBCloneTaskStatus = "failed"
)

// KBCloneProgress represents the progress of a knowledge base clone task
type KBCloneProgress struct {
	TaskID    string            `json:"task_id"`
	SourceID  string            `json:"source_id"`
	TargetID  string            `json:"target_id"`
	Status    KBCloneTaskStatus `json:"status"`
	Progress  int               `json:"progress"`   // 0-100
	Total     int               `json:"total"`      // 总知识数
	Processed int               `json:"processed"`  // 已处理数
	Message   string            `json:"message"`    // 状态消息
	Error     string            `json:"error"`      // 错误信息
	CreatedAt int64             `json:"created_at"` // 任务创建时间
	UpdatedAt int64             `json:"updated_at"` // 最后更新时间
}

// ChunkContext represents chunk content with surrounding context
type ChunkContext struct {
	ChunkID     string `json:"chunk_id"`
	Content     string `json:"content"`
	PrevContent string `json:"prev_content,omitempty"` // Previous chunk content for context
	NextContent string `json:"next_content,omitempty"` // Next chunk content for context
}

// PromptTemplateStructured represents the prompt template structured
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

// GraphRelation represents the relation of the graph
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

// NameSpace represents the name space of the knowledge base and knowledge
type NameSpace struct {
	KnowledgeBase string `json:"knowledge_base"`
	Knowledge     string `json:"knowledge"`
}

// Labels returns the labels of the name space
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
