package types

import (
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestStableChunkIDSameInputSameID(t *testing.T) {
	t.Parallel()

	spec := StableChunkIDSpec{
		KnowledgeID:       "knowledge-1",
		ChunkType:         ChunkTypeText,
		Content:           " hello\nworld ",
		Occurrence:        0,
		ChunkingConfigKey: "token:size=512:overlap=50",
	}

	first := StableChunkID(spec)
	second := StableChunkID(spec)

	require.Equal(t, first, second)
	require.NoError(t, uuid.Validate(first))
}

func TestStableChunkIDNormalizesWhitespace(t *testing.T) {
	t.Parallel()

	base := StableChunkIDSpec{
		KnowledgeID:       "knowledge-1",
		ChunkType:         ChunkTypeText,
		Content:           "hello world",
		Occurrence:        0,
		ChunkingConfigKey: "token:size=512:overlap=50",
	}
	withWhitespace := base
	withWhitespace.Content = " hello\n\tworld "

	require.Equal(t, StableChunkID(base), StableChunkID(withWhitespace))
}

func TestStableChunkIDSeparatesDuplicateOccurrences(t *testing.T) {
	t.Parallel()

	first := StableChunkID(StableChunkIDSpec{
		KnowledgeID:       "knowledge-1",
		ChunkType:         ChunkTypeText,
		Content:           "duplicate",
		Occurrence:        0,
		ChunkingConfigKey: "token:size=512:overlap=50",
	})
	second := StableChunkID(StableChunkIDSpec{
		KnowledgeID:       "knowledge-1",
		ChunkType:         ChunkTypeText,
		Content:           "duplicate",
		Occurrence:        1,
		ChunkingConfigKey: "token:size=512:overlap=50",
	})

	require.NotEqual(t, first, second)
	require.NoError(t, uuid.Validate(first))
	require.NoError(t, uuid.Validate(second))
}

func TestStableChunkIDChangesWhenContentOrChunkingConfigChanges(t *testing.T) {
	t.Parallel()

	base := StableChunkIDSpec{
		KnowledgeID:       "knowledge-1",
		ChunkType:         ChunkTypeText,
		Content:           "alpha",
		Occurrence:        0,
		ChunkingConfigKey: "token:size=512:overlap=50",
	}
	changedContent := base
	changedContent.Content = "beta"
	changedConfig := base
	changedConfig.ChunkingConfigKey = "token:size=1024:overlap=50"

	require.NotEqual(t, StableChunkID(base), StableChunkID(changedContent))
	require.NotEqual(t, StableChunkID(base), StableChunkID(changedConfig))
}

func TestStableContentHashNormalizesEmbeddingText(t *testing.T) {
	t.Parallel()

	require.Equal(t,
		StableContentHash(" Title\n\nBody\ttext "),
		StableContentHash("Title Body text"),
	)
	require.NotEmpty(t, StableContentHash("Title Body text"))
}
