package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
	"sync"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/models/chat"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/Tencent/WeKnora/internal/utils"
)

type graphConfigSummary struct {
	Nodes     []string
	Relations []string
}

var queryKnowledgeGraphTool = BaseTool{
	name: ToolQueryKnowledgeGraph,
	description: `Query knowledge graph to explore entity relationships and knowledge networks.

## Core Function
Explores relationships between entities in knowledge bases that have graph extraction configured.

## When to Use
✅ **Use for**:
- Understanding relationships between entities (e.g., "relationship between Docker and Kubernetes")
- Exploring knowledge networks and concept associations
- Finding related information about specific entities
- Understanding technical architecture and system relationships

❌ **Don't use for**:
- General text search → use knowledge_search
- Knowledge base without graph extraction configured
- Need exact document content → use knowledge_search

## Parameters
- **knowledge_base_ids** (required): Array of short bN knowledge base IDs (1-10). Only KBs with graph extraction configured will be effective.
- **query** (required): Query content - can be entity name, relationship query, or concept search.

## Graph Configuration
Knowledge graph must be pre-configured in knowledge bases:
- **Entity types** (Nodes): e.g., "Technology", "Tool", "Concept"
- **Relationship types** (Relations): e.g., "depends_on", "uses", "contains"

If KB is not configured with graph, tool will return regular search results.

## Workflow
1. **Relationship exploration**: query_knowledge_graph → list_knowledge_chunks (for detailed content)
2. **Network analysis**: query_knowledge_graph → knowledge_search (for comprehensive understanding)
3. **Topic research**: knowledge_search → query_knowledge_graph (for deep entity relationships)

## Notes
- Results indicate graph configuration status
- Cross-KB results are automatically deduplicated
- Results are sorted by relevance`,
	schema: utils.GenerateSchema[QueryKnowledgeGraphInput](),
}

// QueryKnowledgeGraphInput defines the input parameters for query knowledge graph tool
type QueryKnowledgeGraphInput struct {
	KnowledgeBaseIDs []string `json:"knowledge_base_ids" jsonschema:"Array of short bN knowledge base IDs to query"`
	Query            string   `json:"query" jsonschema:"Query content (entity name or query text)"`
}

// QueryKnowledgeGraphTool queries the knowledge graph for entities and relationships
type QueryKnowledgeGraphTool struct {
	BaseTool
	knowledgeService      interfaces.KnowledgeBaseService
	scopeKnowledgeService interfaces.KnowledgeService
	chunkService          interfaces.ChunkService
	chatModel             chat.Chat
	searchTargets         types.SearchTargets
	scopeEnforced         bool
}

// WithKnowledgeScope enables document/tag-level result filtering for Agent
// calls. The graph backend queries by KB, so the tool must enforce narrower
// SearchTargets before returning any result to the model.
func (t *QueryKnowledgeGraphTool) WithKnowledgeScope(
	knowledgeService interfaces.KnowledgeService,
) *QueryKnowledgeGraphTool {
	t.scopeKnowledgeService = knowledgeService
	return t
}

// NewQueryKnowledgeGraphTool creates a new query knowledge graph tool.
//
// chunkService backfills the chunks referenced by matched graph nodes, and
// chatModel extracts the entity names to traverse from the raw query. Both are
// optional: without a chat model (or when extraction fails) the tool skips the
// graph traversal and keeps its retrieval-only behavior.
func NewQueryKnowledgeGraphTool(
	knowledgeService interfaces.KnowledgeBaseService,
	chunkService interfaces.ChunkService,
	chatModel chat.Chat,
	searchTargets ...types.SearchTargets,
) *QueryKnowledgeGraphTool {
	tool := &QueryKnowledgeGraphTool{
		BaseTool:         queryKnowledgeGraphTool,
		knowledgeService: knowledgeService,
		chunkService:     chunkService,
		chatModel:        chatModel,
	}
	// Presence of the variadic argument — not its length — enables the Agent
	// authorization boundary, so an empty scope fails closed.
	if len(searchTargets) > 0 {
		tool.searchTargets = searchTargets[0]
		tool.scopeEnforced = true
	}
	return tool
}

// maxGraphChunkResults caps how many graph-referenced chunks are returned per
// knowledge base, mirroring the MatchCount budget of the retrieval path so one
// hub entity with hundreds of references cannot flood the context window.
const maxGraphChunkResults = 10

