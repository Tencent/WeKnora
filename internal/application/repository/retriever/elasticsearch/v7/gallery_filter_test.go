package v7

import (
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

func TestImageChunkAllowListIsAPositiveFilter(t *testing.T) {
	repo := &elasticsearchRepository{}
	params := types.RetrieveParams{KnowledgeBaseIDs: []string{"kb"}, ChunkIDs: []string{"image-only"}}
	body := []byte(repo.getBaseConds(params))
	require.Contains(t, string(body), `"chunk_id":["image-only"]`)
	require.NotContains(t, string(body), `"must_not":[{"terms":{"chunk_id"`)
}
