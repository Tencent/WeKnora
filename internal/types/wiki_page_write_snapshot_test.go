package types

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestWikiPageWriteSnapshotCopiesMutableFields(t *testing.T) {
	page := &WikiPage{
		ID: "page", Aliases: StringArray{"alias"}, CategoryPath: StringArray{"folder"},
		SourceRefs: StringArray{"source"}, ChunkRefs: StringArray{"chunk"},
		InLinks: StringArray{"incoming"}, OutLinks: StringArray{"outgoing"},
		PageMetadata: JSON(`{"key":"value"}`),
	}
	ctx := WithWikiPageWriteSnapshot(context.Background(), page)
	page.Aliases[0], page.CategoryPath[0] = "changed", "changed"
	page.SourceRefs[0], page.ChunkRefs[0] = "changed", "changed"
	page.InLinks[0], page.OutLinks[0] = "changed", "changed"
	page.PageMetadata[2] = 'x'
	first, ok := WikiPageWriteSnapshotFromContext(ctx)
	require.True(t, ok)
	require.Equal(t, StringArray{"alias"}, first.Aliases)
	require.Equal(t, StringArray{"folder"}, first.CategoryPath)
	require.Equal(t, StringArray{"source"}, first.SourceRefs)
	require.Equal(t, StringArray{"chunk"}, first.ChunkRefs)
	require.Equal(t, StringArray{"incoming"}, first.InLinks)
	require.Equal(t, StringArray{"outgoing"}, first.OutLinks)
	require.JSONEq(t, `{"key":"value"}`, string(first.PageMetadata))
	first.SourceRefs[0] = "second mutation"
	first.PageMetadata[2] = 'z'
	second, ok := WikiPageWriteSnapshotFromContext(ctx)
	require.True(t, ok)
	require.Equal(t, StringArray{"source"}, second.SourceRefs)
	require.JSONEq(t, `{"key":"value"}`, string(second.PageMetadata))
}

func TestWikiPageWriteSnapshotClearPreservesContext(t *testing.T) {
	parent, cancel := context.WithCancel(context.Background())
	ctx := WithWikiPageWriteSnapshot(parent, &WikiPage{ID: "page"})
	cleared := WithWikiPageWriteSnapshot(ctx, nil)
	_, ok := WikiPageWriteSnapshotFromContext(cleared)
	require.False(t, ok)
	_, ok = WikiPageWriteSnapshotFromContext(ctx)
	require.True(t, ok)
	cancel()
	require.ErrorIs(t, cleared.Err(), context.Canceled)
}

func TestWikiPageWriteSnapshotMatchesGuardedFields(t *testing.T) {
	base := &WikiPage{
		ID: "page", TenantID: 1, KnowledgeBaseID: "kb", Slug: "concept/page", Version: 1,
		Content: "body", PageMetadata: JSON(`{"n":9007199254740992,"a":1}`),
	}
	changes := map[string]func(*WikiPage){
		"page":        func(p *WikiPage) { p.ID = "other" },
		"tenant":      func(p *WikiPage) { p.TenantID++ },
		"kb":          func(p *WikiPage) { p.KnowledgeBaseID = "other" },
		"slug":        func(p *WikiPage) { p.Slug = "other" },
		"version":     func(p *WikiPage) { p.Version++ },
		"title":       func(p *WikiPage) { p.Title = "new" },
		"content":     func(p *WikiPage) { p.Content = "new" },
		"summary":     func(p *WikiPage) { p.Summary = "new" },
		"page type":   func(p *WikiPage) { p.PageType = "entity" },
		"status":      func(p *WikiPage) { p.Status = "archived" },
		"aliases":     func(p *WikiPage) { p.Aliases = StringArray{"new"} },
		"sources":     func(p *WikiPage) { p.SourceRefs = StringArray{"new"} },
		"chunks":      func(p *WikiPage) { p.ChunkRefs = StringArray{"new"} },
		"out links":   func(p *WikiPage) { p.OutLinks = StringArray{"new"} },
		"metadata":    func(p *WikiPage) { p.PageMetadata = JSON(`{"n":9007199254740993,"a":1}`) },
		"parent":      func(p *WikiPage) { p.ParentSlug = "new" },
		"folder":      func(p *WikiPage) { p.FolderID = "new" },
		"category":    func(p *WikiPage) { p.CategoryPath = StringArray{"new"} },
		"path":        func(p *WikiPage) { p.WikiPath = "new" },
		"depth":       func(p *WikiPage) { p.Depth++ },
		"sort order":  func(p *WikiPage) { p.SortOrder++ },
		"edit source": func(p *WikiPage) { p.LastEditSource = "user" },
		"editor":      func(p *WikiPage) { p.LastEditorID = "user" },
	}
	for name, change := range changes {
		t.Run(name, func(t *testing.T) {
			current := cloneWikiPageWriteSnapshot(base)
			change(current)
			require.False(t, WikiPageWriteSnapshotMatches(base, current))
		})
	}
	t.Run("independent backlinks and timestamps", func(t *testing.T) {
		current := cloneWikiPageWriteSnapshot(base)
		current.InLinks = StringArray{"new-source"}
		current.UpdatedAt, current.CreatedAt = time.Now(), time.Now()
		current.SourceRefs = StringArray{}
		current.PageMetadata = JSON(`{ "a": 1, "n": 9007199254740992 }`)
		require.True(t, WikiPageWriteSnapshotMatches(base, current))
	})
	t.Run("invalid metadata is not treated as equivalent", func(t *testing.T) {
		current := cloneWikiPageWriteSnapshot(base)
		current.PageMetadata = append(current.PageMetadata, []byte(` trailing`)...)
		require.False(t, WikiPageWriteSnapshotMatches(base, current))
	})
}
