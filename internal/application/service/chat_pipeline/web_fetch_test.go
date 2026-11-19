package chatpipeline

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/Tencent/WeKnora/internal/models/rerank"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/require"
)

type recordingPipelineWebClient struct {
	interfaces.WebClient
	mu   sync.Mutex
	urls []string
}

func (c *recordingPipelineWebClient) Fetch(_ context.Context, url string) (*types.WebPage, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.urls = append(c.urls, url)
	return &types.WebPage{Content: "full page for " + url}, nil
}

type pipelineWebService struct{ client interfaces.WebClient }

func (s pipelineWebService) Resolve(context.Context, string) (interfaces.WebClient, error) {
	return s.client, nil
}

type failingPipelineReranker struct{ fixedReranker }

func (*failingPipelineReranker) Rerank(context.Context, string, []string) ([]rerank.RankResult, error) {
	return nil, errors.New("provider unavailable")
}

func TestWebFetchAfterRerankFallback(t *testing.T) {
	for _, tc := range []struct {
		name    string
		modelID string
		service interfaces.ModelService
		outcome types.RerankOutcome
	}{
		{name: "no model", outcome: types.RerankOutcomeNoModel},
		{
			name: "unavailable model", modelID: "rr", service: failingRerankModelService{},
			outcome: types.RerankOutcomeModelUnavailable,
		},
		{
			name: "provider error", modelID: "rr",
			service: &rerankOnlyModelService{reranker: &failingPipelineReranker{}},
			outcome: types.RerankOutcomeModelError,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			low := &types.SearchResult{
				ID:              "https://low.example",
				KnowledgeSource: "web_search", Content: "low snippet", Score: .1,
			}
			high := &types.SearchResult{
				ID:              "https://high.example",
				KnowledgeSource: "web_search", Content: "high snippet", Score: .9,
			}
			cm := &types.ChatManage{
				PipelineRequest: types.PipelineRequest{
					WebSearchEnabled: true, WebFetchEnabled: true,
					WebSearchProviderID: "provider",
					WebFetchTopN:        3, RerankTopK: 1, RerankModelID: tc.modelID,
				},
				PipelineState: types.PipelineState{
					SearchResult: []*types.SearchResult{low, high}, RewriteQuery: "query",
				},
			}
			client := &recordingPipelineWebClient{}
			fetch := &PluginWebFetch{webSearch: pipelineWebService{client: client}}
			ranking := &PluginRerank{modelService: tc.service}
			nextCalled := false
			err := ranking.OnEvent(context.Background(), types.CHUNK_RERANK, cm, func() *PluginError {
				return fetch.OnEvent(context.Background(), types.WEB_FETCH, cm, func() *PluginError {
					nextCalled = true
					return nil
				})
			})
			require.Nil(t, err)
			require.True(t, nextCalled)
			require.Equal(t, tc.outcome, cm.RerankDiagnostics.Outcome)
			require.Equal(t, []string{high.ID}, client.urls, "fetch only fallback candidates that survive merge top-k")
			require.Equal(t, "full page for "+high.ID, high.Content)
			require.Equal(t, "low snippet", low.Content)
			merged := (&PluginMerge{}).selectInputResults(context.Background(), cm)
			require.Equal(t, []*types.SearchResult{high}, merged)
			require.Equal(t, "full page for "+high.ID, merged[0].Content)
		})
	}
}

func TestWebFetchUsesRankedCandidatesAndTopN(t *testing.T) {
	first := &types.SearchResult{
		ID:              "https://first.example",
		KnowledgeSource: "web_search", Content: "first snippet", Score: .2,
	}
	second := &types.SearchResult{
		ID:              "https://second.example",
		KnowledgeSource: "web_search", Content: "second snippet", Score: .9,
	}
	raw := &types.SearchResult{
		ID:              "https://raw.example",
		KnowledgeSource: "web_search", Content: "raw snippet", Score: 1,
	}
	cm := &types.ChatManage{
		PipelineRequest: types.PipelineRequest{
			WebSearchEnabled: true, WebFetchEnabled: true,
			WebSearchProviderID: "provider",
			WebFetchTopN:        1, RerankTopK: 1,
		},
		PipelineState: types.PipelineState{
			SearchResult: []*types.SearchResult{raw},
			RerankResult: []*types.SearchResult{{
				ID:              "kb",
				KnowledgeSource: "file",
			}, first, second},
		},
	}
	client := &recordingPipelineWebClient{}
	plugin := &PluginWebFetch{webSearch: pipelineWebService{client: client}}
	require.Nil(t, plugin.OnEvent(context.Background(), types.WEB_FETCH, cm, func() *PluginError { return nil }))
	require.Equal(t, []string{first.ID}, client.urls, "preserve reranker order instead of re-sorting it")
	require.Equal(t, "full page for "+first.ID, first.Content)
	require.Equal(t, "second snippet", second.Content)
	require.Equal(t, "raw snippet", raw.Content)
}

func TestWebFetchDisabledDoesNotFetchFallback(t *testing.T) {
	for _, flags := range [][2]bool{{false, true}, {true, false}} {
		cm := &types.ChatManage{
			PipelineRequest: types.PipelineRequest{
				WebSearchEnabled: flags[0], WebFetchEnabled: flags[1],
				WebSearchProviderID: "provider",
			},
			PipelineState: types.PipelineState{SearchResult: []*types.SearchResult{{
				ID:              "https://example.com",
				KnowledgeSource: "web_search", Content: "snippet",
			}}},
		}
		client := &recordingPipelineWebClient{}
		plugin := &PluginWebFetch{webSearch: pipelineWebService{client: client}}
		called := false
		require.Nil(t, plugin.OnEvent(context.Background(), types.WEB_FETCH, cm, func() *PluginError {
			called = true
			return nil
		}))
		require.True(t, called)
		require.Empty(t, client.urls)
	}
}

func TestFallbackSelectionPreservesGraphHitsAndInputOrder(t *testing.T) {
	low := &types.SearchResult{ID: "low", Score: .1}
	graph := &types.SearchResult{ID: "graph", MatchType: types.MatchTypeGraph}
	high := &types.SearchResult{ID: "high", Score: .9}
	cm := &types.ChatManage{
		PipelineRequest: types.PipelineRequest{RerankTopK: 1},
		PipelineState:   types.PipelineState{SearchResult: []*types.SearchResult{low, graph, high}},
	}
	require.Equal(t, []*types.SearchResult{high, graph}, selectFallbackSearchResults(cm))
	require.Equal(t, []*types.SearchResult{low, graph, high}, cm.SearchResult)
	require.Equal(t, []*types.SearchResult{high, graph}, (&PluginMerge{}).selectInputResults(context.Background(), cm))
	cm.RerankTopK = 0
	require.Equal(t, []*types.SearchResult{high, low, graph}, selectFallbackSearchResults(cm))
}
