package types

import "time"

type MessageChunkRef struct {
	TenantID        uint64    `json:"tenant_id" gorm:"primaryKey"`
	MessageID       string    `json:"message_id" gorm:"type:varchar(36);primaryKey"`
	ChunkID         string    `json:"chunk_id" gorm:"type:varchar(36);primaryKey"`
	KnowledgeBaseID string    `json:"knowledge_base_id" gorm:"type:varchar(36);index"`
	KnowledgeID     string    `json:"knowledge_id" gorm:"type:varchar(36)"`
	CreatedAt       time.Time `json:"created_at" gorm:"autoCreateTime"`
}

func (MessageChunkRef) TableName() string {
	return "message_chunk_refs"
}
