package types

import "time"

// ImageVectorJob records independently retryable image indexing. Credentials
// and image bytes never enter this record or the queue payload.
type ImageVectorJob struct {
	ID              string    `gorm:"primaryKey" json:"id"`
	TenantID        uint64    `json:"-"`
	KnowledgeBaseID string    `json:"-"`
	KnowledgeID     string    `json:"knowledge_id"`
	SourceChunkID   string    `json:"-"`
	ImageURL        string    `json:"-"`
	Fingerprint     string    `json:"-"`
	ChunkID         string    `json:"chunk_id"`
	Status          string    `json:"status"`
	Reason          string    `json:"reason,omitempty"`
	LeaseToken      string    `json:"-"`
	LeaseUntil      time.Time `json:"-"`
	CreatedAt       time.Time `json:"-"`
	UpdatedAt       time.Time `json:"updated_at"`
}

// ImageVectorCoverage counts images, not the caption/OCR/vector copies of them.
type ImageVectorCoverage struct {
	Enabled   bool  `json:"enabled"`
	Supported bool  `json:"supported"`
	Total     int64 `json:"total"`
	Completed int64 `json:"completed"`
	Pending   int64 `json:"pending"`
	Failed    int64 `json:"failed"`
	Skipped   int64 `json:"skipped"`
	Missing   int64 `json:"missing"`
}

// ImageVectorPayload addresses only a persisted, server-created job.
type ImageVectorPayload struct {
	TenantID        uint64 `json:"tenant_id"`
	KnowledgeBaseID string `json:"knowledge_base_id"`
	KnowledgeID     string `json:"knowledge_id"`
	JobID           string `json:"job_id"`
}
