package chatpipeline

import (
	"context"
	"strings"
	"sync"
	"time"

	infrawebsearch "github.com/Tencent/WeKnora/internal/infrastructure/web_search"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// PluginWebFetch fetches full page content for reranked web search results.
// It runs between CHUNK_RERANK and CHUNK_MERGE, replacing snippet content
// with the full page text for the top N web results.
type PluginWebFetch struct {
	webSearch interfaces.WebSearchService
}

// pipelineRemoteFetchTimeout matches the local pipeline fetcher budget.
const pipelineRemoteFetchTimeout = 15 * time.Second

// NewPluginWebFetch creates and registers a new PluginWebFetch instance.
func NewPluginWebFetch(eventManager *EventManager, webSearch interfaces.WebSearchService) *PluginWebFetch {
	res := &PluginWebFetch{webSearch: webSearch}
	eventManager.Register(res)
	return res
}

// ActivationEvents returns the event types this plugin handles
func (p *PluginWebFetch) ActivationEvents() []types.EventType {
	return []types.EventType{types.WEB_FETCH}
}

// OnEvent handles the WEB_FETCH event
func (p *PluginWebFetch) OnEvent(
	ctx context.Context,
	eventType types.EventType,
	chatManage *types.ChatManage,
	next func() *PluginError,
) *PluginError {
	if !chatManage.WebFetchEnabled || !chatManage.WebSearchEnabled {
		pipelineInfo(ctx, "WebFetch", "skip", map[string]any{"reason": "disabled"})
		return next()
	}

	topN := chatManage.WebFetchTopN
	if topN <= 0 {
		topN = 3
	}

	// Find web search results in reranked results
	var webResults []*types.SearchResult
	for _, r := range chatManage.RerankResult {
		if strings.ToLower(r.KnowledgeSource) == "web_search" {
			webResults = append(webResults, r)
			if len(webResults) >= topN {
				break
			}
		}
	}

	if len(webResults) == 0 {
		pipelineInfo(ctx, "WebFetch", "skip", map[string]any{"reason": "no_web_results"})
		return next()
	}

	logger.Infof(ctx, "[PIPELINE] stage=WebFetch action=start count=%d", len(webResults))

	// Fetch in parallel
	type fetchResult struct {
		idx     int
		content string
		err     error
	}
	results := make([]fetchResult, len(webResults))
	var wg sync.WaitGroup
	fetcher := p.pageFetcher(ctx, chatManage.WebSearchProviderID)

	for i, r := range webResults {
		wg.Add(1)
		go func(idx int, sr *types.SearchResult) {
			defer wg.Done()
			fetchURL := sr.ID // web search results use URL as ID
			if fetchURL == "" {
				return
			}
			content, err := fetchPage(ctx, fetcher, fetchURL)
			results[idx] = fetchResult{idx: idx, content: content, err: err}
		}(i, r)
	}
	wg.Wait()

	// Replace snippet content with fetched full content
	fetchedCount := 0
	for _, fr := range results {
		if fr.err != nil {
			logger.Warnf(ctx, "[PIPELINE] stage=WebFetch action=fetch_failed url=%s err=%v",
				webResults[fr.idx].ID, fr.err)
			continue
		}
		if fr.content == "" {
			continue
		}
		// Truncate to reasonable size for LLM context. Cut on a rune
		// boundary: slicing bytes split multi-byte characters.
		content := fr.content
		if runes := []rune(content); len(runes) > webFetchMaxRunes {
			content = string(runes[:webFetchMaxRunes]) + "\n...(truncated)"
		}
		webResults[fr.idx].Content = content
		fetchedCount++
	}

	pipelineInfo(ctx, "WebFetch", "complete", map[string]any{
		"fetched": fetchedCount,
		"total":   len(webResults),
	})
	return next()
}

// webFetchMaxRunes caps the fetched page text kept per web result.
const webFetchMaxRunes = 8000

// pageFetcher uses the local reader when no provider is selected or it cannot be loaded.
func (p *PluginWebFetch) pageFetcher(ctx context.Context, providerID string) interfaces.WebFetcher {
	if p.webSearch == nil || strings.TrimSpace(providerID) == "" {
		return infrawebsearch.LocalFetcher()
	}
	client, err := p.webSearch.Resolve(ctx, providerID)
	if err != nil {
		logger.Warnf(ctx, "[WebFetch] provider %s unavailable, using local reader: %v", providerID, err)
		return infrawebsearch.LocalFetcher()
	}
	return client
}

// fetchPage reads through the configured provider inside the quick-answer budget.
func fetchPage(ctx context.Context, fetcher interfaces.WebFetcher, fetchURL string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, pipelineRemoteFetchTimeout)
	defer cancel()
	page, err := fetcher.Fetch(infrawebsearch.WithPipelineFetch(ctx), fetchURL)
	if err != nil {
		return "", err
	}
	return page.Content, nil
}
