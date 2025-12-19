package chatpipline

import (
	"context"
	"sync"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// PluginSearch implements search functionality for chat pipeline
type PluginSearchEntity struct {
	graphRepo     interfaces.RetrieveGraphRepository
	chunkRepo     interfaces.ChunkRepository
	knowledgeRepo interfaces.KnowledgeRepository
}

// NewPluginSearchEntity creates a new plugin search entity
func NewPluginSearchEntity(
	eventManager *EventManager,
	graphRepository interfaces.RetrieveGraphRepository,
	chunkRepository interfaces.ChunkRepository,
	knowledgeRepository interfaces.KnowledgeRepository,
) *PluginSearchEntity {
	res := &PluginSearchEntity{
		graphRepo:     graphRepository,
		chunkRepo:     chunkRepository,
		knowledgeRepo: knowledgeRepository,
	}
	eventManager.Register(res)
	return res
}

// ActivationEvents returns the list of event types this plugin responds to
func (p *PluginSearchEntity) ActivationEvents() []types.EventType {
	return []types.EventType{types.ENTITY_SEARCH}
}

// OnEvent processes triggered events
func (p *PluginSearchEntity) OnEvent(ctx context.Context,
	eventType types.EventType, chatManage *types.ChatManage, next func() *PluginError,
) *PluginError {
	entity := chatManage.Entity
	if len(entity) == 0 {
		logger.Infof(ctx, "No entity found")
		return next()
	}

	// Use EntityKBIDs (knowledge bases with ExtractConfig enabled)
	knowledgeBaseIDs := chatManage.EntityKBIDs
	// Use EntityKnowledge (KnowledgeID -> KnowledgeBaseID mapping for graph-enabled files)
	entityKnowledge := chatManage.EntityKnowledge

	if len(knowledgeBaseIDs) == 0 && len(entityKnowledge) == 0 {
		logger.Warnf(ctx, "No knowledge base IDs or knowledge IDs with ExtractConfig enabled for entity search")
		return next()
	}

	// Parallel search across multiple knowledge bases and individual files
	var wg sync.WaitGroup
	var mu sync.Mutex
	var allNodes []*types.GraphNode
	var allRelations []*types.GraphRelation

	// If specific KnowledgeIDs are provided, search by individual files
	if len(entityKnowledge) > 0 {
		logger.Infof(ctx, "Searching entities across %d knowledge file(s)", len(entityKnowledge))
		for knowledgeID, kbID := range entityKnowledge {
			wg.Add(1)
			go func(knowledgeBaseID, knowledgeID string) {
				defer wg.Done()

				graph, err := p.graphRepo.SearchNode(ctx, types.NameSpace{
					KnowledgeBase: knowledgeBaseID,
					Knowledge:     knowledgeID,
				}, entity)
				if err != nil {
					logger.Errorf(ctx, "Failed to search entity in Knowledge %s: %v", knowledgeID, err)
					return
				}

				logger.Infof(
					ctx,
					"Knowledge %s entity search result count: %d nodes, %d relations",
					knowledgeID,
					len(graph.Node),
					len(graph.Relation),
				)

				mu.Lock()
				allNodes = append(allNodes, graph.Node...)
				allRelations = append(allRelations, graph.Relation...)
				mu.Unlock()
			}(kbID, knowledgeID)
		}
	} else {
		// Otherwise, search by knowledge base
		logger.Infof(ctx, "Searching entities across %d knowledge base(s): %v", len(knowledgeBaseIDs), knowledgeBaseIDs)
		for _, kbID := range knowledgeBaseIDs {
			wg.Add(1)
			go func(knowledgeBaseID string) {
				defer wg.Done()

				graph, err := p.graphRepo.SearchNode(ctx, types.NameSpace{KnowledgeBase: knowledgeBaseID}, entity)
				if err != nil {
					logger.Errorf(ctx, "Failed to search entity in KB %s: %v", knowledgeBaseID, err)
					return
				}

				logger.Infof(
					ctx,
					"KB %s entity search result count: %d nodes, %d relations",
					knowledgeBaseID,
					len(graph.Node),
					len(graph.Relation),
				)

				mu.Lock()
				allNodes = append(allNodes, graph.Node...)
				allRelations = append(allRelations, graph.Relation...)
				mu.Unlock()
			}(kbID)
		}
	}

	wg.Wait()

	// Merge graph data
	chatManage.GraphResult = &types.GraphData{
		Node:     allNodes,
		Relation: allRelations,
	}
	logger.Infof(ctx, "Total entity search result: %d nodes, %d relations", len(allNodes), len(allRelations))

	chunkIDs := filterSeenChunk(ctx, chatManage.GraphResult, chatManage.SearchResult)
	if len(chunkIDs) == 0 {
		logger.Infof(ctx, "No new chunk found")
		return next()
	}
	chunks, err := p.chunkRepo.ListChunksByID(ctx, ctx.Value(types.TenantIDContextKey).(uint64), chunkIDs)
	if err != nil {
		logger.Errorf(ctx, "Failed to list chunks, session_id: %s, error: %v", chatManage.SessionID, err)
		return next()
	}
	knowledgeIDs := []string{}
	for _, chunk := range chunks {
		knowledgeIDs = append(knowledgeIDs, chunk.KnowledgeID)
	}
	knowledges, err := p.knowledgeRepo.GetKnowledgeBatch(
		ctx,
		ctx.Value(types.TenantIDContextKey).(uint64),
		knowledgeIDs,
	)
	if err != nil {
		logger.Errorf(ctx, "Failed to list knowledge, session_id: %s, error: %v", chatManage.SessionID, err)
		return next()
	}

	knowledgeMap := map[string]*types.Knowledge{}
	for _, knowledge := range knowledges {
		knowledgeMap[knowledge.ID] = knowledge
	}
	for _, chunk := range chunks {
		searchResult := chunk2SearchResult(chunk, knowledgeMap[chunk.KnowledgeID])
		chatManage.SearchResult = append(chatManage.SearchResult, searchResult)
	}
	// remove duplicate results
	chatManage.SearchResult = removeDuplicateResults(chatManage.SearchResult)
	if len(chatManage.SearchResult) == 0 {
		logger.Infof(ctx, "No new search result, session_id: %s", chatManage.SessionID)
		return ErrSearchNothing
	}
	logger.Infof(
		ctx,
		"search entity result count: %d, session_id: %s",
		len(chatManage.SearchResult),
		chatManage.SessionID,
	)
	return next()
}

// filterSeenChunk filters seen chunks from the graph
func filterSeenChunk(ctx context.Context, graph *types.GraphData, searchResult []*types.SearchResult) []string {
	seen := map[string]bool{}
	for _, chunk := range searchResult {
		seen[chunk.ID] = true
	}
	logger.Infof(ctx, "filterSeenChunk: seen count: %d", len(seen))

	chunkIDs := []string{}
	for _, node := range graph.Node {
		for _, chunkID := range node.Chunks {
			if seen[chunkID] {
				continue
			}
			seen[chunkID] = true
			chunkIDs = append(chunkIDs, chunkID)
		}
	}
	logger.Infof(ctx, "filterSeenChunk: new chunkIDs count: %d", len(chunkIDs))
	return chunkIDs
}

// chunk2SearchResult converts a chunk to a search result
func chunk2SearchResult(chunk *types.Chunk, knowledge *types.Knowledge) *types.SearchResult {
	return &types.SearchResult{
		ID:                chunk.ID,
		Content:           chunk.Content,
		KnowledgeID:       chunk.KnowledgeID,
		ChunkIndex:        chunk.ChunkIndex,
		KnowledgeTitle:    knowledge.Title,
		StartAt:           chunk.StartAt,
		EndAt:             chunk.EndAt,
		Seq:               chunk.ChunkIndex,
		Score:             1.0,
		MatchType:         types.MatchTypeGraph,
		Metadata:          knowledge.GetMetadata(),
		ChunkType:         string(chunk.ChunkType),
		ParentChunkID:     chunk.ParentChunkID,
		ImageInfo:         chunk.ImageInfo,
		KnowledgeFilename: knowledge.FileName,
		KnowledgeSource:   knowledge.Source,
		ChunkMetadata:     chunk.Metadata,
	}
}
