package service

import (
	"context"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
)

// GetGeneratedQuestions returns all AI-generated questions for a knowledge item,
// aggregated across all of its text chunks.
func (s *knowledgeService) GetGeneratedQuestions(ctx context.Context, knowledgeID string) ([]*types.KnowledgeGeneratedQuestion, error) {
	chunks, err := s.chunkService.ListChunksByKnowledgeID(ctx, knowledgeID)
	if err != nil {
		logger.Errorf(ctx, "GetGeneratedQuestions: failed to list chunks for knowledge %s: %v", knowledgeID, err)
		return nil, err
	}

	results := aggregateGeneratedQuestions(chunks)
	logger.Infof(ctx, "GetGeneratedQuestions: knowledge %s returned %d questions from %d chunks",
		knowledgeID, len(results), len(chunks))
	return results, nil
}

// aggregateGeneratedQuestions extracts all AI-generated questions from a slice of
// chunks, skipping non-text chunks and empty questions. Exported as a package-level
// helper so tests can exercise the aggregation logic without mocking the full service.
func aggregateGeneratedQuestions(chunks []*types.Chunk) []*types.KnowledgeGeneratedQuestion {
	var results []*types.KnowledgeGeneratedQuestion
	for _, chunk := range chunks {
		if chunk.ChunkType != types.ChunkTypeText {
			continue
		}
		meta, err := chunk.DocumentMetadata()
		if err != nil || meta == nil || len(meta.GeneratedQuestions) == 0 {
			continue
		}
		for _, gq := range meta.GeneratedQuestions {
			if gq.Question == "" {
				continue
			}
			results = append(results, &types.KnowledgeGeneratedQuestion{
				ChunkID:    chunk.ID,
				QuestionID: gq.ID,
				Question:   gq.Question,
			})
		}
	}
	return results
}
