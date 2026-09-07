package agent

import (
	"encoding/json"
	"strings"

	"github.com/Tencent/WeKnora/internal/types"
)

// appendKnowledgeRefsFromStep copies retrieval hits out of this round's tool
// results onto the agent state. Smart-reasoning turns never emit the RAG
// references event, so without this the assistant message is stored with an
// empty KnowledgeReferences list and the message-file proxy cannot prove a
// shared-KB image belonged to the turn (#3022).
func appendKnowledgeRefsFromStep(state *types.AgentState, step types.AgentStep) {
	if state == nil {
		return
	}
	seen := make(map[string]bool, len(state.KnowledgeRefs))
	for _, ref := range state.KnowledgeRefs {
		if key := knowledgeRefKey(ref); key != "" {
			seen[key] = true
		}
	}
	for _, call := range step.ToolCalls {
		if call.Result == nil {
			continue
		}
		for _, ref := range searchResultsFromToolData(call.Result.Data) {
			key := knowledgeRefKey(ref)
			if key == "" || seen[key] {
				continue
			}
			seen[key] = true
			state.KnowledgeRefs = append(state.KnowledgeRefs, ref)
		}
	}
}

func knowledgeRefKey(ref *types.SearchResult) string {
	if ref == nil {
		return ""
	}
	if ref.ID != "" {
		return "id:" + ref.ID
	}
	if ref.Content == "" && ref.ImageInfo == "" {
		return ""
	}
	return strings.Join([]string{ref.KnowledgeBaseID, ref.KnowledgeID, ref.Content, ref.ImageInfo}, "\x00")
}

func searchResultsFromToolData(data map[string]interface{}) []*types.SearchResult {
	if len(data) == 0 {
		return nil
	}
	parentKB := firstToolString(data, "knowledge_base_id", "knowledge_base")
	parentKnowledge := firstToolString(data, "knowledge_id")
	var out []*types.SearchResult
	for _, key := range []string{"results", "chunks", "chunk_results"} {
		for _, item := range asToolSlice(data[key]) {
			m := asToolMap(item)
			if m == nil {
				continue
			}
			ref := searchResultFromToolMap(m)
			if ref.KnowledgeBaseID == "" {
				ref.KnowledgeBaseID = parentKB
			}
			if ref.KnowledgeID == "" {
				ref.KnowledgeID = parentKnowledge
			}
			if ref.Content == "" && ref.ImageInfo == "" {
				continue
			}
			out = append(out, ref)
		}
	}
	return out
}

func searchResultFromToolMap(m map[string]interface{}) *types.SearchResult {
	ref := &types.SearchResult{
		ID:              firstToolString(m, "id", "chunk_id"),
		Content:         firstToolString(m, "content"),
		KnowledgeID:     firstToolString(m, "knowledge_id"),
		KnowledgeBaseID: firstToolString(m, "knowledge_base_id", "knowledge_base"),
		KnowledgeTitle:  firstToolString(m, "knowledge_title"),
	}
	if imageInfo := marshalToolImages(m["images"]); imageInfo != "" {
		ref.ImageInfo = imageInfo
	} else if raw := firstToolString(m, "image_info"); raw != "" {
		ref.ImageInfo = raw
	}
	return ref
}

func marshalToolImages(v interface{}) string {
	items := asToolSlice(v)
	if len(items) == 0 {
		return ""
	}
	infos := make([]types.ImageInfo, 0, len(items))
	for _, item := range items {
		m := asToolMap(item)
		if m == nil {
			continue
		}
		info := types.ImageInfo{
			URL:     firstToolString(m, "url"),
			Caption: firstToolString(m, "caption"),
			OCRText: firstToolString(m, "ocr_text"),
		}
		if info.URL == "" && info.Caption == "" && info.OCRText == "" {
			continue
		}
		infos = append(infos, info)
	}
	if len(infos) == 0 {
		return ""
	}
	raw, err := json.Marshal(infos)
	if err != nil {
		return ""
	}
	return string(raw)
}

func firstToolString(m map[string]interface{}, keys ...string) string {
	for _, key := range keys {
		s, _ := m[key].(string)
		if strings.TrimSpace(s) != "" {
			return strings.TrimSpace(s)
		}
	}
	return ""
}

func asToolMap(v interface{}) map[string]interface{} {
	if m, ok := v.(map[string]interface{}); ok {
		return m
	}
	return nil
}

func asToolSlice(v interface{}) []interface{} {
	switch val := v.(type) {
	case []interface{}:
		return val
	case []map[string]interface{}:
		out := make([]interface{}, len(val))
		for i, item := range val {
			out[i] = item
		}
		return out
	default:
		return nil
	}
}
