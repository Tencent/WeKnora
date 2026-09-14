package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"
	"unicode/utf8"

	"github.com/Tencent/WeKnora/internal/modelcontext"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

type memoryWebPages struct {
	mu    sync.Mutex
	pages map[string]string
	fail  bool
}

func (s *memoryWebPages) Save(_ context.Context, content string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.fail {
		return "", fmt.Errorf("storage unavailable")
	}
	path := fmt.Sprintf("web://page-%d", len(s.pages))
	s.pages[path] = content
	return path, nil
}

func (s *memoryWebPages) Read(_ context.Context, path string) ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	content, ok := s.pages[path]
	if !ok {
		return nil, fmt.Errorf("unavailable page")
	}
	return []byte(content), nil
}

func TestFetchSavedPageSurvivesCacheEvictionAndModelEncoding(t *testing.T) {
	store := &memoryWebPages{pages: map[string]string{}}
	fetcher := newStubWebContentFetcher(map[string]string{}, nil)
	fetch := newWebFetchTool(fetcher).WithPageSource(store)
	var saved string
	for i := 0; i < 9; i++ {
		url := fmt.Sprintf("https://example.com/%d", i)
		fetcher.contents[url] = "first line\nsecond line\nthird line"
		result, err := fetch.Execute(t.Context(), webFetchArgs(WebFetchItem{URL: url, Limit: 3}))
		require.NoError(t, err)
		require.True(t, result.Success)
		row := result.Data["results"].([]map[string]interface{})[0]
		if i == 0 {
			saved = row["full_output_path"].(string)
			encoded := modelcontext.NewRegistry(true).ModelToolResultForTool(ToolWebFetch, result)
			require.Contains(t, encoded, saved)
			require.Contains(t, encoded, `tool="read_file"`)
		}
	}
	require.Len(t, fetch.pages, 8)
	reader := NewReadFileTool(nil).WithWebPages(store)
	result, err := reader.Execute(t.Context(), json.RawMessage(fmt.Sprintf(`{"path":%q,"offset":2,"limit":1}`, saved)))
	require.NoError(t, err)
	require.True(t, result.Success, result.Error)
	require.Contains(t, result.Output, "second line")
	require.NotContains(t, result.Output, "first line")
	require.Equal(t, 3, result.Data["next_offset"])
	store.fail = true
	fetcher.contents["https://example.com/fail-save"] = "fetched despite storage failure"
	failedSave, err := fetch.Execute(t.Context(), webFetchArgs(WebFetchItem{URL: "https://example.com/fail-save"}))
	require.NoError(t, err)
	require.True(t, failedSave.Success)
	require.Contains(t, failedSave.Output, "could not be saved")
}

func TestSavedWebPageReadsOversizedUnicodeLineWithoutShell(t *testing.T) {
	line := strings.Repeat("正文", 18000)
	store := &memoryWebPages{pages: map[string]string{"web://long": line + "\nlast line"}}
	registry := NewToolRegistry()
	registry.SetMaxToolOutputSize(4096)
	registry.RegisterTool(NewReadFileTool(nil).WithWebPages(store))
	offset, within := 1, 0
	var restored strings.Builder
	for attempts := 0; attempts < 30; attempts++ {
		args, _ := json.Marshal(map[string]interface{}{"path": "web://long", "offset": offset, "line_offset": within})
		result, err := registry.ExecuteTool(t.Context(), ToolReadFile, args)
		require.NoError(t, err)
		require.True(t, result.Success, result.Error)
		require.True(t, utf8.ValidString(result.Output))
		require.LessOrEqual(t, utf8.RuneCountInString(result.Output), 4096)
		_, part, ok := strings.Cut(result.Output, "```\n")
		require.True(t, ok)
		part, _, ok = strings.Cut(part, "\n```")
		require.True(t, ok)
		restored.WriteString(part)
		if result.Data["next_offset"] == 2 {
			break
		}
		offset = result.Data["next_offset"].(int)
		next := result.Data["next_line_offset"].(int)
		require.Greater(t, next, within)
		within = next
	}
	require.Equal(t, line, restored.String())
}

func TestSearchOptionsAndExplicitPageContent(t *testing.T) {
	svc := &searchOnlyWebService{results: []*types.WebSearchResult{
		{URL: "https://example.com/a", Snippet: "search snippet", Age: "yesterday"},
		{URL: "https://example.com/b", Snippet: "still useful"},
	}}
	fetcher := newStubWebContentFetcher(map[string]string{"https://example.com/a": "verified page"},
		map[string]error{"https://example.com/b": fmt.Errorf("page failed")})
	pages := newWebFetchTool(fetcher).WithPageSource(&memoryWebPages{pages: map[string]string{}})
	search := NewWebSearchTool(svc, 5, "provider").WithPageReader(pages)
	ctx := context.WithValue(t.Context(), types.TenantIDContextKey, uint64(7))
	_, err := search.Execute(ctx, []byte(`{"query":"query","count":2}`))
	require.NoError(t, err)
	require.Empty(t, fetcher.callCount, "page fetching must be opt-in")
	result, err := search.Execute(ctx,
		[]byte(`{"query":"query","count":2,"country":"de","freshness":"pw","content":true}`))
	require.NoError(t, err)
	require.True(t, result.Success)
	require.Equal(t, 2, svc.config.MaxResults)
	require.Equal(t, "DE", svc.config.Filters.Country)
	require.Equal(t, "pw", svc.config.Filters.Freshness)
	rows := result.Data["results"].([]map[string]interface{})
	require.Equal(t, true, rows[0]["page_verified"])
	require.Equal(t, "verified page", rows[0]["page_content"])
	require.Equal(t, "yesterday", rows[0]["age"])
	require.Equal(t, "failed", rows[1]["page_status"])
	require.Equal(t, false, rows[1]["page_verified"])
	require.Equal(t, "still useful", rows[1]["snippet"])
	model := modelcontext.NewRegistry(true).ModelToolResultForTool(ToolWebSearch, result)
	require.Contains(t, model, "verified page")
	require.Contains(t, model, `status="success" verified="true"`)
	require.Contains(t, model, rows[0]["full_output_path"])
	require.Contains(t, model, "still useful")
	for _, args := range []string{
		`{"query":"q","count":0}`, `{"query":"q","count":6}`,
		`{"query":"q","country":"bad"}`, `{"query":"q","freshness":"tomorrow"}`,
	} {
		calls := svc.calls
		result, err := search.Execute(ctx, []byte(args))
		require.NoError(t, err)
		require.False(t, result.Success)
		require.Equal(t, calls, svc.calls)
	}
	failedStorage := newWebFetchTool(fetcher).WithPageSource(&memoryWebPages{fail: true})
	search.WithPageReader(failedStorage)
	result, err = search.Execute(ctx, []byte(`{"query":"q","count":1,"content":true}`))
	require.NoError(t, err)
	require.True(t, result.Success)
	model = modelcontext.NewRegistry(true).ModelToolResultForTool(ToolWebSearch, result)
	require.Contains(t, model, "verified page")
	require.Contains(t, model, "could not be saved")
}
