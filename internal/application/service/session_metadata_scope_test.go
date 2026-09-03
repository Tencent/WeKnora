package service

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/Tencent/WeKnora/internal/types"
)

type metadataScopeResolverStub struct {
	queries   []types.MetadataScopeQuery
	tenantIDs []uint64
	scopes    map[string]types.DocumentScope
}

func (s *metadataScopeResolverStub) ResolveDocumentScope(
	ctx context.Context,
	query types.MetadataScopeQuery,
) (types.DocumentScope, error) {
	tenantID, _ := types.TenantIDFromContext(ctx)
	s.tenantIDs = append(s.tenantIDs, tenantID)
	s.queries = append(s.queries, query)
	return s.scopes[query.KnowledgeBaseID], nil
}

func TestSearchKnowledge_MetadataScopeResolvesPerKBTenantAndIntersectsExplicitIDs(t *testing.T) {
	resolver := &metadataScopeResolverStub{
		scopes: map[string]types.DocumentScope{
			"kb-full": {
				Mode: types.DocumentScopeModeIDs,
				IDs:  []string{"doc-a", "doc-b"},
			},
			"kb-explicit": {
				Mode: types.DocumentScopeModeIDs,
				IDs:  []string{"doc-y"},
			},
			"kb-empty": {Mode: types.DocumentScopeModeNone},
		},
	}
	service := &sessionService{metadataService: resolver}
	targets := types.SearchTargets{
		{
			Type:            types.SearchTargetTypeKnowledgeBase,
			KnowledgeBaseID: "kb-full",
			TenantID:        11,
		},
		{
			Type:            types.SearchTargetTypeKnowledge,
			KnowledgeBaseID: "kb-explicit",
			TenantID:        22,
			KnowledgeIDs:    []string{"doc-x", "doc-y"},
		},
		{
			Type:            types.SearchTargetTypeKnowledgeBase,
			KnowledgeBaseID: "kb-empty",
			TenantID:        33,
		},
	}
	filters := []types.KBMetadataFilter{
		metadataFilterFixture("kb-full"),
		metadataFilterFixture("kb-explicit"),
		metadataFilterFixture("kb-empty"),
	}

	resolved, err := service.resolveMetadataSearchTargets(t.Context(), targets, filters)
	require.NoError(t, err)
	require.Len(t, resolved, 2)
	require.Equal(t, []uint64{11, 22, 33}, resolver.tenantIDs)

	require.Equal(t, types.SearchTargetTypeKnowledge, resolved[0].Type)
	require.Equal(t, []string{"doc-a", "doc-b"}, resolved[0].KnowledgeIDs)
	require.True(t, resolved[0].MetadataFiltered)

	require.Equal(t, []string{"doc-x", "doc-y"}, resolver.queries[1].ExplicitKnowledgeIDs)
	require.Equal(t, []string{"doc-y"}, resolved[1].KnowledgeIDs)
	require.True(t, resolved[1].MetadataFiltered)
}

func TestSearchKnowledge_MetadataScopeNoneRemovesTarget(t *testing.T) {
	resolver := &metadataScopeResolverStub{
		scopes: map[string]types.DocumentScope{
			"kb-empty": {Mode: types.DocumentScopeModeNone},
		},
	}
	service := &sessionService{metadataService: resolver}

	resolved, err := service.resolveMetadataSearchTargets(
		t.Context(),
		types.SearchTargets{{
			Type:            types.SearchTargetTypeKnowledgeBase,
			KnowledgeBaseID: "kb-empty",
			TenantID:        11,
		}},
		[]types.KBMetadataFilter{metadataFilterFixture("kb-empty")},
	)
	require.NoError(t, err)
	require.Empty(t, resolved)
}

func metadataFilterFixture(knowledgeBaseID string) types.KBMetadataFilter {
	return types.KBMetadataFilter{
		KnowledgeBaseID: knowledgeBaseID,
		Conditions: []types.MetadataCondition{{
			MetadataDefinitionID: "definition-1",
			Operator:             types.MetadataOperatorEquals,
			Values:               []any{"guide"},
		}},
	}
}
