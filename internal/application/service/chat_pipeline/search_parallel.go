package chatpipeline

import (
	"context"

	"github.com/Tencent/WeKnora/internal/config"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// PluginSearchParallel implements parallel search functionality combining chunk search and entity search
type PluginSearchParallel struct {
	// Chunk search dependencies
	knowledgeBaseService interfaces.KnowledgeBaseService
	knowledgeService     interfaces.KnowledgeService
	config               *config.Config
	webSearchService     interfaces.WebSearchService
	tenantService        interfaces.TenantService
	sessionService       interfaces.SessionService

	// Entity search dependencies
	graphRepo     interfaces.RetrieveGraphRepository
	chunkRepo     interfaces.ChunkRepository
	knowledgeRepo interfaces.KnowledgeRepository

	// Internal plugins
	searchPlugin       *PluginSearch
	searchEntityPlugin *PluginSearchEntity
}

// NewPluginSearchParallel creates a new parallel search plugin
func NewPluginSearchParallel(
	eventManager *EventManager,
	knowledgeBaseService interfaces.KnowledgeBaseService,
	knowledgeService interfaces.KnowledgeService,
	chunkService interfaces.ChunkService,
	config *config.Config,
	webSearchService interfaces.WebSearchService,
	tenantService interfaces.TenantService,
	sessionService interfaces.SessionService,
	webSearchStateService interfaces.WebSearchStateService,
	webSearchProviderRepo interfaces.WebSearchProviderRepository,
	graphRepository interfaces.RetrieveGraphRepository,
	chunkRepository interfaces.ChunkRepository,
	knowledgeRepository interfaces.KnowledgeRepository,
) *PluginSearchParallel {
	// Create internal plugins without registering them
	searchPlugin := &PluginSearch{
		knowledgeBaseService:  knowledgeBaseService,
		knowledgeService:      knowledgeService,
		chunkService:          chunkService,
		config:                config,
		webSearchService:      webSearchService,
		tenantService:         tenantService,
		sessionService:        sessionService,
		webSearchStateService: webSearchStateService,
		webSearchProviderRepo: webSearchProviderRepo,
	}

	searchEntityPlugin := &PluginSearchEntity{
		graphRepo:     graphRepository,
		chunkRepo:     chunkRepository,
		knowledgeRepo: knowledgeRepository,
	}

	res := &PluginSearchParallel{
		knowledgeBaseService: knowledgeBaseService,
		knowledgeService:     knowledgeService,
		config:               config,
		webSearchService:     webSearchService,
		tenantService:        tenantService,
		sessionService:       sessionService,
		graphRepo:            graphRepository,
		chunkRepo:            chunkRepository,
		knowledgeRepo:        knowledgeRepository,
		searchPlugin:         searchPlugin,
		searchEntityPlugin:   searchEntityPlugin,
	}
	eventManager.Register(res)
	return res
}

// ActivationEvents returns the event types this plugin handles
func (p *PluginSearchParallel) ActivationEvents() []types.EventType {
	return []types.EventType{types.CHUNK_SEARCH_PARALLEL}
}

// OnEvent handles parallel search events - runs chunk search and entity search concurrently
func (p *PluginSearchParallel) OnEvent(ctx context.Context,
	eventType types.EventType, chatManage *types.ChatManage, next func() *PluginError,
) *PluginError {
	// Semantic routing determined that this response does not need retrieval.
	if !chatManage.NeedsRetrieval() {
		pipelineInfo(ctx, "SearchParallel", "skip", map[string]interface{}{
			"session_id": chatManage.SessionID,
			"reason":     "retrieval_plan_none",
		})
		return next()
	}

	pipelineInfo(ctx, "SearchParallel", "start", map[string]interface{}{
		"session_id":    chatManage.SessionID,
		"has_entities":  len(chatManage.Entity) > 0,
		"rewrite_query": chatManage.RewriteQuery,
	})

	// Deep-copy to avoid concurrent read/write on shared slice fields
	chunkCM := chatManage.Clone()
	chunkCM.SearchResult = nil
	if chatManage.RetrievalPlan.Mode == types.RetrievalPlanKBThenWeb {
		// For kb_then_web, run only the KB branches here. The on-demand web
		// fallback is decided AFTER reranking (PluginWebFallback), based on how
		// many KB candidates are actually relevant rather than raw recall count.
		chunkCM.RetrievalPlan = types.RetrievalPlan{
			Mode:       types.RetrievalPlanKBOnly,
			ReasonCode: chatManage.RetrievalPlan.ReasonCode,
		}
	}
	entityCM := chatManage.Clone()
	entityCM.SearchResult = nil

	noop := func() *PluginError { return nil }

	tasks := []ParallelTask{
		{
			Name: "chunk_search",
			Run: func() *PluginError {
				err := p.searchPlugin.OnEvent(ctx, types.CHUNK_SEARCH, chunkCM, noop)
				pipelineInfo(ctx, "SearchParallel", "chunk_search_done", map[string]interface{}{
					"result_count": len(chunkCM.SearchResult),
					"has_error":    err != nil && err != ErrSearchNothing,
				})
				if err == ErrSearchNothing {
					return nil
				}
				return err
			},
		},
		{
			Name: "entity_search",
			Run: func() *PluginError {
				if !chatManage.RetrievalPlan.UsesKB() {
					pipelineInfo(ctx, "SearchParallel", "entity_search_skip", map[string]interface{}{
						"reason": "retrieval_plan_excludes_knowledge_base",
					})
					return nil
				}
				if len(chatManage.Entity) == 0 {
					pipelineInfo(ctx, "SearchParallel", "entity_search_skip", map[string]interface{}{
						"reason": "no_entities",
					})
					return nil
				}
				err := p.searchEntityPlugin.OnEvent(ctx, types.ENTITY_SEARCH, entityCM, noop)
				pipelineInfo(ctx, "SearchParallel", "entity_search_done", map[string]interface{}{
					"result_count": len(entityCM.SearchResult),
					"has_error":    err != nil && err != ErrSearchNothing,
				})
				if err == ErrSearchNothing {
					return nil
				}
				return err
			},
		},
	}

	errs := RunParallel(tasks...)

	// Merge results from both searches. For kb_then_web, the on-demand web
	// fallback happens later in PluginWebFallback, after reranking reveals how
	// many KB candidates are actually relevant.
	chatManage.SearchResult = append(chunkCM.SearchResult, entityCM.SearchResult...)
	chatManage.SearchResult = removeDuplicateResults(chatManage.SearchResult)

	for name, err := range errs {
		logger.Warnf(ctx, "[SearchParallel] %s error: %v", name, err.Err)
	}

	pipelineInfo(ctx, "SearchParallel", "complete", map[string]interface{}{
		"session_id":     chatManage.SessionID,
		"chunk_results":  len(chunkCM.SearchResult),
		"entity_results": len(entityCM.SearchResult),
		"total_results":  len(chatManage.SearchResult),
		"error_count":    len(errs),
	})

	if len(chatManage.SearchResult) == 0 {
		if err, ok := errs["chunk_search"]; ok {
			return err
		}
		return ErrSearchNothing
	}

	return next()
}
