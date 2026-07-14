package types

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMessageExecutionContextRoundTripsFolderScopes(t *testing.T) {
	want := []FolderScope{{KnowledgeBaseID: "kb-1", FolderIDs: []string{"folder-a", "folder-b"}}}
	context := MessageExecutionContext{FolderScopes: want}
	encoded, err := context.Value()
	require.NoError(t, err)

	var decoded MessageExecutionContext
	require.NoError(t, decoded.Scan(encoded))
	assert.Equal(t, want, decoded.FolderScopes)
}

func TestMessageExecutionContextScansLegacyPayloadWithoutFolderScopes(t *testing.T) {
	var context MessageExecutionContext
	require.NoError(t, context.Scan(`{"knowledge_base_ids":["kb-1"],"tag_ids":["tag-1"]}`))
	assert.Equal(t, []string{"kb-1"}, context.KnowledgeBaseIDs)
	assert.Equal(t, []string{"tag-1"}, context.TagIDs)
	assert.Empty(t, context.FolderScopes)
}
