package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/Tencent/WeKnora/internal/modelcontext"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Embedding the unused RAG method deliberately panics if Agent search invokes it.
type searchOnlyWebService struct {
	interfaces.WebSearchService
	results  []*types.WebSearchResult
	config   *types.WebSearchConfig
	query    string
	provider string
	calls    int
}

func (s *searchOnlyWebService) Search(
	_ context.Context, provider string, cfg *types.WebSearchConfig, query string,
) ([]*types.WebSearchResult, error) {
	s.config, s.query, s.provider = cfg, query, provider
	s.calls++
	return s.results, nil
}

func TestAgentWebSearchUsesProviderWithoutRAGDependencies(t *testing.T) {
	svc := &searchOnlyWebService{results: []*types.WebSearchResult{
		nil,
		{URL: "javascript:alert(1)"},
		{URL: "https://example.com/a", Title: "A", Snippet: "证据"},
		{URL: "https://example.com/a#section"},
		{URL: "https://example.com/b"},
		{URL: "https://example.com/c"},
	}}
	cfg := &types.WebSearchConfig{
		CompressionMethod: "rag", MaxResults: 15, IncludeDate: true, Blacklist: []string{"blocked.example"},
	}
	ctx := context.WithValue(t.Context(), types.TenantIDContextKey, uint64(1))
	ctx = context.WithValue(ctx, types.TenantInfoContextKey, &types.Tenant{WebSearchConfig: cfg})
	tool := NewWebSearchTool(svc, 2, "provider-1")
	result, err := tool.Execute(ctx, json.RawMessage(`{"query":"  latest docs  "}`))
	require.NoError(t, err)
	require.True(t, result.Success)
	assert.Equal(t, 2, result.Data["count"])
	assert.Equal(t, "latest docs", svc.query)
	assert.Equal(t, "provider-1", svc.provider)
	assert.Equal(t, "none", svc.config.CompressionMethod)
	assert.True(t, svc.config.IncludeDate)
	assert.Equal(t, cfg.Blacklist, svc.config.Blacklist)
	assert.Equal(t, "rag", cfg.CompressionMethod, "must not mutate tenant configuration")
	assert.NotContains(t, tool.Description(), "MUST complete KB")
}

func TestAgentWebSearchValidatesQueryAndNormalizesResultLimit(t *testing.T) {
	svc := &searchOnlyWebService{}
	tool := NewWebSearchTool(svc, 0, "provider-1")
	assert.Equal(t, types.DefaultWebSearchMaxResults, tool.maxResults)
	assert.Equal(t, 20, NewWebSearchTool(svc, 1000, "provider-1").maxResults)
	ctx := context.WithValue(t.Context(), types.TenantIDContextKey, uint64(1))
	result, err := tool.Execute(ctx, json.RawMessage(`{"query":"   "}`))
	require.Error(t, err)
	assert.False(t, result.Success)
	assert.Zero(t, svc.calls)
}

func TestAgentWebSearchContentFetchesLeadingPagesOnly(t *testing.T) {
	svc := &searchOnlyWebService{results: []*types.WebSearchResult{
		{URL: "https://example.com/a", Snippet: "a"},
		{URL: "https://example.com/b", Snippet: "b"},
		{URL: "https://example.com/c", Snippet: "c"},
		{URL: "https://example.com/d", Snippet: "d"},
		{URL: "https://example.com/e", Snippet: "e"},
	}}
	contents := map[string]string{}
	for _, result := range svc.results {
		contents[result.URL] = "page " + result.URL
	}
	fetcher := newStubWebContentFetcher(contents, nil)
	search := NewWebSearchTool(svc, 5, "provider").WithPageReader(newWebFetchTool(fetcher))
	ctx := context.WithValue(t.Context(), types.TenantIDContextKey, uint64(7))
	result, err := search.Execute(ctx, []byte(`{"query":"q","content":true}`))
	require.NoError(t, err)
	require.True(t, result.Success)
	require.Len(t, fetcher.callCount, 3)
	rows := result.Data["results"].([]map[string]interface{})
	require.Len(t, rows, 5)
	assert.Equal(t, "success", rows[0]["page_status"])
	assert.Equal(t, "success", rows[2]["page_status"])
	assert.Equal(t, "skipped", rows[3]["page_status"])
	assert.Equal(t, "skipped", rows[4]["page_status"])
	assert.Equal(t, "d", rows[3]["snippet"])
}

