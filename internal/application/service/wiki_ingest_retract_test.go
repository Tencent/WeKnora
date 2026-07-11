package service

import (
	"context"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type retractWikiPageService struct {
	interfaces.WikiPageService
	page        *types.WikiPage
	deletedKB   string
	deletedSlug string
}

func (s *retractWikiPageService) GetPageBySlug(_ context.Context, _, _ string) (*types.WikiPage, error) {
	return s.page, nil
}

func (s *retractWikiPageService) DeletePage(_ context.Context, kbID, slug string) error {
	s.deletedKB = kbID
	s.deletedSlug = slug
	return nil
}

func TestReduceSlugUpdatesDeletesPageAfterLastSourceRetraction(t *testing.T) {
	const (
		kbID = "kb-1"
		kid  = "knowledge-1"
		slug = "entity/orphan"
	)
	wikiPages := &retractWikiPageService{page: &types.WikiPage{
		KnowledgeBaseID: kbID,
		Slug:            slug,
		Title:           "Orphan",
		PageType:        types.WikiPageTypeEntity,
		Status:          types.WikiPageStatusPublished,
		Content:         "content that must be removed with its source",
		SourceRefs:      types.StringArray{kid},
	}}
	svc := &wikiIngestService{wikiService: wikiPages}

	changed, affectedType, additionFailed, err := svc.reduceSlugUpdates(
		context.Background(), nil, kbID, slug,
		[]SlugUpdate{{
			Type:        "retract",
			KnowledgeID: kid,
			DocTitle:    "Deleted document",
		}},
		10000, nil, nil,
	)

	require.NoError(t, err)
	assert.True(t, changed)
	assert.Equal(t, "retract", affectedType)
	assert.False(t, additionFailed)
	assert.Equal(t, kbID, wikiPages.deletedKB)
	assert.Equal(t, slug, wikiPages.deletedSlug)
}
