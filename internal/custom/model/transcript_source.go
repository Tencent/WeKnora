package model

import "time"

// VideoTranscriptSource records the Wiki-only source document for one
// immutable video transcript generation. The document body lives in WeKnora;
// this table stores only its identity and processing checkpoint.
type VideoTranscriptSource struct {
	ID                   string    `gorm:"type:varchar(36);primaryKey" json:"id"`
	VideoID              string    `gorm:"type:varchar(36);not null;index:idx_video_transcript_source_video_generation,priority:1;uniqueIndex:uq_video_transcript_sources_video_generation,priority:1" json:"video_id"`
	TranscriptGeneration string    `gorm:"type:varchar(64);not null;index:idx_video_transcript_source_video_generation,priority:2;uniqueIndex:uq_video_transcript_sources_video_generation,priority:2" json:"transcript_generation"`
	KnowledgeID          string    `gorm:"type:varchar(64);not null;default:''" json:"knowledge_id"`
	ContentHash          string    `gorm:"type:varchar(64);not null" json:"content_hash"`
	Status               string    `gorm:"type:varchar(32);not null;index" json:"status"`
	ErrorMessage         string    `gorm:"type:text" json:"error_message,omitempty"`
	CreatedAt            time.Time `json:"created_at"`
	UpdatedAt            time.Time `json:"updated_at"`
}
