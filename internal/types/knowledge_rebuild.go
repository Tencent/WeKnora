package types

import "time"

const (
	RebuildRunStatusPending          = "pending"
	RebuildRunStatusParsing          = "parsing"
	RebuildRunStatusParsed           = "parsed"
	RebuildRunStatusChunksClassified = "chunks_classified"
	RebuildRunStatusMultimodal       = "multimodal"
	RebuildRunStatusArtifactsReady   = "artifacts_ready"
	RebuildRunStatusFinalizing       = "finalizing"
	RebuildRunStatusCommitting       = "committing"
	RebuildRunStatusCompleted        = "completed"
	RebuildRunStatusFailed           = "failed"
	RebuildRunStatusCancelled        = "cancelled"
	RebuildRunStatusSuperseded       = "superseded"
)

// KnowledgeRebuildRun is the durable coordination record for one user-triggered
// reparse. The existing processing-span attempt remains the observability tree;
// this row carries cross-task state that workers need for correctness.
type KnowledgeRebuildRun struct {
	ID                   string     `json:"id" gorm:"type:varchar(36);primaryKey"`
	TenantID             uint64     `json:"tenant_id" gorm:"index:idx_rebuild_run_knowledge_status,priority:1;index:idx_rebuild_run_attempt,priority:1"`
	KnowledgeID          string     `json:"knowledge_id" gorm:"type:varchar(36);index:idx_rebuild_run_knowledge_status,priority:2;index:idx_rebuild_run_attempt,priority:2"`
	Attempt              int        `json:"attempt" gorm:"index:idx_rebuild_run_attempt,priority:3"`
	Status               string     `json:"status" gorm:"type:varchar(32);index:idx_rebuild_run_knowledge_status,priority:3"`
	OldParseStatus       string     `json:"old_parse_status" gorm:"type:varchar(32)"`
	OldEnableStatus      string     `json:"old_enable_status" gorm:"type:varchar(32)"`
	OldEmbeddingModelID  string     `json:"old_embedding_model_id" gorm:"type:varchar(64)"`
	OldChunkCount        int        `json:"old_chunk_count"`
	OldConfigFingerprint string     `json:"old_config_fingerprint" gorm:"type:varchar(64)"`
	NewConfigFingerprint string     `json:"new_config_fingerprint" gorm:"type:varchar(64)"`
	ParseCacheKey        string     `json:"parse_cache_key" gorm:"type:varchar(128)"`
	ParseCacheHit        bool       `json:"parse_cache_hit"`
	CandidateChunks      int        `json:"candidate_chunks"`
	UnchangedChunks      int        `json:"unchanged_chunks"`
	MetadataOnlyChunks   int        `json:"metadata_only_chunks"`
	ChangedNewChunks     int        `json:"changed_new_chunks"`
	StaleChunks          int        `json:"stale_chunks"`
	ChunkDiffReadyAt     *time.Time `json:"chunk_diff_ready_at"`
	ImagesTotal          int        `json:"images_total"`
	ImagesCompleted      int        `json:"images_completed"`
	ImagesFailed         int        `json:"images_failed"`
	OCRCacheHits         int        `json:"ocr_cache_hits"`
	CaptionCacheHits     int        `json:"caption_cache_hits"`
	ArtifactsTotal       int        `json:"artifacts_total"`
	ArtifactsCompleted   int        `json:"artifacts_completed"`
	ArtifactsFailed      int        `json:"artifacts_failed"`
	SummaryRequired      bool       `json:"summary_required"`
	WikiReduceRequired   bool       `json:"wiki_reduce_required"`
	StaleCleanupAt       *time.Time `json:"stale_cleanup_at"`
	WikiReduceEnqueuedAt *time.Time `json:"wiki_reduce_enqueued_at"`
	CommitCompletedAt    *time.Time `json:"commit_completed_at"`
	WikiCompletedAt      *time.Time `json:"wiki_completed_at"`
	ErrorMessage         string     `json:"error_message" gorm:"type:text"`
	StartedAt            time.Time  `json:"started_at"`
	ArtifactsReadyAt     *time.Time `json:"artifacts_ready_at"`
	CompletedAt          *time.Time `json:"completed_at"`
	UpdatedAt            time.Time  `json:"updated_at"`
}

