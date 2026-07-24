package service

import (
	"testing"

	"github.com/Tencent/WeKnora/internal/contentcache"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

func TestNextStableChunkIDIgnoresSourceSequenceAndTracksDuplicates(t *testing.T) {
	inputA := contentcache.ChunkIDInput{
		KnowledgeID: "knowledge-1",
		ChunkType:   types.ChunkTypeText,
		Content:     "alpha",
		Seq:         1,
	}
	inputB := inputA
	inputB.Content = "beta"
	inputB.Seq = 2

	firstOrder := map[string]int{}
	alphaID := nextStableChunkID(firstOrder, inputA)
	betaID := nextStableChunkID(firstOrder, inputB)
	duplicateAlphaID := nextStableChunkID(firstOrder, inputA)

	reordered := map[string]int{}
	inputB.Seq = 99
	require.Equal(t, betaID, nextStableChunkID(reordered, inputB))
	inputA.Seq = 42
	require.Equal(t, alphaID, nextStableChunkID(reordered, inputA))

	require.NotEqual(t, alphaID, duplicateAlphaID)
}

func TestMultimodalPendingKeySeparatesAttempts(t *testing.T) {
	require.Equal(t, "multimodal:pending:knowledge-1", multimodalPendingKey("knowledge-1", 0))
	require.Equal(t, "multimodal:pending:knowledge-1:1", multimodalPendingKey("knowledge-1", 1))
	require.NotEqual(t, multimodalPendingKey("knowledge-1", 1), multimodalPendingKey("knowledge-1", 2))
}

func TestStableGeneratedQuestionIDIsDeterministicAndScopedToChunk(t *testing.T) {
	id := stableGeneratedQuestionID("chunk-a", "What is caching?")
	require.Equal(t, id, stableGeneratedQuestionID("chunk-a", "What is caching?"))
	require.NotEqual(t, id, stableGeneratedQuestionID("chunk-b", "What is caching?"))
	require.NotEqual(t, id, stableGeneratedQuestionID("chunk-a", "What is invalidation?"))
}
