package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type graphEvidenceChunkRepo struct {
	stubGraphChunkRepo
	mu             sync.Mutex
	batches        [][]string
	contentBatches [][]string
	failAt         int
	contentErr     error
	beforeContent  func()
}

func (s *graphEvidenceChunkRepo) ListChunksByIDOnly(ctx context.Context, ids []string) ([]*types.Chunk, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.contentBatches = append(s.contentBatches, append([]string(nil), ids...))
	if s.contentErr != nil {
		return nil, s.contentErr
	}
	if s.beforeContent != nil {
		s.beforeContent()
	}
	return s.stubGraphChunkRepo.ListChunksByIDOnly(ctx, ids)
}

func (s *graphEvidenceChunkRepo) ListChunkEvidenceByIDOnly(ctx context.Context, ids []string) ([]*types.Chunk, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.batches = append(s.batches, append([]string(nil), ids...))
	if len(s.batches) == s.failAt {
		return nil, assert.AnError
	}
	return s.stubGraphChunkRepo.ListChunkEvidenceByIDOnly(ctx, ids)
}

type graphEvidenceKnowledgeService struct {
	interfaces.KnowledgeService
	mu              sync.Mutex
	documents       map[string]*types.Knowledge
	tags            map[string][]*types.KnowledgeTag
	err             error
	tagErr          error
	documentBatches [][]string
	tagBatches      [][]string
}

func (s *graphEvidenceKnowledgeService) GetKnowledgeBatchWithSharedAccess(
	_ context.Context, _ uint64, ids []string,
) ([]*types.Knowledge, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.documentBatches = append(s.documentBatches, append([]string(nil), ids...))
	if s.err != nil {
		return nil, s.err
	}
	var documents []*types.Knowledge
	for _, id := range ids {
		if document := s.documents[id]; document != nil {
			documents = append(documents, document)
		}
	}
	return documents, nil
}

func (s *graphEvidenceKnowledgeService) GetKnowledgeTags(
	_ context.Context, ids []string,
) (map[string][]*types.KnowledgeTag, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.tagBatches = append(s.tagBatches, append([]string(nil), ids...))
	if s.tagErr != nil {
		return nil, s.tagErr
	}
	return s.tags, nil
}

func graphEvidenceScopes() []struct {
	name    string
	targets types.SearchTargets
} {
	return []struct {
		name    string
		targets types.SearchTargets
	}{
		{"whole KB", types.SearchTargets{{Type: types.SearchTargetTypeKnowledgeBase, KnowledgeBaseID: "kb-1"}}},
		{"document", types.SearchTargets{{
			Type: types.SearchTargetTypeKnowledge, KnowledgeBaseID: "kb-1", KnowledgeIDs: []string{"doc"},
		}}},
		{"tag", types.SearchTargets{{
			Type: types.SearchTargetTypeKnowledgeBase, KnowledgeBaseID: "kb-1", TagIDs: []string{"tag"},
		}}},
		{"without scope argument", nil},
	}
}

func liveGraphEvidenceDocuments() *graphEvidenceKnowledgeService {
	return &graphEvidenceKnowledgeService{
		documents: map[string]*types.Knowledge{"doc": {ID: "doc", Title: "Evidence", KnowledgeBaseID: "kb-1"}},
		tags:      map[string][]*types.KnowledgeTag{"doc": {testKnowledgeTag("tag")}},
	}
}

func graphEvidenceGraph(sourceChunks, targetChunks []string) *types.GraphData {
	return &types.GraphData{
		Node: []*types.GraphNode{
			{Name: "Acme", ID: "acme-doc", KnowledgeID: "doc", Chunks: sourceChunks},
			{Name: "Shanghai", ID: "city-doc", KnowledgeID: "doc", Chunks: targetChunks},
		},
		Relation: []*types.GraphRelation{{
			Node1: "Acme", Type: "HEADQUARTERED_IN", Node2: "Shanghai", SourceID: "acme-doc", TargetID: "city-doc",
		}},
	}
}

func graphEvidenceTool(
	graph *types.GraphData, chunks interfaces.ChunkRepository, documents interfaces.KnowledgeService,
	targets types.SearchTargets,
) *QueryKnowledgeGraphTool {
	service := &stubKnowledgeBaseService{kb: &types.KnowledgeBase{
		ID: "kb-1", ExtractConfig: &types.ExtractConfig{Enabled: true, Nodes: []*types.GraphNode{{Name: "Company"}}},
	}}
	tool := NewQueryKnowledgeGraphTool(service)
	if targets != nil {
		tool = NewQueryKnowledgeGraphTool(service, targets)
	}
	return tool.WithGraph(&stubGraphRepo{graph: graph}, chunks).WithKnowledgeScope(documents)
}

