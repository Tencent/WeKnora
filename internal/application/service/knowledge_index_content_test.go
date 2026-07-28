package service

import (
	"context"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/require"
)

func TestBuildKnowledgeIndexContentExcludesCustomMetadata(t *testing.T) {
	t.Parallel()
	knowledge := &types.Knowledge{
		Title:          "  WeKnora Handbook  ",
		CustomMetadata: types.JSON(`{"region":"Shanghai","department":"R&D"}`),
	}

	content := buildKnowledgeIndexContent(knowledge, "Chunk body")

	require.Equal(t, "WeKnora Handbook\nChunk body", content)
	require.NotContains(t, content, "Shanghai")
	require.NotContains(t, content, "department")
}

func TestBuildKnowledgeIndexContentWithoutTitleReturnsContent(t *testing.T) {
	t.Parallel()
	require.Equal(t, "Chunk body", buildKnowledgeIndexContent(&types.Knowledge{
		CustomMetadata: types.JSON(`{"region":"Shanghai"}`),
	}, "Chunk body"))
	require.Equal(t, "Chunk body", buildKnowledgeIndexContent(nil, "Chunk body"))
}

type metadataUpdateKnowledgeRepo struct {
	interfaces.KnowledgeRepository
	knowledge           *types.Knowledge
	summaryStatusUpdate interface{}
}

func (r *metadataUpdateKnowledgeRepo) GetKnowledgeByID(
	_ context.Context, _ uint64, _ string,
) (*types.Knowledge, error) {
	clone := *r.knowledge
	return &clone, nil
}

func (r *metadataUpdateKnowledgeRepo) UpdateKnowledge(_ context.Context, knowledge *types.Knowledge) error {
	clone := *knowledge
	r.knowledge = &clone
	return nil
}

func (r *metadataUpdateKnowledgeRepo) UpdateKnowledgeColumn(
	_ context.Context, _ string, column string, value interface{},
) error {
	if column == "summary_status" {
		r.summaryStatusUpdate = value
	}
	return nil
}

type metadataUpdateChunkRepo struct {
	interfaces.ChunkRepository
	listCalls int
}

func (r *metadataUpdateChunkRepo) ListChunksByKnowledgeID(
	_ context.Context, _ uint64, _ string,
) ([]*types.Chunk, error) {
	r.listCalls++
	return nil, nil
}

func TestUpdateKnowledgeMetadataDoesNotReindexChunks(t *testing.T) {
	t.Parallel()
	repo := &metadataUpdateKnowledgeRepo{knowledge: &types.Knowledge{
		ID:             "knowledge-1",
		TenantID:       7,
		SummaryStatus:  types.SummaryStatusCompleted,
		CustomMetadata: types.JSON(`{"region":"Beijing"}`),
	}}
	chunkRepo := &metadataUpdateChunkRepo{}
	service := &knowledgeService{repo: repo, chunkRepo: chunkRepo}
	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(7))

	err := service.UpdateKnowledge(ctx, &types.Knowledge{
		ID:             "knowledge-1",
		CustomMetadata: types.JSON(`{"region":"Shanghai"}`),
	})

	require.NoError(t, err)
	require.JSONEq(t, `{"region":"Shanghai"}`, string(repo.knowledge.CustomMetadata))
	require.Zero(t, chunkRepo.listCalls)
	require.Equal(t, types.SummaryStatusPending, repo.summaryStatusUpdate)
}
