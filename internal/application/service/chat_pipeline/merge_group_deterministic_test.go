package chatpipeline

import (
	"context"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// groupAndMergeCurrentContent groups results by KnowledgeID and ChunkType
// using two levels of Go maps. Map iteration order is non-deterministic,
// so the concatenated output can appear in arbitrary order. This test
// verifies that the post-merge sort restores deterministic ordering
// (score desc, then KnowledgeID, ChunkType).
func TestGroupAndMergeOverlapping_DeterministicOrdering(t *testing.T) {
	plugin := &PluginMerge{}

	t.Run("sorts by score descending across groups", func(t *testing.T) {
		// Same KnowledgeID, different ChunkTypes -> separate inner-map
		// buckets whose iteration order is randomized.
		chunks := []*types.SearchResult{
			{ID: "low", KnowledgeID: "kb-001", ChunkType: "text", StartAt: 0, EndAt: 100, Score: 0.4},
			{ID: "high", KnowledgeID: "kb-001", ChunkType: "summary", StartAt: 200, EndAt: 300, Score: 0.9},
			{ID: "mid", KnowledgeID: "kb-001", ChunkType: "parent_text", StartAt: 400, EndAt: 500, Score: 0.6},
		}
		results := plugin.groupAndMergeCurrentContent(context.Background(), chunks)

		require.Len(t, results, 3)
		assert.Equal(t, "high", results[0].ID, "highest score (0.9) must be first")
		assert.Equal(t, "mid", results[1].ID, "middle score (0.6) must be second")
		assert.Equal(t, "low", results[2].ID, "lowest score (0.4) must be last")
	})

	t.Run("uses KnowledgeID as first tie-breaker when scores equal", func(t *testing.T) {
		// Different KnowledgeIDs -> separate outer-map buckets whose
		// iteration order is randomized.
		chunks := []*types.SearchResult{
			{ID: "ab", KnowledgeID: "kb-ab", ChunkType: "text", StartAt: 0, EndAt: 50, Score: 0.8},
			{ID: "aa", KnowledgeID: "kb-aa", ChunkType: "text", StartAt: 0, EndAt: 50, Score: 0.8},
		}
		results := plugin.groupAndMergeCurrentContent(context.Background(), chunks)
		require.Len(t, results, 2)
		assert.Equal(t, "aa", results[0].ID, "same score, kb-aa < kb-ab")
	})

	t.Run("merged chunk uses max score and is sorted among other groups", func(t *testing.T) {
		// Two overlapping text chunks merge into one (score = max(0.5,0.7) = 0.7),
		// then the merged result competes with a higher-scored summary chunk
		// across the inner-map boundary.
		chunks := []*types.SearchResult{
			{ID: "low-a", KnowledgeID: "kb-001", ChunkType: "text", ChunkIndex: 1, Content: "first body", StartAt: 0, EndAt: 50, Score: 0.5},
			{ID: "low-b", KnowledgeID: "kb-001", ChunkType: "text", ChunkIndex: 2, Content: "second body", StartAt: 30, EndAt: 80, Score: 0.7},
			{ID: "high", KnowledgeID: "kb-001", ChunkType: "summary", StartAt: 200, EndAt: 300, Score: 0.9},
		}
		results := plugin.groupAndMergeCurrentContent(context.Background(), chunks)
		require.Len(t, results, 2, "two text chunks merged into one, plus one summary")
		assert.Equal(t, "high", results[0].ID, "summary (0.9) beats merged text (0.7)")
	})

	t.Run("uses ChunkType as tie-breaker when score and knowledge match", func(t *testing.T) {
		// Same KnowledgeID, different ChunkTypes -> separate inner-map
		// buckets whose iteration order is randomized.
		chunks := []*types.SearchResult{
			{ID: "txt", KnowledgeID: "kb-x", ChunkType: "text", StartAt: 0, EndAt: 50, Score: 0.8},
			{ID: "sum", KnowledgeID: "kb-x", ChunkType: "summary", StartAt: 100, EndAt: 150, Score: 0.8},
		}
		results := plugin.groupAndMergeCurrentContent(context.Background(), chunks)
		require.Len(t, results, 2)
		assert.Equal(t, "sum", results[0].ID, "same kb+score, 'summary' < 'text'")
	})
}

func TestGroupAndMergeOverlapping_IgnoresStaleEditedRanges(t *testing.T) {
	plugin := &PluginMerge{}
	chunks := []*types.SearchResult{
		{
			ID: "edited-outer", KnowledgeID: "doc", ChunkType: "text", ChunkIndex: 1,
			StartAt: 0, EndAt: 200, Content: "completely rewritten outer content", Score: 0.4,
		},
		{
			ID: "edited-inner", KnowledgeID: "doc", ChunkType: "text", ChunkIndex: 2,
			StartAt: 50, EndAt: 100, Content: "independent current inner content", Score: 0.9,
		},
	}

	results := plugin.groupAndMergeCurrentContent(context.Background(), chunks)
	require.Len(t, results, 1)
	assert.Contains(t, results[0].Content, chunks[0].Content)
	assert.Contains(t, results[0].Content, chunks[1].Content)
	assert.Equal(t, 0.9, results[0].Score)
}

func TestGroupAndMergeOverlapping_DoesNotMergeNonSequentialChunksFromCoordinates(t *testing.T) {
	plugin := &PluginMerge{}
	chunks := []*types.SearchResult{
		{ID: "one", KnowledgeID: "doc", ChunkType: "text", ChunkIndex: 1, StartAt: 0, EndAt: 200, Content: "one"},
		{ID: "three", KnowledgeID: "doc", ChunkType: "text", ChunkIndex: 3, StartAt: 50, EndAt: 100, Content: "three"},
	}

	results := plugin.groupAndMergeCurrentContent(context.Background(), chunks)
	require.Len(t, results, 2)
}

// TestGroupAndMergeOverlapping_CrossKnowledgePreservesMergeLogic verifies
// that chunks from different KnowledgeIDs do NOT accidentally merge across
// knowledge boundaries even when they share StartAt/EndAt ranges.
func TestGroupAndMergeOverlapping_CrossKnowledgePreservesMergeLogic(t *testing.T) {
	plugin := &PluginMerge{}
	chunks := []*types.SearchResult{
		{ID: "a-1", KnowledgeID: "kb-a", ChunkType: "text", StartAt: 0, EndAt: 100, Score: 0.9},
		{ID: "b-1", KnowledgeID: "kb-b", ChunkType: "text", StartAt: 0, EndAt: 100, Score: 0.5},
	}
	results := plugin.groupAndMergeCurrentContent(context.Background(), chunks)

	require.Len(t, results, 2, "chunks from different KBs must not merge")
	assert.Equal(t, "a-1", results[0].ID, "higher score from kb-a first")
	assert.Equal(t, "b-1", results[1].ID, "lower score from kb-b second")
}