func executeGraphEvidenceTool(t *testing.T, tool *QueryKnowledgeGraphTool) *types.ToolResult {
	t.Helper()
	result, err := tool.Execute(context.Background(), json.RawMessage(`{"knowledge_base_ids":["kb-1"],"query":"Acme"}`))
	require.NoError(t, err)
	require.True(t, result.Success)
	return result
}

// Removing the original text must also remove unsupported relations, including
// when whole-KB authorization succeeds and no narrower scope was requested.
func TestQueryKnowledgeGraph_RejectsInvalidEndpointEvidenceInEveryScope(t *testing.T) {
	for _, scope := range graphEvidenceScopes() {
		for _, invalid := range []string{
			"disabled chunk", "deleted chunk", "foreign KB", "deleted document",
			"missing target evidence", "no chunk references",
		} {
			t.Run(scope.name+"/"+invalid, func(t *testing.T) {
				chunk := &types.Chunk{ID: "c1", KnowledgeID: "doc", KnowledgeBaseID: "kb-1", IsEnabled: true}
				chunks := &stubGraphChunkRepo{chunks: map[string]*types.Chunk{"c1": chunk}}
				documents := liveGraphEvidenceDocuments()
				graph := graphEvidenceGraph([]string{"c1"}, []string{"c1"})
				switch invalid {
				case "disabled chunk":
					chunk.IsEnabled = false
				case "deleted chunk":
					delete(chunks.chunks, "c1")
				case "foreign KB":
					chunk.KnowledgeBaseID = "kb-other"
				case "deleted document":
					delete(documents.documents, "doc")
				case "missing target evidence":
					graph.Node[1].Chunks = []string{"missing"}
				case "no chunk references":
					graph.Node[0].Chunks, graph.Node[1].Chunks = nil, nil
				}
				result := executeGraphEvidenceTool(t, graphEvidenceTool(graph, chunks, documents, scope.targets))
				assert.Empty(t, result.Data["relations"])
				assert.NotContains(t, result.Output, "--[HEADQUARTERED_IN]-->")
				assert.Len(t, graph.Relation, 1, "validation must not mutate the graph repository's result")
			})
		}
	}
}

// A live entity with the same name cannot validate a stale document instance,
// including when the caller is allowed to query the whole knowledge base.
func TestQueryKnowledgeGraph_RejectsStaleSameNamedInstancesInEveryScope(t *testing.T) {
	for _, scope := range graphEvidenceScopes() {
		for _, invalid := range []string{"disabled chunk", "deleted document"} {
			t.Run(scope.name+"/"+invalid, func(t *testing.T) {
				documents := liveGraphEvidenceDocuments()
				documents.documents["other"] = &types.Knowledge{ID: "other", KnowledgeBaseID: "kb-1"}
				chunks := &stubGraphChunkRepo{chunks: map[string]*types.Chunk{
					"stale": {ID: "stale", KnowledgeID: "doc", KnowledgeBaseID: "kb-1", IsEnabled: true},
					"live":  {ID: "live", KnowledgeID: "other", KnowledgeBaseID: "kb-1", IsEnabled: true},
				}}
				if invalid == "disabled chunk" {
					chunks.chunks["stale"].IsEnabled = false
				} else {
					delete(documents.documents, "doc")
				}
				graph := graphEvidenceGraph([]string{"stale"}, []string{"stale"})
				graph.Node = append(graph.Node,
					&types.GraphNode{
						Name: "Acme", ID: "acme-other", KnowledgeID: "other", Chunks: []string{"live"},
					},
					&types.GraphNode{
						Name: "Shanghai", ID: "city-other", KnowledgeID: "other", Chunks: []string{"live"},
					},
				)
				graph.Relation = append(graph.Relation, &types.GraphRelation{
					Node1: "Acme", Node2: "Shanghai", Type: "HAS_BRANCH_IN",
					SourceID: "acme-other", TargetID: "city-other",
				})
				result := executeGraphEvidenceTool(t, graphEvidenceTool(graph, chunks, documents, scope.targets))
				assert.NotContains(t, result.Output, "--[HEADQUARTERED_IN]-->")
				if scope.targets == nil || scope.name == "whole KB" {
					relations, ok := result.Data["relations"].([]map[string]interface{})
					require.True(t, ok)
					require.Len(t, relations, 1)
					assert.Equal(t, "HAS_BRANCH_IN", relations[0]["type"])
				} else {
					assert.Empty(t, result.Data["relations"])
				}
			})
		}
	}
}

