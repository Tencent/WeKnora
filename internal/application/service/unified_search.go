package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/Tencent/WeKnora/internal/application/repository"
	apperrors "github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	secutils "github.com/Tencent/WeKnora/internal/utils"
	"golang.org/x/sync/errgroup"
)

const (
	defaultUnifiedSearchTopK = 10
	maxUnifiedSearchTopK     = 50
	defaultUnifiedSearchRRFK = 60
	defaultRAGWeight         = 0.7
	defaultWikiWeight        = 0.3
)

// unifiedSearchService orchestrates the existing RAG and Wiki search paths.
// It intentionally owns only cross-source ranking and deduplication; each
// source keeps its existing authorization, indexing, and ranking behavior.
type unifiedSearchService struct {
	kbService   interfaces.KnowledgeBaseService
	wikiService interfaces.WikiPageService
}

// NewUnifiedSearchService creates the unified RAG + Wiki search service.
func NewUnifiedSearchService(
	kbService interfaces.KnowledgeBaseService,
	wikiService interfaces.WikiPageService,
) interfaces.UnifiedSearchService {
	return &unifiedSearchService{
		kbService:   kbService,
		wikiService: wikiService,
	}
}

type unifiedSearchCandidate struct {
	result *types.UnifiedSearchResult
	ranks  map[types.UnifiedSearchSource]int
}

// Search runs the requested sources in parallel, then applies weighted RRF
// and exact content-fingerprint deduplication across their ranked lists.
func (s *unifiedSearchService) Search(
	ctx context.Context,
	kbID string,
	req types.UnifiedSearchRequest,
) ([]*types.UnifiedSearchResult, error) {
	query := strings.TrimSpace(req.Query)
	if query == "" {
		return nil, apperrors.NewBadRequestError("query cannot be empty")
	}

	topK, err := unifiedSearchTopK(req.TopK)
	if err != nil {
		return nil, err
	}
	sources, explicitSources, err := normalizeUnifiedSearchSources(req.Sources)
	if err != nil {
		return nil, err
	}
	ragWeight, wikiWeight, rrfK, err := unifiedSearchWeights(req)
	if err != nil {
		return nil, err
	}

	kb, err := s.kbService.GetKnowledgeBaseByID(ctx, kbID)
	if err != nil {
		if errors.Is(err, repository.ErrKnowledgeBaseNotFound) {
			return nil, apperrors.NewNotFoundError("knowledge base not found")
		}
		return nil, err
	}
	if explicitSources[types.UnifiedSearchSourceWiki] && !kb.IsWikiEnabled() {
		return nil, apperrors.NewBadRequestError("wiki search is not enabled for this knowledge base")
	}
	if !kb.IsWikiEnabled() {
		delete(sources, types.UnifiedSearchSourceWiki)
	}
	ragWeight, wikiWeight, err = activeUnifiedSearchWeights(sources, ragWeight, wikiWeight)
	if err != nil {
		return nil, err
	}

	candidateLimit := unifiedSearchCandidateLimit(topK)
	var ragResults []*types.SearchResult
	var wikiPages []*types.WikiPage

	group, groupCtx := errgroup.WithContext(ctx)
	if sources[types.UnifiedSearchSourceRAG] {
		group.Go(func() error {
			results, searchErr := s.kbService.HybridSearch(groupCtx, kbID, types.SearchParams{
				QueryText:             query,
				MatchCount:            candidateLimit,
				SkipContextEnrichment: true,
			})
			if searchErr != nil {
				return searchErr
			}
			ragResults = results
			return nil
		})
	}
	if sources[types.UnifiedSearchSourceWiki] && kb.IsWikiEnabled() {
		group.Go(func() error {
			pages, searchErr := s.wikiService.SearchPagesLiteral(groupCtx, kbID, query, candidateLimit)
			if searchErr != nil {
				return searchErr
			}
			wikiPages = pages
			return nil
		})
	}
	if err := group.Wait(); err != nil {
		return nil, err
	}
	availableSources := map[types.UnifiedSearchSource]bool{
		types.UnifiedSearchSourceRAG:  hasUnifiedRAGCandidates(ragResults),
		types.UnifiedSearchSourceWiki: hasUnifiedWikiCandidates(wikiPages),
	}
	if availableSources[types.UnifiedSearchSourceRAG] || availableSources[types.UnifiedSearchSourceWiki] {
		ragWeight, wikiWeight, err = activeUnifiedSearchWeights(availableSources, ragWeight, wikiWeight)
		if err != nil {
			return nil, err
		}
	}

	results := fuseUnifiedSearchResults(ragResults, wikiPages, ragWeight, wikiWeight, rrfK)
	if len(results) > topK {
		results = results[:topK]
	}
	logger.Infof(ctx, "Unified search completed, kb=%s, sources=%v, rag=%d, wiki=%d, results=%d",
		secutils.SanitizeForLog(kbID), sources, len(ragResults), len(wikiPages), len(results))
	return results, nil
}

