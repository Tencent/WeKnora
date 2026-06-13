package service

import (
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

func TestRemoveChunkRefs(t *testing.T) {
	got := removeChunkRefs(
		types.StringArray{"chunk-a", "chunk-b", "chunk-c"},
		map[string]bool{"chunk-b": true},
	)

	require.Equal(t, types.StringArray{"chunk-a", "chunk-c"}, got)
}

func TestRemoveChunkRefsNoRemovedSet(t *testing.T) {
	refs := types.StringArray{"chunk-a", "chunk-b"}

	got := removeChunkRefs(refs, nil)

	require.Equal(t, refs, got)
}
