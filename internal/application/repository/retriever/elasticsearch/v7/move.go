package v7

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
)

func (e *elasticsearchRepository) MoveKnowledgeIndices(
	ctx context.Context,
	sourceKB, targetKB, knowledgeID string,
	_ []string,
	_ int,
	_ string,
) error {
	body, err := json.Marshal(map[string]any{
		"query": map[string]any{"bool": map[string]any{"filter": []any{
			map[string]any{"term": map[string]any{e.idField("knowledge_base_id"): sourceKB}},
			map[string]any{"term": map[string]any{e.idField("knowledge_id"): knowledgeID}},
		}}},
		"script": map[string]any{
			"lang":   "painless",
			"source": "ctx._source.knowledge_base_id = params.target; ctx._source.tag_id = ''",
			"params": map[string]any{"target": targetKB},
		},
	})
	if err != nil {
		return err
	}
	resp, err := e.client.UpdateByQuery(
		[]string{e.index},
		e.client.UpdateByQuery.WithContext(ctx),
		e.client.UpdateByQuery.WithBody(bytes.NewReader(body)),
		e.client.UpdateByQuery.WithRefresh(true),
	)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.IsError() {
		return fmt.Errorf("move indices: %s", resp.String())
	}
	var result struct {
		Total     *int64            `json:"total"`
		Updated   *int64            `json:"updated"`
		TimedOut  bool              `json:"timed_out"`
		Conflicts int               `json:"version_conflicts"`
		Failures  []json.RawMessage `json:"failures"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return err
	}
	if result.Total == nil || result.Updated == nil || *result.Total < 0 || *result.Total != *result.Updated ||
		result.TimedOut || result.Conflicts != 0 || len(result.Failures) != 0 {
		return fmt.Errorf("move indices was incomplete")
	}
	return nil
}
