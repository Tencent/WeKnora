package types

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestKnowledgeScopeRequestFolderScopesMissing(t *testing.T) {
	var request KnowledgeScopeRequest
	require.NoError(t, json.Unmarshal([]byte(`{}`), &request))
	require.Nil(t, request.FolderScopes)

	encoded, err := json.Marshal(request)
	require.NoError(t, err)
	require.NotContains(t, string(encoded), "folder_scopes")
}

func TestKnowledgeScopeRequestFolderScopesNull(t *testing.T) {
	var request KnowledgeScopeRequest
	require.NoError(t, json.Unmarshal([]byte(`{"folder_scopes":null}`), &request))
	require.Nil(t, request.FolderScopes)

	encoded, err := json.Marshal(request)
	require.NoError(t, err)
	require.NotContains(t, string(encoded), "folder_scopes")
}

func TestKnowledgeScopeRequestFolderScopesExplicitEmptyRoundTrip(t *testing.T) {
	var request KnowledgeScopeRequest
	require.NoError(t, json.Unmarshal([]byte(`{"folder_scopes":[]}`), &request))
	require.NotNil(t, request.FolderScopes)
	require.Empty(t, *request.FolderScopes)

	encoded, err := json.Marshal(request)
	require.NoError(t, err)
	require.True(t, strings.Contains(string(encoded), `"folder_scopes":[]`), string(encoded))
}

func TestKnowledgeScopeRequestPointerToNilFolderSliceMarshalsAsExplicitEmpty(t *testing.T) {
	var folderScopes []FolderScopeRequest
	request := KnowledgeScopeRequest{FolderScopes: &folderScopes}

	encoded, err := json.Marshal(request)
	require.NoError(t, err)

	var raw map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(encoded, &raw))
	folderScopesJSON, exists := raw["folder_scopes"]
	require.True(t, exists)

	var encodedScopes []json.RawMessage
	require.NoError(t, json.Unmarshal(folderScopesJSON, &encodedScopes))
	require.Empty(t, encodedScopes)
}

func TestKnowledgeScopeRequestPointerToNilFolderSliceRoundTripsAsExplicitEmpty(t *testing.T) {
	var folderScopes []FolderScopeRequest
	request := KnowledgeScopeRequest{FolderScopes: &folderScopes}

	encoded, err := json.Marshal(request)
	require.NoError(t, err)

	var decoded KnowledgeScopeRequest
	require.NoError(t, json.Unmarshal(encoded, &decoded))
	require.NotNil(t, decoded.FolderScopes)
	require.Empty(t, *decoded.FolderScopes)
	require.NotNil(t, *decoded.FolderScopes)
}

func TestKnowledgeScopeRequestMarshalPreservesMissingFolderScopes(t *testing.T) {
	request := KnowledgeScopeRequest{
		KnowledgeBaseIDs: []string{"visible-kb"},
	}

	encoded, err := json.Marshal(request)
	require.NoError(t, err)

	var raw map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(encoded, &raw))
	_, exists := raw["folder_scopes"]
	require.False(t, exists)
	require.Contains(t, raw, "knowledge_base_ids")
}

func TestKnowledgeScopeRequestMarshalPreservesExplicitEmptyFolderScopes(t *testing.T) {
	folderScopes := []FolderScopeRequest{}
	request := KnowledgeScopeRequest{FolderScopes: &folderScopes}

	encoded, err := json.Marshal(request)
	require.NoError(t, err)

	var raw map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(encoded, &raw))
	folderScopesJSON, exists := raw["folder_scopes"]
	require.True(t, exists)

	var encodedScopes []json.RawMessage
	require.NoError(t, json.Unmarshal(folderScopesJSON, &encodedScopes))
	require.Empty(t, encodedScopes)
}

func TestKnowledgeScopeRequestMarshalPreservesNonEmptyFolderScopes(t *testing.T) {
	includeDescendants := true
	folderScopes := []FolderScopeRequest{{
		KnowledgeBaseID:    "secret-kb",
		FolderIDs:          []string{"secret-folder"},
		IncludeDescendants: &includeDescendants,
	}}
	request := KnowledgeScopeRequest{FolderScopes: &folderScopes}

	encoded, err := json.Marshal(request)
	require.NoError(t, err)

	var decoded KnowledgeScopeRequest
	require.NoError(t, json.Unmarshal(encoded, &decoded))
	require.NotNil(t, decoded.FolderScopes)
	require.Len(t, *decoded.FolderScopes, 1)
	require.Equal(t, "secret-kb", (*decoded.FolderScopes)[0].KnowledgeBaseID)
	require.Equal(t, []string{"secret-folder"}, (*decoded.FolderScopes)[0].FolderIDs)
	require.NotNil(t, (*decoded.FolderScopes)[0].IncludeDescendants)
	require.True(t, *(*decoded.FolderScopes)[0].IncludeDescendants)
}

func TestKnowledgeScopeRequestMarshalDoesNotMutateOriginal(t *testing.T) {
	var folderScopes []FolderScopeRequest
	request := KnowledgeScopeRequest{FolderScopes: &folderScopes}
	originalFolderScopes := request.FolderScopes

	_, err := json.Marshal(request)
	require.NoError(t, err)

	require.Same(t, originalFolderScopes, request.FolderScopes)
	require.Nil(t, *request.FolderScopes)
}

func TestKnowledgeScopeRequestNullFolderScopesStillDecodesAsMissing(t *testing.T) {
	var request KnowledgeScopeRequest

	require.NoError(t, json.Unmarshal([]byte(`{"folder_scopes":null}`), &request))
	require.Nil(t, request.FolderScopes)
}

func TestAuthorizedKnowledgeScopeTargetDoesNotMarshalRuntimeFields(t *testing.T) {
	target := AuthorizedKnowledgeScopeTarget{
		KnowledgeBaseID: "secret-kb",
		SourceTenantID:  987654321,
		KnowledgeIDs:    []string{"secret-knowledge"},
		TagIDs:          []string{"secret-tag"},
		ScopeTagIDs:     []string{"secret-scope-tag"},
	}

	encoded, err := json.Marshal(target)
	require.NoError(t, err)
	require.JSONEq(t, `{}`, string(encoded))
	for _, secret := range []string{
		"secret-tenant",
		"secret-kb",
		"987654321",
		"secret-knowledge",
		"secret-tag",
		"secret-scope-tag",
		"KnowledgeBaseID",
		"SourceTenantID",
		"KnowledgeIDs",
		"TagIDs",
		"ScopeTagIDs",
	} {
		require.NotContains(t, string(encoded), secret)
	}
}

func TestKnowledgeScopeResolveInputDoesNotMarshalRuntimeFields(t *testing.T) {
	request := &KnowledgeScopeRequest{
		KnowledgeBaseIDs: []string{"secret-request-kb"},
	}
	input := KnowledgeScopeResolveInput{
		Request: request,
		AuthorizedTargets: []AuthorizedKnowledgeScopeTarget{{
			KnowledgeBaseID: "secret-authorized-kb",
			SourceTenantID:  987654321,
		}},
	}

	requestJSON, err := json.Marshal(request)
	require.NoError(t, err)
	require.Contains(t, string(requestJSON), `"knowledge_base_ids":["secret-request-kb"]`)

	encoded, err := json.Marshal(input)
	require.NoError(t, err)
	require.JSONEq(t, `{}`, string(encoded))
	for _, secret := range []string{
		"secret-tenant",
		"secret-request-kb",
		"secret-authorized-kb",
		"987654321",
		"Request",
		"AuthorizedTargets",
	} {
		require.NotContains(t, string(encoded), secret)
	}
}
