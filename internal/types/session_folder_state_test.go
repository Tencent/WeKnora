package types

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSessionLastRequestStateScansFolderScopesAndFolderMentions(t *testing.T) {
	payload := `{
		"agent_id":"agent-1",
		"folder_scopes":[{"knowledge_base_id":"kb-1","folder_ids":["folder-a","folder-b"]}],
		"mentioned_items":[{"id":"folder-a","name":"Plans","type":"folder","kb_id":"kb-1","kb_name":"Documents"}]
	}`
	var state SessionLastRequestState
	require.NoError(t, state.Scan([]byte(payload)))
	require.Equal(t, []FolderScope{{KnowledgeBaseID: "kb-1", FolderIDs: []string{"folder-a", "folder-b"}}}, state.FolderScopes)
	require.Len(t, state.MentionedItems, 1)
	assert.Equal(t, "folder", state.MentionedItems[0].Type)
	assert.Equal(t, "folder-a", state.MentionedItems[0].ID)
	assert.Equal(t, "kb-1", state.MentionedItems[0].KBID)

	encoded, err := state.Value()
	require.NoError(t, err)
	bytes, ok := encoded.([]byte)
	require.True(t, ok)
	var roundTrip map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(bytes, &roundTrip))
	assert.Contains(t, roundTrip, "folder_scopes")
	assert.Contains(t, roundTrip, "mentioned_items")
}

func TestSessionLastRequestStateKeepsLegacyPayloadCompatible(t *testing.T) {
	var state SessionLastRequestState
	require.NoError(t, state.Scan(`{"agent_id":"legacy-agent","knowledge_base_ids":["kb-1"],"web_search_enabled":true}`))
	assert.Equal(t, "legacy-agent", state.AgentID)
	assert.Equal(t, []string{"kb-1"}, state.KnowledgeBaseIDs)
	assert.True(t, state.WebSearchEnabled)
	assert.Empty(t, state.FolderScopes)

	// The column previously held unrelated agent-config shapes. Scanning those
	// values remains best-effort and must never fail an old session read.
	require.NoError(t, state.Scan(`{"unknown_legacy_field":{"enabled":true}}`))
	require.NoError(t, state.Scan(`not-json`))
}
