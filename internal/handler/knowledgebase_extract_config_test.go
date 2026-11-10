package handler

import (
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

func extractConfigWithLimit(limit *int) *types.ExtractConfig {
	return &types.ExtractConfig{
		Enabled:                   true,
		Text:                      "extract",
		Tags:                      []string{"Tag"},
		Nodes:                     []*types.GraphNode{{Name: "Node"}},
		Relations:                 []*types.GraphRelation{{Node1: "Node", Node2: "Node", Type: "Tag"}},
		MaxGraphChunksPerDocument: limit,
	}
}

func intPtrForExtractConfig(value int) *int { return &value }

func TestValidateExtractConfigBoundsGraphChunkLimit(t *testing.T) {
	for _, limit := range []int{0, 1, 100000} {
		config := extractConfigWithLimit(intPtrForExtractConfig(limit))
		require.NoError(t, validateExtractConfig(config), "limit %d", limit)
	}
	for _, limit := range []int{-1, 100001} {
		config := extractConfigWithLimit(intPtrForExtractConfig(limit))
		require.Error(t, validateExtractConfig(config), "limit %d", limit)
	}

	disabled := extractConfigWithLimit(intPtrForExtractConfig(-1))
	disabled.Enabled = false
	require.Error(t, validateExtractConfig(disabled),
		"an invalid limit must not be accepted just because extraction is disabled")
}