// maxQueryEntities caps the entity list sent to the graph backend so a chatty
// extraction cannot turn into an unbounded Cypher OR-chain.
const maxQueryEntities = 8

// graphBackendEnabled reports whether the Neo4j-backed graph store is active.
// It mirrors the gate the chat pipeline uses in extract_entity.go; without a
// graph store there is nothing to traverse and the tool must stay on its
// retrieval-only path.
var graphBackendEnabled = func() bool {
	return strings.ToLower(os.Getenv("NEO4J_ENABLE")) == "true"
}

// perKBGraphResult carries what one knowledge base's graph traversal produced.
type perKBGraphResult struct {
	entities  []string
	nodes     []*types.GraphNode
	relations []*types.GraphRelation
	chunks    []*types.SearchResult
	skipped   string // human-readable reason when traversal did not run
}

// Execute performs the knowledge graph query with concurrent KB processing
func (t *QueryKnowledgeGraphTool) Execute(ctx context.Context, args json.RawMessage) (*types.ToolResult, error) {
	// Parse args from json.RawMessage
	var input QueryKnowledgeGraphInput
	if err := json.Unmarshal(args, &input); err != nil {
		return &types.ToolResult{
			Success: false,
			Error:   fmt.Sprintf("Failed to parse args: %v", err),
		}, err
	}

	// Extract knowledge_base_ids array
	if len(input.KnowledgeBaseIDs) == 0 {
		return &types.ToolResult{
			Success: false,
			Error:   "knowledge_base_ids is required and must be a non-empty array",
		}, fmt.Errorf("knowledge_base_ids is required")
	}

	// Validate max 10 KBs
	if len(input.KnowledgeBaseIDs) > 10 {
		return &types.ToolResult{
			Success: false,
			Error:   "knowledge_base_ids must contain at most 10 KB IDs",
		}, fmt.Errorf("too many KB IDs")
	}
	if t.scopeEnforced {
		if err := validateKnowledgeBaseIDsInSearchTargets(t.searchTargets, input.KnowledgeBaseIDs); err != nil {
			return &types.ToolResult{Success: false, Error: err.Error()}, err
		}
	}

	query := input.Query
	if query == "" {
		return &types.ToolResult{
			Success: false,
			Error:   "query is required",
		}, fmt.Errorf("invalid query")
	}

	// Extract entities from the query once — every KB in scope traverses the
	// graph with the same entity list, exactly like the chat pipeline's
	// QUERY_UNDERSTAND -> ENTITY_SEARCH handoff.
	entities := t.extractQueryEntities(ctx, query)
	if len(entities) > 0 {
		logger.Infof(ctx, "[Tool][QueryKnowledgeGraph] Extracted %d entities for graph traversal: %v", len(entities), entities)
	}

	// Concurrently query all knowledge bases
	type graphQueryResult struct {
		kbID    string
		kb      *types.KnowledgeBase
		results []*types.SearchResult
		graph   perKBGraphResult
		err     error
	}

	var wg sync.WaitGroup
	var mu sync.Mutex
	kbResults := make(map[string]*graphQueryResult)

	searchParams := types.SearchParams{
		QueryText:  query,
		MatchCount: 10,
	}

	for _, kbID := range input.KnowledgeBaseIDs {
		wg.Add(1)
		go func(id string) {
			defer wg.Done()

			// Get knowledge base to check graph configuration
			kb, err := t.knowledgeService.GetKnowledgeBaseByIDOnly(ctx, id)
			if err != nil {
				mu.Lock()
				kbResults[id] = &graphQueryResult{kbID: id, err: fmt.Errorf("failed to get knowledge base: %v", err)}
				mu.Unlock()
				return
			}

			// Check if graph extraction is enabled
			if kb.ExtractConfig == nil || (len(kb.ExtractConfig.Nodes) == 0 && len(kb.ExtractConfig.Relations) == 0) {
				mu.Lock()
				kbResults[id] = &graphQueryResult{kbID: id, err: fmt.Errorf("graph extraction not configured")}
				mu.Unlock()
				return
			}

			// Traverse the knowledge graph first. Every failure here is
			// non-fatal: the hybrid retrieval below still answers, just
			// without graph-shaped recall.
			graphRes := t.traverseGraph(ctx, id, entities)

			// Hybrid retrieval keeps recall breadth: the graph pins chunks
			// that are topically tied to the extracted entities, while this
			// path catches relevant chunks whose entities were not extracted.
			results, err := t.knowledgeService.HybridSearch(ctx, id, searchParams)
			if err != nil {
				mu.Lock()
				kbResults[id] = &graphQueryResult{kbID: id, kb: kb, graph: graphRes, err: fmt.Errorf("query failed: %v", err)}
				mu.Unlock()
				return
			}

			// Merge: graph-sourced chunks rank ahead of retrieval chunks
			// (entity-name matches are exact), deduplicated by chunk ID.
			results = mergeGraphAndHybridResults(graphRes.chunks, results)

			if t.scopeEnforced {
				results, err = filterSearchResultsInSearchTargets(
					ctx, t.searchTargets, id, results, t.scopeKnowledgeService,
				)
				if err != nil {
					mu.Lock()
					kbResults[id] = &graphQueryResult{kbID: id, kb: kb, graph: graphRes, err: err}
					mu.Unlock()
					return
				}
			}

			mu.Lock()
			kbResults[id] = &graphQueryResult{kbID: id, kb: kb, results: results, graph: graphRes}
			mu.Unlock()
		}(kbID)
	}

	wg.Wait()

	// Collect and deduplicate results
	seenChunks := make(map[string]*types.SearchResult)
	var errors []string
	graphConfigs := make(map[string]graphConfigSummary)
	kbCounts := make(map[string]int)
	graphStats := make(map[string]map[string]interface{})

	for _, kbID := range input.KnowledgeBaseIDs {
		result := kbResults[kbID]
		if result.err != nil {
			errors = append(errors, fmt.Sprintf("KB %s: %v", kbID, result.err))
			continue
		}

		if result.kb != nil && result.kb.ExtractConfig != nil {
			graphConfigs[kbID] = summarizeGraphConfig(result.kb.ExtractConfig)
		}

		kbCounts[kbID] = len(result.results)
		graphStats[kbID] = graphResultToData(result.graph)
		for _, r := range result.results {
			if _, seen := seenChunks[r.ID]; !seen {
				seenChunks[r.ID] = r
			}
		}
	}

	// Convert map to slice and sort by score
	allResults := make([]*types.SearchResult, 0, len(seenChunks))
	for _, result := range seenChunks {
		allResults = append(allResults, result)
	}

	// Sort by score with a deterministic chunk-ID tie-break: the dedup map
	// above iterates in random order, and equal-score results (e.g. several
	// graph-matched chunks at 1.0) would otherwise shuffle between calls.
	sort.SliceStable(allResults, func(i, j int) bool {
		if allResults[i].Score != allResults[j].Score {
			return allResults[i].Score > allResults[j].Score
		}
		return allResults[i].ID < allResults[j].ID
	})

	if len(allResults) == 0 {
		return &types.ToolResult{
			Success: true,
			Output:  "No relevant graph information found.",
			Data: map[string]interface{}{
				"knowledge_base_ids": input.KnowledgeBaseIDs,
				"query":              query,
				"entities":           entities,
				"results":            []interface{}{},
				"graph_configs":      graphConfigsToData(graphConfigs),
				"graph_config":       aggregateGraphConfig(graphConfigs),
				"graph_traversal":    graphStats,
				"errors":             errors,
			},
		}, nil
	}

	// Format output with enhanced graph information
	output := "=== Knowledge Graph Query ===\n\n"
	output += fmt.Sprintf("📊 Query: %s\n", query)
	output += fmt.Sprintf("🎯 Target Knowledge Bases: %v\n", input.KnowledgeBaseIDs)
	if len(entities) > 0 {
		output += fmt.Sprintf("🔎 Extracted Entities: %v\n", entities)
	}
	output += fmt.Sprintf("✓ Found %d relevant results (deduplicated)\n\n", len(allResults))

	if len(errors) > 0 {
		output += "=== ⚠️ Partial Failures ===\n"
		for _, errMsg := range errors {
			output += fmt.Sprintf("  - %s\n", errMsg)
		}
		output += "\n"
	}

	// Display graph configuration status
	hasGraphConfig := false
	output += "=== 📈 Graph Configuration Status ===\n\n"
	for kbID, config := range graphConfigs {
		hasGraphConfig = true
		output += fmt.Sprintf("Knowledge Base [%s]:\n", kbID)

		if len(config.Nodes) > 0 {
			output += fmt.Sprintf("  ✓ Entity Types (%d): %v\n", len(config.Nodes), config.Nodes)
		} else {
			output += "  ⚠️ No entity types configured\n"
		}

		if len(config.Relations) > 0 {
			output += fmt.Sprintf("  ✓ Relationship Types (%d): %v\n", len(config.Relations), config.Relations)
		} else {
			output += "  ⚠️ No relationship types configured\n"
		}
		output += "\n"
	}

	if !hasGraphConfig {
		output += "⚠️ None of the queried knowledge bases have graph extraction configured\n"
		output += "💡 Hint: Configure entity and relationship types in knowledge base settings\n\n"
	}

	// Display the graph traversal outcome — matched entities, nodes and the
	// relations between them are the actual graph answer for the model.
	output += "=== 🕸️ Graph Traversal ===\n\n"
	traversedAny := false
	visibleRelations := make([]*types.GraphRelation, 0)
	for _, kbID := range input.KnowledgeBaseIDs {
		stats, ok := graphStats[kbID]
		if !ok {
			continue
		}
		skipped, _ := stats["skipped"].(string)
		if skipped != "" {
			output += fmt.Sprintf("Knowledge Base [%s]: traversal skipped (%s)\n", kbID, skipped)
			continue
		}
		nodes, _ := stats["nodes_matched"].(int)
		relations, _ := stats["relations_matched"].(int)
		chunks, _ := stats["graph_chunks"].(int)
		if nodes == 0 && relations == 0 {
			output += fmt.Sprintf("Knowledge Base [%s]: no graph nodes matched the extracted entities\n", kbID)
			continue
		}
		traversedAny = true
		output += fmt.Sprintf("Knowledge Base [%s]: matched %d graph nodes, %d relations, %d referenced chunks\n",
			kbID, nodes, relations, chunks)
		if result := kbResults[kbID]; result != nil {
			visibleRelations = append(visibleRelations, result.graph.relations...)
		}
	}
	if len(visibleRelations) > 0 {
		output += "\nMatched relations:\n"
		limit := len(visibleRelations)
		if limit > 10 {
			limit = 10
		}
		for _, rel := range visibleRelations[:limit] {
			if rel == nil {
				continue
			}
			output += fmt.Sprintf("  - %s --[%s]--> %s\n", rel.Node1, rel.Type, rel.Node2)
		}
		if len(visibleRelations) > limit {
			output += fmt.Sprintf("  ... and %d more relations\n", len(visibleRelations)-limit)
		}
	}
	if !traversedAny && len(visibleRelations) == 0 {
		output += "⚠️ No graph traversal results available for the extracted entities\n"
	}
	output += "\n"

	// Display result counts by KB
	if len(kbCounts) > 0 {
		output += "=== 📚 Knowledge Base Coverage ===\n"
		for kbID, count := range kbCounts {
			output += fmt.Sprintf("  - %s: %d results\n", kbID, count)
		}
		output += "\n"
	}

	// Display search results
	output += "=== 🔍 Query Results ===\n\n"
	if !hasGraphConfig {
		output += "💡 Returning relevant document chunks (knowledge base has no graph configuration)\n\n"
	} else if traversedAny {
		output += "💡 Results combine graph-traversed chunks (graph match) with hybrid retrieval\n\n"
	} else {
		output += "💡 Content retrieval based on graph configuration\n\n"
	}

	formattedResults := make([]map[string]interface{}, 0, len(allResults))
	currentKB := ""

	for i, result := range allResults {
		// Group by knowledge base
		if result.KnowledgeID != currentKB {
			currentKB = result.KnowledgeID
			if i > 0 {
				output += "\n"
			}
			output += fmt.Sprintf("[Source Document: %s]\n\n", result.KnowledgeTitle)
		}

		relevanceLevel := GetRelevanceLevel(result.Score)

		output += fmt.Sprintf("Result #%d:\n", i+1)
		output += fmt.Sprintf("  📍 Relevance: %.2f (%s)\n", result.Score, relevanceLevel)
		output += fmt.Sprintf("  🔗 Match Type: %s\n", FormatMatchType(result.MatchType))
		output += fmt.Sprintf("  📄 Content: %s\n", result.Content)
		output += fmt.Sprintf("  🆔 chunk_id: %s\n\n", result.ID)

		formattedResults = append(formattedResults, map[string]interface{}{
			"result_index":      i + 1,
			"chunk_id":          result.ID,
			"chunk_index":       result.ChunkIndex,
			"chunk_type":        result.ChunkType,
			"content":           result.Content,
			"score":             result.Score,
			"relevance_level":   relevanceLevel,
			"knowledge_id":      result.KnowledgeID,
			"knowledge_base_id": result.KnowledgeBaseID,
			"knowledge_title":   result.KnowledgeTitle,
			"match_type":        FormatMatchType(result.MatchType),
		})
	}

	output += "=== 💡 Tips ===\n"
	output += "- ✓ Results are deduplicated across knowledge bases and sorted by relevance\n"
	output += "- ✓ Use get_chunk_detail to get full content\n"
	output += "- ✓ Use list_knowledge_chunks to explore context\n"
	if !hasGraphConfig {
		output += "- ⚠️ Configure graph extraction for more precise entity-relationship results\n"
	}
	output += "- ⏳ Full graph query language (Cypher) support is under development\n"

	// Build structured graph data for frontend visualization from what the
	// traversal actually matched.
	traversals := make([]perKBGraphResult, 0, len(input.KnowledgeBaseIDs))
	for _, kbID := range input.KnowledgeBaseIDs {
		if result := kbResults[kbID]; result != nil {
			traversals = append(traversals, result.graph)
		}
	}
	graphData := buildGraphVisualizationData(traversals, allResults)

	return &types.ToolResult{
		Success: true,
		Output:  output,
		Data: map[string]interface{}{
			"knowledge_base_ids": input.KnowledgeBaseIDs,
			"query":              query,
			"entities":           entities,
			"results":            formattedResults,
			"count":              len(allResults),
			"kb_counts":          kbCounts,
			"graph_configs":      graphConfigsToData(graphConfigs),
			"graph_config":       aggregateGraphConfig(graphConfigs),
			"graph_traversal":    graphStats,
			"graph_data":         graphData,
			"has_graph_config":   hasGraphConfig,
			"errors":             errors,
			"display_type":       "graph_query_results",
		},
	}, nil
}

