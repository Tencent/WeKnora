package chatpipeline

import (
	"context"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// PluginWebFallback provides on-demand web search for the kb_then_web plan.
//
// It wraps the CHUNK_RERANK stage: reranking of the knowledge-base candidates
// runs first, and only when the knowledge base failed to surface enough
// *relevant* results — judged by the rerank verdict, not by raw recall count —
// does it fetch the web and rerank the combined candidate set.
//
// This is why it MUST be registered before PluginRerank so it sits at the head
// of the CHUNK_RERANK plugin chain and can observe rerank's outcome via next().
//
// The old gate lived in the search stage and fired on raw recall count, so a
// query that recalled many keyword-matched but semantically irrelevant chunks
// (which rerank then discarded entirely) never triggered the web fallback.
type PluginWebFallback struct {
	// fetchWeb performs a web-only search for the current query. It is a field
	// so tests can substitute it without standing up a real web provider.
	fetchWeb func(ctx context.Context, chatManage *types.ChatManage) []*types.SearchResult
}

// NewPluginWebFallback creates and registers the on-demand web fallback plugin.
func NewPluginWebFallback(
	eventManager *EventManager,
	webSearchService interfaces.WebSearchService,
	tenantService interfaces.TenantService,
) *PluginWebFallback {
	// Reuse PluginSearch's web-search path without registering it, mirroring
	// how PluginSearchParallel composes internal, unregistered plugins.
	webSearch := &PluginSearch{
		webSearchService: webSearchService,
		tenantService:    tenantService,
	}
	p := &PluginWebFallback{
		fetchWeb: webSearch.searchWebIfEnabled,
	}
	eventManager.Register(p)
	return p
}

// ActivationEvents returns the event types this plugin handles.
func (p *PluginWebFallback) ActivationEvents() []types.EventType {
	return []types.EventType{types.CHUNK_RERANK}
}

// OnEvent reranks knowledge-base candidates first, then falls back to web
// search on demand when the reranked knowledge-base recall is insufficient.
func (p *PluginWebFallback) OnEvent(ctx context.Context,
	eventType types.EventType, chatManage *types.ChatManage, next func() *PluginError,
) *PluginError {
	// Rerank the knowledge-base candidates first.
	err := next()

	// On-demand web fallback only applies to the kb_then_web plan. Explicit
	// KB/Web/parallel plans and no-retrieval turns are left untouched.
	if chatManage.RetrievalPlan.Mode != types.RetrievalPlanKBThenWeb {
		return err
	}
	// A real rerank failure (not the benign "nothing relevant" signal) must
	// surface unchanged.
	if err != nil && err != ErrSearchNothing {
		return err
	}

	// The knowledge base is sufficient when enough candidates survived
	// reranking. This is the key difference from the old count-based gate:
	// relevance, not raw recall, decides whether the web is consulted.
	if len(chatManage.RerankResult) >= minimumQualifiedKBResults(chatManage.EmbeddingTopK) {
		return err
	}

	webResults := p.fetchWeb(ctx, chatManage)
	pipelineInfo(ctx, "WebFallback", "web_fetch", map[string]interface{}{
		"session_id":  chatManage.SessionID,
		"kb_reranked": len(chatManage.RerankResult),
		"web_results": len(webResults),
	})
	if len(webResults) == 0 {
		// Nothing from the web either — preserve the original rerank verdict
		// (ErrSearchNothing routes into the configured fallback response).
		return err
	}

	// Rerank the knowledge-base candidates together with the fresh web results
	// so the final ordering is consistent across sources. This second next()
	// re-enters the rerank stage only (PluginWebFallback is not re-invoked),
	// so there is no risk of unbounded recursion.
	chatManage.SearchResult = removeDuplicateResults(append(chatManage.SearchResult, webResults...))
	chatManage.RerankResult = nil
	return next()
}
