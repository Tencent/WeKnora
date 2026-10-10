package interfaces

import (
	"context"

	"github.com/Tencent/WeKnora/internal/types"
)

// GeneratedQuestionAligner makes the question-generation index and chunk
// metadata match the knowledge base switch. It is optional on
// KnowledgeBaseService so test doubles that do not manage an index stay
// unchanged. Calling it when the switch is already off still disables
// historical question rows; the true→false edge is not the only path.
type GeneratedQuestionAligner interface {
	AlignGeneratedQuestions(ctx context.Context, kbID string) (*types.GeneratedQuestionAlignResult, error)
}
