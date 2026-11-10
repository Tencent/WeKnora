package service

import (
	"testing"

	"github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

func TestSearchResultsSkipCancelledKnowledge(t *testing.T) {
	svc, db := newKnowledgeSharedAccessService(t, nil)
	require.NoError(t, db.AutoMigrate(&types.Chunk{}))
	search := &knowledgeBaseService{kgRepo: svc.repo, chunkRepo: repository.NewChunkRepository(db)}
	const kbID = "kb"

	var candidates []*types.IndexWithScore
	for _, status := range []string{types.ParseStatusCancelled, types.ParseStatusCompleted} {
		chunkID := status + "-chunk"
		seedKnowledge(t, db, &types.Knowledge{
			ID: status, TenantID: 1, KnowledgeBaseID: kbID, Type: "file", ParseStatus: status,
		})
		chunk := &types.Chunk{
			ID: chunkID, TenantID: 1, KnowledgeBaseID: kbID, KnowledgeID: status,
			Content: status, ChunkType: types.ChunkTypeText, IsEnabled: true,
		}
		if status == types.ParseStatusCompleted {
			chunk.NextChunkID = types.ParseStatusCancelled + "-chunk"
		}
		require.NoError(t, db.Create(chunk).Error)
		candidates = append(candidates, &types.IndexWithScore{ChunkID: chunkID, KnowledgeID: status})
	}

	for _, skipEnrichment := range []bool{true, false} {
		results, err := search.processSearchResults(newSharedAccessContext(), candidates, 1, skipEnrichment)
		require.NoError(t, err)
		require.Len(t, results, 1)
		require.Equal(t, types.ParseStatusCompleted+"-chunk", results[0].ID)
	}
}

func TestIsSearchableChunkSkipsUnsynchronizedEdits(t *testing.T) {
	service := &knowledgeBaseService{}
	for _, status := range []string{"processing", "failed"} {
		chunk := &types.Chunk{ChunkType: types.ChunkTypeText, IndexStatus: status, IsEnabled: true}
		if service.isSearchableChunk(chunk) {
			t.Fatalf("chunk with index status %q should not be searchable", status)
		}
	}
	for _, status := range []string{"", "ready"} {
		chunk := &types.Chunk{ChunkType: types.ChunkTypeText, IndexStatus: status, IsEnabled: true}
		if !service.isSearchableChunk(chunk) {
			t.Fatalf("chunk with index status %q should be searchable", status)
		}
	}
}

func TestIsSearchableChunkSkipsDisabledChunk(t *testing.T) {
	service := &knowledgeBaseService{}
	chunk := &types.Chunk{
		ChunkType:   types.ChunkTypeFAQ,
		IndexStatus: "ready",
		IsEnabled:   false,
	}
	if service.isSearchableChunk(chunk) {
		t.Fatal("disabled FAQ chunk should never be searchable")
	}
}
