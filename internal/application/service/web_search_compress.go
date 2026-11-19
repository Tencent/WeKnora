package service

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// webResultCompressor compresses web results through a temporary, hidden knowledge base.
// The UI does not list the knowledge base because the repository filters temporary ones.
type webResultCompressor struct {
	kbSvc   interfaces.KnowledgeBaseService
	knowSvc interfaces.KnowledgeService
}

// NewWebResultCompressor creates the RAG compressor for web results.
func NewWebResultCompressor(
	kbSvc interfaces.KnowledgeBaseService, knowSvc interfaces.KnowledgeService,
) interfaces.WebResultCompressor {
	return &webResultCompressor{kbSvc: kbSvc, knowSvc: knowSvc}
}

// Compress ingests results into the session's temporary knowledge base, retrieves passages
// for questions, and merges them back into the results by URL.
func (c *webResultCompressor) Compress(
	ctx context.Context, state *types.WebSearchCompressionState, questions []string,
	results []*types.WebSearchResult, cfg *types.WebSearchConfig,
) ([]*types.WebSearchResult, error) {
	if len(results) == 0 || len(questions) == 0 {
		return results, nil
	}
	if state == nil {
		return nil, fmt.Errorf("compression state is required for RAG compression")
	}
	if cfg == nil {
		return nil, fmt.Errorf("web search config is required for RAG compression")
	}
	if cfg.EmbeddingModelID == "" {
		return nil, fmt.Errorf("embedding_model_id is required for RAG compression")
	}
	if err := c.ensureTempKB(ctx, state, cfg); err != nil {
		return nil, err
	}
	c.ingest(ctx, state, results)

	matchCount := cfg.DocumentFragments
	if matchCount <= 0 {
		matchCount = 3
	}
	var allRefs []*types.SearchResult
	for _, q := range questions {
		params := types.SearchParams{
			QueryText:        q,
			VectorThreshold:  0.2,
			KeywordThreshold: 0.2,
			MatchCount:       matchCount,
		}
		refs, err := c.kbSvc.HybridSearch(ctx, state.TempKBID, params)
		if err != nil {
			logger.Warnf(ctx, "hybrid search failed for temp KB: %v", err)
			continue
		}
		allRefs = append(allRefs, refs...)
	}

	selected := selectReferencesRoundRobin(results, allRefs, matchCount*len(results))
	return consolidateReferencesByURL(results, selected), nil
}

// ensureTempKB reuses state.TempKBID when it still exists and creates a new one otherwise.
func (c *webResultCompressor) ensureTempKB(
	ctx context.Context, state *types.WebSearchCompressionState, cfg *types.WebSearchConfig,
) error {
	if strings.TrimSpace(state.TempKBID) != "" {
		_, err := c.kbSvc.GetKnowledgeBaseByID(ctx, state.TempKBID)
		if err == nil {
			return nil
		}
		logger.Warnf(ctx, "Temp KB %s not available, recreating: %v", state.TempKBID, err)
	}
	created, err := c.kbSvc.CreateKnowledgeBase(ctx, &types.KnowledgeBase{
		Name:             fmt.Sprintf("tmp-websearch-%d", time.Now().UnixNano()),
		Description:      "Ephemeral search compression KB",
		IsTemporary:      true,
		EmbeddingModelID: cfg.EmbeddingModelID,
	})
	if err != nil {
		return fmt.Errorf("failed to create temporary knowledge base: %w", err)
	}
	state.TempKBID = created.ID
	return nil
}

// ingest stores each result as a passage once per knowledge base, deduplicated by URL.
func (c *webResultCompressor) ingest(
	ctx context.Context, state *types.WebSearchCompressionState, results []*types.WebSearchResult,
) {
	if state.SeenURLs == nil {
		state.SeenURLs = map[string]bool{}
	}
	for _, r := range results {
		sourceURL := r.URL
		if sourceURL != "" && state.SeenURLs[sourceURL] {
			continue
		}
		contentLines := []string{fmt.Sprintf("[sourceUrl]: %s", sourceURL)}
		for _, part := range []string{r.Title, r.Snippet, r.Content} {
			if part = strings.TrimSpace(part); part != "" {
				contentLines = append(contentLines, part)
			}
		}
		knowledge, err := c.knowSvc.CreateKnowledgeFromPassageSync(ctx, state.TempKBID, contentLines, "")
		if err != nil {
			logger.Warnf(ctx, "failed to ingest passage into temp KB: %v", err)
			continue
		}
		if sourceURL != "" {
			state.SeenURLs[sourceURL] = true
		}
		state.KnowledgeIDs = append(state.KnowledgeIDs, knowledge.ID)
	}
}

// selectReferencesRoundRobin selects up to limit references, distributing fairly across source URLs.
func selectReferencesRoundRobin(
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
func consolidateReferencesByURL(
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
