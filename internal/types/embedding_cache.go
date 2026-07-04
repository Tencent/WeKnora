package types

import "time"

type EmbeddingCache struct {
	ContentHash string `gorm:"column:content_hash;primaryKey;type:varchar(64);not null"`
	ModelID     string `gorm:"column:model_id;primaryKey;type:varchar(128);not null"`
	Dimension   int32  `gorm:"column:dimension;primaryKey;type:int;not null"`
	VectorData  []byte `gorm:"column:vector_data;type:bytea;not null"`

	CreatedAt      time.Time `gorm:"column:created_at;type:timestamp;autoCreateTime"`
	LastAccessedAt time.Time `gorm:"column:last_accessed_at;type:timestamp;autoUpdateTime"`
}

func (EmbeddingCache) TableName() string {
	return "embedding_cache"
}
