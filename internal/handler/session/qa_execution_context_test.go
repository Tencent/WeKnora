package session

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCreateKnowledgeQARequestDecodesFolderScopesAndSuggestionAttribution(t *testing.T) {
	var request CreateKnowledgeQARequest
	require.NoError(t, json.Unmarshal([]byte(`{
		"query":"What changed?",
		"folder_scopes":[{"knowledge_base_id":"kb-1","folder_ids":["folder-1"]}],
		"suggestion_attribution":{"suggestion_set_id":"set-1","question_id":"question-1"}
	}`), &request))
	assert.Equal(t, []types.FolderScope{{KnowledgeBaseID: "kb-1", FolderIDs: []string{"folder-1"}}}, request.FolderScopes)
	require.NotNil(t, request.SuggestionAttribution)
	assert.Equal(t, "set-1", request.SuggestionAttribution.SuggestionSetID)
	assert.Equal(t, "question-1", request.SuggestionAttribution.QuestionID)
}

func TestBuildMessageExecutionContextStoresAndHashesFolderScopes(t *testing.T) {
	agent := &types.CustomAgent{ID: "agent-1", TenantID: 7, Config: types.CustomAgentConfig{ModelID: "model-1"}}
	scopes := []types.FolderScope{{KnowledgeBaseID: "kb-1", FolderIDs: []string{"folder-1"}}}
	snapshot, agentID, tenantID, modelID := buildMessageExecutionContext(
		context.Background(), agent, 0, "", []string{"kb-1"}, nil, []string{"tag-1"}, scopes, nil, nil, false,
	)
	assert.Equal(t, scopes, snapshot.FolderScopes)
	assert.Equal(t, "agent-1", agentID)
	assert.Equal(t, uint64(7), tenantID)
	assert.Equal(t, "model-1", modelID)
	assert.NotEmpty(t, snapshot.AgentConfigHash)

	otherSnapshot, _, _, _ := buildMessageExecutionContext(
		context.Background(), agent, 0, "", []string{"kb-1"}, nil, []string{"tag-1"},
		[]types.FolderScope{{KnowledgeBaseID: "kb-1", FolderIDs: []string{"folder-2"}}}, nil, nil, false,
	)
	assert.NotEqual(t, snapshot.AgentConfigHash, otherSnapshot.AgentConfigHash)
}
