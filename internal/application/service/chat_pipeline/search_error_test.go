package chatpipeline

import (
	"context"
	"errors"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

type failingSearchKnowledgeBaseService struct {
	interfaces.KnowledgeBaseService
	err error
}

func (s *failingSearchKnowledgeBaseService) GetKnowledgeBasesByIDsOnly(
	context.Context, []string,
) ([]*types.KnowledgeBase, error) {
	return []*types.KnowledgeBase{{
		ID:               "kb-1",
		Type:             types.KnowledgeBaseTypeDocument,
		EmbeddingModelID: "embedding-1",
		IndexingStrategy: types.DefaultIndexingStrategy(),
	}}, nil
}

func (s *failingSearchKnowledgeBaseService) ResolveEmbeddingModelKeys(
	context.Context, []*types.KnowledgeBase,
) map[string]string {
	return map[string]string{"kb-1": "doubao|https://ark.cn-beijing.volces.com/api/v3"}
}

func (s *failingSearchKnowledgeBaseService) GetQueryEmbedding(
	context.Context, string, string,
) ([]float32, error) {
	return nil, s.err
}

func (s *failingSearchKnowledgeBaseService) HybridSearch(
	context.Context, string, types.SearchParams,
) ([]*types.SearchResult, error) {
	return nil, s.err
}

// embedding 失败会先降级为关键词检索；本用例中关键词检索也失败，
// 此时必须仍报 search_failed 并保留根因，而不是伪装成"无结果"。
func TestSearchEmbeddingFailureIsNotReportedAsNoResults(t *testing.T) {
	rootCause := errors.New("embedding endpoint unavailable")
	plugin := &PluginSearch{
		knowledgeBaseService: &failingSearchKnowledgeBaseService{err: rootCause},
	}
	chatManage := &types.ChatManage{
		PipelineRequest: types.PipelineRequest{
			SearchTargets: types.SearchTargets{{
				Type:            types.SearchTargetTypeKnowledgeBase,
				KnowledgeBaseID: "kb-1",
			}},
			EmbeddingTopK: 10,
		},
		PipelineState: types.PipelineState{RewriteQuery: "精简邮件效果"},
	}

	err := plugin.OnEvent(context.Background(), types.CHUNK_SEARCH, chatManage, func() *PluginError {
		return nil
	})
	if err == nil || err.ErrorType != ErrSearch.ErrorType {
		t.Fatalf("expected search_failed, got %#v", err)
	}
	if !errors.Is(err.Err, rootCause) {
		t.Fatalf("expected root cause to be preserved, got %v", err.Err)
	}
}

type degradingSearchKnowledgeBaseService struct {
	interfaces.KnowledgeBaseService
	embedErr     error
	gotParams    *types.SearchParams
	searchResult []*types.SearchResult
}

func (s *degradingSearchKnowledgeBaseService) GetKnowledgeBasesByIDsOnly(
	context.Context, []string,
) ([]*types.KnowledgeBase, error) {
	return []*types.KnowledgeBase{{
		ID:               "kb-1",
		Type:             types.KnowledgeBaseTypeDocument,
		EmbeddingModelID: "embedding-1",
		IndexingStrategy: types.DefaultIndexingStrategy(),
	}}, nil
}

func (s *degradingSearchKnowledgeBaseService) ResolveEmbeddingModelKeys(
	context.Context, []*types.KnowledgeBase,
) map[string]string {
	return map[string]string{"kb-1": "doubao|https://ark.cn-beijing.volces.com/api/v3"}
}

func (s *degradingSearchKnowledgeBaseService) GetQueryEmbedding(
	context.Context, string, string,
) ([]float32, error) {
	return nil, s.embedErr
}

func (s *degradingSearchKnowledgeBaseService) HybridSearch(
	_ context.Context, _ string, params types.SearchParams,
) ([]*types.SearchResult, error) {
	s.gotParams = &params
	return s.searchResult, nil
}

// embedding API 限流/长尾时检索必须降级为纯关键词模式返回结果，
// 而不是让整次 knowledge-search 以 500 失败（评测中三次复现的故障）。
func TestSearchEmbeddingFailureDegradesToKeywordSearch(t *testing.T) {
	svc := &degradingSearchKnowledgeBaseService{
		embedErr: errors.New("embedding rate limited"),
		searchResult: []*types.SearchResult{
			{ID: "chunk-1", Content: "关键词命中的内容", KnowledgeID: "k-1"},
		},
	}
	plugin := &PluginSearch{knowledgeBaseService: svc}
	chatManage := &types.ChatManage{
		PipelineRequest: types.PipelineRequest{
			SearchTargets: types.SearchTargets{{
				Type:            types.SearchTargetTypeKnowledgeBase,
				KnowledgeBaseID: "kb-1",
			}},
			EmbeddingTopK: 10,
		},
		PipelineState: types.PipelineState{RewriteQuery: "树状筛选器 新建入口"},
	}

	err := plugin.OnEvent(context.Background(), types.CHUNK_SEARCH, chatManage, func() *PluginError {
		return nil
	})
	if err != nil {
		t.Fatalf("expected degraded keyword search to succeed, got %#v", err)
	}
	if len(chatManage.SearchResult) != 1 || chatManage.SearchResult[0].ID != "chunk-1" {
		t.Fatalf("expected keyword results to be returned, got %#v", chatManage.SearchResult)
	}
	if svc.gotParams == nil || !svc.gotParams.DisableVectorMatch {
		t.Fatalf("expected DisableVectorMatch=true after embedding failure, got %#v", svc.gotParams)
	}
	if len(svc.gotParams.QueryEmbedding) != 0 {
		t.Fatalf("expected empty query embedding in degraded mode")
	}
}

type vectorOnlySearchKnowledgeBaseService struct {
	interfaces.KnowledgeBaseService
	embedErr     error
	hybridCalled bool
}

func (s *vectorOnlySearchKnowledgeBaseService) GetKnowledgeBasesByIDsOnly(
	context.Context, []string,
) ([]*types.KnowledgeBase, error) {
	return []*types.KnowledgeBase{{
		ID:               "faq-1",
		Type:             types.KnowledgeBaseTypeFAQ,
		EmbeddingModelID: "embedding-1",
		IndexingStrategy: types.IndexingStrategy{VectorEnabled: true},
	}}, nil
}

func (s *vectorOnlySearchKnowledgeBaseService) ResolveEmbeddingModelKeys(
	context.Context, []*types.KnowledgeBase,
) map[string]string {
	return map[string]string{"faq-1": "doubao|https://ark.cn-beijing.volces.com/api/v3"}
}

func (s *vectorOnlySearchKnowledgeBaseService) GetQueryEmbedding(
	context.Context, string, string,
) ([]float32, error) {
	return nil, s.embedErr
}

func (s *vectorOnlySearchKnowledgeBaseService) HybridSearch(
	context.Context, string, types.SearchParams,
) ([]*types.SearchResult, error) {
	s.hybridCalled = true
	return nil, nil
}

func TestSearchEmbeddingFailureOnVectorOnlyTargetPreservesRootCause(t *testing.T) {
	rootCause := errors.New("embedding endpoint unavailable")
	svc := &vectorOnlySearchKnowledgeBaseService{embedErr: rootCause}
	plugin := &PluginSearch{knowledgeBaseService: svc}
	chatManage := &types.ChatManage{
		PipelineRequest: types.PipelineRequest{
			SearchTargets: types.SearchTargets{{
				Type:            types.SearchTargetTypeKnowledgeBase,
				KnowledgeBaseID: "faq-1",
			}},
			EmbeddingTopK: 10,
		},
		PipelineState: types.PipelineState{RewriteQuery: "退款规则"},
	}

	err := plugin.OnEvent(context.Background(), types.CHUNK_SEARCH, chatManage, func() *PluginError {
		return nil
	})
	if err == nil || err.ErrorType != ErrSearch.ErrorType {
		t.Fatalf("expected search_failed, got %#v", err)
	}
	if !errors.Is(err.Err, rootCause) {
		t.Fatalf("expected root cause to be preserved, got %v", err.Err)
	}
	if svc.hybridCalled {
		t.Fatal("vector-only target must not run a disabled-vector search")
	}
}

type partialSearchKnowledgeBaseService struct {
	interfaces.KnowledgeBaseService
	rootCause error
}

func (s *partialSearchKnowledgeBaseService) GetKnowledgeBasesByIDsOnly(
	_ context.Context, ids []string,
) ([]*types.KnowledgeBase, error) {
	result := make([]*types.KnowledgeBase, 0, len(ids))
	for _, id := range ids {
		result = append(result, &types.KnowledgeBase{
			ID:               id,
			Type:             types.KnowledgeBaseTypeDocument,
			EmbeddingModelID: "embedding-" + id,
			IndexingStrategy: types.DefaultIndexingStrategy(),
		})
	}
	return result, nil
}

func (s *partialSearchKnowledgeBaseService) ResolveEmbeddingModelKeys(
	_ context.Context, kbs []*types.KnowledgeBase,
) map[string]string {
	result := make(map[string]string, len(kbs))
	for _, kb := range kbs {
		result[kb.ID] = "model-" + kb.ID
	}
	return result
}

func (s *partialSearchKnowledgeBaseService) GetQueryEmbedding(
	context.Context, string, string,
) ([]float32, error) {
	return []float32{1}, nil
}

func (s *partialSearchKnowledgeBaseService) HybridSearch(
	_ context.Context, id string, _ types.SearchParams,
) ([]*types.SearchResult, error) {
	if id == "kb-bad" {
		return nil, s.rootCause
	}
	return []*types.SearchResult{{ID: "chunk-good", Content: "可用结果", KnowledgeID: "knowledge-good"}}, nil
}

type stubWebSearchService struct {
	interfaces.WebSearchService
	results []*types.WebSearchResult
}

func (s *stubWebSearchService) Search(
	_ context.Context, _ string, _ *types.WebSearchConfig, _ string,
) ([]*types.WebSearchResult, error) {
	return s.results, nil
}

func TestSearchKeepsWebResultsWhenKnowledgeBaseFails(t *testing.T) {
	rootCause := errors.New("kb store unavailable")
	plugin := &PluginSearch{
		knowledgeBaseService: &failingSearchKnowledgeBaseService{err: rootCause},
		webSearchService: &stubWebSearchService{
			results: []*types.WebSearchResult{{
				URL:     "https://example.com/doc",
				Title:   "Web hit",
				Content: "web content",
			}},
		},
		tenantService: &struct{ interfaces.TenantService }{},
	}
	chatManage := &types.ChatManage{
		PipelineRequest: types.PipelineRequest{
			SearchTargets: types.SearchTargets{{
				Type:            types.SearchTargetTypeKnowledgeBase,
				KnowledgeBaseID: "kb-1",
			}},
			EmbeddingTopK:       10,
			WebSearchEnabled:    true,
			WebSearchProviderID: "provider-1",
		},
		PipelineState: types.PipelineState{RewriteQuery: "并发检索"},
	}
	nextCalled := false

	err := plugin.OnEvent(context.Background(), types.CHUNK_SEARCH, chatManage, func() *PluginError {
		nextCalled = true
		return nil
	})
	if err != nil {
		t.Fatalf("expected web results to keep pipeline alive, got %#v", err)
	}
	if !nextCalled {
		t.Fatal("expected pipeline to continue with web results")
	}
	if len(chatManage.SearchResult) != 1 || chatManage.SearchResult[0].ID != "https://example.com/doc" {
		t.Fatalf("expected web search result, got %#v", chatManage.SearchResult)
	}
}

type wikiOnlySearchKnowledgeBaseService struct {
	interfaces.KnowledgeBaseService
	embedErr     error
	hybridCalled bool
}

func (s *wikiOnlySearchKnowledgeBaseService) GetKnowledgeBasesByIDsOnly(
	context.Context, []string,
) ([]*types.KnowledgeBase, error) {
	return []*types.KnowledgeBase{{
		ID:               "wiki-1",
		Type:             types.KnowledgeBaseTypeDocument,
		EmbeddingModelID: "embedding-1",
		IndexingStrategy: types.IndexingStrategy{WikiEnabled: true},
	}}, nil
}

func (s *wikiOnlySearchKnowledgeBaseService) ResolveEmbeddingModelKeys(
	context.Context, []*types.KnowledgeBase,
) map[string]string {
	return map[string]string{"wiki-1": "doubao|https://ark.cn-beijing.volces.com/api/v3"}
}

func (s *wikiOnlySearchKnowledgeBaseService) GetQueryEmbedding(
	context.Context, string, string,
) ([]float32, error) {
	return nil, s.embedErr
}

func (s *wikiOnlySearchKnowledgeBaseService) HybridSearch(
	context.Context, string, types.SearchParams,
) ([]*types.SearchResult, error) {
	s.hybridCalled = true
	return nil, nil
}

func TestSearchEmbeddingFailureOnWikiOnlyTargetReportsNoResults(t *testing.T) {
	svc := &wikiOnlySearchKnowledgeBaseService{
		embedErr: errors.New("embedding endpoint unavailable"),
	}
	plugin := &PluginSearch{knowledgeBaseService: svc}
	chatManage := &types.ChatManage{
		PipelineRequest: types.PipelineRequest{
			SearchTargets: types.SearchTargets{{
				Type:            types.SearchTargetTypeKnowledgeBase,
				KnowledgeBaseID: "wiki-1",
			}},
			EmbeddingTopK: 10,
		},
		PipelineState: types.PipelineState{RewriteQuery: "wiki only"},
	}

	err := plugin.OnEvent(context.Background(), types.CHUNK_SEARCH, chatManage, func() *PluginError {
		return nil
	})
	if err != ErrSearchNothing {
		t.Fatalf("expected search_nothing for wiki-only KB, got %#v", err)
	}
	if !svc.hybridCalled {
		t.Fatal("wiki-only target should still delegate empty recall to HybridSearch")
	}
}

func TestSearchKeepsSuccessfulResultsWhenAnotherTargetFails(t *testing.T) {
	rootCause := errors.New("one store unavailable")
	plugin := &PluginSearch{knowledgeBaseService: &partialSearchKnowledgeBaseService{rootCause: rootCause}}
	chatManage := &types.ChatManage{
		PipelineRequest: types.PipelineRequest{
			SearchTargets: types.SearchTargets{
				{Type: types.SearchTargetTypeKnowledgeBase, KnowledgeBaseID: "kb-good"},
				{Type: types.SearchTargetTypeKnowledgeBase, KnowledgeBaseID: "kb-bad"},
			},
			EmbeddingTopK: 10,
		},
		PipelineState: types.PipelineState{RewriteQuery: "部分成功"},
	}
	nextCalled := false

	err := plugin.OnEvent(context.Background(), types.CHUNK_SEARCH, chatManage, func() *PluginError {
		nextCalled = true
		return nil
	})
	if err != nil {
		t.Fatalf("expected partial success to continue, got %#v", err)
	}
	if !nextCalled {
		t.Fatal("expected pipeline to continue with successful results")
	}
	if len(chatManage.SearchResult) != 1 || chatManage.SearchResult[0].ID != "chunk-good" {
		t.Fatalf("expected successful target result, got %#v", chatManage.SearchResult)
	}
}