// The eleventh chunk is the target's only evidence. It need not be displayed,
// but its live row must still be considered when validating the relation.
func TestQueryKnowledgeGraph_ValidatesBeyondEvidenceBudget(t *testing.T) {
	for _, scope := range graphEvidenceScopes() {
		t.Run(scope.name, func(t *testing.T) {
			var ids []string
			chunks := &stubGraphChunkRepo{chunks: make(map[string]*types.Chunk)}
			for i := 0; i < 11; i++ {
				id := fmt.Sprintf("c-%02d", i)
				ids = append(ids, id)
				chunks.chunks[id] = &types.Chunk{
					ID: id, KnowledgeID: "doc", KnowledgeBaseID: "kb-1", Content: id, IsEnabled: true,
				}
			}
			graph := graphEvidenceGraph(ids, []string{ids[10]})
			result := executeGraphEvidenceTool(t, graphEvidenceTool(
				graph, chunks, liveGraphEvidenceDocuments(), scope.targets,
			))
			assert.Len(t, result.Data["relations"], 1)
			assert.Equal(t, 11, result.Data["graph_chunks_total"])
			assert.Equal(t, 1, result.Data["graph_chunks_omitted"])
			rows, ok := result.Data["results"].([]map[string]interface{})
			require.True(t, ok, "expected validated evidence rows")
			require.Len(t, rows, 10)
			for i, row := range rows {
				assert.Equal(t, ids[i], row["chunk_id"])
			}
		})
	}
}

func TestQueryKnowledgeGraph_FillsBudgetAfterScopeFiltering(t *testing.T) {
	for _, scope := range graphEvidenceScopes()[1:3] {
		t.Run(scope.name, func(t *testing.T) {
			chunks := &stubGraphChunkRepo{chunks: make(map[string]*types.Chunk)}
			documents := liveGraphEvidenceDocuments()
			documents.documents["outside"] = &types.Knowledge{ID: "outside", KnowledgeBaseID: "kb-1"}
			var ids []string
			for i := 0; i < 22; i++ {
				id := fmt.Sprintf("c-%02d", i)
				ids = append(ids, id)
				doc := "doc"
				if i < 10 {
					doc = "outside"
				}
				chunks.chunks[id] = &types.Chunk{ID: id, KnowledgeID: doc, KnowledgeBaseID: "kb-1", IsEnabled: true}
			}
			graph := graphEvidenceGraph(ids[10:], []string{ids[21]})
			graph.Node = append([]*types.GraphNode{
				{Name: "Outside", ID: "outside", KnowledgeID: "outside", Chunks: ids[:10]},
			}, graph.Node...)
			result := executeGraphEvidenceTool(t, graphEvidenceTool(graph, chunks, documents, scope.targets))
			rows, ok := result.Data["results"].([]map[string]interface{})
			require.True(t, ok, "expected validated evidence rows")
			require.Len(t, rows, 10)
			for i, row := range rows {
				assert.Equal(t, ids[i+10], row["chunk_id"])
			}
			assert.Len(t, result.Data["relations"], 1)
			assert.Equal(t, 12, result.Data["graph_chunks_total"], "out-of-scope chunks are not display omissions")
			assert.Equal(t, 2, result.Data["graph_chunks_omitted"])
		})
	}
}

// Invalid rows cannot exhaust the display budget; lookups are bounded and IDs
// shared by both endpoints are loaded only once, even across multiple batches.
func TestQueryKnowledgeGraph_BatchesAndFiltersEvidenceBeforeBudget(t *testing.T) {
	const candidates = 270
	var ids []string
	chunks := &graphEvidenceChunkRepo{stubGraphChunkRepo: stubGraphChunkRepo{chunks: make(map[string]*types.Chunk)}}
	for i := 0; i < candidates; i++ {
		id := fmt.Sprintf("c-%03d", i)
		ids = append(ids, id)
		chunks.chunks[id] = &types.Chunk{
			ID: id, KnowledgeID: "doc", KnowledgeBaseID: "kb-1", IsEnabled: i >= candidates-12,
		}
	}
	graph := graphEvidenceGraph(append([]string{""}, ids...), []string{ids[candidates-1], ids[0]})
	result := executeGraphEvidenceTool(t, graphEvidenceTool(graph, chunks, liveGraphEvidenceDocuments(), nil))
	require.Len(t, result.Data["results"], 10)
	assert.Len(t, result.Data["relations"], 1)
	assert.Equal(t, 12, result.Data["graph_chunks_total"], "invalid chunks are not display omissions")
	assert.Equal(t, 2, result.Data["graph_chunks_omitted"])
	var requested []string
	for _, batch := range chunks.batches {
		assert.LessOrEqual(t, len(batch), 128)
		requested = append(requested, batch...)
	}
	assert.Equal(t, ids, requested)
	require.Len(t, chunks.contentBatches, 1)
	assert.Equal(t, ids[candidates-12:candidates-2], chunks.contentBatches[0])
}

