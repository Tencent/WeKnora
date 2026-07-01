package service

import (
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/assert"
)

func TestRewireReusedChunkRelationships(t *testing.T) {
	parent := &types.Chunk{ID: "new-parent", ChunkType: types.ChunkTypeParentText}
	child := &types.Chunk{ID: "child", ChunkType: types.ChunkTypeText, ParentChunkID: "new-parent"}
	first := &types.Chunk{ID: "first", ChunkType: types.ChunkTypeText, NextChunkID: "new-reused"}
	reused := &types.Chunk{ID: "old-reused", ChunkType: types.ChunkTypeText, PreChunkID: "first"}

	rewireReusedChunkRelationships(
		[]*types.Chunk{parent, child, first, reused},
		[]*types.Chunk{parent},
		[]*types.Chunk{first, reused},
		false,
		map[string]string{"new-parent": "old-parent", "new-reused": "old-reused"},
	)

	assert.Equal(t, "old-parent", child.ParentChunkID)
	assert.Equal(t, "old-reused", first.NextChunkID)
	assert.Equal(t, "first", reused.PreChunkID)
	assert.Empty(t, first.PreChunkID)
	assert.Empty(t, reused.NextChunkID)
}
