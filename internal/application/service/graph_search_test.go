package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/Tencent/WeKnora/internal/application/access"
	apperrors "github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

type graphSearchStoreStub struct {
	candidates *types.GraphSearchCandidates
	err        error
	calls      int
	namespace  types.NameSpace
	query      string
}

func (s *graphSearchStoreStub) SearchGraph(
	_ context.Context, ns types.NameSpace, query string,
) (*types.GraphSearchCandidates, error) {
	s.calls++
	s.namespace, s.query = ns, query
	return s.candidates, s.err
}

type graphSearchEvidenceStub struct {
	rows     []types.GraphSearchSourceChunk
	err      error
	calls    int
	tenantID uint64
	kbID     string
	ids      []string
}

func (s *graphSearchEvidenceStub) GetGraphSourceChunks(
	_ context.Context, tenantID uint64, kbID string, ids []string,
) ([]types.GraphSearchSourceChunk, error) {
	s.calls++
	s.tenantID, s.kbID, s.ids = tenantID, kbID, append([]string{}, ids...)
	return s.rows, s.err
}

func graphSearchTestKB() *types.KnowledgeBase {
	return &types.KnowledgeBase{
		ID: "kb", TenantID: 7,
		IndexingStrategy: types.IndexingStrategy{GraphEnabled: true},
		ExtractConfig:    &types.ExtractConfig{Enabled: true},
	}
}

func graphSearchTestContext(kb *types.KnowledgeBase) context.Context {
	grant := &access.KBAccess{
		KnowledgeBase: kb, EffectiveTenantID: kb.TenantID,
		Caller:     types.Caller{TenantID: 1, UserID: "reader", Role: types.TenantRoleViewer},
		Permission: types.OrgRoleViewer,
	}
	return grant.WithGrant(context.Background())
}

func graphSearchTestSource(id, doc string) types.GraphSearchSourceChunk {
	return types.GraphSearchSourceChunk{
		ID: id, KnowledgeID: doc,
		TenantID: 7, KnowledgeBaseID: "kb", Title: doc, Content: "source " + id,
	}
}

func TestGraphSearchPublicIdentitiesDirectionAndProvenance(t *testing.T) {
	kb := graphSearchTestKB()
	graph := &types.GraphData{
		Node: []*types.GraphNode{
			{
				ID: "physical-a1", Name: "Acme", KnowledgeID: "doc1",
				Chunks: []string{"c1"}, Attributes: []string{"attr1"},
			},
			{ID: "physical-b1", Name: "Shanghai", KnowledgeID: "doc1", Chunks: []string{"c1"}},
			{
				ID: "physical-a2", Name: "Acme", KnowledgeID: "doc2",
				Chunks: []string{"c2"}, Attributes: []string{"attr2"},
			},
			{ID: "physical-b2", Name: "Shanghai", KnowledgeID: "doc2", Chunks: []string{"c2"}},
			{ID: "physical-dead", Name: "Acme", KnowledgeID: "deleted", Chunks: []string{"missing"}},
		},
		Relation: []*types.GraphRelation{
			{ID: "r1", SourceID: "physical-a1", TargetID: "physical-b1", Type: "HEADQUARTERED_IN"},
			{ID: "r1-duplicate", SourceID: "physical-a1", TargetID: "physical-b1", Type: "HEADQUARTERED_IN"},
			{ID: "r2", SourceID: "physical-b1", TargetID: "physical-a1", Type: "RELATED_TO"},
			{ID: "r3", SourceID: "physical-a2", TargetID: "physical-b2", Type: "HAS_BRANCH_IN"},
			{ID: "self", SourceID: "physical-a1", TargetID: "physical-a1", Type: "SELF"},
			{ID: "dead", SourceID: "physical-dead", TargetID: "physical-b1", Type: "SECRET"},
			{ID: "cross-document", SourceID: "physical-a1", TargetID: "physical-b2", Type: "INVALID"},
			{Node1: "Acme", Node2: "Shanghai", Type: "NAMES_ARE_NOT_IDENTITIES"},
		},
	}
	store := &graphSearchStoreStub{candidates: &types.GraphSearchCandidates{Graph: graph}}
	evidence := &graphSearchEvidenceStub{rows: []types.GraphSearchSourceChunk{
		graphSearchTestSource("c1", "doc1"), graphSearchTestSource("c2", "doc2"),
	}}
	svc := NewGraphSearchService(store, evidence)
	result, err := svc.Search(graphSearchTestContext(kb), kb, types.GraphSearchRequest{Query: " Acme "})
	require.NoError(t, err)
	require.Len(t, result.Entities, 4)
	require.Len(t, result.Relationships, 4)
	require.Len(t, result.SourceChunks, 2)
	require.False(t, result.Meta.Truncated)
	require.NotEqual(t, result.Entities[0].ID, result.Entities[2].ID, "same names in different documents stay distinct")
	require.Equal(t, "Acme", store.query)
	require.Equal(t, types.NameSpace{KnowledgeBase: "kb"}, store.namespace)
	require.Equal(t, uint64(7), evidence.tenantID, "use shared KB owner, never caller/execution tenant")
	require.Equal(t, "kb", evidence.kbID)
	require.Equal(t, result.Entities[0].ID, result.Relationships[0].SourceID)
	require.Equal(t, result.Entities[1].ID, result.Relationships[0].TargetID)
	require.Equal(t, result.Entities[1].ID, result.Relationships[1].SourceID)
	data, err := json.Marshal(result)
	require.NoError(t, err)
	for _, secret := range []string{"physical-", "SECRET", "INVALID", "NAMES_ARE_NOT_IDENTITIES", "tenant_id"} {
		require.NotContains(t, string(data), secret)
	}
	// Reindexing changes backend IDs but not the public document-instance ID.
	graph.Node[0].ID = "new-backend-id"
	result2, err := svc.Search(graphSearchTestContext(kb), kb, types.GraphSearchRequest{Query: "Acme"})
	require.NoError(t, err)
	require.Equal(t, result.Entities[0].ID, result2.Entities[0].ID)
	require.NotEqual(t, graphSearchPublicID("a:b", "c"), graphSearchPublicID("a", "b:c"))
}

