package dto

import (
	"encoding/json"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

func TestNewKnowledgeFolderEnsurePathsResponsePreservesOrderAndExactKeys(t *testing.T) {
	const sharedFolderID = "30000000-0000-4000-8000-000000000003"
	results := []types.KnowledgeFolderEnsurePathResult{
		{ClientKey: "src/internal", FolderID: sharedFolderID},
		{ClientKey: "src/Internal", FolderID: "20000000-0000-4000-8000-000000000002"},
		{ClientKey: "same/path", FolderID: sharedFolderID},
	}
	before := append([]types.KnowledgeFolderEnsurePathResult(nil), results...)

	response := NewKnowledgeFolderEnsurePathsResponse(results)

	require.NotNil(t, response)
	requireKnowledgeFolderExactKeys(t, response, knowledgeFolderKeySet("items"))
	require.Len(t, response.Items, 3)
	require.Equal(t, "src/internal", response.Items[0].ClientKey)
	require.Equal(t, sharedFolderID, response.Items[0].FolderID)
	require.Equal(t, "src/Internal", response.Items[1].ClientKey)
	require.Equal(t, "same/path", response.Items[2].ClientKey)
	require.Equal(t, sharedFolderID, response.Items[2].FolderID)
	for _, item := range response.Items {
		requireKnowledgeFolderExactKeys(
			t,
			item,
			knowledgeFolderKeySet("client_key", "folder_id"),
		)
	}
	require.Equal(t, before, results)

	body, err := json.Marshal(response)
	require.NoError(t, err)
	var raw struct {
		Items []map[string]json.RawMessage `json:"items"`
	}
	require.NoError(t, json.Unmarshal(body, &raw))
	for _, item := range raw.Items {
		for _, internalKey := range []string{
			"tenant_id",
			"knowledge_base_id",
			"parent_id",
			"name",
			"path",
			"depth",
			"sort_order",
			"created_at",
			"updated_at",
			"deleted_at",
			"folder_version",
			"folder_indexed_version",
		} {
			require.NotContains(t, item, internalKey)
		}
	}
}

func TestNewKnowledgeFolderEnsurePathsResponseUsesEmptyItemsArray(t *testing.T) {
	inputs := [][]types.KnowledgeFolderEnsurePathResult{nil, {}}
	for _, input := range inputs {
		response := NewKnowledgeFolderEnsurePathsResponse(input)
		require.NotNil(t, response)
		require.NotNil(t, response.Items)
		require.Empty(t, response.Items)
		requireKnowledgeFolderExactKeys(t, response, knowledgeFolderKeySet("items"))

		body, err := json.Marshal(response)
		require.NoError(t, err)
		require.JSONEq(t, `{"items":[]}`, string(body))
	}
}
