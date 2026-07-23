package session

import (
	"encoding/json"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	multiScopeKB      = "11111111-1111-1111-1111-111111111111"
	multiScopeFolderA = "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"
	multiScopeFolderB = "bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb"
)

func decodeFolderScopes(t *testing.T, payload string) []types.FolderScope {
	t.Helper()
	var scopes []types.FolderScope
	require.NoError(t, json.Unmarshal([]byte(payload), &scopes))
	return scopes
}

func TestNormalizeFolderScopesAcceptsNewAndLegacyShapes(t *testing.T) {
	newScopes := decodeFolderScopes(t, `[{"knowledge_base_id":"`+multiScopeKB+`","folder_ids":["`+multiScopeFolderB+`","`+multiScopeFolderA+`","`+multiScopeFolderA+`"]}]`)
	normalized, err := normalizeFolderScopes(newScopes)
	require.NoError(t, err)
	require.Len(t, normalized, 1)
	assert.Equal(t, []string{multiScopeFolderA, multiScopeFolderB}, normalized[0].FolderIDs)
	assert.Nil(t, normalized[0].FolderID)

	legacyScopes := decodeFolderScopes(t, `[{"knowledge_base_id":"`+multiScopeKB+`","folder_id":"`+multiScopeFolderA+`"}]`)
	normalized, err = normalizeFolderScopes(legacyScopes)
	require.NoError(t, err)
	require.Len(t, normalized, 1)
	assert.Equal(t, []string{multiScopeFolderA}, normalized[0].FolderIDs)

	legacyWholeKB := decodeFolderScopes(t, `[{"knowledge_base_id":"`+multiScopeKB+`","folder_id":null}]`)
	normalized, err = normalizeFolderScopes(legacyWholeKB)
	require.NoError(t, err)
	assert.Empty(t, normalized)
}

func TestNormalizeFolderScopesRejectsAmbiguousOrWideningShapes(t *testing.T) {
	tests := []struct {
		name    string
		payload string
	}{
		{
			name:    "new and legacy fields together",
			payload: `[{"knowledge_base_id":"` + multiScopeKB + `","folder_ids":["` + multiScopeFolderA + `"],"folder_id":"` + multiScopeFolderB + `"}]`,
		},
		{name: "empty folder ids", payload: `[{"knowledge_base_id":"` + multiScopeKB + `","folder_ids":[]}]`},
		{name: "null folder ids", payload: `[{"knowledge_base_id":"` + multiScopeKB + `","folder_ids":null}]`},
		{name: "both fields missing", payload: `[{"knowledge_base_id":"` + multiScopeKB + `"}]`},
		{name: "empty legacy id", payload: `[{"knowledge_base_id":"` + multiScopeKB + `","folder_id":""}]`},
		{name: "empty new id", payload: `[{"knowledge_base_id":"` + multiScopeKB + `","folder_ids":[""]}]`},
		{name: "invalid uuid", payload: `[{"knowledge_base_id":"` + multiScopeKB + `","folder_ids":["not-a-uuid"]}]`},
		{
			name: "duplicate kb scope",
			payload: `[{"knowledge_base_id":"` + multiScopeKB + `","folder_ids":["` + multiScopeFolderA + `"]},` +
				`{"knowledge_base_id":"` + multiScopeKB + `","folder_ids":["` + multiScopeFolderB + `"]}]`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := normalizeFolderScopes(decodeFolderScopes(t, tt.payload))
			assert.Error(t, err)
		})
	}
}
