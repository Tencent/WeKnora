package agent

import (
	"strings"

	"github.com/Tencent/WeKnora/internal/types"
)

// collectAgentKnowledgeReferences records the chunks that successful retrieval
// tools exposed to the model. ToolResult owns the runtime-only transport so
// references are not copied into persisted AgentSteps or client tool payloads.
func collectAgentKnowledgeReferences(state *types.AgentState, toolCalls []types.ToolCall) {
	if state == nil {
		return
	}

	byID := make(map[string]*types.SearchResult, len(state.KnowledgeRefs))
	for _, reference := range state.KnowledgeRefs {
		if reference == nil {
			continue
		}
		id := strings.TrimSpace(reference.ID)
		if id != "" {
			if existing, ok := byID[id]; ok {
				mergeKnowledgeReferenceSubChunks(existing, reference)
				continue
			}
			byID[id] = reference
		}
	}

	for _, toolCall := range toolCalls {
		if toolCall.Result == nil || !toolCall.Result.Success {
			continue
		}
		for _, reference := range toolCall.Result.KnowledgeReferences {
			if !types.IsFeedbackTrackableSearchResult(reference) {
				continue
			}
			id := strings.TrimSpace(reference.ID)
			if id == "" {
				continue
			}
			if existing, ok := byID[id]; ok {
				mergeKnowledgeReferenceSubChunks(existing, reference)
				continue
			}
			byID[id] = reference
			state.KnowledgeRefs = append(state.KnowledgeRefs, reference)
		}
	}
}

func mergeKnowledgeReferenceSubChunks(dst, src *types.SearchResult) {
	if dst == nil || src == nil || len(src.SubChunkID) == 0 {
		return
	}
	seen := make(map[string]struct{}, len(dst.SubChunkID)+len(src.SubChunkID))
	merged := make([]string, 0, len(dst.SubChunkID)+len(src.SubChunkID))
	add := func(id string) {
		id = strings.TrimSpace(id)
		if id == "" {
			return
		}
		if _, ok := seen[id]; ok {
			return
		}
		seen[id] = struct{}{}
		merged = append(merged, id)
	}
	for _, id := range dst.SubChunkID {
		add(id)
	}
	for _, id := range src.SubChunkID {
		add(id)
	}
	dst.SubChunkID = merged
}
