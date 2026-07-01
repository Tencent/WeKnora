package agent

import (
	"encoding/json"

	"github.com/Tencent/WeKnora/internal/types"
)

func collectKnowledgeRefsFromStep(step types.AgentStep) []*types.SearchResult {
	refs := make([]*types.SearchResult, 0)
	for _, tc := range step.ToolCalls {
		if tc.Result == nil || len(tc.Result.Data) == 0 {
			continue
		}
		refs = append(refs, collectKnowledgeRefsFromToolData(tc.Result.Data)...)
	}
	return refs
}

func collectKnowledgeRefsFromToolData(data map[string]interface{}) []*types.SearchResult {
	refs := make([]*types.SearchResult, 0)
	for _, key := range []string{"results", "chunk_results"} {
		refs = append(refs, searchResultsFromStructuredValue(data[key])...)
	}
	return refs
}

func searchResultsFromStructuredValue(value interface{}) []*types.SearchResult {
	if value == nil {
		return nil
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return nil
	}
	var rows []map[string]interface{}
	if err := json.Unmarshal(raw, &rows); err != nil {
		return nil
	}

	refs := make([]*types.SearchResult, 0, len(rows))
	for _, row := range rows {
		ref := searchResultFromStructuredRow(row)
		if ref != nil {
			refs = append(refs, ref)
		}
	}
	return refs
}

func searchResultFromStructuredRow(row map[string]interface{}) *types.SearchResult {
	id := stringFromAny(row["chunk_id"])
	chunkType := stringFromAny(row["chunk_type"])
	if id == "" {
		id = stringFromAny(row["faq_id"])
		if chunkType == "" && id != "" {
			chunkType = "faq"
		}
	}
	if id == "" {
		return nil
	}
	if chunkType == "" {
		chunkType = "text"
	}
	chunkIndex := intFromAny(row["chunk_index"])
	if chunkIndex == 0 {
		chunkIndex = intFromAny(row["index"])
	}
	return &types.SearchResult{
		ID:                id,
		Content:           stringFromAny(row["content"]),
		KnowledgeID:       stringFromAny(row["knowledge_id"]),
		KnowledgeTitle:    stringFromAny(row["knowledge_title"]),
		KnowledgeBaseID:   stringFromAny(row["knowledge_base_id"]),
		KnowledgeFilename: stringFromAny(row["knowledge_filename"]),
		ChunkIndex:        chunkIndex,
		ChunkType:         chunkType,
		Score:             floatFromAny(row["score"]),
	}
}

func mergeKnowledgeRefs(existing, next []*types.SearchResult) []*types.SearchResult {
	if len(next) == 0 {
		return existing
	}
	seen := make(map[string]bool, len(existing)+len(next))
	merged := make([]*types.SearchResult, 0, len(existing)+len(next))
	for _, ref := range existing {
		if ref == nil || ref.ID == "" || seen[ref.ID] {
			continue
		}
		seen[ref.ID] = true
		merged = append(merged, ref)
	}
	for _, ref := range next {
		if ref == nil || ref.ID == "" || seen[ref.ID] {
			continue
		}
		seen[ref.ID] = true
		merged = append(merged, ref)
	}
	return merged
}

func stringFromAny(value interface{}) string {
	switch v := value.(type) {
	case string:
		return v
	default:
		return ""
	}
}

func intFromAny(value interface{}) int {
	switch v := value.(type) {
	case int:
		return v
	case int64:
		return int(v)
	case float64:
		return int(v)
	case json.Number:
		i, _ := v.Int64()
		return int(i)
	default:
		return 0
	}
}

func floatFromAny(value interface{}) float64 {
	switch v := value.(type) {
	case float64:
		return v
	case float32:
		return float64(v)
	case int:
		return float64(v)
	case int64:
		return float64(v)
	case json.Number:
		f, _ := v.Float64()
		return f
	default:
		return 0
	}
}
