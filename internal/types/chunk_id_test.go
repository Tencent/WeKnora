package types

import (
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNormalizeForHash(t *testing.T) {
	assert.Equal(t, "hello\nworld", NormalizeForHash("  hello  \r\nworld\t \n", ""))
	assert.Equal(t, "Heading\n\nhello", NormalizeForHash(" hello ", " Heading \r\n"))
	assert.Equal(t, "Heading", NormalizeForHash("", " Heading \t"))
	assert.Equal(t, "", NormalizeForHash(" \r\n\t ", ""))
}

func TestContentHash(t *testing.T) {
	hash1 := ContentHash(" hello \r\nworld\t", "")
	hash2 := ContentHash("hello\nworld", "")

	require.Len(t, hash1, 64)
	assert.Equal(t, hash1, hash2)
	assert.Empty(t, ContentHash(" \n\t ", ""))
	assert.NotEqual(t, ContentHash("hello", ""), ContentHash("hello", "section"))
}

func TestStableChunkID(t *testing.T) {
	hash := ContentHash("hello", "")
	id1 := StableChunkID(1, "knowledge-a", ChunkTypeText, hash, 0)
	id2 := StableChunkID(1, "knowledge-a", ChunkTypeText, hash, 0)

	assert.Equal(t, id1, id2)
	assert.Len(t, id1, 36)
	_, err := uuid.Parse(id1)
	require.NoError(t, err)
}

func TestChunkIDAllocator_ReparseReproducible(t *testing.T) {
	allocate := func() []string {
		allocator := NewChunkIDAllocator(1, "knowledge-a")
		out := make([]string, 0, 3)
		for _, content := range []string{"hello", "hello", "world"} {
			id, _ := allocator.Allocate(ChunkTypeText, content, "")
			out = append(out, id)
		}
		return out
	}

	assert.Equal(t, allocate(), allocate())
}

func TestChunkIDAllocator_NormalizedContentKeepsSameIDAcrossReparse(t *testing.T) {
	allocator1 := NewChunkIDAllocator(1, "knowledge-a")
	id1, hash1 := allocator1.Allocate(ChunkTypeText, " hello  \r\nworld\t ", " # Title \r\n")

	allocator2 := NewChunkIDAllocator(1, "knowledge-a")
	id2, hash2 := allocator2.Allocate(ChunkTypeText, "hello\nworld", "# Title")

	assert.Equal(t, hash1, hash2)
	assert.Equal(t, id1, id2)
}

func TestChunkIDAllocator_DuplicateContentUsesOccurrence(t *testing.T) {
	allocator := NewChunkIDAllocator(1, "knowledge-a")

	id1, hash1 := allocator.Allocate(ChunkTypeText, "same", "")
	id2, hash2 := allocator.Allocate(ChunkTypeText, " same \n", "")

	assert.Equal(t, hash1, hash2)
	assert.NotEqual(t, id1, id2)
	assert.Equal(t, StableChunkID(1, "knowledge-a", ChunkTypeText, hash1, 0), id1)
	assert.Equal(t, StableChunkID(1, "knowledge-a", ChunkTypeText, hash1, 1), id2)
}

func TestChunkIDAllocator_IsolatedByKnowledgeAndType(t *testing.T) {
	hash := ContentHash("same", "")

	assert.NotEqual(t,
		StableChunkID(1, "knowledge-a", ChunkTypeText, hash, 0),
		StableChunkID(1, "knowledge-b", ChunkTypeText, hash, 0),
	)
	assert.NotEqual(t,
		StableChunkID(1, "knowledge-a", ChunkTypeText, hash, 0),
		StableChunkID(1, "knowledge-a", ChunkTypeParentText, hash, 0),
	)
}
