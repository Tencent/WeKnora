package types

import "time"

type ProcessingLogicalStage string

const (
	ProcessingStageDocReader   ProcessingLogicalStage = "docreader"
	ProcessingStageChunking    ProcessingLogicalStage = "chunking"
	ProcessingStageEmbedding   ProcessingLogicalStage = "embedding"
	ProcessingStageMultimodal  ProcessingLogicalStage = "multimodal"
	ProcessingStagePostProcess ProcessingLogicalStage = "postprocess"
	ProcessingStageSummaryGen  ProcessingLogicalStage = "summary"
	ProcessingStageQuestion    ProcessingLogicalStage = "question"
	ProcessingStageGraph       ProcessingLogicalStage = "graph"
	ProcessingStageWiki        ProcessingLogicalStage = "wiki"
)

var ProcessingLogicalStages = []ProcessingLogicalStage{
	ProcessingStageDocReader,
	ProcessingStageChunking,
	ProcessingStageEmbedding,
	ProcessingStageMultimodal,
	ProcessingStagePostProcess,
	ProcessingStageSummaryGen,
	ProcessingStageQuestion,
	ProcessingStageGraph,
	ProcessingStageWiki,
}

type ProcessingStageState string

const (
	ProcessingStateNotReached     ProcessingStageState = "not_reached"
	ProcessingStateQueued         ProcessingStageState = "queued"
	ProcessingStateRunning        ProcessingStageState = "running"
	ProcessingStateRetrying       ProcessingStageState = "retrying"
	ProcessingStateDone           ProcessingStageState = "done"
	ProcessingStateDoneWithErrors ProcessingStageState = "done_with_errors"
	ProcessingStateFailed         ProcessingStageState = "failed"
	ProcessingStateSkipped        ProcessingStageState = "skipped"
	ProcessingStateCancelled      ProcessingStageState = "cancelled"
)

const (
	ProcessingExecutorModeAsynq = "asynq"
	ProcessingExecutorModeLite  = "lite_inline"

	ProcessingQueueSnapshotOK            = "ok"
	ProcessingQueueSnapshotPartial       = "partial"
	ProcessingQueueSnapshotDegraded      = "degraded"
	ProcessingQueueSnapshotNotApplicable = "not_applicable"
)

type ProcessingDashboardFilter struct {
	TenantID           uint64
	UserID             string
	AccessibleScopes   []KnowledgeSearchScope
	KnowledgeBaseID    string
	Keyword            string
	ActivePreviewLimit int
}

type ProcessingDashboardSource struct {
	ExecutorMode    string   `json:"executor_mode"`
	QueueSnapshot   string   `json:"queue_snapshot"`
	TruncatedQueues []string `json:"truncated_queues,omitempty"`
	Message         string   `json:"message,omitempty"`
}

type ProcessingDashboardFilters struct {
	KnowledgeBaseID string `json:"knowledge_base_id"`
	Keyword         string `json:"keyword"`
}

type ProcessingStageGroup struct {
	Key    string                   `json:"key"`
	Name   string                   `json:"name"`
	Stages []ProcessingLogicalStage `json:"stages"`
}

type ProcessingStageProgress struct {
	Completed int    `json:"completed"`
	Total     int    `json:"total"`
	Failed    int    `json:"failed"`
	Unit      string `json:"unit"`
	Reliable  bool   `json:"reliable"`
}

type ProcessingStageItem struct {
	KnowledgeID       string                   `json:"knowledge_id"`
	KnowledgeBaseID   string                   `json:"knowledge_base_id"`
	KnowledgeBaseName string                   `json:"knowledge_base_name"`
	Title             string                   `json:"title"`
	Attempt           int                      `json:"attempt"`
	Stage             ProcessingLogicalStage   `json:"stage"`
	State             ProcessingStageState     `json:"state"`
	Progress          *ProcessingStageProgress `json:"progress,omitempty"`
	Phase             string                   `json:"phase,omitempty"`
	StartedAt         *time.Time               `json:"started_at,omitempty"`
	QueuedAt          *time.Time               `json:"queued_at,omitempty"`
	NextRetryAt       *time.Time               `json:"next_retry_at,omitempty"`
	LastProgressAt    *time.Time               `json:"last_progress_at,omitempty"`
	FinishedAt        *time.Time               `json:"finished_at,omitempty"`
	ElapsedMs         int64                    `json:"elapsed_ms,omitempty"`
	DurationMs        int64                    `json:"duration_ms,omitempty"`
	FailedChildren    int                      `json:"failed_children,omitempty"`
	ErrorCode         string                   `json:"error_code,omitempty"`
	ErrorMessage      string                   `json:"error_message,omitempty"`
	SkipReason        string                   `json:"skip_reason,omitempty"`
}