// traverseGraph runs the entity-name graph traversal for one knowledge base
// and backfills the chunks referenced by the matched nodes. Any precondition
// miss or backend error degrades to a skipped marker instead of failing the
// tool — retrieval still runs afterwards.
func (t *QueryKnowledgeGraphTool) traverseGraph(
	ctx context.Context,
	kbID string,
	entities []string,
) perKBGraphResult {
	res := perKBGraphResult{entities: entities}
	if !graphBackendEnabled() {
		res.skipped = "graph backend disabled (NEO4J_ENABLE is not true)"
		return res
	}
	if len(entities) == 0 {
		res.skipped = "no entities extracted from query"
		return res
	}

	graph, err := t.knowledgeService.SearchGraphNodes(ctx, kbID, entities)
	if err != nil {
		logger.Warnf(ctx, "[Tool][QueryKnowledgeGraph] Graph traversal failed for KB %s, falling back to retrieval only: %v", kbID, err)
		res.skipped = fmt.Sprintf("graph traversal failed: %v", err)
		return res
	}
	if graph == nil || (len(graph.Node) == 0 && len(graph.Relation) == 0) {
		res.skipped = "no graph nodes or relations matched"
		return res
	}

	res.nodes = graph.Node
	res.relations = graph.Relation
	res.chunks = t.graphChunkSearchResults(ctx, graph)
	return res
}

