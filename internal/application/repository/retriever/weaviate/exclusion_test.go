package weaviate

import (
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

func TestKnowledgeExclusionsUseScalarNotEqual(t *testing.T) {
	for _, ids := range [][]string{nil, {"current-1"}, {"current-1", "current-2"}} {
		filter := (&weaviateRepository{}).getBaseFilter(types.RetrieveParams{
			KnowledgeBaseIDs: []string{"history"}, ExcludeKnowledgeIDs: ids,
		})
		query := strings.ReplaceAll(filter.String(), " ", "")
		require.Contains(t, query, "operator:And")
		require.Equal(t, len(ids), strings.Count(query, "operator:NotEqual"))
		require.NotContains(t, query, `valueText:["current`)
		for _, id := range ids {
			require.Contains(t, query, `valueText:"`+id+`"`)
		}
	}
}
