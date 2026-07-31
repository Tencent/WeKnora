package dto

import (
	"encoding/json"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

func TestNewKnowledgeFolderMoveResponseUsesCountsOnly(t *testing.T) {
	result := &types.KnowledgeFolderMoveResult{
		ChangedCount:   3,
		UnchangedCount: 2,
	}

	response := NewKnowledgeFolderMoveResponse(result)

	require.NotNil(t, response)
	requireKnowledgeFolderExactKeys(
		t,
		response,
		knowledgeFolderKeySet("changed_count", "unchanged_count"),
	)
	require.Equal(t, 3, response.ChangedCount)
	require.Equal(t, 2, response.UnchangedCount)

	body, err := json.Marshal(response)
	require.NoError(t, err)
	require.JSONEq(t, `{"changed_count":3,"unchanged_count":2}`, string(body))
	for _, forbidden := range []string{
		"knowledge_ids",
		"target_folder_id",
		"tenant_id",
		"knowledge_base_id",
		"folder_id",
		"pending",
	} {
		require.NotContains(t, string(body), forbidden)
	}
}

func TestNewKnowledgeFolderMoveResponseReturnsNilForNilResult(t *testing.T) {
	require.Nil(t, NewKnowledgeFolderMoveResponse(nil))
}

func TestKnowledgeFolderMoveRequestDistinguishesExplicitRootFromMissingTarget(t *testing.T) {
	var explicitRoot KnowledgeFolderMoveRequest
	require.NoError(
		t,
		json.Unmarshal(
			[]byte(`{"knowledge_ids":["knowledge-1"],"target_folder_id":""}`),
			&explicitRoot,
		),
	)
	require.Equal(t, []string{"knowledge-1"}, explicitRoot.KnowledgeIDs)
	require.NotNil(t, explicitRoot.TargetFolderID)
	require.Equal(t, types.KnowledgeFolderRootID, *explicitRoot.TargetFolderID)

	var missingTarget KnowledgeFolderMoveRequest
	require.NoError(
		t,
		json.Unmarshal([]byte(`{"knowledge_ids":["knowledge-1"]}`), &missingTarget),
	)
	require.Nil(t, missingTarget.TargetFolderID)
}
