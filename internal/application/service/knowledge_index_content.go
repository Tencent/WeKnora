package service

import (
	"strings"

	"github.com/Tencent/WeKnora/internal/types"
)

// knowledgeWithIndexTitle returns knowledge for embedding/LLM input. When title
// differs from the stored value, a shallow copy is used so the row is not
// rewritten. Callers must not persist the copy.
func knowledgeWithIndexTitle(knowledge *types.Knowledge, title string) *types.Knowledge {
	if knowledge == nil {
		return nil
	}
	if title == knowledge.Title {
		return knowledge
	}
	cp := *knowledge
	cp.Title = title
	return &cp
}

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
