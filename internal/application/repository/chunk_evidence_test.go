package repository

import (
	"context"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

func TestListChunkEvidenceByIDOnlyLoadsMetadataAndExcludesSoftDeleted(t *testing.T) {
	db := setupChunkTestDB(t)
	repo := NewChunkRepository(db)
	ctx := context.Background()
	for i, id := range []string{"live", "shared", "disabled", "deleted"} {
		chunk := makeChunk("kb", "doc", types.ChunkTypeText)
		chunk.ID = id
		chunk.TenantID = uint64(i + 1)
		chunk.Content = "body that validation must not load"
		chunk.ChunkIndex = 42
		chunk.ParentChunkID = "parent"
		require.NoError(t, repo.CreateChunks(ctx, []*types.Chunk{chunk}))
	}
	require.NoError(t, db.Model(&types.Chunk{}).Where("id = ?", "disabled").
		Update("is_enabled", false).Error)
	require.NoError(t, db.Delete(&types.Chunk{ID: "deleted"}).Error)

	rows, err := repo.ListChunkEvidenceByIDOnly(ctx, []string{"live", "shared", "disabled", "deleted", "missing"})
	require.NoError(t, err)
	require.Len(t, rows, 3)
	enabled := make(map[string]bool)
	for _, row := range rows {
		enabled[row.ID] = row.IsEnabled
		require.Equal(t, "kb", row.KnowledgeBaseID)
		require.Equal(t, "doc", row.KnowledgeID)
		require.Empty(t, row.Content)
		require.Zero(t, row.TenantID)
		require.Zero(t, row.ChunkIndex)
		require.Empty(t, row.ParentChunkID)
	}
	require.Equal(t, map[string]bool{"live": true, "shared": true, "disabled": false}, enabled)

	full, err := repo.ListChunksByIDOnly(ctx, []string{"live"})
	require.NoError(t, err)
	require.Len(t, full, 1)
	require.Equal(t, "body that validation must not load", full[0].Content,
		"the existing content lookup must keep its full-row contract")
}

func TestListChunkEvidenceByIDOnlyEmptyIDsDoNotQuery(t *testing.T) {
	repo := NewChunkRepository(nil)
	rows, err := repo.ListChunkEvidenceByIDOnly(context.Background(), nil)
	require.NoError(t, err)
	require.Empty(t, rows)
}
