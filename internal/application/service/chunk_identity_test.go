package service

import (
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

func TestStableChunkIDIsContentAddressed(t *testing.T) {
	t.Parallel()

	base := stableChunkIdentity{
		KnowledgeID: "knowledge-1",
		ChunkType:   types.ChunkTypeText,
		Content:     "Cafe\u0301\r\nline  ",
		Occurrence:  0,
	}
	canonical := base
	canonical.Content = "Café\nline"

	require.Equal(t, stableChunkID(base), stableChunkID(canonical))
	require.NotEqual(t, stableChunkID(base), stableChunkID(stableChunkIdentity{
		KnowledgeID: base.KnowledgeID,
		ChunkType:   base.ChunkType,
		Content:     base.Content,
		Occurrence:  1,
	}))
	require.NotEqual(t, stableChunkID(base), stableChunkID(stableChunkIdentity{
		KnowledgeID: "knowledge-2",
		ChunkType:   base.ChunkType,
		Content:     base.Content,
		Occurrence:  base.Occurrence,
	}))
}

func TestStableDerivedIDSeparatesArtifactKinds(t *testing.T) {
	t.Parallel()
	require.Equal(t, stableDerivedID("owner", "summary"), stableDerivedID("owner", "summary"))
	require.NotEqual(t, stableDerivedID("owner", "summary"), stableDerivedID("owner", "question"))
}