func TestGraphSearchRequiresEverySourceInExactDocument(t *testing.T) {
	kb := graphSearchTestKB()
	for name, rows := range map[string][]types.GraphSearchSourceChunk{
		"missing or disabled second source": {graphSearchTestSource("c1", "doc")},
		"wrong document": {
			graphSearchTestSource("c1", "doc"), graphSearchTestSource("c2", "another"),
		},
		"wrong tenant": {
			graphSearchTestSource("c1", "doc"),
			{ID: "c2", KnowledgeID: "doc", TenantID: 8, KnowledgeBaseID: "kb"},
		},
		"wrong KB": {
			graphSearchTestSource("c1", "doc"),
			{ID: "c2", KnowledgeID: "doc", TenantID: 7, KnowledgeBaseID: "other"},
		},
	} {
		t.Run(name, func(t *testing.T) {
			store := &graphSearchStoreStub{candidates: &types.GraphSearchCandidates{Graph: &types.GraphData{
				Node: []*types.GraphNode{
					{
						ID: "n", Name: "Secret", KnowledgeID: "doc", Chunks: []string{"c1", "c2"},
						Attributes: []string{"do not expose"},
					},
				},
				Relation: []*types.GraphRelation{{SourceID: "n", TargetID: "n", Type: "SECRET"}},
			}}}
			result, err := NewGraphSearchService(store, &graphSearchEvidenceStub{rows: rows}).
				Search(graphSearchTestContext(kb), kb, types.GraphSearchRequest{Query: "Secret"})
			require.NoError(t, err)
			require.Empty(t, result.Entities)
			require.Empty(t, result.Relationships)
			require.Empty(t, result.SourceChunks)
		})
	}
}

func TestGraphSearchAdmissionAndFailures(t *testing.T) {
	kb := graphSearchTestKB()
	for name, ctx := range map[string]context.Context{
		"no grant":       context.Background(),
		"wrong resource": graphSearchTestContext(&types.KnowledgeBase{ID: "other", TenantID: 7}),
		"wrong owner":    graphSearchTestContext(&types.KnowledgeBase{ID: "kb", TenantID: 8}),
		"narrowed key": types.WithTenantAPIKeyScope(graphSearchTestContext(kb),
			types.TenantAPIKeyScope{KnowledgeBaseIDs: types.StringArray{"other"}}),
		"chat-only direct caller": types.WithTenantAPIKeyScope(graphSearchTestContext(kb),
			types.TenantAPIKeyScope{Capabilities: types.StringArray{"chat"}}),
		"ingest-only direct caller": types.WithTenantAPIKeyScope(graphSearchTestContext(kb),
			types.TenantAPIKeyScope{Capabilities: types.StringArray{"ingest"}}),
	} {
		t.Run(name, func(t *testing.T) {
			store, evidence := &graphSearchStoreStub{}, &graphSearchEvidenceStub{}
			result, err := NewGraphSearchService(store, evidence).Search(ctx, kb,
				types.GraphSearchRequest{Query: "Acme"})
			require.Nil(t, result)
			var appErr *apperrors.AppError
			require.ErrorAs(t, err, &appErr)
			require.Equal(t, apperrors.ErrForbidden, appErr.Code)
			require.Zero(t, store.calls+evidence.calls)
		})
	}
	for _, legacyConfigOnly := range []bool{false, true} {
		disabled := *kb
		disabled.IndexingStrategy.GraphEnabled = legacyConfigOnly
		if legacyConfigOnly {
			disabled.ExtractConfig = &types.ExtractConfig{Enabled: false}
		}
		store := &graphSearchStoreStub{}
		_, err := NewGraphSearchService(store, &graphSearchEvidenceStub{}).
			Search(graphSearchTestContext(&disabled), &disabled, types.GraphSearchRequest{Query: "Acme"})
		var appErr *apperrors.AppError
		require.ErrorAs(t, err, &appErr)
		require.Equal(t, apperrors.ErrConflict, appErr.Code)
		require.Zero(t, store.calls)
	}
	for _, storeErr := range []error{nil, errors.New("backend password must not be returned")} {
		store := &graphSearchStoreStub{err: storeErr}
		_, err := NewGraphSearchService(store, &graphSearchEvidenceStub{}).
			Search(graphSearchTestContext(kb), kb, types.GraphSearchRequest{Query: "Acme"})
		var appErr *apperrors.AppError
		require.ErrorAs(t, err, &appErr)
		require.Equal(t, apperrors.ErrServiceUnavailable, appErr.Code)
		require.NotContains(t, err.Error(), "password")
	}
	store := &graphSearchStoreStub{candidates: &types.GraphSearchCandidates{Graph: &types.GraphData{
		Node: []*types.GraphNode{{ID: "n", Name: "Acme", KnowledgeID: "doc", Chunks: []string{"c1"}}},
	}}}
	result, err := NewGraphSearchService(store, &graphSearchEvidenceStub{err: errors.New("SQL error")}).
		Search(graphSearchTestContext(kb), kb, types.GraphSearchRequest{Query: "Acme"})
	require.Nil(t, result, "validation failure must not return partial graph data")
	var appErr *apperrors.AppError
	require.ErrorAs(t, err, &appErr)
	require.Equal(t, apperrors.ErrInternalServer, appErr.Code)
}

