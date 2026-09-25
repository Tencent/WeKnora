package langfuse

import (
	"github.com/Tencent/WeKnora/internal/types"
)

// SummarizeMemoryRecallOutput builds Langfuse output for a memory.recall span.
func SummarizeMemoryRecallOutput(meta map[string]interface{}, items []*types.MemoryItem) map[string]interface{} {
	out := make(map[string]interface{}, len(meta)+3)
	for k, v := range meta {
		out[k] = v
	}
	recalled, truncated := summarizeMemoryItems(items, defaultHitPreviewLimit)
	out["recalled_items"] = recalled
	if truncated > 0 {
		out["recalled_items_truncated"] = truncated
	}
	return out
}

func summarizeMemoryItems(items []*types.MemoryItem, limit int) ([]map[string]interface{}, int) {
	if limit <= 0 {
		limit = defaultHitPreviewLimit
	}
	if len(items) == 0 {
		return nil, 0
	}
	n := len(items)
	truncated := 0
	if n > limit {
		truncated = n - limit
		n = limit
	}
	out := make([]map[string]interface{}, 0, n)
	for i := 0; i < n; i++ {
		item := items[i]
		if item == nil {
			continue
		}
		row := map[string]interface{}{
			"id":         item.ID,
			"kind":       item.Kind,
			"topic":      TruncateRunes(item.Topic, 80),
			"importance": item.Importance,
			"preview":    TruncateRunes(item.Content, 160),
		}
		out = append(out, row)
	}
	return out, truncated
}

// SummarizeRetrievalContextOutput builds Langfuse output for retrieval conditioning.
func SummarizeRetrievalContextOutput(
	background string,
	interests, documents []string,
	items []*types.MemoryItem,
) map[string]interface{} {
	conditioned, _ := summarizeMemoryItems(items, defaultHitPreviewLimit)
	out := map[string]interface{}{
		"background":        TruncateRunes(background, 240),
		"interest_count":    len(interests),
		"document_count":    len(documents),
		"interests":         interests,
		"documents":         documents,
		"conditioned_items": conditioned,
	}
	if out["background"] == "" {
		delete(out, "background")
	}
	return out
}
