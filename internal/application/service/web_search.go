package service

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/application/service/web_search"
	"github.com/Tencent/WeKnora/internal/config"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/searchutil"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// WebSearchService provides web search functionality
type WebSearchService struct {
	providers map[string]interfaces.WebSearchProvider
	timeout   int
}

// CompressWithRAG performs RAG-based compression using a temporary, hidden knowledge base.
// The temporary knowledge base is deleted after use. The UI will not list it due to repo filtering.
func (s *WebSearchService) CompressWithRAG(
	ctx context.Context, sessionID string, tempKBID string, questions []string,
	webSearchResults []*types.WebSearchResult, cfg *types.WebSearchConfig,
	kbSvc interfaces.KnowledgeBaseService, knowSvc interfaces.KnowledgeService,
	seenURLs map[string]bool, knowledgeIDs []string,
) (compressed []*types.WebSearchResult, kbID string, newSeen map[string]bool, newIDs []string, err error) {
	if len(webSearchResults) == 0 || len(questions) == 0 {
		return
	}
	if cfg == nil {
		return nil, tempKBID, seenURLs, knowledgeIDs, fmt.Errorf("web search config is required for RAG compression")
	}
	if cfg.EmbeddingModelID == "" {
		return nil, tempKBID, seenURLs, knowledgeIDs, fmt.Errorf("embedding_model_id is required for RAG compression")
	}
	var createdKB *types.KnowledgeBase
	// reuse or create temp KB
	if strings.TrimSpace(tempKBID) != "" {
		createdKB, err = kbSvc.GetKnowledgeBaseByID(ctx, tempKBID)
		if err != nil {
			logger.Warnf(ctx, "Temp KB %s not available, recreating: %v", tempKBID, err)
			createdKB = nil
		}
	}
	if createdKB == nil {
		kb := &types.KnowledgeBase{
			Name:             fmt.Sprintf("tmp-websearch-%d", time.Now().UnixNano()),
			Description:      "Ephemeral search compression KB",
			IsTemporary:      true,
			EmbeddingModelID: cfg.EmbeddingModelID,
		}
		createdKB, err = kbSvc.CreateKnowledgeBase(ctx, kb)
		if err != nil {
			return nil, tempKBID, seenURLs, knowledgeIDs, fmt.Errorf(
				"failed to create temporary knowledge base: %w",
				err,
			)
		}
		tempKBID = createdKB.ID
	}

	// Ingest all web results as passages synchronously
	// dedupe by URL across queries within the same temp KB for this request/session
	if seenURLs == nil {
		seenURLs = map[string]bool{}
	}
	for _, r := range webSearchResults {
		sourceURL := r.URL
		title := strings.TrimSpace(r.Title)
		snippet := strings.TrimSpace(r.Snippet)
		body := strings.TrimSpace(r.Content)
		// skip if already ingested for this KB
		if sourceURL != "" && seenURLs[sourceURL] {
			continue
		}
		contentLines := make([]string, 0, 4)
		contentLines = append(contentLines, fmt.Sprintf("[sourceUrl]: %s", sourceURL))
		if title != "" {
			contentLines = append(contentLines, title)
		}
		if snippet != "" {
			contentLines = append(contentLines, snippet)
		}
		if body != "" {
			contentLines = append(contentLines, body)
		}
		knowledge, err := knowSvc.CreateKnowledgeFromPassageSync(ctx, createdKB.ID, contentLines)
		if err != nil {
			logger.Warnf(ctx, "failed to ingest passage into temp KB: %v", err)
			continue
		}
		if sourceURL != "" {
			seenURLs[sourceURL] = true
		}
		knowledgeIDs = append(knowledgeIDs, knowledge.ID)
	}

	// Retrieve references for questions
	matchCount := cfg.DocumentFragments
	if matchCount <= 0 {
		matchCount = 3
	}
	var allRefs []*types.SearchResult
	for _, q := range questions {
		params := types.SearchParams{
			QueryText:        q,
			VectorThreshold:  0.5,
			KeywordThreshold: 0.5,
			MatchCount:       matchCount,
		}
		results, err := kbSvc.HybridSearch(ctx, tempKBID, params)
		if err != nil {
			logger.Warnf(ctx, "hybrid search failed for temp KB: %v", err)
			continue
		}
		allRefs = append(allRefs, results...)
	}

	// Round-robin select references across the original results by source URL
	selected := s.selectReferencesRoundRobin(webSearchResults, allRefs, matchCount*len(webSearchResults))
	// Consolidate by URL back into the web results
	compressedResults := s.consolidateReferencesByURL(webSearchResults, selected)
	return compressedResults, tempKBID, seenURLs, knowledgeIDs, nil
}

