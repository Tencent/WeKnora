package chatpipeline

import (
	"context"
	"errors"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

type evidenceSchemaGraphRepo struct {
	*entityGraphRepo
	typedGraph  *types.GraphData
	typedErr    error
	nameErr     error
	typedScopes []types.NameSpace
	names       []string
}

func (r *evidenceSchemaGraphRepo) SearchNodeWithSchema(
	_ context.Context, namespace types.NameSpace, _ []types.GraphQuery,
) (*types.GraphData, error) {
	r.typedScopes = append(r.typedScopes, namespace)
	return r.typedGraph, r.typedErr
}

func (r *evidenceSchemaGraphRepo) SearchNode(
	ctx context.Context, namespace types.NameSpace, names []string,
) (*types.GraphData, error) {
	r.names = append([]string(nil), names...)
	graph, _ := r.entityGraphRepo.SearchNode(ctx, namespace, names)
	return graph, r.nameErr
}

type evidenceChunkRepo struct {
	*expandChunkRepo
	err error
}

func (r *evidenceChunkRepo) ListChunksByIDOnly(ctx context.Context, ids []string) ([]*types.Chunk, error) {
	if r.err != nil {
		return nil, r.err
	}
	return r.expandChunkRepo.ListChunksByIDOnly(ctx, ids)
}

type evidenceKnowledgeRepo struct {
	*entityKnowledgeRepo
	err error
}

func (r *evidenceKnowledgeRepo) GetKnowledgeBatchByIDOnly(
	ctx context.Context, ids []string,
) ([]*types.Knowledge, error) {
	if r.err != nil {
		return nil, r.err
	}
	return r.entityKnowledgeRepo.GetKnowledgeBatchByIDOnly(ctx, ids)
}

func TestSearchEntitySchemaFallbackUsesLiveScopedEvidence(t *testing.T) {
	for _, scopeMode := range []string{"document", "tag"} {
		t.Run(scopeMode, func(t *testing.T) {
			for _, tc := range []struct {
				name         string
				wantFallback bool
				wantIDs      []string
			}{
				{"disabled chunk", true, []string{"legacy"}},
				{"deleted chunk", true, []string{"legacy"}},
				{"deleted document", true, []string{"legacy"}},
				{"out of scope", true, []string{"legacy"}},
				{"foreign KB", true, []string{"legacy"}},
				{"empty graph", true, []string{"legacy"}},
				{"node without evidence", true, []string{"legacy"}},
				{"valid typed", false, []string{"typed"}},
				{"already seen typed", false, []string{"typed"}},
				{"invalid name evidence", true, nil},
				{"typed query error", false, nil},
				{"chunk lookup error", false, nil},
				{"document lookup error", false, nil},
				{"name query error", true, nil},
			} {
				t.Run(tc.name, func(t *testing.T) {
					target := &types.SearchTarget{
						Type: types.SearchTargetTypeKnowledgeBase, KnowledgeBaseID: "kb", TenantID: 2,
						TagIDs: []string{"selected"},
					}
					namespace := types.NameSpace{KnowledgeBase: "kb"}
					if scopeMode == "document" {
						target.Type = types.SearchTargetTypeKnowledge
						target.KnowledgeIDs = []string{"doc"}
						namespace.Knowledge = "doc"
					}
					chunks := map[string]*types.Chunk{
						"typed":  entityChunk("typed", 2, "kb", "doc"),
						"legacy": entityChunk("legacy", 2, "kb", "doc"),
					}
					chunks["typed"].TagID = "selected"
					chunks["legacy"].TagID = "selected"
					knowledge := map[string]*types.Knowledge{
						"doc": {ID: "doc", KnowledgeBaseID: "kb"},
					}
					repo := &evidenceSchemaGraphRepo{
						entityGraphRepo: &entityGraphRepo{graph: &types.GraphData{Node: []*types.GraphNode{
							{Name: "Alex", Chunks: []string{"legacy"}},
						}}},
						typedGraph: &types.GraphData{Node: []*types.GraphNode{
							{Name: "Alex", Chunks: []string{"typed"}},
						}},
					}
					chunkRepo := &evidenceChunkRepo{expandChunkRepo: &expandChunkRepo{chunks: chunks}}
					knowledgeRepo := &evidenceKnowledgeRepo{
						entityKnowledgeRepo: &entityKnowledgeRepo{knowledge: knowledge},
					}
					cm := &types.ChatManage{
						PipelineRequest: types.PipelineRequest{SearchTargets: types.SearchTargets{target}},
						PipelineState: types.PipelineState{
							EntityKBIDs: []string{"kb"},
							EntityQueries: map[string][]types.GraphQuery{
								"kb": {{Name: "Alex", EntityType: "Person"}},
							},
						},
					}
					switch tc.name {
					case "disabled chunk":
						chunks["typed"].IsEnabled = false
					case "deleted chunk":
						delete(chunks, "typed")
					case "deleted document":
						chunks["typed"].KnowledgeID = "gone"
					case "out of scope":
						if scopeMode == "document" {
							chunks["typed"].KnowledgeID = "other"
							knowledge["other"] = &types.Knowledge{ID: "other", KnowledgeBaseID: "kb"}
						} else {
							chunks["typed"].TagID = "other"
						}
					case "foreign KB":
						chunks["typed"].KnowledgeBaseID = "other"
					case "empty graph":
						repo.typedGraph = nil
					case "node without evidence":
						repo.typedGraph.Node = []*types.GraphNode{nil, {Name: "Alex"}}
					case "already seen typed":
						cm.SearchResult = []*types.SearchResult{{ID: "typed"}}
					case "invalid name evidence":
						chunks["typed"].IsEnabled = false
						chunks["legacy"].TagID = "other"
					case "typed query error":
						repo.typedErr = errors.New("graph unavailable")
					case "chunk lookup error":
						chunkRepo.err = errors.New("chunk store unavailable")
					case "document lookup error":
						knowledgeRepo.err = errors.New("document store unavailable")
					case "name query error":
						chunks["typed"].IsEnabled = false
						repo.nameErr = errors.New("name search unavailable")
					}
					plugin := &PluginSearchEntity{
						graphRepo: repo, chunkRepo: chunkRepo, knowledgeRepo: knowledgeRepo,
					}
					ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(1))
					err := plugin.OnEvent(ctx, types.ENTITY_SEARCH, cm, func() *PluginError { return nil })
					if tc.name == "invalid name evidence" {
						require.Equal(t, ErrSearchNothing, err)
					} else {
						require.Nil(t, err)
					}
					require.Equal(t, []types.NameSpace{namespace}, repo.typedScopes)
					if tc.wantFallback {
						require.Equal(t, []types.NameSpace{namespace}, repo.queries)
						require.Equal(t, []string{"Alex"}, repo.names)
					} else {
						require.Empty(t, repo.queries)
					}
					var gotIDs []string
					for _, result := range cm.SearchResult {
						gotIDs = append(gotIDs, result.ID)
					}
					require.Equal(t, tc.wantIDs, gotIDs)
				})
			}
		})
	}
}

