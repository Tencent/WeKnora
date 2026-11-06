package service

import (
	"testing"

	"github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

func TestSearchResultsExcludeCancelledParse(t *testing.T) {
	knowledge, db := newKnowledgeSharedAccessService(t, nil)
	require.NoError(t, db.AutoMigrate(&types.Chunk{}))
	for _, doc := range []struct {
		id     string
		status string
	}{
		{"cancelled", types.ParseStatusCancelled},
		{"completed", types.ParseStatusCompleted},
	} {
		seedKnowledge(t, db, &types.Knowledge{
			ID: doc.id, TenantID: 1, KnowledgeBaseID: "kb",
			Type: "file", ParseStatus: doc.status,
		})
		require.NoError(t, db.Create(&types.Chunk{
			ID: doc.id + "-chunk", TenantID: 1, KnowledgeBaseID: "kb", KnowledgeID: doc.id,
			Content: doc.id + " content", ChunkType: types.ChunkTypeText, IsEnabled: true,
		}).Error)
	}

	svc := &knowledgeBaseService{kgRepo: knowledge.repo, chunkRepo: repository.NewChunkRepository(db)}
	ctx := newSharedAccessContext()
	// An index may still return chunks written before cancellation, including
	// a cancelled chunk ranked ahead of an otherwise valid hit.
	candidates := []*types.IndexWithScore{
		{KnowledgeID: "cancelled", ChunkID: "cancelled-chunk", Score: 0.99},
		{KnowledgeID: "completed", ChunkID: "completed-chunk", Score: 0.90},
	}
	for _, skipEnrichment := range []bool{false, true} {
		results, err := svc.processSearchResults(ctx, candidates, 1, skipEnrichment)
		require.NoError(t, err)
		require.Len(t, results, 1)
		require.Equal(t, "completed-chunk", results[0].ID)
	}
}
