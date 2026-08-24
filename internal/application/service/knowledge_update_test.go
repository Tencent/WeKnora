package service

import (
	"context"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

func TestUpdateKnowledgeExplicitEmptyDescriptionClearsSummary(t *testing.T) {
	t.Parallel()
	repo := &metadataUpdateKnowledgeRepo{knowledge: &types.Knowledge{
		ID:              "knowledge-1",
		TenantID:        7,
		KnowledgeBaseID: "kb-1",
		Description:     "old summary",
		SummaryStatus:   types.SummaryStatusCompleted,
	}}
	service := &knowledgeService{
		repo: repo, kbService: metadataUpdateKBService{},
	}
	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(7))

	err := service.UpdateKnowledge(ctx, &types.Knowledge{
		ID:                   "knowledge-1",
		DescriptionSpecified: true,
		Description:          "",
	})
	require.NoError(t, err)
	require.Empty(t, repo.knowledge.Description)
	require.Equal(t, types.SummaryStatusNone, repo.knowledge.SummaryStatus)
}

func TestUpdateKnowledgeOmittedDescriptionPreservesExisting(t *testing.T) {
	t.Parallel()
	repo := &metadataUpdateKnowledgeRepo{knowledge: &types.Knowledge{
		ID:              "knowledge-1",
		TenantID:        7,
		KnowledgeBaseID: "kb-1",
		Description:     "keep me",
		SummaryStatus:   types.SummaryStatusCompleted,
	}}
	service := &knowledgeService{
		repo: repo, kbService: metadataUpdateKBService{},
	}
	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(7))

	err := service.UpdateKnowledge(ctx, &types.Knowledge{
		ID:    "knowledge-1",
		Title: "new title",
	})
	require.NoError(t, err)
	require.Equal(t, "new title", repo.knowledge.Title)
	require.Equal(t, "keep me", repo.knowledge.Description)
	require.Equal(t, types.SummaryStatusCompleted, repo.knowledge.SummaryStatus)
}

func TestUpdateKnowledgeManualDescriptionSetsCompletedStatus(t *testing.T) {
	t.Parallel()
	repo := &metadataUpdateKnowledgeRepo{knowledge: &types.Knowledge{
		ID:              "knowledge-1",
		TenantID:        7,
		KnowledgeBaseID: "kb-1",
		SummaryStatus:   types.SummaryStatusFailed,
	}}
	service := &knowledgeService{
		repo: repo, kbService: metadataUpdateKBService{},
	}
	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(7))

	err := service.UpdateKnowledge(ctx, &types.Knowledge{
		ID:                   "knowledge-1",
		DescriptionSpecified: true,
		Description:          "manual summary",
	})
	require.NoError(t, err)
	require.Equal(t, "manual summary", repo.knowledge.Description)
	require.Equal(t, types.SummaryStatusCompleted, repo.knowledge.SummaryStatus)
}

func TestUpdateKnowledgeDescriptionOnlyDoesNotTouchMetadata(t *testing.T) {
	t.Parallel()
	repo := &metadataUpdateKnowledgeRepo{knowledge: &types.Knowledge{
		ID:              "knowledge-1",
		TenantID:        7,
		KnowledgeBaseID: "kb-1",
		Description:     "old summary",
		SummaryStatus:   types.SummaryStatusCompleted,
		CustomMetadata:  types.JSON(`{"region":"Beijing"}`),
	}}
	service := &knowledgeService{
		repo: repo, kbService: metadataUpdateKBService{},
	}
	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(7))

	err := service.UpdateKnowledge(ctx, &types.Knowledge{
		ID:                   "knowledge-1",
		DescriptionSpecified: true,
		Description:          "updated summary",
	})
	require.NoError(t, err)
	require.JSONEq(t, `{"region":"Beijing"}`, string(repo.knowledge.CustomMetadata))
}
