package tools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

func TestWikiListSourceChunksReturnsCitedOriginalText(t *testing.T) {
	page := newTestWikiPage("kb-1", "concept/root-crack")
	page.Title = "叶根断裂"
	page.SourceRefs = types.StringArray{"doc-a|手册A"}
	page.ChunkRefs = types.StringArray{"chunk-1"}

	tool := NewWikiListSourceChunksTool(
		&fakeWikiPageService{pages: map[string]*types.WikiPage{
			wikiPageKey("kb-1", page.Slug): page,
		}},
		nil,
		NewWikiScopesFromKBIDs([]string{"kb-1"}),
		NewWikiRouteResolver(),
	)
	raw, _ := json.Marshal(map[string]any{"slugs": []string{page.Slug}})
	got, err := tool.Execute(context.Background(), raw)
	require.NoError(t, err)
	require.True(t, got.Success, got.Error)
	require.Contains(t, got.Output, "original text for chunk-1")
	require.Contains(t, got.Output, "[[concept/root-crack|叶根断裂]]")
	require.NotContains(t, got.Output, "no_chunk_refs")
}

func TestWikiListSourceChunksEmptyRefs(t *testing.T) {
	page := newTestWikiPage("kb-1", "summary/doc-a")
	page.PageType = types.WikiPageTypeSummary
	tool := NewWikiListSourceChunksTool(
		&fakeWikiPageService{pages: map[string]*types.WikiPage{
			wikiPageKey("kb-1", page.Slug): page,
		}},
		nil,
		NewWikiScopesFromKBIDs([]string{"kb-1"}),
		NewWikiRouteResolver(),
	)
	raw, _ := json.Marshal(map[string]any{"slug": page.Slug})
	got, err := tool.Execute(context.Background(), raw)
	require.NoError(t, err)
	require.True(t, got.Success, got.Error)
	require.True(t, strings.Contains(got.Output, "no_chunk_refs"))
}

func TestWikiListSourceChunksNotFound(t *testing.T) {
	tool := NewWikiListSourceChunksTool(
		&fakeWikiPageService{pages: map[string]*types.WikiPage{}},
		nil,
		NewWikiScopesFromKBIDs([]string{"kb-1"}),
		NewWikiRouteResolver(),
	)
	raw, _ := json.Marshal(map[string]any{"slugs": []string{"entity/missing"}})
	got, err := tool.Execute(context.Background(), raw)
	require.NoError(t, err)
	require.False(t, got.Success)
	require.Contains(t, got.Error, "not found")
}