// selectReferencesRoundRobin selects up to limit references, distributing fairly across source URLs.
func (s *WebSearchService) selectReferencesRoundRobin(
	raw []*types.WebSearchResult,
	refs []*types.SearchResult,
	limit int,
) []*types.SearchResult {
	if limit <= 0 || len(refs) == 0 {
		return nil
	}
	// group refs by url marker in content
	urlToRefs := map[string][]*types.SearchResult{}
	for _, r := range refs {
		url := extractSourceURLFromContent(r.Content)
		if url == "" {
			continue
		}
		urlToRefs[url] = append(urlToRefs[url], r)
	}
	// preserve order based on raw results
	order := make([]string, 0, len(raw))
	seen := map[string]bool{}
	for _, r := range raw {
		if r.URL != "" && !seen[r.URL] {
			order = append(order, r.URL)
			seen[r.URL] = true
		}
	}
	var out []*types.SearchResult
	for len(out) < limit {
		progress := false
		for _, url := range order {
			if len(out) >= limit {
				break
			}
			list := urlToRefs[url]
			if len(list) == 0 {
				continue
			}
			out = append(out, list[0])
			urlToRefs[url] = list[1:]
			progress = true
		}
		if !progress {
			break
		}
	}
	return out
}

// consolidateReferencesByURL merges selected references back into the original results grouped by URL.
func (s *WebSearchService) consolidateReferencesByURL(
	raw []*types.WebSearchResult,
	selected []*types.SearchResult,
) []*types.WebSearchResult {
	if len(selected) == 0 {
		return raw
	}
	agg := map[string][]string{}
	for _, ref := range selected {
		url := extractSourceURLFromContent(ref.Content)
		if url == "" {
			continue
		}
		// strip the first marker line to avoid duplication
		agg[url] = append(agg[url], stripMarker(ref.Content))
	}
	// build outputs, preserving raw ordering and metadata
	out := make([]*types.WebSearchResult, 0, len(raw))
	for _, r := range raw {
		parts := agg[r.URL]
		if len(parts) == 0 {
			out = append(out, r)
			continue
		}
		merged := strings.Join(parts, "\n---\n")
		out = append(out, &types.WebSearchResult{
			Title:       r.Title,
			URL:         r.URL,
			Snippet:     r.Snippet,
			Content:     merged,
			Source:      r.Source,
			PublishedAt: r.PublishedAt,
		})
	}
	return out
}

func extractSourceURLFromContent(content string) string {
	if content == "" {
		return ""
	}
	lines := strings.Split(content, "\n")
	if len(lines) == 0 {
		return ""
	}
	first := strings.TrimSpace(lines[0])
	const prefix = "[sourceUrl]: "
	if strings.HasPrefix(first, prefix) {
		return strings.TrimSpace(strings.TrimPrefix(first, prefix))
	}
	return ""
}

func stripMarker(content string) string {
	lines := strings.Split(content, "\n")
	if len(lines) == 0 {
		return content
	}
	if strings.HasPrefix(strings.TrimSpace(lines[0]), "[sourceUrl]: ") {
		return strings.Join(lines[1:], "\n")
	}
	return content
}

