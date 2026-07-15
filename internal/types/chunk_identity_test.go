package types

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNormalizeChunkContentCanonicalizesWhitespace(t *testing.T) {
	assert.Equal(t, "alpha\nbeta", NormalizeChunkContent("  alpha\r\nbeta  "))
	assert.Equal(t, "alpha\nbeta", NormalizeChunkContent("alpha\rbeta"))
}

func TestChunkIDAllocatorIsStableAndDisambiguatesDuplicates(t *testing.T) {
	first := NewChunkIDAllocator("knowledge-1")
	id1, hash1 := first.Next(ChunkTypeText, "  alpha\r\n")
	id2, hash2 := first.Next(ChunkTypeText, "alpha\n")

	second := NewChunkIDAllocator("knowledge-1")
	again1, againHash1 := second.Next(ChunkTypeText, "alpha\n")
	again2, againHash2 := second.Next(ChunkTypeText, "alpha\r\n")

	assert.Len(t, id1, 36)
	assert.NotEqual(t, id1, id2)
	assert.Equal(t, id1, again1)
	assert.Equal(t, id2, again2)
	assert.Equal(t, hash1, hash2)
	assert.Equal(t, hash1, againHash1)
	assert.Equal(t, hash2, againHash2)
}

func TestChunkIDAllocatorDoesNotChurnAfterUnrelatedInsertion(t *testing.T) {
	before := NewChunkIDAllocator("knowledge-1")
	alphaBefore, _ := before.Next(ChunkTypeText, "alpha")
	betaBefore, _ := before.Next(ChunkTypeText, "beta")

	after := NewChunkIDAllocator("knowledge-1")
	_, _ = after.Next(ChunkTypeText, "inserted")
	alphaAfter, _ := after.Next(ChunkTypeText, "alpha")
	betaAfter, _ := after.Next(ChunkTypeText, "beta")

	assert.Equal(t, alphaBefore, alphaAfter)
	assert.Equal(t, betaBefore, betaAfter)
}

func TestEmbeddingFingerprintIncludesEffectiveInputs(t *testing.T) {
	baseInput := EmbeddingInput("Document", "# Heading", " body ")
	base := EmbeddingFingerprint("model-1", 1536, baseInput)

	assert.NotEqual(t, base, EmbeddingFingerprint("model-2", 1536, baseInput))
	assert.NotEqual(t, base, EmbeddingFingerprint("model-1", 3072, baseInput))
	assert.NotEqual(t, base, EmbeddingFingerprint("model-1", 1536,
		EmbeddingInput("Other", "# Heading", " body ")))
	assert.NotEqual(t, base, EmbeddingFingerprint("model-1", 1536,
		EmbeddingInput("Document", "# Other", " body ")))
}

func TestWithChunkEmbeddingFingerprintPreservesMetadata(t *testing.T) {
	metadata, err := json.Marshal(map[string]any{"questions": []string{"q1"}})
	require.NoError(t, err)

	updated, err := WithChunkEmbeddingFingerprint(JSON(metadata), "fingerprint")
	require.NoError(t, err)
	assert.Equal(t, "fingerprint", ChunkEmbeddingFingerprint(updated))

	values, err := updated.Map()
	require.NoError(t, err)
	assert.Equal(t, []any{"q1"}, values["questions"])
}

func TestWithChunkEmbeddingFingerprintInitializesNullMetadata(t *testing.T) {
	updated, err := WithChunkEmbeddingFingerprint(JSON("null"), "fingerprint")
	require.NoError(t, err)
	assert.Equal(t, "fingerprint", ChunkEmbeddingFingerprint(updated))
	assert.JSONEq(t, `{"_weknora_embedding_fingerprint":"fingerprint"}`, string(updated))
}

func TestWithChunkEmbeddingFingerprintPreservesLargeNumberTokens(t *testing.T) {
	metadata := JSON(`{"document_id":9007199254740993}`)

	updated, err := WithChunkEmbeddingFingerprint(metadata, "fingerprint")
	require.NoError(t, err)

	assert.Contains(t, string(updated), `"document_id":9007199254740993`)
	assert.Equal(t, "fingerprint", ChunkEmbeddingFingerprint(updated))
}
