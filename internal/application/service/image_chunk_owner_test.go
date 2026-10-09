package service

import (
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

func TestImageChunkOwnerUsesExactTargets(t *testing.T) {
	chunks := []types.ParsedChunk{
		{
			ChunkID: "first",
			Content: "unrelated",
		},
		{
			ChunkID: "prefix",
			Content: "![image](resource://abc-more)",
		},
		{
			ChunkID: "actual",
			Content: "![image](resource://abc)",
		},
	}
	require.Equal(t, "actual", imageChunkOwner("resource://abc", chunks))
	require.Empty(t, imageChunkOwner("resource://missing", chunks))
	require.Empty(t, imageChunkOwner("", chunks))
	require.Empty(t,
		imageChunkOwner("resource://abc",
			append(chunks,
				types.ParsedChunk{
					ChunkID: "duplicate",
					Content: "![same](resource://abc)",
				})))
	require.Empty(t,
		imageChunkOwner("resource://abc",
			[]types.ParsedChunk{{
				ChunkID: "prose",
				Content: "resource://abc",
			}}))
}
