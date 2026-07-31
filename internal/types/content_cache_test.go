package types

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStableChunkID_DeterministicAndSensitive(t *testing.T) {
	id := StableChunkID("knowledge-1", "text", "3", "hello world")
	require.Len(t, id, 32)

	// Same inputs -> same ID (the reparse-reuse property).
	assert.Equal(t, id, StableChunkID("knowledge-1", "text", "3", "hello world"))

	// Any input change -> different ID (content edits invalidate references).
	assert.NotEqual(t, id, StableChunkID("knowledge-1", "text", "3", "hello world!"))
	assert.NotEqual(t, id, StableChunkID("knowledge-1", "text", "4", "hello world"))
	assert.NotEqual(t, id, StableChunkID("knowledge-2", "text", "3", "hello world"))
	assert.NotEqual(t, id, StableChunkID("knowledge-1", "parent_text", "3", "hello world"))
}

func TestStableChunkID_DistinguishesIdenticalChunkBodies(t *testing.T) {
	// Two chunks in the same document with identical content must not collide:
	// the sequence number is part of the key.
	a := StableChunkID("k", "text", "1", "same")
	b := StableChunkID("k", "text", "2", "same")
	assert.NotEqual(t, a, b)
}

func TestContentCacheKey_DeterministicAndSensitive(t *testing.T) {
	k1 := ContentCacheKey(ContentCacheKindEmbedding, "model-a", "text")
	k2 := ContentCacheKey(ContentCacheKindEmbedding, "model-a", "text")
	assert.Equal(t, k1, k2)
	assert.True(t, strings.HasPrefix(k1, ContentCacheKindEmbedding+":"))

	// Model / content / kind changes each move the key (layered invalidation).
	assert.NotEqual(t, k1, ContentCacheKey(ContentCacheKindEmbedding, "model-b", "text"))
	assert.NotEqual(t, k1, ContentCacheKey(ContentCacheKindEmbedding, "model-a", "text2"))
	assert.NotEqual(t, k1, ContentCacheKey(ContentCacheKindVLM, "model-a", "text"))

	// Must fit the cache_key VARCHAR(128) primary key.
	require.LessOrEqual(t, len(k1), 128)
}
