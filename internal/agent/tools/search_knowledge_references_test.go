package tools

import (
	"context"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

// The turn's citations are built from ToolResult.KnowledgeRefs, so the hits
// have to leave formatOutput as typed results and not only as the flattened
// Data rows the model reads: those rows drop fields the reference panel needs
// and cannot be turned back into a SearchResult without guessing.
func TestFormatOutputCarriesTypedKnowledgeRefs(t *testing.T) {
	t.Parallel()
	first := &types.SearchResult{ID: "chunk-1", KnowledgeID: "doc-1", Content: "alpha", Score: 0.9}
	second := &types.SearchResult{ID: "chunk-2", KnowledgeID: "doc-2", Content: "beta", Score: 0.7}

	res := (&SearchKnowledgeTool{}).formatOutput(
		context.Background(),
		[]*searchResultWithMeta{
			{SearchResult: first, KnowledgeBaseID: "kb-1"},
			{SearchResult: second, KnowledgeBaseID: "kb-1"},
		},
		[]string{"kb-1"}, "alpha", SearchModeHybrid,
	)

	require.True(t, res.Success)
	require.Equal(t, []*types.SearchResult{first, second}, res.KnowledgeRefs)
}

func TestFormatOutputWithNoHitsCarriesNoRefs(t *testing.T) {
	t.Parallel()
	res := (&SearchKnowledgeTool{}).formatOutput(
		context.Background(), nil, []string{"kb-1"}, "alpha", SearchModeSemantic)

	require.True(t, res.Success)
	require.Empty(t, res.KnowledgeRefs)
}
