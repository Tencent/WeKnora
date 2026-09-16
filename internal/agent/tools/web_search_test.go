package tools

import (
	"context"
	"encoding/json"
	"testing"

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
