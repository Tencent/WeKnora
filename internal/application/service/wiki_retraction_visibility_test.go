package service

import (
	"context"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/require"
)

type retractionVisibilityPages struct {
	interfaces.WikiPageService
	page    *types.WikiPage
	written *types.WikiPage
	deleted bool
}

func (s *retractionVisibilityPages) GetPageBySlug(context.Context, string, string) (*types.WikiPage, error) {
	pageCopy := *s.page
	return &pageCopy, nil
}

func (s *retractionVisibilityPages) UpdatePage(_ context.Context, page *types.WikiPage) (*types.WikiPage, error) {
	s.written = page
	return page, nil
}

func (s *retractionVisibilityPages) DeletePage(context.Context, string, string) error {
	s.deleted = true
	return nil
}

func TestWikiRetractionKeepsFenceUntilSuccessfulRewrite(t *testing.T) {
	page := &types.WikiPage{
		Slug: "entity/shared", Title: "共享页", Content: "旧文贡献", SourceRefs: types.StringArray{"survivor"},
	}
	require.NoError(t, page.SetPendingRetraction("deleted", true))
	pages := &retractionVisibilityPages{page: page}
	service := &wikiIngestService{wikiService: pages}
	batch := &WikiBatchContext{
		SlugTitleMany:               func(context.Context, []string) map[string]string { return nil },
		SummaryContentByKnowledgeID: func(context.Context, string) string { return "保留文献的内容" },
	}
	updates := []SlugUpdate{{Type: "retract", Slug: page.Slug, KnowledgeID: "deleted", RetractDocContent: "旧文贡献"}}
	// Empty model output must not consume a retract op or lift its public fence.
	_, _, _, _, err := service.reduceSlugUpdates(
		context.Background(), &templateCaptureChatModel{}, "kb", page.Slug, updates, 7, batch, nil)
	require.Error(t, err)
	require.Nil(t, pages.written)
	metadata, err := page.PageMetadata.Map()
	require.NoError(t, err)
	require.NotEmpty(t, metadata["pending_retractions"])
	model := &templateCaptureChatModel{response: "SUMMARY: 新摘要\n保留文献的内容"}
	_, _, _, _, err = service.reduceSlugUpdates(context.Background(), model, "kb", page.Slug, updates, 7, batch, nil)
	require.NoError(t, err)
	require.NotNil(t, pages.written)
	metadata, err = pages.written.PageMetadata.Map()
	require.NoError(t, err)
	require.NotContains(t, metadata, "pending_retractions")
}

func TestWikiRetractionDeletesPageAfterItsLastSourceIsRemoved(t *testing.T) {
	page := &types.WikiPage{Slug: "entity/sole", SourceRefs: types.StringArray{"deleted"}}
	pages := &retractionVisibilityPages{page: page}
	service := &wikiIngestService{wikiService: pages}
	updates := []SlugUpdate{{Type: "retractStale", KnowledgeID: "deleted"}}
	_, _, _, _, err := service.reduceSlugUpdates(context.Background(), &templateCaptureChatModel{},
		"kb", page.Slug, updates, 7, &WikiBatchContext{}, nil)
	require.NoError(t, err)
	require.True(t, pages.deleted)
}
