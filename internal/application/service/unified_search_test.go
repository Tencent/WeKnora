package service

import (
	"context"
	"fmt"
	"testing"

	"github.com/Tencent/WeKnora/internal/application/repository"
	apperrors "github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/require"
)

type unifiedSearchKBStub struct {
	interfaces.KnowledgeBaseService
	kb         *types.KnowledgeBase
	kbErr      error
	ragResults []*types.SearchResult
	ragCallCnt int
	lastParams types.SearchParams
}

func (s *unifiedSearchKBStub) GetKnowledgeBaseByID(context.Context, string) (*types.KnowledgeBase, error) {
	return s.kb, s.kbErr
}

func (s *unifiedSearchKBStub) HybridSearch(_ context.Context, _ string, params types.SearchParams) ([]*types.SearchResult, error) {
	s.ragCallCnt++
	s.lastParams = params
	return s.ragResults, nil
}

type unifiedSearchWikiStub struct {
	interfaces.WikiPageService
	pages            []*types.WikiPage
	callCount        int
	literalCallCount int
}

func (s *unifiedSearchWikiStub) SearchPages(context.Context, string, string, int) ([]*types.WikiPage, error) {
	s.callCount++
	return s.pages, nil
}

func (s *unifiedSearchWikiStub) SearchPagesLiteral(context.Context, string, string, int) ([]*types.WikiPage, error) {
	s.callCount++
	s.literalCallCount++
	return s.pages, nil
}

func TestFuseUnifiedSearchResultsUsesRRFAndKeepsProvenance(t *testing.T) {
	rag := []*types.SearchResult{
		{ID: "chunk-a", Content: "A", KnowledgeID: "doc-a", KnowledgeBaseID: "kb-1", KnowledgeTitle: "Document A"},
		{ID: "chunk-b", Content: "B", KnowledgeID: "doc-b", KnowledgeBaseID: "kb-1", KnowledgeTitle: "Document B"},
	}
	wiki := []*types.WikiPage{
		{ID: "page-b", Content: "B", KnowledgeBaseID: "kb-1", Slug: "b", Title: "Wiki B"},
		{ID: "page-c", Content: "C", KnowledgeBaseID: "kb-1", Slug: "c", Title: "Wiki C"},
	}

	results := fuseUnifiedSearchResults(rag, wiki, 0.7, 0.3, 60)

	require.Len(t, results, 3)
	require.Equal(t, "B", results[0].Content)
	require.Len(t, results[0].Sources, 2)
	require.Equal(t, "doc-b", results[0].KnowledgeID)
	require.Equal(t, "page-b", results[0].WikiPageID)
	require.Greater(t, results[0].Score, results[1].Score)
}

func TestFuseUnifiedSearchResultsDeduplicatesNormalizedContent(t *testing.T) {
	rag := []*types.SearchResult{{ID: "chunk-1", Content: " Refund   WINDOW \n"}}
	wiki := []*types.WikiPage{{ID: "page-1", Content: " refund window "}}

	results := fuseUnifiedSearchResults(rag, wiki, 0.7, 0.3, 60)

	require.Len(t, results, 1)
	require.Len(t, results[0].Sources, 2)
	require.Equal(t, "chunk-1", results[0].Sources[0].ID)
	require.Equal(t, "page-1", results[0].Sources[1].ID)
}

func TestFuseUnifiedSearchResultsSkipsEmptyContent(t *testing.T) {
	results := fuseUnifiedSearchResults(
		[]*types.SearchResult{{ID: "empty", Content: "  "}},
		[]*types.WikiPage{{ID: "summary-only", Summary: "Useful summary"}},
		0.7, 0.3, 60,
	)

	require.Len(t, results, 1)
	require.Equal(t, "Useful summary", results[0].Content)
}

func TestFuseUnifiedSearchResultsKeepsAllDuplicateProvenance(t *testing.T) {
	results := fuseUnifiedSearchResults(
		[]*types.SearchResult{
			{ID: "chunk-1", Content: "Shared text", KnowledgeID: "doc-1"},
			{ID: "chunk-2", Content: "shared  text", KnowledgeID: "doc-2"},
			{ID: "chunk-3", Content: "Unique text", KnowledgeID: "doc-3"},
		},
		nil,
		1, 0, 60,
	)

	require.Len(t, results, 2)
	require.Len(t, results[0].Sources, 2)
	require.Equal(t, "doc-1", results[0].Sources[0].KnowledgeID)
	require.Equal(t, "doc-2", results[0].Sources[1].KnowledgeID)
	require.Equal(t, 1.0/61.0, results[0].Score)
	require.Equal(t, 1.0/62.0, results[1].Score)
}