// TestAgentWebSearchClampsCountToConfiguredMaximum an over-maximum count is
// clamped to the configured maximum instead of failing the whole call, and the
// correction reaches both the UI-facing output and the rendered model view.
func TestAgentWebSearchClampsCountToConfiguredMaximum(t *testing.T) {
	svc := &searchOnlyWebService{results: []*types.WebSearchResult{
		{URL: "https://example.com/a"},
		{URL: "https://example.com/b"},
		{URL: "https://example.com/c"},
	}}
	tool := NewWebSearchTool(svc, 2, "provider-1")
	ctx := context.WithValue(t.Context(), types.TenantIDContextKey, uint64(1))

	result, err := tool.Execute(ctx, json.RawMessage(`{"query":"q","count":3}`))
	require.NoError(t, err)
	require.True(t, result.Success)
	require.Equal(t, 1, svc.calls, "the search must still run")
	require.Equal(t, 2, svc.config.MaxResults, "count must clamp to the maximum")
	require.Contains(t, result.Output, "requested count 3 is outside 1-2 and was clamped to 2.")
	require.Equal(t, true, result.Data["count_clamped"])
	require.Equal(t, 3, result.Data["count_requested"])
	require.Equal(t, 2, result.Data["count_maximum"])

	// The model reads the rendered retrieval block, not Output, once a row exists.
	model := modelcontext.NewRegistry(true).ModelToolResultForTool(ToolWebSearch, result)
	require.Contains(t, model, `<count_clamped requested="3" maximum="2">`)
}

// TestAgentWebSearchRejectsNonPositiveCount a count below 1 is a malformed
// argument, not a near miss: it fails before the provider is called.
func TestAgentWebSearchRejectsNonPositiveCount(t *testing.T) {
	svc := &searchOnlyWebService{results: []*types.WebSearchResult{{URL: "https://example.com/a"}}}
	tool := NewWebSearchTool(svc, 2, "provider-1")
	ctx := context.WithValue(t.Context(), types.TenantIDContextKey, uint64(1))

	for _, count := range []int{-1, 0} {
		svc.calls = 0
		result, err := tool.Execute(ctx, json.RawMessage(fmt.Sprintf(`{"query":"q","count":%d}`, count)))
		require.NoError(t, err)
		require.False(t, result.Success, "count=%d must fail", count)
		require.Contains(t, result.Error, "count must be between 1 and 2")
		require.Equal(t, 0, svc.calls, "count=%d must not reach the provider", count)
	}
}

// TestAgentWebSearchClampNoteSurvivesEmptyResults the zero-result early return
// must carry the same correction as the normal path.
func TestAgentWebSearchClampNoteSurvivesEmptyResults(t *testing.T) {
	svc := &searchOnlyWebService{}
	tool := NewWebSearchTool(svc, 2, "provider-1")
	ctx := context.WithValue(t.Context(), types.TenantIDContextKey, uint64(1))

	result, err := tool.Execute(ctx, json.RawMessage(`{"query":"q","count":5}`))
	require.NoError(t, err)
	require.True(t, result.Success)
	require.Equal(t, 1, svc.calls)
	require.Equal(t, 2, svc.config.MaxResults)
	require.Contains(t, result.Output, "was clamped to 2.")
	require.Equal(t, true, result.Data["count_clamped"])

	model := modelcontext.NewRegistry(true).ModelToolResultForTool(ToolWebSearch, result)
	require.Contains(t, model, "was clamped to 2.")
}

// TestAgentWebSearchCountWithinRangeIsHonoured an in-range count is honoured as given, with no note.
func TestAgentWebSearchCountWithinRangeIsHonoured(t *testing.T) {
	svc := &searchOnlyWebService{results: []*types.WebSearchResult{
		{URL: "https://example.com/a"},
		{URL: "https://example.com/b"},
	}}
	tool := NewWebSearchTool(svc, 5, "provider-1")
	ctx := context.WithValue(t.Context(), types.TenantIDContextKey, uint64(1))

	result, err := tool.Execute(ctx, json.RawMessage(`{"query":"q","count":1}`))
	require.NoError(t, err)
	require.True(t, result.Success)
	require.Equal(t, 1, svc.config.MaxResults)
	require.NotContains(t, result.Output, "was clamped")
}

// TestPatchWebSearchCountSchema the effective maximum must reach the schema, and
// any malformed input leaves the schema unchanged rather than breaking the tool.
func TestPatchWebSearchCountSchema(t *testing.T) {
	raw := json.RawMessage(`{"type":"object","properties":{` +
		`"query":{"type":"string"},"count":{"type":"integer","description":"old"}},"required":["query"]}`)

	var schema map[string]any
	require.NoError(t, json.Unmarshal(patchWebSearchCountSchema(raw, 7), &schema))
	props, ok := schema["properties"].(map[string]any)
	require.True(t, ok)
	count, ok := props["count"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t,
		"Number of results to return, 1 to 7 (the configured maximum). Omit to use the maximum.",
		count["description"])
	assert.Equal(t, "object", schema["type"])
	assert.Equal(t, []any{"query"}, schema["required"])

	broken := json.RawMessage(`not json`)
	assert.Equal(t, broken, patchWebSearchCountSchema(broken, 7))

	noProperties := json.RawMessage(`{"type":"object"}`)
	assert.Equal(t, noProperties, patchWebSearchCountSchema(noProperties, 7))
}
