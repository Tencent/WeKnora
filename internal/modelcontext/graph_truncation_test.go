package modelcontext

import (
	"fmt"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

// graphQueryResultData builds the Data of a graph query answer whose relation
// list stopped at the tool's cap, as the tool reports it.
func graphQueryResultData(relations []map[string]interface{}, totals map[string]interface{}) map[string]interface{} {
	data := map[string]interface{}{
		"display_type": "graph_query_results",
		"results": []map[string]interface{}{{
			"chunk_id":          "graph-chunk-real",
			"chunk_index":       4,
			"knowledge_id":      "graph-doc-real",
			"knowledge_base_id": "graph-kb-real",
			"knowledge_title":   "Graph Source",
			"content":           "A relates to B.",
		}},
		"relations": relations,
	}
	for key, value := range totals {
		data[key] = value
	}
	return data
}

// The model reads a view rebuilt from Data, never the tool's Output, so a
// capped relation list has to carry its marker into that view: without it the
// model treats the returned subgraph as the entity's whole neighbourhood.
func TestModelOutputMarksCappedGraphRelations(t *testing.T) {
	registry := newSourceRegistry()
	relations := make([]map[string]interface{}, 0, 30)
	for i := 0; i < 30; i++ {
		relations = append(relations, map[string]interface{}{
			"source": "Docker", "type": "depends_on", "target": fmt.Sprintf("Service%02d", i),
		})
	}
	output := registry.ModelOutput(&types.ToolResult{
		Success: true,
		Output:  "=== Knowledge Graph Query ===\n\n✓ Found 30 of 42 relations and 1 relevant chunks (deduplicated)\n",
		Data: graphQueryResultData(relations, map[string]interface{}{
			"relations_total":      42,
			"relations_omitted":    12,
			"graph_chunks_total":   14,
			"graph_chunks_omitted": 4,
			"query_terms_total":    13,
			"query_terms_omitted":  5,
		}),
	})
	t.Logf("model view:\n%s", output)

	require.Contains(t, output, `<graph_truncated relations_shown="30" relations_total="42" `+
		`chunks_fetched="10" chunks_total="14" terms_shown="8" terms_total="13">`)
	require.Contains(t, output, "not the complete picture")
	require.Contains(t, output, `<relation source="Docker" type="depends_on" target="Service00" />`)
}

// An answer under every cap must stay clean: the marker only means a subset
// when it appears.
func TestModelOutputOmitsGraphTruncationMarkerWhenComplete(t *testing.T) {
	registry := newSourceRegistry()
	output := registry.ModelOutput(&types.ToolResult{
		Success: true,
		Data: graphQueryResultData([]map[string]interface{}{
			{"source": "Kubernetes", "type": "orchestrates", "target": "Docker"},
		}, nil),
	})

	require.Contains(t, output, `<relation source="Kubernetes" type="orchestrates" target="Docker" />`)
	require.NotContains(t, output, "graph_truncated")
}
