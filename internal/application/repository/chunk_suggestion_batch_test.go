package repository

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestChunkKnowledgeIDBatchesBoundSuggestedQuestionQueries(t *testing.T) {
	ids := make([]string, 1201)
	for i := range ids {
		ids[i] = fmt.Sprintf("knowledge-%04d", i)
	}

	batches := chunkKnowledgeIDBatches(ids)
	require.Len(t, batches, 3)
	require.Len(t, batches[0], maxKnowledgeIDsPerChunkQuery)
	require.Len(t, batches[1], maxKnowledgeIDsPerChunkQuery)
	require.Len(t, batches[2], 201)

	flattened := make([]string, 0, len(ids))
	for _, batch := range batches {
		require.LessOrEqual(t, len(batch), maxKnowledgeIDsPerChunkQuery)
		flattened = append(flattened, batch...)
	}
	require.Equal(t, ids, flattened)
	require.Nil(t, chunkKnowledgeIDBatches(nil))
}
