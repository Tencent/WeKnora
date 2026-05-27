package types

import "time"

// ProcessingStage names — kept as a closed set on the Go side so the
// frontend timeline can rely on exactly five segments. Adding a new stage
// requires a coordinated frontend release; rename in this file is breaking.
const (
	ProcessingStageDocReader   = "docreader"
	ProcessingStageChunking    = "chunking"
	ProcessingStageEmbedding   = "embedding"
	ProcessingStageMultimodal  = "multimodal"
	ProcessingStagePostProcess = "postprocess"
)

// AllProcessingStages is the canonical, ordered list. Used by the API
// to fill in pending entries when no row exists yet for a stage so the
// timeline always renders five segments even before parsing starts.
var AllProcessingStages = []string{
	ProcessingStageDocReader,
	ProcessingStageChunking,
	ProcessingStageEmbedding,
	ProcessingStageMultimodal,
	ProcessingStagePostProcess,
}

// ProcessingStageStatus values. Distinct from Knowledge.parse_status —
// each stage row tracks its own state independently of the parent.
const (
	ProcessingStagePending = "pending" // not started yet
	ProcessingStageRunning = "running" // currently executing
	ProcessingStageDone    = "done"
	ProcessingStageFailed  = "failed"
	ProcessingStageSkipped = "skipped" // e.g. multimodal skipped for image-free docs
)

// KnowledgeProcessingStage is one row in knowledge_processing_stages.
//
// We intentionally keep it as a flat struct with both Go and JSON tags so
// the same type round-trips through GORM (DB layer) and the API response.
// Sensitive ErrorDetail (full stack) is excluded from the default JSON
// output via "-" — handlers must opt in explicitly for admin views.
type KnowledgeProcessingStage struct {
	ID           int64      `gorm:"primaryKey;column:id"               json:"-"`
	KnowledgeID  string     `gorm:"column:knowledge_id;index"          json:"knowledge_id"`
	Stage        string     `gorm:"column:stage;size:32"               json:"stage"`
	Status       string     `gorm:"column:status;size:16"              json:"status"`
	StartedAt    *time.Time `gorm:"column:started_at"                  json:"started_at,omitempty"`
	FinishedAt   *time.Time `gorm:"column:finished_at"                 json:"finished_at,omitempty"`
	DurationMs   int64      `gorm:"column:duration_ms"                 json:"duration_ms,omitempty"`
	ErrorCode    string     `gorm:"column:error_code;size:64"          json:"error_code,omitempty"`
	ErrorMessage string     `gorm:"column:error_message;type:text"     json:"error_message,omitempty"`
	ErrorDetail  string     `gorm:"column:error_detail;type:text"      json:"-"`
	Attempt      int        `gorm:"column:attempt"                     json:"attempt"`
	CreatedAt    time.Time  `gorm:"column:created_at;autoCreateTime"   json:"created_at"`
	UpdatedAt    time.Time  `gorm:"column:updated_at;autoUpdateTime"   json:"updated_at"`
}

// TableName pins the GORM table since the natural pluralization
// ("knowledge_processing_stages") happens to match what the migration
// creates — but explicit beats implicit here.
func (KnowledgeProcessingStage) TableName() string {
	return "knowledge_processing_stages"
}
