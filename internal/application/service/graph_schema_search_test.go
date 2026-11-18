package service

import (
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

func TestGraphSearchSchemaTypeIdentities(t *testing.T) {
	kb := graphSearchTestKB()
	graph := &types.GraphData{
		Node: []*types.GraphNode{
			{ID: "person", Name: "Alex", EntityType: "Person", KnowledgeID: "doc", Chunks: []string{"c"}},
			{ID: "company", Name: "Alex", EntityType: "Company", KnowledgeID: "doc", Chunks: []string{"c"}},
			{ID: "legacy", Name: "Alex", KnowledgeID: "doc", Chunks: []string{"c"}},
		},
		Relation: []*types.GraphRelation{
			{ID: "works", SourceID: "person", TargetID: "company", Type: "WORKS_AT"},
		},
	}
	store := &graphSearchStoreStub{candidates: &types.GraphSearchCandidates{Graph: graph}}
	evidence := &graphSearchEvidenceStub{rows: []types.GraphSearchSourceChunk{graphSearchTestSource("c", "doc")}}
	result, err := NewGraphSearchService(store, evidence).Search(
		graphSearchTestContext(kb), kb, types.GraphSearchRequest{Query: "Alex"})
	require.NoError(t, err)
	require.Len(t, result.Entities, 3)
	byType := map[string]types.GraphSearchEntity{}
	for _, entity := range result.Entities {
		byType[entity.EntityType] = entity
	}
	require.Len(t, byType, 3)
	require.NotEqual(t, byType["Person"].ID, byType["Company"].ID)
	require.NotEqual(t, byType["Person"].ID, byType[""].ID)
	require.Equal(t, graphSearchPublicID("entity", kb.ID, "doc", "Alex"), byType[""].ID,
		"legacy public IDs must remain stable")
	require.Len(t, result.Relationships, 1)
	require.Equal(t, byType["Person"].ID, result.Relationships[0].SourceID)
	require.Equal(t, byType["Company"].ID, result.Relationships[0].TargetID)
}
