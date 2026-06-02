package types

// Asynq queue names. MUST stay in sync with the Queues weight map in
// router.NewAsynqServer — a task enqueued to a queue that the server does not
// list will never be consumed.
const (
	QueueCritical   = "critical"
	QueueDefault    = "default"
	QueueLow        = "low"
	// QueueMultimodal isolates high-volume, slow VLM image tasks (OCR + caption)
	// so a single large scanned PDF (hundreds–thousands of page images) cannot
	// saturate the shared worker pool and block user-facing document parsing in
	// the default queue.
	QueueMultimodal = "multimodal"
	// QueueGraph isolates high-volume, slow graph-extraction tasks (one per
	// chunk, LLM-backed, only when Neo4j is enabled). Same rationale as
	// QueueMultimodal: a large document must not flood the default queue.
	QueueGraph = "graph"
	// QueueQuestion isolates high-volume, slow question-generation tasks (one
	// per 20-chunk batch, LLM-backed). Keeps a large document's hundreds of
	// question batches from starving the lightweight tasks in the low queue
	// (summary, deletes, wiki ingest).
	QueueQuestion = "question"
)

const (
	TypeChunkExtract         = "chunk:extract"
	TypeDocumentProcess      = "document:process"       // 文档处理任务
	TypeFAQImport            = "faq:import"             // FAQ导入任务（包含dry run模式）
	TypeQuestionGeneration   = "question:generation"    // 问题生成任务
	TypeSummaryGeneration    = "summary:generation"     // 摘要生成任务
	TypeKBClone              = "kb:clone"               // 知识库复制任务
	TypeIndexDelete          = "index:delete"           // 索引删除任务
	TypeKBDelete             = "kb:delete"              // 知识库删除任务
	TypeKnowledgeListDelete  = "knowledge:list_delete"  // 批量删除知识任务
	TypeKnowledgeMove        = "knowledge:move"         // 知识移动任务
	TypeDataTableSummary     = "datatable:summary"      // 表格摘要任务
	TypeImageMultimodal      = "image:multimodal"       // 图片多模态处理任务（OCR + VLM Caption）
	TypeKnowledgePostProcess = "knowledge:post_process" // 知识后处理任务（统一调度）
	TypeManualProcess        = "manual:process"         // 手工知识更新任务（cleanup + 重新索引）
	TypeDataSourceSync       = "datasource:sync"        // 数据源同步任务
	TypeWikiIngest           = "wiki:ingest"            // Wiki 页面同步任务
)

// ExtractChunkPayload represents the extract chunk task payload
type ExtractChunkPayload struct {
	TracingContext
	TenantID uint64 `json:"tenant_id"`
	ChunkID  string `json:"chunk_id"`
	ModelID  string `json:"model_id"`
	// KnowledgeID + Attempt link the per-chunk extract back to the parent
	// parse attempt's postprocess stage so the worker can record a
	// postprocess.graph.chunk[i] subspan. 0 / "" means "skip span
	// recording" for legacy in-flight tasks.
	KnowledgeID string `json:"knowledge_id,omitempty"`
	Attempt     int    `json:"attempt,omitempty"`
	// ChunkIndex is the 0-based ordinal of this chunk inside the parent
	// knowledge's text-chunk set, used as the subspan name suffix
	// ("postprocess.graph.chunk[3]") so the timeline preserves order.
	ChunkIndex int `json:"chunk_index,omitempty"`
}

// DocumentProcessPayload represents the document process task payload
type DocumentProcessPayload struct {
	TracingContext
	RequestId                string   `json:"request_id"`
	TenantID                 uint64   `json:"tenant_id"`
	KnowledgeID              string   `json:"knowledge_id"`
	KnowledgeBaseID          string   `json:"knowledge_base_id"`
	FilePath                 string   `json:"file_path,omitempty"` // 文件路径（文件导入时使用）
	FileName                 string   `json:"file_name,omitempty"` // 文件名（文件导入时使用）
	FileType                 string   `json:"file_type,omitempty"` // 文件类型（文件导入时使用）
	URL                      string   `json:"url,omitempty"`       // URL（URL导入时使用）
	FileURL                  string   `json:"file_url,omitempty"`  // 文件资源链接（file_url导入时使用）
	Passages                 []string `json:"passages,omitempty"`  // 文本段落（文本导入时使用）
	EnableMultimodel         bool     `json:"enable_multimodel"`
	EnableQuestionGeneration bool     `json:"enable_question_generation"` // 是否启用问题生成
	QuestionCount            int      `json:"question_count,omitempty"`   // 每个chunk生成的问题数量
	Language                 string   `json:"language,omitempty"`         // Request locale for {{language}} in prompt templates
	// Attempt is the per-knowledge attempt number this task belongs to.
	// Set on enqueue (initial parse → attempt 1; reparse → max+1) so
	// every span recorded by this task lands on the right attempt
	// branch. Asynq retries within an attempt keep the same value so
	// retried spans overwrite the previous attempt's row rather than
	// fan out into a new attempt for every retry.
	Attempt int `json:"attempt,omitempty"`
}

