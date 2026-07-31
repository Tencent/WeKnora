package service

import (
	"strings"

	"github.com/Tencent/WeKnora/internal/types"
)

// buildKnowledgeIndexContent adds only the document title to searchable text.
// Custom metadata stays document-scoped: it is supplied once to the answer
// model and summary model, rather than repeated in every chunk embedding.
func buildKnowledgeIndexContent(knowledge *types.Knowledge, content string) string {
	if knowledge == nil {
		return content
	}
	title := strings.TrimSpace(knowledge.Title)
	if title == "" {
		return content
	}
	return title + "\n" + content
}

func applyChunkGenerationIndexInfo(info *types.IndexInfo, chunk *types.Chunk) *types.IndexInfo {
	if info == nil || chunk == nil || chunk.GenerationID == "" {
		return info
	}
	info.GenerationID = chunk.GenerationID
	info.VisibilityKey = chunk.KnowledgeID + ":" + chunk.GenerationID
	return info
}
