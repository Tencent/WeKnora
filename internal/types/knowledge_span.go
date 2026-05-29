package types

import "time"

// Span kinds — kept narrow because every kind has dedicated rendering on
// the frontend timeline:
//
//   - SpanKindRoot     — the per-(knowledge, attempt) trace root. Always
//     the parent_span_id ancestor of every other span
//     in that attempt. UI uses it for total elapsed.
//   - SpanKindStage    — one of the 5 canonical stages (DocReader, etc.).
//     UI renders these as the timeline segments.
//   - SpanKindSubSpan  — anything inside a stage (e.g. multimodal.image[i]).
//     UI shows them as collapsible children.
//   - SpanKindGeneration — a SubSpan that wraps an LLM/VLM call. Same UI
//     treatment as SubSpan but tagged so we can stitch
//     to the matching Langfuse generation.
const (
	SpanKindRoot       = "root"
	SpanKindStage      = "stage"
	SpanKindSubSpan    = "subspan"
	SpanKindGeneration = "generation"
)

// Span statuses. We deliberately distinguish "failed" (this span itself
// errored) from "cancelled" (an upstream span failed and we abandoned this
// one without running it) so the UI can render the cause differently —
// "you broke X, so we never ran Y" vs. "Y itself broke".
const (
	SpanStatusPending   = "pending"
	SpanStatusRunning   = "running"
	SpanStatusDone      = "done"
	SpanStatusFailed    = "failed"
	SpanStatusSkipped   = "skipped"   // intentionally not run (e.g. multimodal on a text-only doc)
	SpanStatusCancelled = "cancelled" // not run because an upstream span failed
)

// Stage names — the closed set the UI builds its 5-segment timeline from.
// Adding a stage requires a coordinated frontend release. SubSpan names
// are free-form (e.g. "multimodal.image[0]") and don't go through this
// list.
const (
	StageDocReader   = "docreader"
	StageChunking    = "chunking"
	StageEmbedding   = "embedding"
	StageMultimodal  = "multimodal"
	StagePostProcess = "postprocess"
)

// AllStages is the canonical, ordered stage list. Used by the API layer
// to synthesize "pending" placeholders so the timeline always renders five
// segments even before parsing starts.
var AllStages = []string{
	StageDocReader,
	StageChunking,
	StageEmbedding,
	StageMultimodal,
	StagePostProcess,
}

// StageDependencies declares the DAG between stages. Used by the tracker
// to cascade-cancel dependents when a stage fails — a Chunking failure
// silently turns Embedding/Multimodal/PostProcess into "cancelled" so the
// timeline shows a clear blast radius instead of three pending spinners.
//
// Important: Multimodal does NOT depend on Embedding. They share Chunking
// as their upstream and are otherwise independent (Multimodal kicks off
// regardless of vector indexing config). PostProcess joins both before
// running its handlers.
var StageDependencies = map[string][]string{
	StageDocReader:   nil,
	StageChunking:    {StageDocReader},
	StageEmbedding:   {StageChunking},
	StageMultimodal:  {StageChunking},
	StagePostProcess: {StageEmbedding, StageMultimodal},
}

// KnowledgeProcessingSpan is one row in knowledge_processing_spans.
//
// Field tags pull double duty: GORM (storage) and JSON (API). ErrorDetail
// is excluded by default — handlers must opt in for admin views, matching
// how the dead-letter middleware already protects raw stack traces.
type KnowledgeProcessingSpan struct {
	ID           int64      `gorm:"primaryKey;column:id"             json:"-"`
	KnowledgeID  string     `gorm:"column:knowledge_id"              json:"knowledge_id"`
	Attempt      int        `gorm:"column:attempt"                   json:"attempt"`
	SpanID       string     `gorm:"column:span_id;size:64"           json:"span_id"`
	ParentSpanID string     `gorm:"column:parent_span_id;size:64"    json:"parent_span_id,omitempty"`
	Name         string     `gorm:"column:name;size:64"              json:"name"`
	Kind         string     `gorm:"column:kind;size:16"              json:"kind"`
	Status       string     `gorm:"column:status;size:16"            json:"status"`
	Input        JSONMap    `gorm:"column:input;type:jsonb"          json:"input,omitempty"`
	Output       JSONMap    `gorm:"column:output;type:jsonb"         json:"output,omitempty"`
	Metadata     JSONMap    `gorm:"column:metadata;type:jsonb"       json:"metadata,omitempty"`
	ErrorCode    string     `gorm:"column:error_code;size:64"        json:"error_code,omitempty"`
	ErrorMessage string     `gorm:"column:error_message;type:text"   json:"error_message,omitempty"`
	ErrorDetail  string     `gorm:"column:error_detail;type:text"    json:"-"`
	StartedAt    *time.Time `gorm:"column:started_at"                json:"started_at,omitempty"`
	FinishedAt   *time.Time `gorm:"column:finished_at"               json:"finished_at,omitempty"`
	DurationMs   int64      `gorm:"column:duration_ms"               json:"duration_ms,omitempty"`
	CreatedAt    time.Time  `gorm:"column:created_at;autoCreateTime" json:"created_at"`
	UpdatedAt    time.Time  `gorm:"column:updated_at;autoUpdateTime" json:"updated_at"`
}

// TableName pins the table because GORM's default pluralization
// ("knowledge_processing_spans") happens to match — explicit beats
// implicit.
func (KnowledgeProcessingSpan) TableName() string {
	return "knowledge_processing_spans"
}

// SpanTreeNode is the API-only tree projection. The repo returns flat
// rows; the handler/tracker assembles SpanTreeNode for the response.
type SpanTreeNode struct {
	KnowledgeProcessingSpan
	Children []*SpanTreeNode `json:"children,omitempty"`
}