// FAQImportPayload represents the FAQ import task payload (including dry run mode)
type FAQImportPayload struct {
	TracingContext
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
	TracingContext
	TenantID        uint64 `json:"tenant_id"`
	KnowledgeBaseID string `json:"knowledge_base_id"`
	KnowledgeID     string `json:"knowledge_id"`
	QuestionCount   int    `json:"question_count"`
	// Language is the request locale (e.g. zh-CN, en-US) when the task was enqueued, used for {{language}} / {{lang}} in templates.
	Language string `json:"language,omitempty"`
	// Attempt links this task to the parent parse attempt so the worker
	// can record a postprocess.question subspan under the right attempt's
	// postprocess stage. 0 means "skip span recording" (legacy in-flight
	// tasks queued before this field shipped, or callers without a
	// tracker).
	Attempt int `json:"attempt,omitempty"`
	// ChunkIDs switches the handler into batched fan-out mode: the task
	// generates questions for this ordered window of text chunks only.
	// Batching (rather than one task per chunk) keeps the task count
	// bounded for very large documents, while still giving each batch
	// independent retry / cancellation / tracing and letting the worker
	// do a single embedding BatchIndex per batch. Empty means the legacy
	// whole-knowledge mode (kept for in-flight tasks queued before fan-out
	// shipped), where the handler iterates all text chunks itself.
	// Following the ExtractChunkPayload precedent, we carry only chunk ids
	// (not their content) so the payload stays small and the worker reads
	// fresh content at run time.
	ChunkIDs []string `json:"chunk_ids,omitempty"`
	// ChunkID is the single-chunk variant of ChunkIDs, retained only so
	// tasks enqueued by an interim per-chunk build still run (treated as a
	// one-element batch). New enqueues use ChunkIDs.
	ChunkID string `json:"chunk_id,omitempty"`
	// BatchIndex is the 0-based ordinal of this batch inside the parent
	// knowledge's text-chunk set, used as the subspan name suffix
	// ("postprocess.question.batch[3]") so the timeline preserves order.
	BatchIndex int `json:"batch_index,omitempty"`
	// PrevChunkID / NextChunkID are the text chunks (by StartAt) just
	// outside this batch window, computed at enqueue time so the worker can
	// rebuild the same surrounding context the legacy whole-knowledge loop
	// used at the batch boundaries, without re-listing every chunk of the
	// knowledge. Empty when the batch is at a document boundary.
	PrevChunkID string `json:"prev_chunk_id,omitempty"`
	NextChunkID string `json:"next_chunk_id,omitempty"`
}

// SummaryGenerationPayload represents the summary generation task payload
type SummaryGenerationPayload struct {
	TracingContext
	TenantID        uint64 `json:"tenant_id"`
	KnowledgeBaseID string `json:"knowledge_base_id"`
	KnowledgeID     string `json:"knowledge_id"`
	Language        string `json:"language,omitempty"`
	// Attempt links this task to the parent parse attempt so the worker
	// can record a postprocess.summary subspan under the right attempt's
	// postprocess stage. See QuestionGenerationPayload.Attempt notes.
	Attempt int `json:"attempt,omitempty"`
}

// KBClonePayload represents the knowledge base clone task payload
type KBClonePayload struct {
	TracingContext
	TenantID uint64 `json:"tenant_id"`
	TaskID   string `json:"task_id"`
	SourceID string `json:"source_id"`
	TargetID string `json:"target_id"`
}

// IndexDeletePayload represents the index delete task payload
type IndexDeletePayload struct {
	TracingContext
	TenantID         uint64                  `json:"tenant_id"`
	KnowledgeBaseID  string                  `json:"knowledge_base_id"`
	EmbeddingModelID string                  `json:"embedding_model_id"`
	KBType           string                  `json:"kb_type"`
	ChunkIDs         []string                `json:"chunk_ids"`
	EffectiveEngines []RetrieverEngineParams `json:"effective_engines"`
	// VectorStoreID is the bound store snapshot taken at enqueue time so the
	// async worker can resolve the same store the KB was bound to.
	// nil means the KB had no binding — falls back to EffectiveEngines.
	VectorStoreID *string `json:"vector_store_id,omitempty"`
}

