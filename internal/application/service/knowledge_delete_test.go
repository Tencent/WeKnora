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

func TestBuildKnowledgeVectorDeleteGroupsSeparatesBoundStores(t *testing.T) {
	storeA := "store-a"
	storeB := "store-b"
	kbs := map[string]*types.KnowledgeBase{
		"kb-default": {ID: "kb-default"},
		"kb-a":       {ID: "kb-a", VectorStoreID: &storeA},
		"kb-b":       {ID: "kb-b", VectorStoreID: &storeB},
	}
	knowledges := []*types.Knowledge{
		{ID: "default-1", KnowledgeBaseID: "kb-default", EmbeddingModelID: "model-1", Type: "file"},
		{ID: "store-a-1", KnowledgeBaseID: "kb-a", EmbeddingModelID: "model-1", Type: "file"},
		{ID: "store-a-2", KnowledgeBaseID: "kb-a", EmbeddingModelID: "model-1", Type: "file"},
		{ID: "store-b-1", KnowledgeBaseID: "kb-b", EmbeddingModelID: "model-1", Type: "file"},
	}

	groups := buildKnowledgeVectorDeleteGroups(knowledges, kbs)

	actual := make(map[string][]string, len(groups))
	for _, group := range groups {
		actual[group.VectorStoreID] = group.KnowledgeIDs
	}
	require.Equal(t, []string{"default-1"}, actual[""])
	require.Equal(t, []string{"store-a-1", "store-a-2"}, actual["store-a"])
	require.Equal(t, []string{"store-b-1"}, actual["store-b"])
}