// graphChunkSearchResults backfills search results for the chunks referenced
// by the matched graph nodes. Missing chunk lookups are skipped silently: the
// chunk may have been deleted after graph extraction, and one stale reference
// must not sink the whole traversal.
func (t *QueryKnowledgeGraphTool) graphChunkSearchResults(
	ctx context.Context,
	graph *types.GraphData,
) []*types.SearchResult {
	if t.chunkService == nil || graph == nil || len(graph.Node) == 0 {
		return nil
	}

	orderedIDs := make([]string, 0, len(graph.Node))
	seen := make(map[string]struct{})
	for _, node := range graph.Node {
		if node == nil {
			continue
		}
		for _, chunkID := range node.Chunks {
			if chunkID == "" {
				continue
			}
			if _, ok := seen[chunkID]; ok {
				continue
			}
			seen[chunkID] = struct{}{}
			orderedIDs = append(orderedIDs, chunkID)
			if len(orderedIDs) >= maxGraphChunkResults {
				break
			}
		}
		if len(orderedIDs) >= maxGraphChunkResults {
			break
		}
	}
	if len(orderedIDs) == 0 {
		return nil
	}

	// Graph nodes carry chunk IDs verbatim from the graph store, which is
	// tenant-agnostic; the Only variant resolves shared-KB chunks too.
	chunks, err := t.chunkService.GetRepository().ListChunksByIDOnly(ctx, orderedIDs)
	if err != nil {
		logger.Warnf(ctx, "[Tool][QueryKnowledgeGraph] Failed to backfill graph chunks: %v", err)
		return nil
	}

	knowledgeByID := make(map[string]*types.Knowledge)
	if t.scopeKnowledgeService != nil {
		knowledgeIDs := make([]string, 0, len(chunks))
		knowledgeSeen := make(map[string]struct{})
		for _, chunk := range chunks {
			if chunk == nil {
				continue
			}
			if _, ok := knowledgeSeen[chunk.KnowledgeID]; ok {
				continue
			}
			knowledgeSeen[chunk.KnowledgeID] = struct{}{}
			knowledgeIDs = append(knowledgeIDs, chunk.KnowledgeID)
		}
		if len(knowledgeIDs) > 0 {
			var tenantID uint64
			if id, ok := types.TenantIDFromContext(ctx); ok {
				tenantID = id
			}
			if knowledges, kerr := t.scopeKnowledgeService.GetKnowledgeBatchWithSharedAccess(ctx, tenantID, knowledgeIDs); kerr == nil {
				for _, knowledge := range knowledges {
					if knowledge != nil {
						knowledgeByID[knowledge.ID] = knowledge
					}
				}
			} else {
				logger.Warnf(ctx, "[Tool][QueryKnowledgeGraph] Failed to resolve knowledge titles for graph chunks: %v", kerr)
			}
		}
	}

	// Preserve the graph's reference order and cap the chunk count.
	byID := make(map[string]*types.Chunk, len(chunks))
	for _, chunk := range chunks {
		if chunk != nil {
			byID[chunk.ID] = chunk
		}
	}
	results := make([]*types.SearchResult, 0, len(orderedIDs))
	for _, id := range orderedIDs {
		chunk, ok := byID[id]
		if !ok {
			continue
		}
		knowledge := knowledgeByID[chunk.KnowledgeID]
		result := &types.SearchResult{
			ID:              chunk.ID,
			Content:         chunk.Content,
			ContentRevision: chunk.ContentRevision,
			KnowledgeID:     chunk.KnowledgeID,
			ChunkIndex:      chunk.ChunkIndex,
			Seq:             chunk.ChunkIndex,
			Score:           1.0,
			MatchType:       types.MatchTypeGraph,
			ChunkType:       string(chunk.ChunkType),
			ParentChunkID:   chunk.ParentChunkID,
			ImageInfo:       chunk.ImageInfo,
			ChunkMetadata:   chunk.Metadata,
		}
		if knowledge != nil {
			result.KnowledgeTitle = knowledge.Title
			result.Metadata = knowledge.GetMetadata()
			result.KnowledgeFilename = knowledge.FileName
			result.KnowledgeSource = knowledge.Source
			result.KnowledgeChannel = knowledge.Channel
			result.KnowledgeBaseID = knowledge.KnowledgeBaseID
		}
		results = append(results, result)
	}
	return results
}