// Search performs web search using the specified provider
// This method implements the interface expected by PluginSearch
func (s *WebSearchService) Search(
	ctx context.Context,
	config *types.WebSearchConfig,
	query string,
) ([]*types.WebSearchResult, error) {
	if config == nil {
		return nil, fmt.Errorf("web search config is required")
	}

	provider, ok := s.providers[config.Provider]
	if !ok {
		return nil, fmt.Errorf("web search provider %s is not available", config.Provider)
	}

	// Set timeout
	timeout := time.Duration(s.timeout) * time.Second
	if timeout == 0 {
		timeout = 10 * time.Second
	}

	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	// Perform search
	results, err := provider.Search(ctx, query, config.MaxResults, config.IncludeDate)
	if err != nil {
		return nil, fmt.Errorf("web search failed: %w", err)
	}

	// Apply blacklist filtering
	results = s.filterBlacklist(results, config.Blacklist)

	// Apply compression if needed
	if config.CompressionMethod != "none" && config.CompressionMethod != "" {
		// Compression will be handled later in the integration layer
		// For now, we just return the results
	}

	return results, nil
}

// NewWebSearchService creates a new web search service
func NewWebSearchService(cfg *config.Config, registry *web_search.Registry) (interfaces.WebSearchService, error) {
	timeout := 10 // default timeout
	if cfg.WebSearch != nil && cfg.WebSearch.Timeout > 0 {
		timeout = cfg.WebSearch.Timeout
	}

	// Create all registered providers
	providers, err := registry.CreateAllProviders()
	if err != nil {
		return nil, err
	}

	for id := range providers {
		logger.Infof(context.Background(), "Initialized web search provider: %s", id)
	}

	return &WebSearchService{
		providers: providers,
		timeout:   timeout,
	}, nil
}

// filterBlacklist filters results based on blacklist rules
func (s *WebSearchService) filterBlacklist(
	results []*types.WebSearchResult,
	blacklist []string,
) []*types.WebSearchResult {
	if len(blacklist) == 0 {
		return results
	}

	filtered := make([]*types.WebSearchResult, 0, len(results))

	for _, result := range results {
		shouldFilter := false

		for _, rule := range blacklist {
			if s.matchesBlacklistRule(result.URL, rule) {
				shouldFilter = true
				break
			}
		}

		if !shouldFilter {
			filtered = append(filtered, result)
		}
	}

	return filtered
}

// matchesBlacklistRule checks if a URL matches a blacklist rule
// Supports both pattern matching (e.g., *://*.example.com/*) and regex patterns (e.g., /example\.(net|org)/)
func (s *WebSearchService) matchesBlacklistRule(url, rule string) bool {
	// Check if it's a regex pattern (starts and ends with /)
	if strings.HasPrefix(rule, "/") && strings.HasSuffix(rule, "/") {
		pattern := rule[1 : len(rule)-1]
		matched, err := regexp.MatchString(pattern, url)
		if err != nil {
			logger.Warnf(context.Background(), "Invalid regex pattern in blacklist: %s, error: %v", rule, err)
			return false
		}
		return matched
	}

	// Pattern matching (e.g., *://*.example.com/*)
	pattern := strings.ReplaceAll(rule, "*", ".*")
	pattern = "^" + pattern + "$"
	matched, err := regexp.MatchString(pattern, url)
	if err != nil {
		logger.Warnf(context.Background(), "Invalid pattern in blacklist: %s, error: %v", rule, err)
		return false
	}
	return matched
}

// ConvertWebSearchResults converts WebSearchResult to SearchResult
func ConvertWebSearchResults(webResults []*types.WebSearchResult) []*types.SearchResult {
	return searchutil.ConvertWebSearchResults(
		webResults,
		searchutil.WithSeqFunc(func(idx int) int { return idx }),
	)
}
