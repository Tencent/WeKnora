package types

import (
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStableDocumentChunkID_DeterministicAndUUIDShaped(t *testing.T) {
	a := StableDocumentChunkID("kb-doc-1", "hash-aaa", ChunkIDRoleText)
	b := StableDocumentChunkID("kb-doc-1", "hash-aaa", ChunkIDRoleText)
	assert.Equal(t, a, b)
	_, err := uuid.Parse(a)
	require.NoError(t, err)

	assert.NotEqual(t, a, StableDocumentChunkID("kb-doc-2", "hash-aaa", ChunkIDRoleText))
	assert.NotEqual(t, a, StableDocumentChunkID("kb-doc-1", "hash-bbb", ChunkIDRoleText))
	assert.NotEqual(t, a, StableDocumentChunkID("kb-doc-1", "hash-aaa", ChunkIDRoleParentText))
}