func TestGraphSearchBudgetsAndClipping(t *testing.T) {
	kb := graphSearchTestKB()
	graph := &types.GraphData{}
	evidence := &graphSearchEvidenceStub{}
	for i := range 513 {
		chunkID, nodeID := fmt.Sprintf("c%d", i), fmt.Sprintf("n%d", i)
		graph.Node = append(graph.Node, &types.GraphNode{
			ID: nodeID, Name: nodeID,
			KnowledgeID: "doc", Chunks: []string{chunkID, chunkID},
			Attributes: []string{strings.Repeat("中", 1001)},
		})
		evidence.rows = append(evidence.rows, graphSearchTestSource(chunkID, "doc"))
	}
	for i := range 3 {
		graph.Relation = append(graph.Relation, &types.GraphRelation{
			SourceID: "n0", TargetID: "n1", Type: fmt.Sprintf("REL%d", i),
		})
	}
	store := &graphSearchStoreStub{candidates: &types.GraphSearchCandidates{
		Graph: graph, TruncationReasons: []string{"candidate_seeds", "candidate_relations"},
	}}
	result, err := NewGraphSearchService(store, evidence).Search(graphSearchTestContext(kb), kb,
		types.GraphSearchRequest{Query: "n", MaxNodes: 2, MaxSourceChunks: 2, MaxRelations: 1})
	require.NoError(t, err)
	require.Len(t, evidence.ids, types.GraphSearchMaxEvidence)
	require.Len(t, result.Entities, 2)
	require.Len(t, result.SourceChunks, 2)
	require.Len(t, result.Relationships, 1)
	require.True(t, result.Meta.Truncated)
	for _, reason := range []string{
		"candidate_seeds", "candidate_relations", "candidate_source_chunks",
		"max_nodes", "max_relations", "entity_fields",
	} {
		require.Contains(t, result.Meta.TruncationReasons, reason)
	}
	require.Equal(t, 1000, utf8.RuneCountInString(result.Entities[0].Attributes[0]))
	// The chunk limit must never create dangling node/relationship references.
	result, err = NewGraphSearchService(store, evidence).Search(graphSearchTestContext(kb), kb,
		types.GraphSearchRequest{Query: "n", MaxSourceChunks: 1})
	require.NoError(t, err)
	require.Len(t, result.Entities, 1)
	require.Len(t, result.SourceChunks, 1)
	require.Empty(t, result.Relationships)
	require.Contains(t, result.Meta.TruncationReasons, "max_source_chunks")
}

func TestGraphSearchEmptyAndIsolatedEntity(t *testing.T) {
	kb := graphSearchTestKB()
	store := &graphSearchStoreStub{candidates: &types.GraphSearchCandidates{Graph: &types.GraphData{}}}
	evidence := &graphSearchEvidenceStub{}
	svc := NewGraphSearchService(store, evidence)
	result, err := svc.Search(graphSearchTestContext(kb), kb, types.GraphSearchRequest{Query: "none"})
	require.NoError(t, err)
	require.Zero(t, evidence.calls)
	data, err := json.Marshal(result)
	require.NoError(t, err)
	for _, field := range []string{`"entities":[]`, `"relationships":[]`, `"source_chunks":[]`} {
		require.Contains(t, string(data), field)
	}
	store.candidates.Graph.Node = []*types.GraphNode{
		{ID: "isolated", Name: "Isolated", KnowledgeID: "doc", Chunks: []string{"c1"}},
	}
	source := graphSearchTestSource("c1", "doc")
	source.ContentTruncated = true
	evidence.rows = []types.GraphSearchSourceChunk{source}
	result, err = svc.Search(graphSearchTestContext(kb), kb, types.GraphSearchRequest{Query: "Isolated"})
	require.NoError(t, err)
	require.Len(t, result.Entities, 1)
	require.Empty(t, result.Relationships)
	require.Contains(t, result.Meta.TruncationReasons, "source_fields")
}
