package session

import (
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNormalizeKnowledgeFolderScopesMergesAndSorts(t *testing.T) {
	folderA := "00000000-0000-4000-8000-000000000001"
	folderB := "00000000-0000-4000-8000-000000000002"

	scopes, err := normalizeKnowledgeFolderScopes(
		[]types.KnowledgeFolderScope{
			{KnowledgeBaseID: "kb-1", FolderIDs: []string{folderB, folderA}},
			{KnowledgeBaseID: "kb-1", FolderIDs: []string{folderA}},
		},
		[]string{"kb-1"},
	)

	require.NoError(t, err)
	require.Len(t, scopes, 1)
	assert.Equal(t, "kb-1", scopes[0].KnowledgeBaseID)
	assert.Equal(t, []string{folderA, folderB}, scopes[0].FolderIDs)
}

func TestNormalizeKnowledgeFolderScopesRejectsUnselectedKnowledgeBase(t *testing.T) {
	_, err := normalizeKnowledgeFolderScopes(
		[]types.KnowledgeFolderScope{{
			KnowledgeBaseID: "kb-2",
			FolderIDs:       []string{uuid.NewString()},
		}},
		[]string{"kb-1"},
	)

	require.ErrorContains(t, err, "not selected")
}

func TestNormalizeKnowledgeFolderScopesRejectsEmptyFolderList(t *testing.T) {
	_, err := normalizeKnowledgeFolderScopes(
		[]types.KnowledgeFolderScope{{
			KnowledgeBaseID: "kb-1",
			FolderIDs:       nil,
		}},
		[]string{"kb-1"},
	)

	require.ErrorContains(t, err, "at least one folder")
}
