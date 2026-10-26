package types

import "time"

// ReparseBatch is a durable, tenant-scoped snapshot of a reparse selection.
// It records membership, while progress is always derived from current
// knowledge rows rather than from a historical worker attempt.
type ReparseBatch struct {
	ID              string    `json:"id" gorm:"primaryKey;type:varchar(36)"`
	TenantID        uint64    `json:"-" gorm:"index;not null"`
	KnowledgeBaseID string    `json:"kb_id" gorm:"index;not null"`
	Mode            string    `json:"mode" gorm:"type:varchar(24);not null"`
	Name            string    `json:"name,omitempty"`
	ManifestSHA256  string    `json:"-" gorm:"type:char(64);index"`
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`
}

// TableName identifies the durable tracking table.
func (ReparseBatch) TableName() string { return "reparse_batches" }

// ReparseBatchMember freezes one selected document id. Submission state is
// intentionally separate from the document's current parse state.
type ReparseBatchMember struct {
	BatchID         string    `json:"-" gorm:"primaryKey;type:varchar(36)"`
	KnowledgeID     string    `json:"knowledge_id" gorm:"primaryKey;type:varchar(36)"`
	SubmissionID    string    `json:"-" gorm:"type:varchar(36);index"`
	SubmissionState string    `json:"-" gorm:"type:varchar(24);not null;default:waiting_start"`
	SubmissionError string    `json:"submission_error,omitempty" gorm:"type:text"`
	CreatedAt       time.Time `json:"-"`
	UpdatedAt       time.Time `json:"-"`
}

// TableName identifies the durable tracking table.
func (ReparseBatchMember) TableName() string { return "reparse_batch_members" }

// ReparseBatchSubmission is the durable request receipt used to make worker
// handoff deterministic and to keep ambiguous handoffs visible for review.
type ReparseBatchSubmission struct {
	ID             string    `json:"id" gorm:"primaryKey;type:varchar(36)"`
	BatchID        string    `json:"batch_id" gorm:"index;not null"`
	TenantID       uint64    `json:"-" gorm:"index;not null"`
	IdempotencyKey string    `json:"-" gorm:"type:varchar(255);index"`
	RequestSHA256  string    `json:"-" gorm:"type:char(64);not null"`
	State          string    `json:"-" gorm:"type:varchar(24);not null"`
	TaskID         string    `json:"-" gorm:"type:varchar(128)"`
	CreatedAt      time.Time `json:"-"`
	UpdatedAt      time.Time `json:"-"`
}

// TableName identifies the durable tracking table.
func (ReparseBatchSubmission) TableName() string { return "reparse_batch_submissions" }

// ReparseBatchCounts partitions frozen members by their current dispatch/document state.
type ReparseBatchCounts struct {
	WaitingStart     int64 `json:"waiting_start"`
	Queued           int64 `json:"queued"`
	Processing       int64 `json:"processing"`
	Finalizing       int64 `json:"finalizing"`
	Completed        int64 `json:"completed"`
	Failed           int64 `json:"failed"`
	Cancelled        int64 `json:"cancelled"`
	Missing          int64 `json:"missing"`
	SubmissionFailed int64 `json:"submission_failed"`
	Unknown          int64 `json:"unknown"`
}

// ReparseBatchSummaryCounts reports current summary state independently from parsing.
type ReparseBatchSummaryCounts struct {
	None       int64 `json:"none"`
	Pending    int64 `json:"pending"`
	Processing int64 `json:"processing"`
	Completed  int64 `json:"completed"`
	Failed     int64 `json:"failed"`
	Unknown    int64 `json:"unknown"`
}

// ReparseBatchView combines frozen metadata with current-state aggregate counts.
type ReparseBatchView struct {
	*ReparseBatch
	Total         int64                     `json:"total"`
	Counts        ReparseBatchCounts        `json:"counts"`
	SummaryCounts ReparseBatchSummaryCounts `json:"summary_counts"`
}

// ReparseBatchMemberView exposes safe current state without raw task payloads.
type ReparseBatchMemberView struct {
	KnowledgeID     string `json:"knowledge_id"`
	Title           string `json:"title,omitempty"`
	State           string `json:"state"`
	ParseStatus     string `json:"parse_status,omitempty"`
	SummaryStatus   string `json:"summary_status,omitempty"`
	SubmissionError string `json:"submission_error,omitempty"`
}
