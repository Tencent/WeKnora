package tools

import (
	"context"
	"encoding/json"
	"fmt"
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

// TestAgentWebSearchClampsCountToConfiguredMaximum count 超出上限时截断，而不是整轮失败。
//
// count 只是"要几条"的基数提示，不是带身份的目标集合。硬拒绝并不比截断更安全
// ——两条路都受同一个上限约束——却会让模型白丢一轮，并把"模型填错参数"混进真正
// 的失败信号里（调用方把 Success=false 一律记为 span error）。
// 同类标量 search_knowledge.limit / search_memory.limit 走的也是截断。
func TestAgentWebSearchClampsCountToConfiguredMaximum(t *testing.T) {
	svc := &searchOnlyWebService{results: []*types.WebSearchResult{
		{URL: "https://example.com/a"},
		{URL: "https://example.com/b"},
		{URL: "https://example.com/c"},
	}}
	tool := NewWebSearchTool(svc, 2, "provider-1")
	ctx := context.WithValue(t.Context(), types.TenantIDContextKey, uint64(1))

	for _, count := range []int{-1, 0, 3} {
		svc.calls = 0
		result, err := tool.Execute(ctx, json.RawMessage(fmt.Sprintf(`{"query":"q","count":%d}`, count)))
		require.NoError(t, err)
		require.True(t, result.Success, "count=%d must not fail the call", count)
		require.Equal(t, 1, svc.calls, "count=%d must still run the search", count)
		require.Equal(t, 2, svc.config.MaxResults, "count=%d must clamp to the maximum", count)
		require.Contains(t, result.Output,
			fmt.Sprintf("requested count %d is outside 1-2 and was clamped to 2.", count))
	}
}

// TestAgentWebSearchCountWithinRangeIsHonoured 范围内的 count 仍按原样生效，且不加提示。
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

// TestPatchWebSearchCountSchema 动态上限必须写进 schema，且任何异常输入都不能
// 影响工具可用性——最坏情况只是描述保持原样。
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
