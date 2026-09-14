package repository

import (
	"context"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

func TestTagFieldUpdatesReturnAllAffectedChunks(t *testing.T) {
	for _, flagsOnly := range []bool{false, true} {
		name := "tag only"
		if flagsOnly {
			name = "flags only"
		}
		t.Run(name, func(t *testing.T) {
			db := setupChunkTestDB(t)
			repo := NewChunkRepository(db)
			for _, chunk := range []*types.Chunk{
				{ID: "selected", TenantID: 7, KnowledgeBaseID: "kb", ChunkType: types.ChunkTypeFAQ, TagID: "tag"},
				{ID: "excluded", TenantID: 7, KnowledgeBaseID: "kb", ChunkType: types.ChunkTypeFAQ, TagID: "tag"},
				{ID: "other-kb", TenantID: 7, KnowledgeBaseID: "other", ChunkType: types.ChunkTypeFAQ, TagID: "tag"},
				{ID: "other-tenant", TenantID: 8, KnowledgeBaseID: "kb", ChunkType: types.ChunkTypeFAQ, TagID: "tag"},
				{ID: "deleted", TenantID: 7, KnowledgeBaseID: "kb", ChunkType: types.ChunkTypeFAQ, TagID: "tag"},
			} {
				require.NoError(t, db.Create(chunk).Error)
			}
			require.NoError(t, db.Delete(&types.Chunk{ID: "deleted"}).Error)
			next := "next"
			var tag *string
			var flags types.ChunkFlags
			if flagsOnly {
				flags = types.ChunkFlagRecommended
			} else {
				tag = &next
			}
			ids, err := repo.UpdateChunkFieldsByTagID(
				context.Background(),
				7,
				"kb",
				"tag",
				nil,
				flags,
				0,
				tag,
				[]string{"excluded"},
			)
			require.NoError(t, err)
			require.Equal(t, []string{"selected"}, ids)
			selected, err := repo.GetChunkByID(context.Background(), 7, "selected")
			require.NoError(t, err)
			if flagsOnly {
				require.True(t, selected.Flags.HasFlag(types.ChunkFlagRecommended))
			} else {
				require.Equal(t, "next", selected.TagID)
			}
		})
	}
}