func (KnowledgeRebuildRun) TableName() string { return "knowledge_rebuild_runs" }

// KnowledgeRebuildImageResult makes image completion idempotent across Asynq
// retries. A run/image-index pair is counted at most once in the parent run.
type KnowledgeRebuildImageResult struct {
	ID              string    `json:"id" gorm:"type:varchar(36);primaryKey"`
	RunID           string    `json:"run_id" gorm:"type:varchar(36);uniqueIndex:idx_rebuild_image_run_index,priority:1;index"`
	ImageIndex      int       `json:"image_index" gorm:"uniqueIndex:idx_rebuild_image_run_index,priority:2"`
	Status          string    `json:"status" gorm:"type:varchar(16)"`
	OCRCacheKey     string    `json:"ocr_cache_key" gorm:"type:varchar(128)"`
	CaptionCacheKey string    `json:"caption_cache_key" gorm:"type:varchar(128)"`
	OCRCacheHit     bool      `json:"ocr_cache_hit"`
	CaptionCacheHit bool      `json:"caption_cache_hit"`
	ErrorMessage    string    `json:"error_message" gorm:"type:text"`
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`
}

func (KnowledgeRebuildImageResult) TableName() string {
	return "knowledge_rebuild_image_results"
}

const (
	RebuildChunkClassUnchanged    = "unchanged"
	RebuildChunkClassMetadataOnly = "metadata_only"
	RebuildChunkClassChangedNew   = "changed_new"
	RebuildChunkClassStale        = "stale"
)

// KnowledgeRebuildChunkResult persists the chunk-level classification produced
// before enrichment. Later stages can consume these rows without recalculating
// the diff or touching unchanged chunks.
type KnowledgeRebuildChunkResult struct {
	ID                  string    `json:"id" gorm:"type:varchar(36);primaryKey"`
	RunID               string    `json:"run_id" gorm:"type:varchar(36);uniqueIndex:idx_rebuild_chunk_run_chunk,priority:1;index"`
	ChunkID             string    `json:"chunk_id" gorm:"type:varchar(36);uniqueIndex:idx_rebuild_chunk_run_chunk,priority:2;index"`
	ChunkType           ChunkType `json:"chunk_type" gorm:"type:varchar(20);index"`
	Classification      string    `json:"classification" gorm:"type:varchar(24);index"`
	ContentFingerprint  string    `json:"content_fingerprint" gorm:"type:varchar(64)"`
	MetadataFingerprint string    `json:"metadata_fingerprint" gorm:"type:varchar(64)"`
	CreatedAt           time.Time `json:"created_at"`
	UpdatedAt           time.Time `json:"updated_at"`
}

func (KnowledgeRebuildChunkResult) TableName() string {
	return "knowledge_rebuild_chunk_results"
}

const (
	RebuildArtifactStageSummary  = "summary"
	RebuildArtifactStageQuestion = "question"
	RebuildArtifactStageGraph    = "graph"
)

// KnowledgeRebuildArtifactResult makes downstream terminal completion
// idempotent. The unique run/stage/key tuple is also the authority for whether
// a finalizing counter slot has already been drained.
type KnowledgeRebuildArtifactResult struct {
	ID           string    `json:"id" gorm:"type:varchar(36);primaryKey"`
	RunID        string    `json:"run_id" gorm:"type:varchar(36);uniqueIndex:idx_rebuild_artifact_run_stage_key,priority:1;index"`
	Stage        string    `json:"stage" gorm:"type:varchar(24);uniqueIndex:idx_rebuild_artifact_run_stage_key,priority:2;index"`
	ArtifactKey  string    `json:"artifact_key" gorm:"type:varchar(128);uniqueIndex:idx_rebuild_artifact_run_stage_key,priority:3"`
	Status       string    `json:"status" gorm:"type:varchar(16);index"`
	ErrorMessage string    `json:"error_message" gorm:"type:text"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

func (KnowledgeRebuildArtifactResult) TableName() string {
	return "knowledge_rebuild_artifact_results"
}
