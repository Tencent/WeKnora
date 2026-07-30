package tools

import (
	"fmt"

	"github.com/Tencent/WeKnora/internal/types"
)

// ExtractFeedbackReferences returns only the final KB chunks exposed by a
// successful retrieval tool result. Raw search candidates never enter this
// metadata, and web tools do not attach it.
func ExtractFeedbackReferences(
	toolName string,
	raw []types.AgentFeedbackReference,
) (types.References, []types.ChunkFeedbackScope) {
	if toolName != ToolKnowledgeSearch && toolName != ToolGrepChunks {
		return nil, nil
	}
	if len(raw) == 0 {
		return nil, nil
	}
	references := make(types.References, 0, len(raw))
	scopes := make([]types.ChunkFeedbackScope, 0, len(raw))
	seen := make(map[string]struct{}, len(raw))
	for _, item := range raw {
		if item.TenantID == 0 || item.Result == nil || item.Result.ID == "" ||
			item.Result.KnowledgeBaseID == "" ||
			item.Result.ChunkType == string(types.ChunkTypeWebSearch) ||
			item.Result.KnowledgeSource == "web_search" ||
			item.Result.MatchType == types.MatchTypeHistory {
			continue
		}
		key := fmt.Sprintf("%d\x00%s\x00%s", item.TenantID, item.Result.KnowledgeBaseID, item.Result.ID)
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		resultCopy := *item.Result
		references = append(references, &resultCopy)
		scopes = append(scopes, types.ChunkFeedbackScope{
			TenantID:        item.TenantID,
			KnowledgeBaseID: item.Result.KnowledgeBaseID,
			ChunkID:         item.Result.ID,
		})
	}
	return references, scopes
}
