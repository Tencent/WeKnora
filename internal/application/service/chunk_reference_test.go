package service

import (
	"context"
	"testing"

	"github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func newTestChunkService(t *testing.T) (interfaces.ChunkService, *gorm.DB) {
	t.Helper()

	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&types.Chunk{}))

	return NewChunkService(repository.NewChunkRepository(db), nil, nil, nil), db
}

func TestGetChunkByIDOnlyResolvesFabricatedKnowledgeChunkReference(t *testing.T) {
	svc, db := newTestChunkService(t)

	knowledgeID := uuid.NewString()
	want := &types.Chunk{
		ID:              uuid.NewString(),
		TenantID:        1,
		KnowledgeID:     knowledgeID,
		KnowledgeBaseID: uuid.NewString(),
		Content:         "chunk referenced by fallback id",
		ChunkIndex:      7,
		ChunkType:       types.ChunkTypeText,
		IsEnabled:       true,
	}
	require.NoError(t, db.Create(want).Error)

	got, err := svc.GetChunkByIDOnly(context.Background(), knowledgeID+"_chunk_7")
	require.NoError(t, err)
	require.Equal(t, want.ID, got.ID)
}

func TestGetChunkByIDOnlyRejectsMalformedFabricatedReference(t *testing.T) {
	svc, _ := newTestChunkService(t)

	_, err := svc.GetChunkByIDOnly(context.Background(), uuid.NewString()+"_chunk_not-a-number")
	require.ErrorIs(t, err, ErrChunkNotFound)
}