func unifiedSearchTopK(topK int) (int, error) {
	if topK == 0 {
		return defaultUnifiedSearchTopK, nil
	}
	if topK < 0 || topK > maxUnifiedSearchTopK {
		return 0, apperrors.NewBadRequestError(fmt.Sprintf("top_k must be between 1 and %d", maxUnifiedSearchTopK))
	}
	return topK, nil
}

func normalizeUnifiedSearchSources(requested []types.UnifiedSearchSource) (map[types.UnifiedSearchSource]bool, map[types.UnifiedSearchSource]bool, error) {
	sources := make(map[types.UnifiedSearchSource]bool, 2)
	explicit := make(map[types.UnifiedSearchSource]bool, 2)
	isDefault := len(requested) == 0
	if len(requested) == 0 {
		requested = []types.UnifiedSearchSource{
			types.UnifiedSearchSourceRAG,
			types.UnifiedSearchSourceWiki,
		}
	}
	for _, source := range requested {
		switch source {
		case types.UnifiedSearchSourceRAG, types.UnifiedSearchSourceWiki:
			sources[source] = true
			if !isDefault {
				explicit[source] = true
			}
		default:
			return nil, nil, apperrors.NewBadRequestError(fmt.Sprintf("unsupported search source %q", source))
		}
	}
	return sources, explicit, nil
}

func unifiedSearchWeights(req types.UnifiedSearchRequest) (float64, float64, int, error) {
	ragWeight, wikiWeight := req.RAGWeight, req.WikiWeight
	if ragWeight < 0 || wikiWeight < 0 {
		return 0, 0, 0, apperrors.NewBadRequestError("rag_weight and wiki_weight must be non-negative")
	}
	if ragWeight == 0 {
		ragWeight = defaultRAGWeight
	}
	if wikiWeight == 0 {
		wikiWeight = defaultWikiWeight
	}
	weightSum := ragWeight + wikiWeight
	rrfK := req.RRFK
	if rrfK == 0 {
		rrfK = defaultUnifiedSearchRRFK
	}
	if rrfK < 1 || rrfK > 1000 {
		return 0, 0, 0, apperrors.NewBadRequestError("rrf_k must be between 1 and 1000")
	}
	return ragWeight / weightSum, wikiWeight / weightSum, rrfK, nil
}

func activeUnifiedSearchWeights(
	sources map[types.UnifiedSearchSource]bool,
	ragWeight, wikiWeight float64,
) (float64, float64, error) {
	if sources[types.UnifiedSearchSourceRAG] && ragWeight <= 0 {
		return 0, 0, apperrors.NewBadRequestError("rag_weight must be positive when rag is selected")
	}
	if sources[types.UnifiedSearchSourceWiki] && wikiWeight <= 0 {
		return 0, 0, apperrors.NewBadRequestError("wiki_weight must be positive when wiki is selected")
	}
	if !sources[types.UnifiedSearchSourceRAG] {
		ragWeight = 0
	}
	if !sources[types.UnifiedSearchSourceWiki] {
		wikiWeight = 0
	}
	weightSum := ragWeight + wikiWeight
	if weightSum <= 0 {
		return 0, 0, apperrors.NewBadRequestError("selected search sources must have a positive weight")
	}
	return ragWeight / weightSum, wikiWeight / weightSum, nil
}

func unifiedSearchCandidateLimit(topK int) int {
	limit := topK * 2
	if limit < 20 {
		return 20
	}
	if limit > maxUnifiedSearchTopK {
		return maxUnifiedSearchTopK
	}
	return limit
}

func fuseUnifiedSearchResults(
	ragResults []*types.SearchResult,
	wikiPages []*types.WikiPage,
	ragWeight, wikiWeight float64,
	rrfK int,
) []*types.UnifiedSearchResult {
	candidates := make(map[string]*unifiedSearchCandidate, len(ragResults)+len(wikiPages))
	addRAGCandidates(candidates, ragResults)
	addWikiCandidates(candidates, wikiPages)

	results := make([]*types.UnifiedSearchResult, 0, len(candidates))
	for _, candidate := range candidates {
		for source, rank := range candidate.ranks {
			weight := ragWeight
			if source == types.UnifiedSearchSourceWiki {
				weight = wikiWeight
			}
			candidate.result.Score += weight / float64(rrfK+rank)
		}
		results = append(results, candidate.result)
	}
	sort.SliceStable(results, func(i, j int) bool {
		if results[i].Score != results[j].Score {
			return results[i].Score > results[j].Score
		}
		if results[i].Title != results[j].Title {
			return results[i].Title < results[j].Title
		}
		if results[i].ID != results[j].ID {
			return results[i].ID < results[j].ID
		}
		return results[i].Content < results[j].Content
	})
	return results
}