func TestFuseUnifiedSearchResultsKeepsHighestRankedWikiIdentity(t *testing.T) {
	results := fuseUnifiedSearchResults(
		nil,
		[]*types.WikiPage{
			{ID: "page-1", Content: "Shared text", Slug: "first", Title: "First", KnowledgeBaseID: "kb-1"},
			{ID: "page-2", Content: "shared  text", Slug: "second", Title: "Second", KnowledgeBaseID: "kb-1"},
		},
		0, 1, 60,
	)

	require.Len(t, results, 1)
	require.Equal(t, "page-1", results[0].ID)
	require.Equal(t, "page-1", results[0].WikiPageID)
	require.Equal(t, "first", results[0].WikiSlug)
	require.Equal(t, "First", results[0].Title)
	require.Len(t, results[0].Sources, 2)
}

func TestNormalizeUnifiedSearchSourcesDefaultsWithoutExplicitWikiRequirement(t *testing.T) {
	sources, explicit, err := normalizeUnifiedSearchSources(nil)

	require.NoError(t, err)
	require.True(t, sources[types.UnifiedSearchSourceRAG])
	require.True(t, sources[types.UnifiedSearchSourceWiki])
	require.Empty(t, explicit)
}

func TestUnifiedSearchWeightsNormalizeAndValidate(t *testing.T) {
	ragWeight, wikiWeight, rrfK, err := unifiedSearchWeights(types.UnifiedSearchRequest{
		RAGWeight:  2,
		WikiWeight: 1,
		RRFK:       10,
	})

	require.NoError(t, err)
	require.Equal(t, 2.0/3.0, ragWeight)
	require.Equal(t, 1.0/3.0, wikiWeight)
	require.Equal(t, 10, rrfK)

	ragWeight, wikiWeight, _, err = unifiedSearchWeights(types.UnifiedSearchRequest{RAGWeight: 2})
	require.NoError(t, err)
	require.InDelta(t, 2.0/2.3, ragWeight, 1e-12)
	require.InDelta(t, 0.3/2.3, wikiWeight, 1e-12)

	ragWeight, wikiWeight, _, err = unifiedSearchWeights(types.UnifiedSearchRequest{WikiWeight: 1})
	require.NoError(t, err)
	require.InDelta(t, 0.7/1.7, ragWeight, 1e-12)
	require.InDelta(t, 1.0/1.7, wikiWeight, 1e-12)

	_, _, _, err = unifiedSearchWeights(types.UnifiedSearchRequest{RAGWeight: -1})
	require.Error(t, err)
}

func TestActiveUnifiedSearchWeightsRenormalizeSelectedSources(t *testing.T) {
	ragWeight, wikiWeight, err := activeUnifiedSearchWeights(
		map[types.UnifiedSearchSource]bool{types.UnifiedSearchSourceRAG: true},
		0.7,
		0.3,
	)

	require.NoError(t, err)
	require.Equal(t, 1.0, ragWeight)
	require.Zero(t, wikiWeight)

	_, _, err = activeUnifiedSearchWeights(
		map[types.UnifiedSearchSource]bool{types.UnifiedSearchSourceRAG: true},
		0,
		1,
	)
	require.Error(t, err)
}

func TestUnifiedSearchCandidateLimitBoundsOverRetrieval(t *testing.T) {
	require.Equal(t, 20, unifiedSearchCandidateLimit(1))
	require.Equal(t, 20, unifiedSearchCandidateLimit(10))
	require.Equal(t, 30, unifiedSearchCandidateLimit(15))
	require.Equal(t, 50, unifiedSearchCandidateLimit(50))
}

func TestUnifiedSearchSkipsWikiForDefaultDocumentKB(t *testing.T) {
	kbStub := &unifiedSearchKBStub{
		kb:         &types.KnowledgeBase{ID: "kb-1"},
		ragResults: []*types.SearchResult{{ID: "chunk-1", Content: "RAG result"}},
	}
	wikiStub := &unifiedSearchWikiStub{
		pages: []*types.WikiPage{{ID: "page-1", Content: "Wiki result"}},
	}

	results, err := (&unifiedSearchService{kbService: kbStub, wikiService: wikiStub}).Search(
		context.Background(), "kb-1", types.UnifiedSearchRequest{Query: "result"},
	)

	require.NoError(t, err)
	require.Len(t, results, 1)
	require.Equal(t, 1.0/61.0, results[0].Score)
	require.Equal(t, 1, kbStub.ragCallCnt)
	require.Equal(t, 20, kbStub.lastParams.MatchCount)
	require.True(t, kbStub.lastParams.SkipContextEnrichment)
	require.Equal(t, 0, wikiStub.callCount)
}

