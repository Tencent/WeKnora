package types

import (
	"time"
)

type WikiGraphCache struct {
	ContentHash    string    `gorm:"column:content_hash;primaryKey"`
	ChatModelID    string    `gorm:"column:chat_model_id;primaryKey"`
	PromptVersion  string    `gorm:"column:prompt_version;primaryKey"`
	EntitiesData   string    `gorm:"column:entities_data"`
	CreatedAt      time.Time `gorm:"column:created_at"`
	LastAccessedAt time.Time `gorm:"column:last_accessed_at"`
}

func (WikiGraphCache) TableName() string {
	return "graph_chunk_cache"
}