func addRAGCandidates(candidates map[string]*unifiedSearchCandidate, results []*types.SearchResult) {
	seen := make(map[string]struct{}, len(results))
	uniqueRank := 0
	for _, item := range results {
		if item == nil || strings.TrimSpace(item.Content) == "" {
			continue
		}
		fingerprint := unifiedContentFingerprint(item.Content)
		if fingerprint == "" {
			continue
		}
		_, alreadyRanked := seen[fingerprint]
		if !alreadyRanked {
			seen[fingerprint] = struct{}{}
			uniqueRank++
		}
		candidate := candidates[fingerprint]
		source := types.UnifiedSearchResultSource{
			Type:        types.UnifiedSearchSourceRAG,
			ID:          item.ID,
			Title:       item.KnowledgeTitle,
			KnowledgeID: item.KnowledgeID,
		}
		if candidate == nil {
			candidate = &unifiedSearchCandidate{
				result: &types.UnifiedSearchResult{
					ID:              item.ID,
					Content:         item.Content,
					Title:           item.KnowledgeTitle,
					KnowledgeBaseID: item.KnowledgeBaseID,
					KnowledgeID:     item.KnowledgeID,
					Sources:         []types.UnifiedSearchResultSource{source},
				},
				ranks: make(map[types.UnifiedSearchSource]int),
			}
			candidates[fingerprint] = candidate
		} else {
			if candidate.result.KnowledgeID == "" {
				candidate.result.KnowledgeID = item.KnowledgeID
			}
			if candidate.result.KnowledgeBaseID == "" {
				candidate.result.KnowledgeBaseID = item.KnowledgeBaseID
			}
			appendUnifiedSource(candidate.result, source)
		}
		if !alreadyRanked {
			candidate.ranks[types.UnifiedSearchSourceRAG] = uniqueRank
		}
	}
}

func hasUnifiedRAGCandidates(results []*types.SearchResult) bool {
	for _, item := range results {
		if item != nil && unifiedContentFingerprint(item.Content) != "" {
			return true
		}
	}
	return false
}

func addWikiCandidates(candidates map[string]*unifiedSearchCandidate, pages []*types.WikiPage) {
	seen := make(map[string]struct{}, len(pages))
	uniqueRank := 0
	for _, page := range pages {
		content := unifiedWikiCandidateContent(page)
		if content == "" {
			continue
		}
		fingerprint := unifiedContentFingerprint(content)
		if fingerprint == "" {
			continue
		}
		_, alreadyRanked := seen[fingerprint]
		if !alreadyRanked {
			seen[fingerprint] = struct{}{}
			uniqueRank++
		}
		candidate := candidates[fingerprint]
		source := types.UnifiedSearchResultSource{
			Type:     types.UnifiedSearchSourceWiki,
			ID:       page.ID,
			Title:    page.Title,
			WikiSlug: page.Slug,
		}
		if candidate == nil {
			candidate = &unifiedSearchCandidate{
				result: &types.UnifiedSearchResult{
					ID:              page.ID,
					Content:         content,
					Summary:         page.Summary,
					Title:           page.Title,
					KnowledgeBaseID: page.KnowledgeBaseID,
					WikiPageID:      page.ID,
					WikiSlug:        page.Slug,
					Sources:         []types.UnifiedSearchResultSource{source},
				},
				ranks: make(map[types.UnifiedSearchSource]int),
			}
			candidates[fingerprint] = candidate
		} else {
			if candidate.result.WikiPageID == "" {
				candidate.result.WikiPageID = page.ID
				candidate.result.WikiSlug = page.Slug
			}
			if candidate.result.KnowledgeBaseID == "" {
				candidate.result.KnowledgeBaseID = page.KnowledgeBaseID
			}
			if candidate.result.Title == "" {
				candidate.result.Title = page.Title
			}
			if candidate.result.Summary == "" {
				candidate.result.Summary = page.Summary
			}
			appendUnifiedSource(candidate.result, source)
		}
		if !alreadyRanked {
			candidate.ranks[types.UnifiedSearchSourceWiki] = uniqueRank
		}
	}
}

func hasUnifiedWikiCandidates(pages []*types.WikiPage) bool {
	for _, page := range pages {
		if unifiedContentFingerprint(unifiedWikiCandidateContent(page)) != "" {
			return true
		}
	}
	return false
}

func unifiedWikiCandidateContent(page *types.WikiPage) string {
	if page == nil {
		return ""
	}
	if content := strings.TrimSpace(page.Content); content != "" {
		return content
	}
	return strings.TrimSpace(page.Summary)
}

func appendUnifiedSource(result *types.UnifiedSearchResult, source types.UnifiedSearchResultSource) {
	for _, existing := range result.Sources {
		if existing.Type == source.Type && existing.ID == source.ID {
			return
		}
	}
	result.Sources = append(result.Sources, source)
}

func unifiedContentFingerprint(content string) string {
	normalized := strings.ToLower(strings.Join(strings.Fields(content), " "))
	if normalized == "" {
		return ""
	}
	digest := sha256.Sum256([]byte(normalized))
	return hex.EncodeToString(digest[:])
}