// mergeGraphAndHybridResults places graph-sourced chunks first and drops the
// retrieval duplicates of chunks the graph already surfaced.
func mergeGraphAndHybridResults(graphResults, hybridResults []*types.SearchResult) []*types.SearchResult {
	if len(graphResults) == 0 {
		return hybridResults
	}
	seen := make(map[string]struct{}, len(graphResults))
	for _, r := range graphResults {
		seen[r.ID] = struct{}{}
	}
	merged := make([]*types.SearchResult, 0, len(graphResults)+len(hybridResults))
	merged = append(merged, graphResults...)
	for _, r := range hybridResults {
		if _, ok := seen[r.ID]; ok {
			continue
		}
		merged = append(merged, r)
	}
	return merged
}

// extractQueryEntities asks the agent's chat model to pull the entity names
// out of the raw query — the same handoff the chat pipeline performs between
// QUERY_UNDERSTAND and ENTITY_SEARCH. Failures return nil so the caller stays
// on its retrieval-only path instead of erroring out.
func (t *QueryKnowledgeGraphTool) extractQueryEntities(ctx context.Context, query string) []string {
	if t.chatModel == nil {
		return nil
	}

	think := false
	opts := &chat.ChatOptions{
		Temperature: 0.1,
		MaxTokens:   256,
		Thinking:    &think,
	}
	messages := []chat.Message{
		{Role: "system", Content: queryEntityExtractionSystemPrompt},
		{Role: "user", Content: query},
	}
	modelCtx := types.WithLLMCallMetadata(ctx, "graph_query_entity_extraction", "")
	response, err := t.chatModel.Chat(modelCtx, messages, opts)
	if err != nil {
		logger.Warnf(ctx, "[Tool][QueryKnowledgeGraph] Entity extraction failed, skipping graph traversal: %v", err)
		return nil
	}
	if response == nil {
		return nil
	}

	entities := parseEntityList(response.Content)
	if len(entities) == 0 {
		logger.Infof(ctx, "[Tool][QueryKnowledgeGraph] Entity extraction returned no usable entities")
	}
	return entities
}