func TestUnifiedSearchRejectsExplicitWikiForDocumentKB(t *testing.T) {
	kbStub := &unifiedSearchKBStub{kb: &types.KnowledgeBase{ID: "kb-1"}}
	wikiStub := &unifiedSearchWikiStub{}

	_, err := (&unifiedSearchService{kbService: kbStub, wikiService: wikiStub}).Search(
		context.Background(), "kb-1", types.UnifiedSearchRequest{
			Query:   "result",
			Sources: []types.UnifiedSearchSource{types.UnifiedSearchSourceWiki},
		},
	)

	require.Error(t, err)
	require.Equal(t, 0, wikiStub.callCount)
}

func TestUnifiedSearchMapsMissingKBToNotFound(t *testing.T) {
	kbStub := &unifiedSearchKBStub{kbErr: fmt.Errorf("load kb: %w", repository.ErrKnowledgeBaseNotFound)}

	_, err := (&unifiedSearchService{kbService: kbStub, wikiService: &unifiedSearchWikiStub{}}).Search(
		context.Background(), "missing", types.UnifiedSearchRequest{Query: "result"},
	)

	appErr, ok := apperrors.IsAppError(err)
	require.True(t, ok)
	require.Equal(t, apperrors.ErrNotFound, appErr.Code)
}

func TestUnifiedSearchCallsBothSourcesForWikiEnabledKB(t *testing.T) {
	kbStub := &unifiedSearchKBStub{
		kb:         &types.KnowledgeBase{ID: "kb-1", IndexingStrategy: types.IndexingStrategy{WikiEnabled: true}},
		ragResults: []*types.SearchResult{{ID: "chunk-1", Content: "RAG result"}},
	}
	wikiStub := &unifiedSearchWikiStub{
		pages: []*types.WikiPage{{ID: "page-1", Content: "Wiki result"}},
	}

	results, err := (&unifiedSearchService{kbService: kbStub, wikiService: wikiStub}).Search(
		context.Background(), "kb-1", types.UnifiedSearchRequest{Query: "result"},
	)

	require.NoError(t, err)
	require.Len(t, results, 2)
	require.Equal(t, 1, kbStub.ragCallCnt)
	require.Equal(t, 1, wikiStub.callCount)
	require.Equal(t, 1, wikiStub.literalCallCount)
}

func TestUnifiedSearchRenormalizesAfterFilteringEmptyWikiCandidates(t *testing.T) {
	kbStub := &unifiedSearchKBStub{
		kb:         &types.KnowledgeBase{ID: "kb-1", IndexingStrategy: types.IndexingStrategy{WikiEnabled: true}},
		ragResults: []*types.SearchResult{{ID: "chunk-1", Content: "RAG result"}},
	}
	wikiStub := &unifiedSearchWikiStub{
		pages: []*types.WikiPage{{ID: "page-1", Title: "Title-only result"}},
	}

	results, err := (&unifiedSearchService{kbService: kbStub, wikiService: wikiStub}).Search(
		context.Background(), "kb-1", types.UnifiedSearchRequest{Query: "result"},
	)

	require.NoError(t, err)
	require.Len(t, results, 1)
	require.Equal(t, 1.0/61.0, results[0].Score)
}

func TestUnifiedSearchRenormalizesAfterFilteringEmptyRAGCandidates(t *testing.T) {
	kbStub := &unifiedSearchKBStub{
		kb:         &types.KnowledgeBase{ID: "kb-1", IndexingStrategy: types.IndexingStrategy{WikiEnabled: true}},
		ragResults: []*types.SearchResult{{ID: "chunk-1", Content: "  "}},
	}
	wikiStub := &unifiedSearchWikiStub{
		pages: []*types.WikiPage{{ID: "page-1", Content: "Wiki result"}},
	}

	results, err := (&unifiedSearchService{kbService: kbStub, wikiService: wikiStub}).Search(
		context.Background(), "kb-1", types.UnifiedSearchRequest{Query: "result"},
	)

	require.NoError(t, err)
	require.Len(t, results, 1)
	require.Equal(t, 1.0/61.0, results[0].Score)
}
