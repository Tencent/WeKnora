package chatpipline

import (
	"context"
	"strings"

	"github.com/Tencent/WeKnora/internal/config"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// PluginSearch implements search functionality for chat pipeline
type PluginSearch struct {
	knowledgeBaseService interfaces.KnowledgeBaseService
	modelService         interfaces.ModelService
	config               *config.Config
}

func NewPluginSearch(eventManager *EventManager,
	knowledgeBaseService interfaces.KnowledgeBaseService,
	modelService interfaces.ModelService,
	config *config.Config,
) *PluginSearch {
	res := &PluginSearch{
		knowledgeBaseService: knowledgeBaseService,
		modelService:         modelService,
		config:               config,
	}
	eventManager.Register(res)
	return res
}

// ActivationEvents returns the event types this plugin handles
func (p *PluginSearch) ActivationEvents() []types.EventType {
	return []types.EventType{types.CHUNK_SEARCH}
}

// OnEvent handles search events in the chat pipeline
func (p *PluginSearch) OnEvent(ctx context.Context,
	eventType types.EventType, chatManage *types.ChatManage, next func() *PluginError,
) *PluginError {
	// Prepare search parameters
	searchParams := types.SearchParams{
		QueryText:        strings.TrimSpace(chatManage.RewriteQuery),
		VectorThreshold:  chatManage.VectorThreshold,
		KeywordThreshold: chatManage.KeywordThreshold,
		MatchCount:       chatManage.EmbeddingTopK,
	}
	logger.Infof(ctx, "Search parameters: %v", searchParams)

	// Perform initial hybrid search
	searchResults, err := p.knowledgeBaseService.HybridSearch(ctx, chatManage.KnowledgeBaseID, searchParams)
	logger.Infof(ctx, "Search results count: %d, error: %v", len(searchResults), err)
	if err != nil {
		return ErrSearch.WithError(err)
	}
	chatManage.SearchResult = searchResults
	logger.Infof(ctx, "Search result count: %d", len(chatManage.SearchResult))

	// Add relevant results from chat history
	historyResult := p.getSearchResultFromHistory(chatManage)
	if historyResult != nil {
		logger.Infof(ctx, "Add history result, result count: %d", len(historyResult))
		chatManage.SearchResult = append(chatManage.SearchResult, historyResult...)
	}

	// Try search with processed query if different from rewrite query
	if chatManage.RewriteQuery != chatManage.ProcessedQuery {
		searchParams.QueryText = strings.TrimSpace(chatManage.ProcessedQuery)
		searchResults, err = p.knowledgeBaseService.HybridSearch(ctx, chatManage.KnowledgeBaseID, searchParams)
		logger.Infof(ctx, "Search by processed query: %s, results count: %d, error: %v",
			searchParams.QueryText, len(searchResults), err,
		)
		if err != nil {
			return ErrSearch.WithError(err)
		}
		chatManage.SearchResult = append(chatManage.SearchResult, searchResults...)
	}

	// remove duplicate results
	chatManage.SearchResult = removeDuplicateResults(chatManage.SearchResult)

	// Return if we have results
	if len(chatManage.SearchResult) != 0 {
		logger.Infof(
			ctx,
			"Get search results, count: %d, session_id: %s",
			len(chatManage.SearchResult), chatManage.SessionID,
		)
		return next()
	}
	logger.Infof(ctx, "No search result, session_id: %s", chatManage.SessionID)
	return ErrSearchNothing
}

// getSearchResultFromHistory retrieves relevant knowledge references from chat history
func (p *PluginSearch) getSearchResultFromHistory(chatManage *types.ChatManage) []*types.SearchResult {
	if len(chatManage.History) == 0 {
		return nil
	}
	// Search history in reverse chronological order
	for i := len(chatManage.History) - 1; i >= 0; i-- {
		if len(chatManage.History[i].KnowledgeReferences) > 0 {
			// Mark all references as history matches
			for _, reference := range chatManage.History[i].KnowledgeReferences {
				reference.MatchType = types.MatchTypeHistory
			}
			return chatManage.History[i].KnowledgeReferences
		}
	}
	return nil
}

func removeDuplicateResults(results []*types.SearchResult) []*types.SearchResult {
	seen := make(map[string]bool)
	var uniqueResults []*types.SearchResult
	for _, result := range results {
		if !seen[result.ID] {
			seen[result.ID] = true
			uniqueResults = append(uniqueResults, result)
		}
	}
	return uniqueResults
}