const queryEntityExtractionSystemPrompt = `Extract the entities from the user's question that could appear as nodes in a knowledge graph.

Rules:
1. Output ONLY a JSON array of strings. No explanations, no markdown fences.
2. Each string is one entity name, kept exactly as it appears in the question and in its original language.
3. Include named people, organizations, products, technologies, concepts and other entities relevant to the question.
4. Prefer 1 to 6 precise entities; skip vague or generic words.
5. If no entity can be extracted, output [].

Example:
Question: "What is the relationship between Docker and Kubernetes?"
Output: ["Docker", "Kubernetes"]`

// parseEntityList parses the model's JSON array output, tolerating markdown
// code fences and non-string noise, and caps the result for the graph backend.
func parseEntityList(content string) []string {
	trimmed := strings.TrimSpace(content)
	if trimmed == "" {
		return nil
	}
	// Strip a single markdown fence wrapper if present.
	if strings.HasPrefix(trimmed, "```") {
		if idx := strings.Index(trimmed, "\n"); idx >= 0 {
			trimmed = trimmed[idx+1:]
		}
		trimmed = strings.TrimSuffix(strings.TrimSpace(trimmed), "```")
		trimmed = strings.TrimSpace(trimmed)
	}
	// Tolerate a leading prose sentence by anchoring on the first '['.
	if start := strings.Index(trimmed, "["); start > 0 {
		trimmed = trimmed[start:]
	}
	if end := strings.LastIndex(trimmed, "]"); end >= 0 {
		trimmed = trimmed[:end+1]
	}

	var raw []string
	if err := json.Unmarshal([]byte(trimmed), &raw); err != nil {
		return nil
	}

	seen := make(map[string]struct{}, len(raw))
	entities := make([]string, 0, len(raw))
	for _, entity := range raw {
		entity = strings.TrimSpace(entity)
		if entity == "" {
			continue
		}
		if _, ok := seen[entity]; ok {
			continue
		}
		seen[entity] = struct{}{}
		entities = append(entities, entity)
		if len(entities) >= maxQueryEntities {
			break
		}
	}
	if len(entities) == 0 {
		return nil
	}
	return entities
}

