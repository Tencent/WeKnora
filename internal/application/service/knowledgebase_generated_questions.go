package service

import (
	"context"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
)

// markSkipGeneratedQuestions sets SkipGeneratedQuestions from the live switch
// so rerank passages omit historical questions. A lookup failure leaves the
// flag unset.
func (s *knowledgeBaseService) markSkipGeneratedQuestions(ctx context.Context, results []*types.SearchResult) {
	ids := types.SearchResultKnowledgeBaseIDs(results)
	if len(ids) == 0 {
		return
	}
	kbs, err := s.repo.GetKnowledgeBaseByIDs(ctx, ids)
	if err != nil {
		logger.Warnf(ctx, "Failed to read question generation config for rerank: %v", err)
		return
	}
	types.ApplySkipGeneratedQuestions(results, types.QuestionGenerationOffIDs(kbs))
}