func TestSchemaEvidenceValidationKeepsExactNamespaceAndBudget(t *testing.T) {
	chunks := map[string]*types.Chunk{}
	var chunkIDs []string
	for i := range maxEntityChunks + 1 {
		id := string(rune('a' + i))
		chunkIDs = append(chunkIDs, id)
		chunks[id] = entityChunk(id, 2, "kb", "other")
	}
	// A usable chunk beyond the bounded candidate window must not cause
	// an unbounded evidence lookup.
	chunks[chunkIDs[maxEntityChunks]].KnowledgeID = "doc"
	plugin := &PluginSearchEntity{
		chunkRepo: &expandChunkRepo{chunks: chunks},
		knowledgeRepo: &entityKnowledgeRepo{knowledge: map[string]*types.Knowledge{
			"doc":   {ID: "doc", KnowledgeBaseID: "kb"},
			"other": {ID: "other", KnowledgeBaseID: "kb"},
		}},
	}
	graph := &types.GraphData{Node: []*types.GraphNode{{Chunks: chunkIDs}}}
	targets := types.SearchTargets{{Type: types.SearchTargetTypeKnowledgeBase, KnowledgeBaseID: "kb"}}
	usable, err := plugin.hasUsableGraphEvidence(
		context.Background(), graph, types.NameSpace{KnowledgeBase: "kb", Knowledge: "doc"}, targets,
	)
	require.NoError(t, err)
	require.False(t, usable)
	graph.Node[0].Chunks = []string{chunkIDs[maxEntityChunks]}
	usable, err = plugin.hasUsableGraphEvidence(
		context.Background(), graph, types.NameSpace{KnowledgeBase: "kb", Knowledge: "doc"}, targets,
	)
	require.NoError(t, err)
	require.True(t, usable)
}
