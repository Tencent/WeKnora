package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"testing"

	"github.com/Tencent/WeKnora/internal/modelcontext"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fakeWebSearchProvider struct {
	results  []*types.WebSearchResult
	request  types.WebSearchRequest
	calls    int
	filtered bool
	mu       sync.Mutex
	fetched  []string
}

func (p *fakeWebSearchProvider) Capabilities() types.WebSearchCapabilities {
	return types.WebSearchCapabilities{Filters: p.filtered}
}

func (p *fakeWebSearchProvider) Fetch(_ context.Context, pageURL string) (*types.WebPage, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.fetched = append(p.fetched, pageURL)
	return &types.WebPage{Content: "provider page " + pageURL}, nil
}

func (p *fakeWebSearchProvider) Search(
	_ context.Context, req types.WebSearchRequest,
) ([]*types.WebSearchResult, error) {
	p.request = req
	p.calls++
	return p.results, nil
}

func TestAgentWebSearchBuildsRequestAndFiltersRows(t *testing.T) {
	svc := &fakeWebSearchProvider{results: []*types.WebSearchResult{
		nil,
		{URL: "javascript:alert(1)"},
		{URL: "https://example.com/a", Title: "A", Snippet: "证据"},
		{URL: "https://example.com/a#section"},
		{URL: "https://example.com/b"},
		{URL: "https://example.com/c"},
	}}
	cfg := &types.WebSearchConfig{MaxResults: 15, IncludeDate: true}
	ctx := context.WithValue(t.Context(), types.TenantIDContextKey, uint64(1))
	ctx = context.WithValue(ctx, types.TenantInfoContextKey, &types.Tenant{WebSearchConfig: cfg})
	tool := NewWebSearchTool(svc, 2)
	result, err := tool.Execute(ctx, json.RawMessage(`{"query":"  latest docs  "}`))
	require.NoError(t, err)
	require.True(t, result.Success)
	assert.Equal(t, 2, result.Data["count"])
	assert.Equal(t, types.WebSearchRequest{Query: "latest docs", MaxResults: 2, IncludeDate: true}, svc.request)
	assert.NotContains(t, tool.Description(), "MUST complete KB")
}

func TestAgentWebSearchValidatesQueryAndNormalizesResultLimit(t *testing.T) {
	svc := &fakeWebSearchProvider{}
	tool := NewWebSearchTool(svc, 0)
	assert.Equal(t, types.DefaultWebSearchMaxResults, tool.maxResults)
	assert.Equal(t, 20, NewWebSearchTool(svc, 1000).maxResults)
	ctx := context.WithValue(t.Context(), types.TenantIDContextKey, uint64(1))
	result, err := tool.Execute(ctx, json.RawMessage(`{"query":"   "}`))
	require.Error(t, err)
	assert.False(t, result.Success)
	assert.Zero(t, svc.calls)
}

