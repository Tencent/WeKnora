package types

import (
	"time"
)

type WikiDocMapCache struct {
	ContentHash    string    `gorm:"column:content_hash"`
	Granularity    string    `gorm:"column:granularity"`
	ChatModelID    string    `gorm:"column:chat_model_id"`
	PromptVersion  string    `gorm:"column:prompt_version"`
	MappedData     string    `gorm:"column:mapped_data"`
	CreatedAt      time.Time `gorm:"column:created_at"`
	LastAccessedAt time.Time `gorm:"column:last_accessed_at"`
}

func (WikiDocMapCache) TableName() string {
	return "wiki_doc_map_cache"
}
