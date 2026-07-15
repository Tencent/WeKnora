package service

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSplitKnowledgeIDBatchesBoundsFolderRetrievalParameters(t *testing.T) {
	ids := make([]string, 1201)
	for i := range ids {
		ids[i] = fmt.Sprintf("knowledge-%04d", i)
	}

	batches := splitKnowledgeIDBatches(ids)
	require.Len(t, batches, 3)
	require.Len(t, batches[0], maxKnowledgeIDsPerRetrieve)
	require.Len(t, batches[1], maxKnowledgeIDsPerRetrieve)
	require.Len(t, batches[2], 201)

	flattened := make([]string, 0, len(ids))
	for _, batch := range batches {
		require.LessOrEqual(t, len(batch), maxKnowledgeIDsPerRetrieve)
		flattened = append(flattened, batch...)
	}
	require.Equal(t, ids, flattened)
	require.Equal(t, [][]string{nil}, splitKnowledgeIDBatches(nil))
}
