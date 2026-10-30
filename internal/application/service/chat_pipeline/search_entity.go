package chatpipeline

import (
	"context"
	"slices"
	"sync"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/searchutil"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"golang.org/x/sync/errgroup"
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

	// Graph-enabled KBs are a capability filter, not a replacement for the
	// normalized document/tag scopes in SearchTargets.
	enabledKBs := make(map[string]bool, len(chatManage.EntityKBIDs))
	for _, kbID := range chatManage.EntityKBIDs {
		enabledKBs[kbID] = true
	}
	namespaces := make(map[types.NameSpace]bool)
	for _, target := range chatManage.SearchTargets {
		if target == nil || target.KnowledgeBaseID == "" || !enabledKBs[target.KnowledgeBaseID] {
			continue
		}
		switch target.Type {
		case types.SearchTargetTypeKnowledgeBase:
			namespaces[types.NameSpace{KnowledgeBase: target.KnowledgeBaseID}] = true
		case types.SearchTargetTypeKnowledge:
			for _, knowledgeID := range target.KnowledgeIDs {
				if knowledgeID != "" {
					namespaces[types.NameSpace{KnowledgeBase: target.KnowledgeBaseID, Knowledge: knowledgeID}] = true
				}
			}
		}
	}
	if len(namespaces) == 0 {
		logger.Debugf(ctx, "No graph-enabled search targets")
		return next()
	}

	// Parallel search across multiple knowledge bases and individual files
	var group errgroup.Group
	// A tag can resolve to many files; bound the graph-store fan-out.
	group.SetLimit(4)
	var mu sync.Mutex
	var allNodes []*types.GraphNode
	var allRelations []*types.GraphRelation

	for namespace := range namespaces {
		// A whole-KB query already includes its explicitly selected files.
		if namespace.Knowledge != "" && namespaces[types.NameSpace{KnowledgeBase: namespace.KnowledgeBase}] {
			continue
		}
		group.Go(func() error {
			graph, err := p.graphRepo.SearchNode(ctx, namespace, entity)
			if err != nil {
				logger.Errorf(
					ctx,
					"Failed to search entity in KB %s, knowledge %s: %v",
					namespace.KnowledgeBase,
					namespace.Knowledge,
					err,
				)
				return nil
			}
			if graph == nil {
				return nil
			}
			mu.Lock()
			allNodes = append(allNodes, graph.Node...)
			allRelations = append(allRelations, graph.Relation...)
			mu.Unlock()
			return nil
		})
	}

	_ = group.Wait()

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
	// Graph hits from an org-shared KB belong to the sharing workspace, so
	// the lookups are not tenant scoped (as for parents, #3342). The graph
	// namespaces come from this turn's targets, but stored graph evidence may
	// be stale. Recheck its current document and physical tag scope below.
	listed, err := p.chunkRepo.ListChunksByIDOnly(ctx, chunkIDs)
	if err != nil {
		logger.Errorf(ctx, "Failed to list chunks, session_id: %s, error: %v", chatManage.SessionID, err)
		return next()
	}
	chunks := make([]*types.Chunk, 0, len(listed))
	knowledgeIDs := []string{}
	for _, chunk := range listed {
		if chunk == nil || !enabledKBs[chunk.KnowledgeBaseID] || !entityChunkInScope(chunk, chatManage.SearchTargets) {
			continue
		}
		chunks = append(chunks, chunk)
		knowledgeIDs = append(knowledgeIDs, chunk.KnowledgeID)
	}
	knowledges, err := p.knowledgeRepo.GetKnowledgeBatchByIDOnly(ctx, knowledgeIDs)
	if err != nil {
		logger.Errorf(ctx, "Failed to list knowledge, session_id: %s, error: %v", chatManage.SessionID, err)
		return next()
	}

	knowledgeMap := map[string]*types.Knowledge{}
	for _, knowledge := range knowledges {
		knowledgeMap[knowledge.ID] = knowledge
	}
	var entityResults []*types.SearchResult
	for _, chunk := range chunks {
		// A chunk can outlive its soft-deleted document, and a disabled
		// chunk must not come back through the graph either.
		knowledge := knowledgeMap[chunk.KnowledgeID]
		if knowledge == nil || !chunk.IsEnabled {
			continue
		}
		entityResults = append(entityResults, chunk2SearchResult(chunk, knowledge))
	}
	searchutil.EnrichSearchResultsImageInfoOnly(ctx, p.chunkRepo, entityResults)
	chatManage.SearchResult = append(chatManage.SearchResult, entityResults...)
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

func entityChunkInScope(chunk *types.Chunk, targets types.SearchTargets) bool {
	for _, target := range targets {
		if target == nil || target.KnowledgeBaseID != chunk.KnowledgeBaseID {
			continue
		}
		switch target.Type {
		case types.SearchTargetTypeKnowledgeBase:
		case types.SearchTargetTypeKnowledge:
			if !slices.Contains(target.KnowledgeIDs, chunk.KnowledgeID) {
				continue
			}
		default:
			continue
		}
		if len(target.TagIDs) == 0 || slices.Contains(target.TagIDs, chunk.TagID) {
			return true
		}
	}
	return false
}

// maxEntityChunks bounds the chunks graph entity search adds to a turn.
const maxEntityChunks = 30

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
			// Every chunk of every matched node was fetched and passed on;
			// entity search runs on an empty result list, so nothing was
			// ever filtered as seen. Cap it.
			if len(chunkIDs) >= maxEntityChunks {
				break
			}
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

// chunk2SearchResult converts a chunk to a search result.
//
// Graph hits come from entity lookups, not similarity search, so they have no
// retrieval score. They used to carry a fixed 1.0, which ranked them above
// every scored hit whenever rerank did not run. With 0 they fill the slots
// scored hits leave; when rerank runs, CompositeScore substitutes the model
// score for the missing retrieval score.
func chunk2SearchResult(chunk *types.Chunk, knowledge *types.Knowledge) *types.SearchResult {
	return &types.SearchResult{
		ID:                      chunk.ID,
		Content:                 chunk.Content,
		ContentRevision:         chunk.ContentRevision,
		KnowledgeID:             chunk.KnowledgeID,
		ChunkIndex:              chunk.ChunkIndex,
		KnowledgeTitle:          knowledge.Title,
		StartAt:                 chunk.StartAt,
		EndAt:                   chunk.EndAt,
		Seq:                     chunk.ChunkIndex,
		Score:                   0,
		MatchType:               types.MatchTypeGraph,
		Metadata:                knowledge.GetMetadata(),
		ChunkType:               string(chunk.ChunkType),
		ParentChunkID:           chunk.ParentChunkID,
		ImageInfo:               chunk.ImageInfo,
		KnowledgeFilename:       knowledge.FileName,
		KnowledgeSource:         knowledge.Source,
		KnowledgeChannel:        knowledge.Channel,
		KnowledgeDescription:    knowledge.Description,
		KnowledgeCustomMetadata: knowledge.CustomMetadataText(),
		ChunkMetadata:           chunk.Metadata,
		KnowledgeBaseID:         knowledge.KnowledgeBaseID,
		SourceLocators:          chunk.SourceLocators,
	}
}
