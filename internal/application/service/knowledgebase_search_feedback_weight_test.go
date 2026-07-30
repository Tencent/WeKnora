package service

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

func TestRankCandidatesByRecallWeightBeforeTruncation(t *testing.T) {
	repo := &recallWeightChunkRepo{
		chunks: []*types.Chunk{
			{ID: "raw-top", TenantID: 1, RecallWeight: 0.5},
			{ID: "raw-middle", TenantID: 1, RecallWeight: 1},
			// Tenant 99 represents a chunk from a shared knowledge base. The
			// lookup is deliberately ID-only, so its owner tenant is preserved.
			{ID: "boosted-shared", TenantID: 99, RecallWeight: 2},
		},
	}
	svc := &knowledgeBaseService{chunkRepo: repo}
	candidates := []*types.IndexWithScore{
		{ChunkID: "raw-top", Score: 0.9},
		{ChunkID: "raw-middle", Score: 0.8},
		{ChunkID: "boosted-shared", Score: 0.5},
	}

	svc.rankCandidatesByRecallWeight(context.Background(), candidates, true)

	require.Equal(t, []string{"boosted-shared", "raw-middle", "raw-top"}, candidateChunkIDs(candidates))
	require.Equal(t, []float64{0.5, 0.8, 0.9}, candidateScores(candidates),
		"pre-truncation ranking must not mutate raw scores or double-apply the weight")
	require.ElementsMatch(t, []string{"raw-top", "raw-middle", "boosted-shared"}, repo.requestedIDs)
}

func TestRankCandidatesByRecallWeightDisabledKeepsHistoricalOrder(t *testing.T) {
	repo := &recallWeightChunkRepo{
		chunks: []*types.Chunk{{ID: "second", RecallWeight: 10}},
	}
	svc := &knowledgeBaseService{chunkRepo: repo}
	candidates := []*types.IndexWithScore{
		{ChunkID: "first", Score: 0.9},
		{ChunkID: "second", Score: 0.1},
	}

	svc.rankCandidatesByRecallWeight(context.Background(), candidates, false)

	require.Equal(t, []string{"first", "second"}, candidateChunkIDs(candidates))
	require.Empty(t, repo.requestedIDs)
}

func TestRankCandidatesByRecallWeightRepositoryFailureIsFailOpen(t *testing.T) {
	repo := &recallWeightChunkRepo{err: errors.New("database unavailable")}
	svc := &knowledgeBaseService{chunkRepo: repo}
	candidates := []*types.IndexWithScore{
		{ChunkID: "first", Score: 0.9},
		{ChunkID: "second", Score: 0.8},
	}

	svc.rankCandidatesByRecallWeight(context.Background(), candidates, true)

	require.Equal(t, []string{"first", "second"}, candidateChunkIDs(candidates))
	require.Equal(t, []float64{0.9, 0.8}, candidateScores(candidates))
}

func TestBuildSearchResultCarriesRecallWeightWithoutMutatingScore(t *testing.T) {
	svc := &knowledgeBaseService{}

	result := svc.buildSearchResult(
		&types.Chunk{ID: "chunk-1", RecallWeight: 1.5},
		&types.Knowledge{ID: "knowledge-1", KnowledgeBaseID: "kb-1"},
		0.6,
		types.MatchTypeEmbedding,
		"matched",
	)

	require.Equal(t, 1.5, result.RecallWeight)
	require.Equal(t, 0.6, result.Score)
}

type recallWeightChunkRepo struct {
	interfaces.ChunkRepository

	chunks       []*types.Chunk
	err          error
	requestedIDs []string
}

func (r *recallWeightChunkRepo) ListChunksByIDOnly(ctx context.Context, ids []string) ([]*types.Chunk, error) {
	r.requestedIDs = append(r.requestedIDs, ids...)
	return r.chunks, r.err
}

func candidateChunkIDs(candidates []*types.IndexWithScore) []string {
	ids := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		ids = append(ids, candidate.ChunkID)
	}
	return ids
}

func candidateScores(candidates []*types.IndexWithScore) []float64 {
	scores := make([]float64, 0, len(candidates))
	for _, candidate := range candidates {
		scores = append(scores, candidate.Score)
	}
	return scores
}
