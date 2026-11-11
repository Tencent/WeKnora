package service

import (
	"testing"

	"github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

func TestRerankEnrichmentDropsChangedChunkRevision(t *testing.T) {
	svc, db := newKnowledgeSharedAccessService(t, nil)
	require.NoError(t, db.AutoMigrate(&types.Chunk{}))
	search := &knowledgeBaseService{kgRepo: svc.repo, chunkRepo: repository.NewChunkRepository(db)}
	ctx := newSharedAccessContext()
	seedKnowledge(t, db, &types.Knowledge{
		ID: "doc", TenantID: 1, KnowledgeBaseID: "kb", Type: "file", ParseStatus: types.ParseStatusCompleted,
	})

	var pool []*types.IndexWithScore
	for _, id := range []string{"changed", "unchanged"} {
		require.NoError(t, db.Create(&types.Chunk{
			ID: id, TenantID: 1, KnowledgeBaseID: "kb", KnowledgeID: "doc",
			Content: id + " original", ChunkType: types.ChunkTypeText, IsEnabled: true,
			ContentRevision: 1, IndexStatus: "ready",
		}).Error)
		pool = append(pool, &types.IndexWithScore{ChunkID: id, KnowledgeID: "doc"})
	}
	require.NoError(t, db.Create(&types.Chunk{
		ID: "neighbor", TenantID: 1, KnowledgeBaseID: "kb", KnowledgeID: "doc",
		Content: "context for the edited chunk", ChunkType: types.ChunkTypeText, IsEnabled: true,
	}).Error)
	require.NoError(t, db.Model(&types.Chunk{}).Where("id = ?", "changed").
		Update("next_chunk_id", "neighbor").Error)
	candidates, err := search.processSearchResults(ctx, pool, len(pool), true)
	require.NoError(t, err)
	require.Len(t, candidates, 2)
	require.Equal(t, "changed original", candidates[0].Content)
	candidates[1].Score = 0.91

	// Complete an edit while the reranker is scoring the original passages.
	require.NoError(t, db.Model(&types.Chunk{}).Where("id = ?", "changed").
		Updates(map[string]any{"content": "edited body", "content_revision": 2}).Error)
	results, err := search.enrichRerankedResults(ctx, pool, candidates)
	require.NoError(t, err)
	require.Len(t, results, 1)
	require.Equal(t, "unchanged", results[0].ID)
	require.Equal(t, 0.91, results[0].Score)
}
