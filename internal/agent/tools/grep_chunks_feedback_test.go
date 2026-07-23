package tools

import (
	"context"
	"regexp"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

func TestGrepChunksScoreAppliesFeedbackRecallWeight(t *testing.T) {
	tool := &GrepChunksTool{}
	results := []chunkWithTitle{
		{Chunk: types.Chunk{ID: "reduced", Content: "target text", RecallWeight: 0.8}},
		{Chunk: types.Chunk{ID: "boosted", Content: "target text", RecallWeight: 1.2}},
	}

	scored := tool.scoreChunks(context.Background(), results, []*regexp.Regexp{regexp.MustCompile("target")})

	require.Len(t, scored, 2)
	require.InDelta(t, 0.8, scored[0].MatchScore, 1e-9)
	require.InDelta(t, 1.0, scored[1].MatchScore, 1e-9)
}