type ProcessingStageSummary struct {
	Key                ProcessingLogicalStage `json:"key"`
	Group              string                 `json:"group"`
	Order              int                    `json:"order"`
	Title              string                 `json:"title"`
	Description        string                 `json:"description"`
	RunningCount       int                    `json:"running_count"`
	QueuedCount        int                    `json:"queued_count"`
	RetryingCount      int                    `json:"retrying_count"`
	RetryingObservable bool                   `json:"retrying_observable"`
	CompletionReliable bool                   `json:"completion_reliable"`
	CountsReliable     bool                   `json:"counts_reliable"`
	RunningItems       []ProcessingStageItem  `json:"running_items"`
}

type ProcessingDashboardResponse struct {
	GeneratedAt time.Time                  `json:"generated_at"`
	Source      ProcessingDashboardSource  `json:"source"`
	Filters     ProcessingDashboardFilters `json:"filters"`
	Groups      []ProcessingStageGroup     `json:"groups"`
	Stages      []ProcessingStageSummary   `json:"stages"`
}

type ProcessingStageItemsResponse struct {
	GeneratedAt time.Time                 `json:"generated_at"`
	Source      ProcessingDashboardSource `json:"source"`
	Stage       ProcessingLogicalStage    `json:"stage"`
	State       ProcessingStageState      `json:"state"`
	Items       []ProcessingStageItem     `json:"items"`
	NextCursor  string                    `json:"next_cursor"`
	Total       int                       `json:"total"`
}

type ProcessingKnowledgeDetailResponse struct {
	GeneratedAt          time.Time                 `json:"generated_at"`
	Source               ProcessingDashboardSource `json:"source"`
	Knowledge            *Knowledge                `json:"knowledge"`
	CurrentAttempt       int                       `json:"current_attempt"`
	ParseStatus          string                    `json:"parse_status"`
	PendingSubtasksCount int                       `json:"pending_subtasks_count"`
	Stages               []ProcessingStageItem     `json:"stages"`
	RawTrace             []*SpanTreeNode           `json:"raw_trace"`
}

type ProcessingQueueChildAggregate struct {
	ChildKey       string
	PendingCount   int
	ScheduledCount int
	RetryCount     int
	ActiveCount    int
	QueuedAt       *time.Time
	ActiveAt       *time.Time
	NextRetryAt    *time.Time
	LastErrorAt    *time.Time
	LastError      string
}

type ProcessingQueueAggregate struct {
	KnowledgeID        string
	Attempt            int
	Stage              ProcessingLogicalStage
	PendingCount       int
	ScheduledCount     int
	RetryCount         int
	ActiveCount        int
	EarliestEnqueuedAt *time.Time
	EarliestActiveAt   *time.Time
	NextRetryAt        *time.Time
	LastErrorAt        *time.Time
	LastError          string
	Children           map[string]*ProcessingQueueChildAggregate
}

type ProcessingQueueSnapshot struct {
	ExecutorMode    string
	Status          string
	GeneratedAt     time.Time
	TruncatedQueues []string
	Aggregates      map[string]*ProcessingQueueAggregate
	Message         string
}

type ProcessingKnowledgeRow struct {
	ID                   string
	TenantID             uint64
	KnowledgeBaseID      string
	KnowledgeBaseName    string
	Title                string
	FileName             string
	ParseStatus          string
	PendingSubtasksCount int
	UpdatedAt            time.Time
	CreatedAt            time.Time
	ErrorMessage         string
}

type ProcessingAttemptRow struct {
	KnowledgeID string
	Attempt     int
}

type ProcessingWikiPendingRow struct {
	KnowledgeID string
	QueuedAt    time.Time
	FailCount   int
	CursorID    int64
}

type ProcessingFanoutSpanBucket struct {
	KnowledgeID string
	Attempt     int
	Stage       ProcessingLogicalStage
	Status      string
	ErrorCode   string
	Count       int
	UpdatedAt   time.Time
}

type ProcessingFanoutStageAggregate struct {
	KnowledgeID        string
	Attempt            int
	Stage              ProcessingLogicalStage
	TerminalDoneCount  int
	TerminalTotalCount int
	LatestUpdatedAt    *time.Time
	Details            map[string]KnowledgeProcessingSpan
	CompletionReliable bool
}
