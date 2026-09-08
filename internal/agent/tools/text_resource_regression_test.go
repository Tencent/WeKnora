package tools

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/modelcontext"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

func TestOversizedStructuredResultPreservesExistingWebPointer(t *testing.T) {
	pages := &memoryWebPages{pages: map[string]string{"web://original": "first\nFULL_PAGE_EVIDENCE\nlast"}}
	outputs := &testOutputStore{memoryWebPages: memoryWebPages{pages: map[string]string{}}}
	registry := NewToolRegistry()
	registry.SetMaxToolOutputSize(2048)
	registry.SetOutputSource(outputs)
	registry.RegisterTool(&longResultTool{BaseTool: NewBaseTool(ToolWebFetch, "", nil), result: &types.ToolResult{
		Success: true, Output: "short snippet", Data: map[string]interface{}{
			"full_output_path": "web://original", "raw_content": strings.Repeat("structured text", 2000),
		},
	}})
	result, err := registry.ExecuteTool(t.Context(), ToolWebFetch, []byte(`{}`))
	require.NoError(t, err)
	require.Equal(t, "web://original", result.Data["full_output_path"])
	require.Empty(t, outputs.pages)
	require.Contains(t, modelcontext.NewRegistry(true).ModelToolResultForTool(ToolWebFetch, result), "web://original")
	reader := NewReadFileTool(nil).WithWebPages(pages)
	page, err := reader.Execute(t.Context(), []byte(`{"path":"web://original","offset":2,"limit":1}`))
	require.NoError(t, err)
	require.Contains(t, page.Output, "FULL_PAGE_EVIDENCE")
}

func TestBatchWebFetchSnapshotRetainsNestedFullPagePointers(t *testing.T) {
	pages := &memoryWebPages{pages: map[string]string{}}
	outputs := &testOutputStore{memoryWebPages: memoryWebPages{pages: map[string]string{}}}
	content := strings.Repeat("\"\\", 5000) + "\nFULL_PAGE_END"
	fetcher := newStubWebContentFetcher(map[string]string{"https://example.com/page": content}, nil)
	registry := NewToolRegistry()
	registry.SetMaxToolOutputSize(4096)
	registry.SetOutputSource(outputs)
	registry.RegisterTool(newWebFetchTool(fetcher).WithPageSource(pages))
	result, err := registry.ExecuteTool(t.Context(), ToolWebFetch, webFetchArgs(WebFetchItem{
		URL: "https://example.com/page", Limit: 8000,
	}))
	require.NoError(t, err)
	require.True(t, result.Success, result.Error)
	require.Equal(t, true, result.Data["output_truncated"])
	row := result.Data["results"].([]map[string]interface{})[0]
	path := row["full_output_path"].(string)
	require.True(t, strings.HasPrefix(path, "web://"))
	snapshot := result.Data["full_output_path"].(string)
	require.Contains(t, outputs.pages[snapshot], path)
	reader := NewReadFileTool(nil).WithWebPages(pages)
	args, err := json.Marshal(ReadFileInput{Path: path, Offset: 2, Limit: 1})
	require.NoError(t, err)
	page, err := reader.Execute(t.Context(), args)
	require.NoError(t, err)
	require.Contains(t, page.Output, "FULL_PAGE_END")
}

func TestBinaryGrepReportsIncompleteScan(t *testing.T) {
	reader := NewReadFileTool(nil).WithMemory(&testMemoryResources{files: map[string]string{
		"memory://items/binary.md": "\x00\x01\x02\x03", "memory://items/text.md": "plain text",
	}})
	result, err := NewGrepFilesTool(reader).Execute(t.Context(), []byte(`{"path":"memory://","pattern":"missing"}`))
	require.NoError(t, err)
	require.True(t, result.Success)
	require.Equal(t, true, result.Data["partial_scan"])
	require.Equal(t, 1, result.Data["skipped_files"])
	require.Contains(t, result.Output, "Partial scan")
}

func TestWikiOversizedSnapshotIsBoundedAndMarkedPartial(t *testing.T) {
	first, second := newTestWikiPage("kb-1", "concept/first"), newTestWikiPage("kb-1", "concept/second")
	first.Content = strings.Repeat("a", 5*1024*1024)
	second.Content = strings.Repeat("b", 5*1024*1024)
	svc := &fakeWikiPageService{pages: map[string]*types.WikiPage{
		wikiPageKey("kb-1", first.Slug): first, wikiPageKey("kb-1", second.Slug): second,
	}}
	store := &testOutputStore{memoryWebPages: memoryWebPages{pages: map[string]string{}}}
	registry := NewToolRegistry()
	registry.SetMaxToolOutputSize(2048)
	registry.RegisterTool(
		NewWikiReadPageTool(svc, nil, NewWikiScopesFromKBIDs([]string{"kb-1"}), NewWikiRouteResolver()),
	)
	registry.SetOutputSource(store)
	result, err := registry.ExecuteTool(
		t.Context(),
		ToolWikiReadPage,
		[]byte(`{"slugs":["concept/first","concept/second"]}`),
	)
	require.NoError(t, err)
	require.True(t, result.Success, result.Error)
	require.Equal(t, true, result.Data["output_snapshot_partial"])
	require.Equal(t, []string{"concept/second"}, result.Data["snapshot_omitted_slugs"])
	snapshot := store.pages[result.Data["full_output_path"].(string)]
	require.LessOrEqual(t, int64(len(snapshot)), maxReadSandboxDownloadBytes)
	require.Contains(t, snapshot, first.Content)
	require.NotContains(t, snapshot, second.Content)
	require.NotContains(t, result.Output, "Saved complete")
	require.Contains(t, result.Output, "Partial snapshot")
	require.Contains(
		t,
		modelcontext.NewRegistry(true).ModelToolResultForTool(ToolWikiReadPage, result),
		"Partial snapshot",
	)
}
