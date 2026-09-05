package types

import "time"

// EmbedProgress records a committed chunk vector for resume support. Rows are
// written after the corresponding chunk has been persisted to the retrieval
// stores; document-processing retries skip chunks listed here instead of
// re-embedding the whole document from scratch.
type EmbedProgress struct {
	KnowledgeID string    `gorm:"column:knowledge_id;primaryKey"`
	ChunkID     string    `gorm:"column:chunk_id;primaryKey"`
	CreatedAt   time.Time `gorm:"column:created_at"`
}

// TableName returns the progress table name.
func (EmbedProgress) TableName() string { return "knowledge_embed_progress" }