// KBDeletePayload represents the knowledge base delete task payload
type KBDeletePayload struct {
	TracingContext
	TenantID         uint64                  `json:"tenant_id"`
	KnowledgeBaseID  string                  `json:"knowledge_base_id"`
	EffectiveEngines []RetrieverEngineParams `json:"effective_engines"`
	// VectorStoreID is the bound store snapshot taken at enqueue time (before
	// soft-delete) so the async worker can resolve the right store. nil means
	// the KB had no binding — falls back to EffectiveEngines.
	VectorStoreID *string `json:"vector_store_id,omitempty"`
}

// KnowledgeListDeletePayload represents the batch knowledge delete task payload
type KnowledgeListDeletePayload struct {
	TracingContext
	TenantID     uint64   `json:"tenant_id"`
	KnowledgeIDs []string `json:"knowledge_ids"`
}

// KnowledgeMovePayload represents the knowledge move task payload
type KnowledgeMovePayload struct {
	TracingContext
	TenantID     uint64   `json:"tenant_id"`
	TaskID       string   `json:"task_id"`
	KnowledgeIDs []string `json:"knowledge_ids"`
	SourceKBID   string   `json:"source_kb_id"`
	TargetKBID   string   `json:"target_kb_id"`
	Mode         string   `json:"mode"` // "reuse_vectors" or "reparse"
}

// KnowledgeMoveProgress represents the progress of a knowledge move task
type KnowledgeMoveProgress struct {
	TaskID     string            `json:"task_id"`
	SourceKBID string            `json:"source_kb_id"`
	TargetKBID string            `json:"target_kb_id"`
	Status     KBCloneTaskStatus `json:"status"`
	Progress   int               `json:"progress"`   // 0-100
	Total      int               `json:"total"`      // 总知识数
	Processed  int               `json:"processed"`  // 已处理数
	Failed     int               `json:"failed"`     // 失败数
	Message    string            `json:"message"`    // 状态消息
	Error      string            `json:"error"`      // 错误信息
	CreatedAt  int64             `json:"created_at"` // 任务创建时间
	UpdatedAt  int64             `json:"updated_at"` // 最后更新时间
}

// ManualProcessPayload represents the manual knowledge processing task payload.
// Used for both create (publish) and update operations.
type ManualProcessPayload struct {
	TracingContext
	RequestId       string `json:"request_id"`
	TenantID        uint64 `json:"tenant_id"`
	KnowledgeID     string `json:"knowledge_id"`
	KnowledgeBaseID string `json:"knowledge_base_id"`
	Content         string `json:"content"`      // cleaned markdown content
	NeedCleanup     bool   `json:"need_cleanup"` // true for update, false for create
}

// ImageMultimodalPayload represents the image multimodal processing task payload.
type ImageMultimodalPayload struct {
	TracingContext
	TenantID        uint64 `json:"tenant_id"`
	KnowledgeID     string `json:"knowledge_id"`
	KnowledgeBaseID string `json:"knowledge_base_id"`
	ChunkID         string `json:"chunk_id"`         // parent text chunk
	ImageURL        string `json:"image_url"`        // provider:// URL (e.g. local://..., minio://...)
	ImageLocalPath  string `json:"image_local_path"` // deprecated: kept for backward compat with in-flight tasks
	EnableOCR       bool   `json:"enable_ocr"`
	EnableCaption   bool   `json:"enable_caption"`
	Language        string `json:"language,omitempty"`          // Request locale for {{language}} in prompt templates
	ImageSourceType string `json:"image_source_type,omitempty"` // Source type of the image (e.g., "scanned_pdf")
	// Attempt links this image task back to the parent ProcessDocument
	// attempt so the worker can record its image[i] subspan under the
	// same attempt's multimodal stage span.
	Attempt int `json:"attempt,omitempty"`
	// ImageIndex is the 0-based ordinal of this image inside the
	// parent's image set. Used as the subspan name suffix
	// ("multimodal.image[3]") so the timeline preserves order.
	ImageIndex int `json:"image_index,omitempty"`
}

// KnowledgePostProcessPayload represents the knowledge post process task payload.
type KnowledgePostProcessPayload struct {
	TracingContext
	TenantID        uint64 `json:"tenant_id"`
	KnowledgeID     string `json:"knowledge_id"`
	KnowledgeBaseID string `json:"knowledge_base_id"`
	Language        string `json:"language,omitempty"` // Request locale for {{language}} in prompt templates
	Attempt         int    `json:"attempt,omitempty"`
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