// graphResultToData summarizes one KB's traversal for the structured output.
func graphResultToData(result perKBGraphResult) map[string]interface{} {
	data := map[string]interface{}{
		"entities":          result.entities,
		"nodes_matched":     len(result.nodes),
		"relations_matched": len(result.relations),
		"graph_chunks":      len(result.chunks),
	}
	if result.skipped != "" {
		data["skipped"] = result.skipped
	}
	return data
}

func summarizeGraphConfig(config *types.ExtractConfig) graphConfigSummary {
	if config == nil {
		return graphConfigSummary{}
	}

	return graphConfigSummary{
		Nodes:     uniqueSortedNodeNames(config.Nodes),
		Relations: uniqueSortedRelationNames(config.Relations),
	}
}

func uniqueSortedNodeNames(nodes []*types.GraphNode) []string {
	seen := make(map[string]struct{}, len(nodes))
	names := make([]string, 0, len(nodes))
	for _, node := range nodes {
		if node == nil || node.Name == "" {
			continue
		}
		if _, exists := seen[node.Name]; exists {
			continue
		}
		seen[node.Name] = struct{}{}
		names = append(names, node.Name)
	}
	sort.Strings(names)
	return names
}

func uniqueSortedRelationNames(relations []*types.GraphRelation) []string {
	seen := make(map[string]struct{}, len(relations))
	names := make([]string, 0, len(relations))
	for _, relation := range relations {
		if relation == nil || relation.Type == "" {
			continue
		}
		if _, exists := seen[relation.Type]; exists {
			continue
		}
		seen[relation.Type] = struct{}{}
		names = append(names, relation.Type)
	}
	sort.Strings(names)
	return names
}

