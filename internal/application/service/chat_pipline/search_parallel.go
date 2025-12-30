package chatpipline

import (
	"context"
	"sync"

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
	pipelineInfo(ctx, "SearchParallel", "start", map[string]interface{}{
		"session_id":    chatManage.SessionID,
		"has_entities":  len(chatManage.Entity) > 0,
		"rewrite_query": chatManage.RewriteQuery,
	})

	var wg sync.WaitGroup
	var mu sync.Mutex
	var chunkSearchErr *PluginError
	var entitySearchErr *PluginError

	// Use separate ChatManage copies to avoid concurrent write conflicts
	chunkChatManage := *chatManage
	chunkChatManage.SearchResult = nil

	entityChatManage := *chatManage
	entityChatManage.SearchResult = nil

	// Run chunk search and entity search in parallel
	wg.Add(2)

	// Goroutine 1: Chunk Search
	go func() {
		defer wg.Done()
		err := p.searchPlugin.OnEvent(ctx, types.CHUNK_SEARCH, &chunkChatManage, func() *PluginError {
			return nil
		})
		if err != nil && err != ErrSearchNothing {
			mu.Lock()
			chunkSearchErr = err
			mu.Unlock()
		}
		pipelineInfo(ctx, "SearchParallel", "chunk_search_done", map[string]interface{}{
			"result_count": len(chunkChatManage.SearchResult),
			"has_error":    err != nil && err != ErrSearchNothing,
		})
	}()

	// Goroutine 2: Entity Search (only if entities are available)
	go func() {
		defer wg.Done()
		if len(chatManage.Entity) == 0 {
			pipelineInfo(ctx, "SearchParallel", "entity_search_skip", map[string]interface{}{
				"reason": "no_entities",
			})
			return
		}
		err := p.searchEntityPlugin.OnEvent(ctx, types.ENTITY_SEARCH, &entityChatManage, func() *PluginError {
			return nil
		})
		if err != nil && err != ErrSearchNothing {
			mu.Lock()
			entitySearchErr = err
			mu.Unlock()
		}
		pipelineInfo(ctx, "SearchParallel", "entity_search_done", map[string]interface{}{
			"result_count": len(entityChatManage.SearchResult),
			"has_error":    err != nil && err != ErrSearchNothing,
		})
	}()

	wg.Wait()

	// Merge results from both searches (no concurrent access now)
	chatManage.SearchResult = append(chunkChatManage.SearchResult, entityChatManage.SearchResult...)
	chatManage.SearchResult = removeDuplicateResults(chatManage.SearchResult)

	// Log any errors but don't fail the pipeline if at least one search succeeded
	if chunkSearchErr != nil {
		logger.Warnf(ctx, "[SearchParallel] Chunk search error: %v", chunkSearchErr.Err)
	}
	if entitySearchErr != nil {
		logger.Warnf(ctx, "[SearchParallel] Entity search error: %v", entitySearchErr.Err)
	}

	pipelineInfo(ctx, "SearchParallel", "complete", map[string]interface{}{
		"session_id":          chatManage.SessionID,
		"chunk_results":       len(chunkChatManage.SearchResult),
		"entity_results":      len(entityChatManage.SearchResult),
		"total_results":       len(chatManage.SearchResult),
		"chunk_search_error":  chunkSearchErr != nil,
		"entity_search_error": entitySearchErr != nil,
	})

	// Return error only if both searches failed and we have no results
	if len(chatManage.SearchResult) == 0 {
		if chunkSearchErr != nil {
			return chunkSearchErr
		}
		return ErrSearchNothing
	}

	return next()
}
