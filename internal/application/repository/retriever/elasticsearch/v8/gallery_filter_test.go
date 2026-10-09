package v8

import (
	"encoding/json"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

func TestImageChunkAllowListIsAPositiveFilter(t *testing.T) {
	repo := &elasticsearchRepository{}
	params := types.RetrieveParams{KnowledgeBaseIDs: []string{"kb"}, ChunkIDs: []string{"image-only"}}
	body, err := json.Marshal(repo.getBaseConds(params))
	require.NoError(t, err)
	require.Contains(t, string(body), `"chunk_id":["image-only"]`)
	require.NotContains(t, string(body), `"must_not":[{"terms":{"chunk_id"`)
}
