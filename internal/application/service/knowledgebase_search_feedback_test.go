package service

import (
	"context"
	"testing"

	"github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestApplyChunkRecallWeightsAppliesOnce(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&types.Chunk{}))

	chunkID := uuid.NewString()
	require.NoError(t, db.Create(&types.Chunk{
		ID:              chunkID,
		TenantID:        1,
		KnowledgeBaseID: "kb-1",
		KnowledgeID:     "knowledge-1",
		Content:         "chunk content",
		ChunkType:       types.ChunkTypeText,
		IsEnabled:       true,
		Status:          int(types.ChunkStatusIndexed),
		RecallWeight:    1.2,
	}).Error)

	svc := &knowledgeBaseService{chunkRepo: repository.NewChunkRepository(db)}
	results := []*types.IndexWithScore{
		{ChunkID: chunkID, Score: 1.0},
	}

	weighted := svc.applyChunkRecallWeights(context.Background(), results)
	require.Len(t, weighted, 1)
	assert.InDelta(t, 1.2, weighted[0].Score, 0.0001)
}
