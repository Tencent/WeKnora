package repository

import (
	"context"
	"fmt"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

func TestWikiPageListSourceFilterPaginatesAllTerms(t *testing.T) {
	db := setupWikiPagesTestDB(t)
	repo := NewWikiPageRepository(db)
	const source = `doc_%"\\中文`
	for i := 0; i < 947; i++ {
		page := makeWikiPage("kb", fmt.Sprintf("entity/term-%04d", i), "entity", "published")
		page.Title = "重复标题"
		ref := source
		if i%2 == 0 {
			ref += "|文章标题"
		}
		page.SourceRefs = types.StringArray{ref}
		require.NoError(t, db.Create(page).Error)
	}
	extras := []struct{ kb, source, status string }{
		{"other", source, "published"},
		{"kb", source + "suffix", "published"},
		{"kb", "another|" + source, "published"},
		{"kb", source, "archived"},
	}
	for i, extra := range extras {
		p := makeWikiPage(extra.kb, fmt.Sprintf("entity/extra-%d", i), "entity", extra.status)
		p.SourceRefs = types.StringArray{extra.source}
		require.NoError(t, db.Create(p).Error)
	}
	found := make(map[string]bool)
	lastID := ""
	for page := 1; page <= 5; page++ {
		rows, total, err := repo.List(context.Background(), &types.WikiPageListRequest{
			KnowledgeBaseID: "kb", SourceKnowledgeID: source, PageType: "entity,concept", Status: "published",
			Page: page, PageSize: 200, SortBy: "title", SortOrder: "asc",
		})
		require.NoError(t, err)
		require.EqualValues(t, 947, total)
		for _, row := range rows {
			require.True(t, row.ID > lastID)
			lastID = row.ID
			require.False(t, found[row.Slug])
			found[row.Slug] = true
		}
	}
	require.Len(t, found, 947)
}
