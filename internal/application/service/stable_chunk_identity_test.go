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

func TestNextStableChunkIDDuplicateReorderPreservesIDSet(t *testing.T) {
	duplicate := contentcache.ChunkIDInput{
		KnowledgeID: "knowledge-1",
		ChunkType:   types.ChunkTypeText,
		Content:     "repeat me",
	}
	other := duplicate
	other.Content = "middle"

	first := map[string]int{}
	idsBefore := []string{
		nextStableChunkID(first, duplicate),
		nextStableChunkID(first, other),
		nextStableChunkID(first, duplicate),
	}

	reordered := map[string]int{}
	idsAfter := []string{
		nextStableChunkID(reordered, duplicate),
		nextStableChunkID(reordered, duplicate),
		nextStableChunkID(reordered, other),
	}

	require.ElementsMatch(t, idsBefore, idsAfter)
}

func TestNextStableChunkIDDeletingFirstDuplicateKeepsLargestOverlap(t *testing.T) {
	duplicate := contentcache.ChunkIDInput{
		KnowledgeID: "knowledge-1",
		ChunkType:   types.ChunkTypeText,
		Content:     "identical paragraph",
	}

	seen := map[string]int{}
	idsBefore := map[string]struct{}{
		nextStableChunkID(seen, duplicate): {},
		nextStableChunkID(seen, duplicate): {},
		nextStableChunkID(seen, duplicate): {},
	}

	afterDelete := map[string]int{}
	for i := 0; i < 2; i++ {
		id := nextStableChunkID(afterDelete, duplicate)
		if _, ok := idsBefore[id]; !ok {
			t.Fatalf("remaining duplicate id %s was not present before delete", id)
		}
	}
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
