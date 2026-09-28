package chatpipeline

import (
	"context"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

type entityGraphRepo struct {
	interfaces.RetrieveGraphRepository
	graph *types.GraphData
}

func (r *entityGraphRepo) SearchNode(context.Context, types.NameSpace, []string) (*types.GraphData, error) {
	return r.graph, nil
}

type entityKnowledgeRepo struct {
	interfaces.KnowledgeRepository
	knowledge map[string]*types.Knowledge
}

func (r *entityKnowledgeRepo) GetKnowledgeBatchByIDOnly(_ context.Context, ids []string) ([]*types.Knowledge, error) {
	var out []*types.Knowledge
	for _, id := range ids {
		if k := r.knowledge[id]; k != nil {
			out = append(out, k)
		}
	}
	return out, nil
}

// Graph hits resolve across workspaces for the KBs in scope, never outside
// them, and a chunk whose document is gone is skipped instead of panicking.
func TestSearchEntityResolvesSharedChunksWithinScope(t *testing.T) {
	plugin := &PluginSearchEntity{
		graphRepo: &entityGraphRepo{graph: &types.GraphData{Node: []*types.GraphNode{
			{Name: "Docker", Chunks: []string{"shared", "outside", "orphan"}},
		}}},
		chunkRepo: &expandChunkRepo{chunks: map[string]*types.Chunk{
			"shared":  entityChunk("shared", 2, "kb-shared", "doc"),
			"outside": entityChunk("outside", 3, "kb-other", "doc-x"),
			"orphan":  entityChunk("orphan", 2, "kb-shared", "gone"),
		}},
		knowledgeRepo: &entityKnowledgeRepo{knowledge: map[string]*types.Knowledge{
			"doc":   {ID: "doc", KnowledgeBaseID: "kb-shared", Title: "Shared doc"},
			"doc-x": {ID: "doc-x", KnowledgeBaseID: "kb-other", Title: "Other"},
		}},
	}
	cm := &types.ChatManage{PipelineState: types.PipelineState{
		Entity: []string{"Docker"}, EntityKBIDs: []string{"kb-shared"},
	}}
	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(1))

	if err := plugin.OnEvent(ctx, types.ENTITY_SEARCH, cm, func() *PluginError { return nil }); err != nil {
		t.Fatalf("OnEvent: %v", err)
	}
	if len(cm.SearchResult) != 1 || cm.SearchResult[0].ID != "shared" ||
		cm.SearchResult[0].KnowledgeTitle != "Shared doc" {
		t.Fatalf("entity results = %+v", cm.SearchResult)
	}
}

func entityChunk(id string, tenantID uint64, kbID, knowledgeID string) *types.Chunk {
	return &types.Chunk{
		ID: id, TenantID: tenantID, KnowledgeBaseID: kbID, KnowledgeID: knowledgeID, IsEnabled: true, Content: id,
	}
}
