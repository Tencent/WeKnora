package tools

import (
	"context"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type schemaFallbackGraphRepo struct {
	stubGraphRepo
	typedGraph *types.GraphData
	typedErr   error
	nameErr    error
	typedCalls int
	nameCalls  int
}

func (s *schemaFallbackGraphRepo) SearchNodeWithSchema(
	_ context.Context, _ types.NameSpace, _ []types.GraphQuery,
) (*types.GraphData, error) {
	s.typedCalls++
	return s.typedGraph, s.typedErr
}

func (s *schemaFallbackGraphRepo) SearchNode(
	ctx context.Context, namespace types.NameSpace, terms []string,
) (*types.GraphData, error) {
	s.nameCalls++
	if s.nameErr != nil {
		return nil, s.nameErr
	}
	return s.stubGraphRepo.SearchNode(ctx, namespace, terms)
}

func schemaFallbackGraph(chunk, suffix string) *types.GraphData {
	return &types.GraphData{
		Node: []*types.GraphNode{
			{Name: "Alex", ID: "alex-" + suffix, Chunks: []string{chunk}},
			{Name: "Acme", ID: "acme-" + suffix, Chunks: []string{chunk}},
		},
		Relation: []*types.GraphRelation{{
			Node1: "Alex", Node2: "Acme", Type: "WORKS_AT",
			SourceID: "alex-" + suffix, TargetID: "acme-" + suffix,
		}},
	}
}

func TestQueryKnowledgeGraphSchemaFallbackValidatesEvidenceAndScope(t *testing.T) {
	for _, scope := range []struct {
		name   string
		target types.SearchTarget
	}{
		{"document", types.SearchTarget{
			Type: types.SearchTargetTypeKnowledge, KnowledgeBaseID: "kb-1", KnowledgeIDs: []string{"doc-a"},
		}},
		{"tag", types.SearchTarget{
			Type: types.SearchTargetTypeKnowledgeBase, KnowledgeBaseID: "kb-1", TagIDs: []string{"allowed"},
		}},
	} {
		for _, tc := range []struct {
			name         string
			typedGraph   *types.GraphData
			nameGraph    *types.GraphData
			typedErr     error
			nameErr      error
			evidenceFail bool
			wantFallback bool
			wantEvidence bool
			wantErr      bool
		}{
			{
				name:       "out-of-scope typed hit falls back to in-scope legacy evidence",
				typedGraph: schemaFallbackGraph("c-b", "b"), nameGraph: schemaFallbackGraph("c-a", "a"),
				wantFallback: true, wantEvidence: true,
			},
			{
				name:      "empty typed result falls back",
				nameGraph: schemaFallbackGraph("c-a", "a"), wantFallback: true, wantEvidence: true,
			},
			{
				name:       "typed nodes without evidence fall back",
				typedGraph: &types.GraphData{Node: []*types.GraphNode{{Name: "Alex", ID: "no-evidence"}}},
				nameGraph:  schemaFallbackGraph("c-a", "a"), wantFallback: true, wantEvidence: true,
			},
			{
				name:       "valid typed evidence does not fall back",
				typedGraph: schemaFallbackGraph("c-a", "a"), nameGraph: schemaFallbackGraph("c-b", "b"),
				wantEvidence: true,
			},
			{
				name:       "fallback cannot expose out-of-scope legacy evidence",
				typedGraph: schemaFallbackGraph("c-b", "b"), nameGraph: schemaFallbackGraph("c-b", "b"),
				wantFallback: true,
			},
			{
				name: "typed query error does not fall back", typedErr: assert.AnError, wantErr: true,
			},
			{
				name:       "evidence lookup error does not fall back",
				typedGraph: schemaFallbackGraph("c-b", "b"), evidenceFail: true, wantErr: true,
			},
			{
				name:       "name lookup error is returned",
				typedGraph: schemaFallbackGraph("c-b", "b"), nameErr: assert.AnError,
				wantFallback: true, wantErr: true,
			},
		} {
			t.Run(scope.name+"/"+tc.name, func(t *testing.T) {
				repo := &schemaFallbackGraphRepo{
					stubGraphRepo: stubGraphRepo{graph: tc.nameGraph},
					typedGraph:    tc.typedGraph, typedErr: tc.typedErr, nameErr: tc.nameErr,
				}
				chunks := &graphEvidenceChunkRepo{stubGraphChunkRepo: stubGraphChunkRepo{
					chunks: map[string]*types.Chunk{
						"c-a": {
							ID: "c-a", KnowledgeBaseID: "kb-1", KnowledgeID: "doc-a",
							IsEnabled: true, Content: "Alex works at Acme.",
						},
						"c-b": {
							ID: "c-b", KnowledgeBaseID: "kb-1", KnowledgeID: "doc-b",
							IsEnabled: true, Content: "Out of scope.",
						},
					},
				}}
				if tc.evidenceFail {
					chunks.failAt = 1
				}
				documents := &graphEvidenceKnowledgeService{
					documents: map[string]*types.Knowledge{
						"doc-a": {ID: "doc-a", KnowledgeBaseID: "kb-1", Title: "Allowed"},
						"doc-b": {ID: "doc-b", KnowledgeBaseID: "kb-1", Title: "Excluded"},
					},
					tags: map[string][]*types.KnowledgeTag{"doc-a": {testKnowledgeTag("allowed")}},
				}
				tool := NewQueryKnowledgeGraphTool(&stubKnowledgeBaseService{}, types.SearchTargets{&scope.target}).
					WithGraph(repo, chunks).WithKnowledgeScope(documents)
				lookup, err := tool.queryGraph(context.Background(), "kb-1", []string{"Alex"},
					[]types.GraphQuery{{Name: "Alex", EntityType: "Person"}})
				require.Equal(t, 1, repo.typedCalls)
				require.Equal(t, tc.wantFallback, repo.nameCalls == 1)
				if tc.wantErr {
					require.Error(t, err)
					require.Empty(t, lookup.chunks)
					return
				}
				require.NoError(t, err)
				if tc.wantEvidence {
					require.Len(t, lookup.chunks, 1)
					require.Equal(t, "c-a", lookup.chunks[0].ID)
					require.Len(t, lookup.graph.Relation, 1)
					require.Equal(t, "alex-a", lookup.graph.Relation[0].SourceID)
				} else {
					require.Empty(t, lookup.chunks)
					if lookup.graph != nil {
						require.Empty(t, lookup.graph.Relation)
					}
				}
			})
		}
	}
}
