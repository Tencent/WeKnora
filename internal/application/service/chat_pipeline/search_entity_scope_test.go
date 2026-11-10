package chatpipeline

import (
	"context"
	"fmt"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/models/chat"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

type entityGraphRepo struct {
	interfaces.RetrieveGraphRepository
	graph   *types.GraphData
	mu      sync.Mutex
	queries []types.NameSpace
}

func (r *entityGraphRepo) SearchNode(
	_ context.Context,
	namespace types.NameSpace,
	_ []string,
) (*types.GraphData, error) {
	r.mu.Lock()
	r.queries = append(r.queries, namespace)
	r.mu.Unlock()
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
	cm := &types.ChatManage{
		PipelineRequest: types.PipelineRequest{SearchTargets: types.SearchTargets{
			{Type: types.SearchTargetTypeKnowledgeBase, KnowledgeBaseID: "kb-shared", TenantID: 2},
		}},
		PipelineState: types.PipelineState{Entity: []string{"Docker"}, EntityKBIDs: []string{"kb-shared"}},
	}
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

type entityTargetKBRepo struct {
	interfaces.KnowledgeBaseRepository
	requested []string
}

func (r *entityTargetKBRepo) GetKnowledgeBaseByIDs(_ context.Context, ids []string) ([]*types.KnowledgeBase, error) {
	r.requested = append([]string(nil), ids...)
	var out []*types.KnowledgeBase
	for _, id := range ids {
		out = append(out, &types.KnowledgeBase{
			ID: id, ExtractConfig: &types.ExtractConfig{Enabled: id != "kb-disabled"},
		})
	}
	return out, nil
}

type entityTargetChat struct {
	openStreamChat
	calls int
}

func (m *entityTargetChat) Chat(context.Context, []chat.Message, *chat.ChatOptions) (*types.ChatResponse, error) {
	m.calls++
	return &types.ChatResponse{Content: `[{"entity":"Docker"}]`}, nil
}

// Exercise extraction and search together: both stages must keep the same
// normalized scope, even when legacy request IDs disagree with it.
func TestEntityRetrievalUsesSearchTargets(t *testing.T) {
	t.Setenv("NEO4J_ENABLE", "true")
	tests := []struct {
		name       string
		rawKBs     []string
		rawDocs    []string
		targets    types.SearchTargets
		wantIDs    []string
		wantScopes []types.NameSpace
	}{
		{
			name:   "tagged documents exclude same-KB evidence outside the tag",
			rawKBs: []string{"kb-shared"},
			targets: types.SearchTargets{{
				Type:            types.SearchTargetTypeKnowledge,
				KnowledgeBaseID: "kb-shared",
				TenantID:        2,
				KnowledgeIDs:    []string{"doc-shared", "doc-disabled", "doc-gone"},
				ScopeTagIDs:     []string{"tag-selected"},
			}},
			wantIDs: []string{"shared"},
			wantScopes: []types.NameSpace{
				{KnowledgeBase: "kb-shared", Knowledge: "doc-shared"},
				{KnowledgeBase: "kb-shared", Knowledge: "doc-disabled"},
				{KnowledgeBase: "kb-shared", Knowledge: "doc-gone"},
			},
		},
		{
			name: "tag-only mention uses resolved targets without raw IDs",
			targets: types.SearchTargets{{
				Type: types.SearchTargetTypeKnowledge, KnowledgeBaseID: "kb-shared",
				TenantID: 2, KnowledgeIDs: []string{"doc-shared"}, ScopeTagIDs: []string{"tag-selected"},
			}},
			wantIDs:    []string{"shared"},
			wantScopes: []types.NameSpace{{KnowledgeBase: "kb-shared", Knowledge: "doc-shared"}},
		},
		{
			name:    "normalized targets override raw request IDs",
			rawKBs:  []string{"kb-other"},
			rawDocs: []string{"doc-other"},
			targets: types.SearchTargets{{
				Type: types.SearchTargetTypeKnowledge, KnowledgeBaseID: "kb-shared",
				KnowledgeIDs: []string{"doc-shared"},
			}},
			wantIDs:    []string{"shared"},
			wantScopes: []types.NameSpace{{KnowledgeBase: "kb-shared", Knowledge: "doc-shared"}},
		},
		{
			name:   "empty tag results never fall back to raw KB or file IDs",
			rawKBs: []string{"kb-shared"}, rawDocs: []string{"doc-shared"},
		},
		{
			name:    "empty document target is not a whole-KB target",
			rawKBs:  []string{"kb-shared"},
			targets: types.SearchTargets{{Type: types.SearchTargetTypeKnowledge, KnowledgeBaseID: "kb-shared"}},
		},
		{
			name:   "whole KB and file in another KB are both searched",
			rawKBs: []string{"kb-full"}, rawDocs: []string{"doc-shared"},
			targets: types.SearchTargets{
				{Type: types.SearchTargetTypeKnowledgeBase, KnowledgeBaseID: "kb-full"},
				{
					Type:            types.SearchTargetTypeKnowledge,
					KnowledgeBaseID: "kb-shared",
					TenantID:        2,
					KnowledgeIDs:    []string{"doc-shared"},
				},
			},
			wantIDs: []string{"full", "shared"},
			wantScopes: []types.NameSpace{
				{KnowledgeBase: "kb-full"}, {KnowledgeBase: "kb-shared", Knowledge: "doc-shared"},
			},
		},
		{
			name: "whole KB covers repeated KB and file targets without extra queries",
			targets: types.SearchTargets{
				{Type: types.SearchTargetTypeKnowledgeBase, KnowledgeBaseID: "kb-full"},
				{Type: types.SearchTargetTypeKnowledgeBase, KnowledgeBaseID: "kb-full"},
				{
					Type: types.SearchTargetTypeKnowledge, KnowledgeBaseID: "kb-full",
					KnowledgeIDs: []string{"doc-full", "doc-full"},
				},
			},
			wantIDs: []string{"full"}, wantScopes: []types.NameSpace{{KnowledgeBase: "kb-full"}},
		},
		{
			name: "physical chunk tags remain constrained in KB targets",
			targets: types.SearchTargets{{
				Type: types.SearchTargetTypeKnowledgeBase, KnowledgeBaseID: "kb-shared",
				TenantID: 2, TagIDs: []string{"tag-selected"},
			}},
			wantIDs: []string{"shared"}, wantScopes: []types.NameSpace{{KnowledgeBase: "kb-shared"}},
		},
		{
			name:    "KBs without graph extraction do not produce graph queries",
			targets: types.SearchTargets{{Type: types.SearchTargetTypeKnowledgeBase, KnowledgeBaseID: "kb-disabled"}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			model := &entityTargetChat{}
			kbRepo := &entityTargetKBRepo{}
			extract := &PluginExtractEntity{
				modelService: &stubModelService{model: model}, knowledgeBaseRepo: kbRepo,
				template: &types.PromptTemplateStructured{Description: "Extract entities"},
			}
			graphRepo := &entityGraphRepo{graph: &types.GraphData{Node: []*types.GraphNode{
				{Name: "Docker", Chunks: []string{"shared", "outside", "disabled", "orphan", "foreign", "full"}},
			}}}
			chunks := map[string]*types.Chunk{
				"shared":   entityChunk("shared", 2, "kb-shared", "doc-shared"),
				"outside":  entityChunk("outside", 2, "kb-shared", "doc-outside"),
				"disabled": entityChunk("disabled", 2, "kb-shared", "doc-disabled"),
				"orphan":   entityChunk("orphan", 2, "kb-shared", "doc-gone"),
				"foreign":  entityChunk("foreign", 3, "kb-other", "doc-other"),
				"full":     entityChunk("full", 1, "kb-full", "doc-full"),
			}
			chunks["shared"].TagID = "tag-selected"
			chunks["disabled"].IsEnabled = false
			knowledge := map[string]*types.Knowledge{}
			for _, chunk := range chunks {
				if chunk.KnowledgeID != "doc-gone" {
					knowledge[chunk.KnowledgeID] = &types.Knowledge{
						ID: chunk.KnowledgeID, KnowledgeBaseID: chunk.KnowledgeBaseID,
					}
				}
			}
			search := &PluginSearchEntity{
				graphRepo: graphRepo, chunkRepo: &expandChunkRepo{chunks: chunks},
				knowledgeRepo: &entityKnowledgeRepo{knowledge: knowledge},
			}
			cm := &types.ChatManage{PipelineRequest: types.PipelineRequest{
				Query: "Docker", TenantID: 1, KnowledgeBaseIDs: tt.rawKBs,
				KnowledgeIDs: tt.rawDocs, SearchTargets: tt.targets,
			}}
			ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(1))
			if err := extract.OnEvent(ctx, types.QUERY_UNDERSTAND, cm, func() *PluginError { return nil }); err != nil {
				t.Fatalf("extract: %v", err)
			}
			if err := search.OnEvent(ctx, types.ENTITY_SEARCH, cm, func() *PluginError { return nil }); err != nil {
				t.Fatalf("search: %v", err)
			}
			var gotIDs []string
			for _, result := range cm.SearchResult {
				gotIDs = append(gotIDs, result.ID)
			}
			slices.Sort(gotIDs)
			if !slices.Equal(gotIDs, tt.wantIDs) {
				t.Fatalf("result IDs = %v, want %v", gotIDs, tt.wantIDs)
			}
			if len(graphRepo.queries) != len(tt.wantScopes) {
				t.Fatalf("graph queries = %+v, want %+v", graphRepo.queries, tt.wantScopes)
			}
			for _, scope := range tt.wantScopes {
				if !slices.Contains(graphRepo.queries, scope) {
					t.Errorf("missing graph namespace %+v in %+v", scope, graphRepo.queries)
				}
			}
			if len(tt.targets) == 0 && (model.calls != 0 || len(kbRepo.requested) != 0) {
				t.Fatal("empty normalized scope must not trigger graph extraction or KB lookups")
			}
		})
	}
}

type blockingEntityGraphRepo struct {
	interfaces.RetrieveGraphRepository
	entered chan types.NameSpace
	mu      sync.Mutex
	active  int
	peak    int
	queries []types.NameSpace
}

func (r *blockingEntityGraphRepo) SearchNode(
	ctx context.Context,
	namespace types.NameSpace,
	_ []string,
) (*types.GraphData, error) {
	r.mu.Lock()
	r.active++
	r.peak = max(r.peak, r.active)
	r.queries = append(r.queries, namespace)
	r.mu.Unlock()
	defer func() {
		r.mu.Lock()
		r.active--
		r.mu.Unlock()
	}()
	r.entered <- namespace
	<-ctx.Done()
	return nil, ctx.Err()
}

func TestSearchEntityBoundsTagFanoutAndFinishesCanceledQueries(t *testing.T) {
	var knowledgeIDs []string
	for i := range 12 {
		knowledgeIDs = append(knowledgeIDs, fmt.Sprintf("doc-%d", i))
	}
	repo := &blockingEntityGraphRepo{entered: make(chan types.NameSpace, len(knowledgeIDs))}
	plugin := &PluginSearchEntity{graphRepo: repo}
	cm := &types.ChatManage{
		PipelineRequest: types.PipelineRequest{SearchTargets: types.SearchTargets{{
			Type: types.SearchTargetTypeKnowledge, KnowledgeBaseID: "kb", KnowledgeIDs: knowledgeIDs,
			ScopeTagIDs: []string{"tag-many-documents"},
		}}},
		PipelineState: types.PipelineState{Entity: []string{"Docker"}, EntityKBIDs: []string{"kb"}},
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan *PluginError, 1)
	go func() { done <- plugin.OnEvent(ctx, types.ENTITY_SEARCH, cm, func() *PluginError { return nil }) }()
	for range 4 {
		select {
		case <-repo.entered:
		case <-time.After(5 * time.Second):
			t.Fatal("graph searches did not start")
		}
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("graph failures should preserve the existing partial-success behavior: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("canceled graph searches did not finish")
	}
	if repo.peak != 4 || repo.active != 0 || len(repo.queries) != len(knowledgeIDs) {
		t.Fatalf("graph fan-out: peak=%d, active=%d, queries=%v", repo.peak, repo.active, repo.queries)
	}
	for _, knowledgeID := range knowledgeIDs {
		if !slices.Contains(repo.queries, types.NameSpace{KnowledgeBase: "kb", Knowledge: knowledgeID}) {
			t.Errorf("missing graph query for %s", knowledgeID)
		}
	}
}
