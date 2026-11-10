package opensearch

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"

	osapi "github.com/opensearch-project/opensearch-go/v4/opensearchapi"
)

// MoveKnowledgeIndices changes every vector for the source document, including
// historical vectors whose chunks no longer exist, while preserving vector IDs.
func (r *Repository) MoveKnowledgeIndices(
	ctx context.Context,
	sourceKB, targetKB, knowledgeID string,
	_ []string,
	_ int,
	_ string,
) error {
	body, err := json.Marshal(map[string]any{
		"query": map[string]any{"bool": map[string]any{"filter": []any{
			map[string]any{"term": map[string]any{"knowledge_base_id": sourceKB}},
			map[string]any{"term": map[string]any{"knowledge_id": knowledgeID}},
		}}},
		"script": map[string]any{
			"lang":   "painless",
			"source": "ctx._source.knowledge_base_id = params.target; ctx._source.tag_id = '';",
			"params": map[string]any{"target": targetKB},
		},
	})
	if err != nil {
		return fmt.Errorf("opensearch: marshal move: %w", err)
	}
	refresh := true
	resp, err := r.client.UpdateByQuery(ctx, osapi.UpdateByQueryReq{
		Indices: []string{r.baseIndex + "_*"}, Body: bytes.NewReader(body),
		Params: osapi.UpdateByQueryParams{Refresh: &refresh},
	})
	if err != nil {
		return wrapTransport(err)
	}
	if resp == nil {
		return fmt.Errorf("opensearch: missing move response: %w", ErrTransport)
	}
	defer drainAndClose(resp.Inspect().Response.Body)
	return inspectByQueryResult(io.LimitReader(resp.Inspect().Response.Body, 16<<20), true)
}