func TestQueryKnowledgeGraph_BoundsCandidateValidation(t *testing.T) {
	for _, scope := range graphEvidenceScopes() {
		for _, tt := range []struct {
			name       string
			candidates int
			duplicates bool
			invalid    bool
			unresolved bool
		}{
			{name: "exact limit", candidates: graphQueryMaxEvidenceCandidates},
			{name: "only duplicates after limit", candidates: graphQueryMaxEvidenceCandidates, duplicates: true},
			{name: "one over limit", candidates: graphQueryMaxEvidenceCandidates + 1},
			{name: "many over limit", candidates: graphQueryMaxEvidenceCandidates*3 + 1},
			{name: "all invalid", candidates: graphQueryMaxEvidenceCandidates + 1, invalid: true},
			{name: "unresolved endpoint", candidates: graphQueryMaxEvidenceCandidates + 1, unresolved: true},
		} {
			t.Run(scope.name+"/"+tt.name, func(t *testing.T) {
				chunks := &graphEvidenceChunkRepo{
					stubGraphChunkRepo: stubGraphChunkRepo{chunks: make(map[string]*types.Chunk)},
				}
				var ids []string
				for i := 0; i < tt.candidates; i++ {
					id := fmt.Sprintf("c-%04d", i)
					ids = append(ids, id)
					chunks.chunks[id] = &types.Chunk{
						ID: id, KnowledgeID: "doc", KnowledgeBaseID: "kb-1",
						IsEnabled: !tt.invalid, Content: id,
					}
				}
				target := ids[10] // beyond display cap, within validation budget
				if tt.unresolved {
					target = ids[len(ids)-1]
				}
				source := append([]string{""}, ids...)
				if tt.duplicates {
					source = append(source, ids...)
				}
				documents := liveGraphEvidenceDocuments()
				graph := graphEvidenceGraph(source, []string{target})
				result := executeGraphEvidenceTool(t, graphEvidenceTool(graph, chunks, documents, scope.targets))

				scanned := min(tt.candidates, graphQueryMaxEvidenceCandidates)
				require.Len(t, chunks.batches, (scanned+graphQueryChunkBatchSize-1)/graphQueryChunkBatchSize)
				var requested []string
				for _, batch := range chunks.batches {
					require.LessOrEqual(t, len(batch), graphQueryChunkBatchSize)
					requested = append(requested, batch...)
				}
				require.Equal(t, ids[:scanned], requested, "never query IDs outside the validation budget")
				if tt.invalid {
					require.Empty(t, chunks.contentBatches)
					require.Empty(t, documents.documentBatches)
					require.Empty(t, documents.tagBatches)
					require.Empty(t, result.Data["results"])
				} else {
					require.Len(t, chunks.contentBatches, 1)
					require.Equal(t, ids[:graphQueryMaxChunks], chunks.contentBatches[0])
					require.Len(t, documents.documentBatches, len(chunks.batches))
					if scope.name == "tag" {
						require.Len(t, documents.tagBatches, len(chunks.batches))
					}
				}
				if tt.invalid || tt.unresolved {
					require.Empty(t, result.Data["relations"], "unchecked evidence must not validate an endpoint")
				} else {
					require.Len(t, result.Data["relations"], 1)
				}
				limited := tt.candidates > graphQueryMaxEvidenceCandidates
				require.Equal(t, limited, result.Data["graph_validation_truncated"] == true)
				if limited {
					require.Equal(t, scanned, result.Data["graph_candidates_validated_total"])
					require.Equal(t, graphQueryMaxEvidenceCandidates, result.Data["graph_validation_limit_per_kb"])
					require.Equal(t, true, result.Data["graph_chunks_total_is_lower_bound"])
					require.Equal(t, true, result.Data["relations_total_is_lower_bound"])
					require.Contains(t, result.Output, "Some candidate evidence was not checked")
					valid := scanned
					if tt.invalid {
						valid = 0
					}
					require.Equal(t, valid, result.Data["graph_chunks_total"])
					require.Equal(t, max(0, valid-graphQueryMaxChunks), result.Data["graph_chunks_omitted"])
					require.Contains(t, result.Output, fmt.Sprintf("at least %d valid in-scope evidence chunks", valid))
				} else {
					require.NotContains(t, result.Output, "validation stopped")
					require.NotContains(t, result.Data, "graph_chunks_total_is_lower_bound")
				}
			})
		}
	}
}

