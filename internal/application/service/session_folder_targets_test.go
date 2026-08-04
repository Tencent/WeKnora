package service

import (
	"context"
	"fmt"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type folderTargetService struct {
	interfaces.KnowledgeFolderService
	idsByFolder map[string][]string
	tenantID    uint64
	kbID        string
}

func (s *folderTargetService) ResolveKnowledgeIDs(
	_ context.Context,
	tenantID uint64,
	kbID string,
	folderIDs []string,
) ([]string, error) {
	s.tenantID = tenantID
	s.kbID = kbID
	result := make([]string, 0)
	for _, folderID := range folderIDs {
		ids, ok := s.idsByFolder[folderID]
		if !ok {
			return nil, fmt.Errorf("unknown folder %s", folderID)
		}
		result = append(result, ids...)
	}
	return result, nil
}

func TestApplyKnowledgeFolderScopes_RestrictsFullKnowledgeBase(t *testing.T) {
	folders := &folderTargetService{
		idsByFolder: map[string][]string{"folder-a": {"doc-2", "doc-1", "doc-2"}},
	}
	svc := &sessionService{knowledgeFolderService: folders}
	target := &types.SearchTarget{
		Type:            types.SearchTargetTypeKnowledgeBase,
		KnowledgeBaseID: "kb-1",
		TenantID:        42,
	}

	result, err := svc.applyKnowledgeFolderScopes(
		context.Background(),
		types.SearchTargets{target},
		[]types.KnowledgeFolderScope{{
			KnowledgeBaseID: "kb-1",
			FolderIDs:       []string{"folder-a"},
		}},
	)

	require.NoError(t, err)
	require.Len(t, result, 1)
	assert.Equal(t, uint64(42), folders.tenantID)
	assert.Equal(t, "kb-1", folders.kbID)
	assert.Equal(t, types.SearchTargetTypeKnowledge, result[0].Type)
	assert.Equal(t, []string{"doc-2", "doc-1"}, result[0].KnowledgeIDs)
	assert.True(t, result[0].DisableRecallThresholds)
}

func TestApplyKnowledgeFolderScopes_IntersectsExplicitDocuments(t *testing.T) {
	svc := &sessionService{knowledgeFolderService: &folderTargetService{
		idsByFolder: map[string][]string{"folder-a": {"doc-1", "doc-2"}},
	}}
	target := &types.SearchTarget{
		Type:            types.SearchTargetTypeKnowledge,
		KnowledgeBaseID: "kb-1",
		TenantID:        7,
		KnowledgeIDs:    []string{"doc-2", "doc-3"},
	}

	result, err := svc.applyKnowledgeFolderScopes(
		context.Background(),
		types.SearchTargets{target},
		[]types.KnowledgeFolderScope{{
			KnowledgeBaseID: "kb-1",
			FolderIDs:       []string{"folder-a"},
		}},
	)

	require.NoError(t, err)
	assert.Equal(t, []string{"doc-2"}, result[0].KnowledgeIDs)
}

func TestApplyKnowledgeFolderScopes_EmptyFolderStaysEmpty(t *testing.T) {
	svc := &sessionService{knowledgeFolderService: &folderTargetService{
		idsByFolder: map[string][]string{"empty-folder": {}},
	}}
	target := &types.SearchTarget{
		Type:            types.SearchTargetTypeKnowledgeBase,
		KnowledgeBaseID: "kb-1",
		TenantID:        7,
	}

	result, err := svc.applyKnowledgeFolderScopes(
		context.Background(),
		types.SearchTargets{target},
		[]types.KnowledgeFolderScope{{
			KnowledgeBaseID: "kb-1",
			FolderIDs:       []string{"empty-folder"},
		}},
	)

	require.NoError(t, err)
	assert.Equal(t, types.SearchTargetTypeKnowledge, result[0].Type)
	assert.Equal(t, []string{emptyKnowledgeScopeID}, result[0].KnowledgeIDs)
}

func TestApplyKnowledgeFolderScopes_RejectsUnauthorizedKnowledgeBase(t *testing.T) {
	svc := &sessionService{knowledgeFolderService: &folderTargetService{}}

	_, err := svc.applyKnowledgeFolderScopes(
		context.Background(),
		types.SearchTargets{{
			Type:            types.SearchTargetTypeKnowledgeBase,
			KnowledgeBaseID: "kb-1",
			TenantID:        7,
		}},
		[]types.KnowledgeFolderScope{{
			KnowledgeBaseID: "kb-2",
			FolderIDs:       []string{"folder-a"},
		}},
	)

	require.ErrorContains(t, err, "not authorized")
}
