package chatpipeline

import (
	"context"
	"strings"
	"sync"

	"github.com/Tencent/WeKnora/internal/infrastructure/web_fetch"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
)

// PluginWebFetch fetches full page content for reranked web search results.
// It runs between CHUNK_RERANK and CHUNK_MERGE, replacing snippet content
// with the full page text for the top N web results.
type PluginWebFetch struct{}

// NewPluginWebFetch creates and registers a new PluginWebFetch instance
func NewPluginWebFetch(eventManager *EventManager) *PluginWebFetch {
	res := &PluginWebFetch{}
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

	for i, r := range webResults {
		wg.Add(1)
		go func(idx int, sr *types.SearchResult) {
			defer wg.Done()
			fetchURL := sr.ID // web search results use URL as ID
			if fetchURL == "" {
				return
			}
			content, err := web_fetch.FetchURLContent(ctx, fetchURL)
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
		// Truncate to reasonable size for LLM context
		content := fr.content
		if len(content) > 8000 {
			content = content[:8000] + "\n...(truncated)"
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