func TestAgentWebSearchContentFetchesLeadingPagesOnly(t *testing.T) {
	svc := &fakeWebSearchProvider{results: []*types.WebSearchResult{
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
	// The plain schema (DeepSeek and most providers) offers content too.
	search := NewWebSearchTool(svc, 5).WithPageReader(NewWebFetchTool(fetcher))
	var schema map[string]any
	require.NoError(t, json.Unmarshal(search.Parameters(), &schema))
	assert.Contains(t, schema["properties"].(map[string]any), "content")

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

func TestAgentWebSearchContentReadsThroughProviderByDefault(t *testing.T) {
	svc := &fakeWebSearchProvider{results: []*types.WebSearchResult{{URL: "https://example.com/a"}}}
	search := NewWebSearchTool(svc, 5)
	ctx := context.WithValue(t.Context(), types.TenantIDContextKey, uint64(7))

	result, err := search.Execute(ctx, []byte(`{"query":"q","content":true}`))
	require.NoError(t, err)
	rows := result.Data["results"].([]map[string]interface{})
	assert.Equal(t, "provider page https://example.com/a", rows[0]["page_content"])
	assert.Equal(t, []string{"https://example.com/a"}, svc.fetched)
}

func TestWebSearchWithoutProviderFails(t *testing.T) {
	tool := NewWebSearchTool(nil, 5)
	result, err := tool.Execute(t.Context(), []byte(`{"query":"q"}`))
	require.Error(t, err)
	require.False(t, result.Success)
	require.Contains(t, result.Error, "no web search provider configured")
}

// TestAgentWebSearchClampsCountToConfiguredMaximum an over-maximum count is
// clamped to the configured maximum instead of failing the whole call, and the
// correction reaches both the UI-facing output and the rendered model view.
func TestAgentWebSearchClampsCountToConfiguredMaximum(t *testing.T) {
	svc := &fakeWebSearchProvider{results: []*types.WebSearchResult{
		{URL: "https://example.com/a"},
		{URL: "https://example.com/b"},
		{URL: "https://example.com/c"},
	}}
	tool := NewWebSearchTool(svc, 2)
	ctx := context.WithValue(t.Context(), types.TenantIDContextKey, uint64(1))

	result, err := tool.Execute(ctx, json.RawMessage(`{"query":"q","count":3}`))
	require.NoError(t, err)
	require.True(t, result.Success)
	require.Equal(t, 1, svc.calls, "the search must still run")
	require.Equal(t, 2, svc.request.MaxResults, "count must clamp to the maximum")
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
	svc := &fakeWebSearchProvider{results: []*types.WebSearchResult{{URL: "https://example.com/a"}}}
	tool := NewWebSearchTool(svc, 2)
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
	svc := &fakeWebSearchProvider{}
	tool := NewWebSearchTool(svc, 2)
	ctx := context.WithValue(t.Context(), types.TenantIDContextKey, uint64(1))

	result, err := tool.Execute(ctx, json.RawMessage(`{"query":"q","count":5}`))
	require.NoError(t, err)
	require.True(t, result.Success)
	require.Equal(t, 1, svc.calls)
	require.Equal(t, 2, svc.request.MaxResults)
	require.Contains(t, result.Output, "was clamped to 2.")
	require.Equal(t, true, result.Data["count_clamped"])

	model := modelcontext.NewRegistry(true).ModelToolResultForTool(ToolWebSearch, result)
	require.Contains(t, model, "was clamped to 2.")
}

// TestAgentWebSearchCountWithinRangeIsHonoured an in-range count is honoured as given, with no note.
func TestAgentWebSearchCountWithinRangeIsHonoured(t *testing.T) {
	svc := &fakeWebSearchProvider{results: []*types.WebSearchResult{
		{URL: "https://example.com/a"},
		{URL: "https://example.com/b"},
	}}
	tool := NewWebSearchTool(svc, 5)
	ctx := context.WithValue(t.Context(), types.TenantIDContextKey, uint64(1))

	result, err := tool.Execute(ctx, json.RawMessage(`{"query":"q","count":1}`))
	require.NoError(t, err)
	require.True(t, result.Success)
	require.Equal(t, 1, svc.request.MaxResults)
	require.NotContains(t, result.Output, "was clamped")
}

// TestWebSearchSchemaFollowsProvider DeepSeek gets the query schema. Brave gets
// country and freshness, with a description that documents those fields.
func TestWebSearchSchemaFollowsProvider(t *testing.T) {
	svc := &fakeWebSearchProvider{results: []*types.WebSearchResult{
		{URL: "https://github.com/trending", Title: "Trending", Snippet: "today"},
	}}
	deepseek := NewWebSearchTool(svc, 5)
	braveSvc := &fakeWebSearchProvider{results: svc.results, filtered: true}
	brave := NewWebSearchTool(braveSvc, 5)
	ctx := context.WithValue(t.Context(), types.TenantIDContextKey, uint64(1))

	var plain map[string]any
	require.NoError(t, json.Unmarshal(deepseek.Parameters(), &plain))
	plainProps := plain["properties"].(map[string]any)
	assert.NotContains(t, plainProps, "country")
	assert.NotContains(t, plainProps, "freshness")
	assert.Contains(t, deepseek.Description(), "Include the place and the time range in")
	assert.NotContains(t, deepseek.Description(), "freshness is pd")

	var filtered map[string]any
	require.NoError(t, json.Unmarshal(brave.Parameters(), &filtered))
	filteredProps := filtered["properties"].(map[string]any)
	assert.Contains(t, filteredProps, "country")
	assert.Contains(t, filteredProps, "freshness")
	assert.Contains(t, brave.Description(), "freshness is pd, pw, pm, or py")

	result, err := deepseek.Execute(ctx, json.RawMessage(
		`{"query":"GitHub trending","freshness":"pd","country":"US"}`))
	require.NoError(t, err)
	require.True(t, result.Success)
	require.Equal(t, 1, svc.calls)
	require.Empty(t, svc.request.Filters)

	result, err = brave.Execute(ctx, json.RawMessage(`{"query":"GitHub trending","freshness":"pd","country":"us"}`))
	require.NoError(t, err)
	require.True(t, result.Success)
	require.Equal(t, types.WebSearchFilters{Country: "US", Freshness: "pd"}, braveSvc.request.Filters)
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
