package types

import (
	"time"
)

type WikiGraphCache struct {
	ContentHash    string    `gorm:"column:content_hash"`
	ChatModelID    string    `gorm:"column:chat_model_id"`
	PromptVersion  string    `gorm:"column:prompt_version"`
	EntitiesData   string    `gorm:"column:entities_data"`
	CreatedAt      time.Time `gorm:"column:created_at"`
	LastAccessedAt time.Time `gorm:"column:last_accessed_at"`
}

func (WikiGraphCache) TableName() string {
	return "graph_chunk_cache"
}