func graphConfigsToData(graphConfigs map[string]graphConfigSummary) map[string]map[string]interface{} {
	if len(graphConfigs) == 0 {
		return nil
	}

	data := make(map[string]map[string]interface{}, len(graphConfigs))
	for kbID, config := range graphConfigs {
		data[kbID] = map[string]interface{}{
			"nodes":     config.Nodes,
			"relations": config.Relations,
		}
	}
	return data
}

func aggregateGraphConfig(graphConfigs map[string]graphConfigSummary) map[string]interface{} {
	if len(graphConfigs) == 0 {
		return nil
	}

	merged := graphConfigSummary{}
	for _, config := range graphConfigs {
		merged.Nodes = append(merged.Nodes, config.Nodes...)
		merged.Relations = append(merged.Relations, config.Relations...)
	}

	return map[string]interface{}{
		"nodes":     uniqueStrings(merged.Nodes),
		"relations": uniqueStrings(merged.Relations),
	}
}

func uniqueStrings(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		if value == "" {
			continue
		}
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}

// buildGraphVisualizationData builds structured data for graph visualization.
// Entity nodes and relation edges come from what the traversal actually
// matched; chunk nodes are appended so the frontend can still anchor result
// previews when traversal was skipped.
func buildGraphVisualizationData(traversals []perKBGraphResult, results []*types.SearchResult) map[string]interface{} {
	nodes := make([]map[string]interface{}, 0)
	edges := make([]map[string]interface{}, 0)

	seenEntities := make(map[string]bool)
	seenEdges := make(map[string]bool)
	for _, traversal := range traversals {
		for _, node := range traversal.nodes {
			if node == nil || node.Name == "" || seenEntities[node.Name] {
				continue
			}
			seenEntities[node.Name] = true
			entityNode := map[string]interface{}{
				"id":     node.Name,
				"label":  node.Name,
				"chunks": len(node.Chunks),
				"type":   "entity",
			}
			if len(node.Attributes) > 0 {
				entityNode["attributes"] = node.Attributes
			}
			nodes = append(nodes, entityNode)
		}
		for _, rel := range traversal.relations {
			if rel == nil || rel.Node1 == "" || rel.Node2 == "" {
				continue
			}
			key := fmt.Sprintf("%s\x00%s\x00%s", rel.Node1, rel.Node2, rel.Type)
			if seenEdges[key] {
				continue
			}
			seenEdges[key] = true
			edges = append(edges, map[string]interface{}{
				"source": rel.Node1,
				"target": rel.Node2,
				"label":  rel.Type,
				"type":   "relation",
			})
		}
	}

	// Chunk nodes keep the previous visualization anchor behavior.
	seenChunks := make(map[string]bool)
	for i, result := range results {
		if !seenChunks[result.ID] {
			nodes = append(nodes, map[string]interface{}{
				"id":       result.ID,
				"label":    fmt.Sprintf("Chunk %d", i+1),
				"content":  result.Content,
				"kb_id":    result.KnowledgeID,
				"kb_title": result.KnowledgeTitle,
				"score":    result.Score,
				"type":     "chunk",
			})
			seenChunks[result.ID] = true
		}
	}

	return map[string]interface{}{
		"nodes":       nodes,
		"edges":       edges,
		"total_nodes": len(nodes),
		"total_edges": len(edges),
	}
}
