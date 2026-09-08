package opensearch

import "context"

// MoveKnowledgeIndices changes the KB binding while preserving vector IDs.
func (r *Repository) MoveKnowledgeIndices(
	ctx context.Context,
	sourceKB, targetKB, knowledgeID string,
	chunkIDs []string,
	_ int,
	_ string,
) error {
	if len(chunkIDs) == 0 {
		return nil
	}
	return r.updateByQueryScript(
		ctx,
		chunkIDs,
		"if (ctx._source.knowledge_base_id == params.source && ctx._source.knowledge_id "+
			"== params.knowledge) { ctx._source.knowledge_base_id = params.target; "+
			"ctx._source.tag_id = ''; } else { ctx.op = 'noop'; }",

		map[string]any{"source": sourceKB, "target": targetKB, "knowledge": knowledgeID},
	)
}
