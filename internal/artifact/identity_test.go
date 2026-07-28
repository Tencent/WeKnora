package artifact

import (
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStableEntityIdentityIgnoresGlobalOrder(t *testing.T) {
	knowledgeID := uuid.New().String()
	input := EntityIdentityInput{
		KnowledgeID:  knowledgeID,
		IDVersion:    StableEntityIDVersion,
		EntityType:   "text",
		SourceAnchor: "page=2;block=heading-intro",
		Content:      "Exact persisted content\r\n",
	}
	before, err := StableEntityIdentity(input)
	require.NoError(t, err)

	// A new unrelated block inserted before this block does not alter any
	// semantic field or introduce a sequence number.
	after, err := StableEntityIdentity(input)
	require.NoError(t, err)
	assert.Equal(t, before, after)

	input.Content = "Exact persisted content\n"
	changed, err := StableEntityIdentity(input)
	require.NoError(t, err)
	assert.NotEqual(t, before.ID, changed.ID)
	assert.Equal(t, before.MatchKey, changed.MatchKey)
}

func TestStableEntityIdentityIncludesParentAndLocalDuplicateOrdinal(t *testing.T) {
	knowledgeID := uuid.New().String()
	allocator := NewIdentityAllocator(knowledgeID, StableEntityIDVersion)

	first, err := allocator.Next("text", "parent-a", "block=7", "same")
	require.NoError(t, err)
	second, err := allocator.Next("text", "parent-a", "block=7", "same")
	require.NoError(t, err)
	otherParent, err := allocator.Next("text", "parent-b", "block=7", "same")
	require.NoError(t, err)

	assert.NotEqual(t, first.ID, second.ID)
	assert.NotEqual(t, first.MatchKey, second.MatchKey)
	assert.NotEqual(t, first.ID, otherParent.ID)
}

func TestReuseUniqueLegacyIDOnlyWhenUnambiguous(t *testing.T) {
	desired, err := StableEntityIdentity(EntityIdentityInput{
		KnowledgeID:  uuid.New().String(),
		IDVersion:    StableEntityIDVersion,
		EntityType:   "text",
		SourceAnchor: "block=1",
		Content:      "content",
	})
	require.NoError(t, err)
	legacyID := uuid.New().String()

	reused, ok, err := ReuseUniqueLegacyID(desired, map[string][]string{
		desired.SemanticKey: {legacyID},
	})
	require.NoError(t, err)
	assert.True(t, ok)
	assert.Equal(t, legacyID, reused)

	reused, ok, err = ReuseUniqueLegacyID(desired, map[string][]string{
		desired.SemanticKey: {legacyID, uuid.New().String()},
	})
	require.NoError(t, err)
	assert.False(t, ok)
	assert.Equal(t, desired.ID, reused)
}