func TestQueryKnowledgeGraph_RemovesEvidenceChangedBeforeContentRead(t *testing.T) {
	for _, change := range []string{"disabled", "deleted", "foreign KB", "different document"} {
		t.Run(change, func(t *testing.T) {
			chunk := &types.Chunk{ID: "c1", KnowledgeID: "doc", KnowledgeBaseID: "kb-1", IsEnabled: true}
			chunks := &graphEvidenceChunkRepo{
				stubGraphChunkRepo: stubGraphChunkRepo{chunks: map[string]*types.Chunk{"c1": chunk}},
			}
			chunks.beforeContent = func() {
				switch change {
				case "disabled":
					chunk.IsEnabled = false
				case "deleted":
					delete(chunks.chunks, chunk.ID)
				case "foreign KB":
					chunk.KnowledgeBaseID = "kb-other"
				case "different document":
					chunk.KnowledgeID = "other"
				}
			}
			result := executeGraphEvidenceTool(t, graphEvidenceTool(
				graphEvidenceGraph([]string{"c1"}, []string{"c1"}), chunks, liveGraphEvidenceDocuments(), nil,
			))
			require.Empty(t, result.Data["results"])
			require.Empty(t, result.Data["relations"])
		})
	}
}

func TestQueryKnowledgeGraph_ValidationFailuresExcludeGraphAndPreserveText(t *testing.T) {
	for _, failure := range []string{
		"chunk lookup", "document lookup", "missing chunk repository", "missing document service", "later batch",
		"content lookup",
	} {
		t.Run(failure, func(t *testing.T) {
			chunks := &graphEvidenceChunkRepo{stubGraphChunkRepo: stubGraphChunkRepo{chunks: map[string]*types.Chunk{
				"c1": {ID: "c1", KnowledgeID: "doc", KnowledgeBaseID: "kb-1", IsEnabled: true},
			}}}
			var chunkRepo interfaces.ChunkRepository = chunks
			documents := liveGraphEvidenceDocuments()
			var documentService interfaces.KnowledgeService = documents
			graph := graphEvidenceGraph([]string{"c1"}, []string{"c1"})
			switch failure {
			case "chunk lookup":
				chunks.failAt = 1
			case "document lookup":
				documents.err = assert.AnError
			case "content lookup":
				chunks.contentErr = assert.AnError
			case "missing chunk repository":
				chunkRepo = nil
			case "missing document service":
				documentService = nil
			case "later batch":
				chunks.failAt = 2
				for i := 0; i < 140; i++ {
					graph.Node[0].Chunks = append(graph.Node[0].Chunks, fmt.Sprintf("missing-%03d", i))
				}
			}
			tool := graphEvidenceTool(graph, chunkRepo, documentService, graphEvidenceScopes()[0].targets)
			tool.knowledgeService.(*stubKnowledgeBaseService).results = []*types.SearchResult{
				{ID: "text", KnowledgeID: "doc", KnowledgeBaseID: "kb-1", Content: "Text fallback"},
			}
			result := executeGraphEvidenceTool(t, tool)
			assert.Empty(t, result.Data["relations"])
			rows := result.Data["results"].([]map[string]interface{})
			require.Len(t, rows, 1)
			assert.Equal(t, "text", rows[0]["chunk_id"])
			assert.NotEmpty(t, result.Data["errors"])
			assert.Contains(t, result.Output, "graph query failed")
			assert.NotContains(t, result.Output, "Truncated", "validation failures are not capped evidence")
			_, hasChunkTotal := result.Data["graph_chunks_total"]
			assert.False(t, hasChunkTotal)
		})
	}
}

func TestQueryKnowledgeGraph_RejectsRelationsWhenTagValidationFails(t *testing.T) {
	documents := liveGraphEvidenceDocuments()
	documents.tagErr = assert.AnError
	chunks := &stubGraphChunkRepo{chunks: map[string]*types.Chunk{
		"c1": {ID: "c1", KnowledgeID: "doc", KnowledgeBaseID: "kb-1", IsEnabled: true},
	}}
	result := executeGraphEvidenceTool(t, graphEvidenceTool(
		graphEvidenceGraph([]string{"c1"}, []string{"c1"}), chunks, documents, graphEvidenceScopes()[2].targets,
	))
	assert.Empty(t, result.Data["relations"])
	assert.Empty(t, result.Data["results"])
	assert.NotEmpty(t, result.Data["errors"])
	assert.Contains(t, result.Output, "failed to validate graph result scope")
}
