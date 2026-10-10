package service

import (
	"context"
	"errors"

	"github.com/Tencent/WeKnora/internal/application/service/retriever"
	apperrors "github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
)

type generatedQuestionMetadataStore interface {
	SetGeneratedQuestionsInactive(ctx context.Context, kbID string, inactive bool) (int64, error)
}

// AlignGeneratedQuestions makes generated-question index rows and chunk
// metadata follow the current switch. Disable sets is_enabled=false and marks
// the questions inactive. Enable flips both back, so the existing vectors and
// question text are used again without another LLM run.
func (s *knowledgeBaseService) AlignGeneratedQuestions(
	ctx context.Context, kbID string,
) (*types.GeneratedQuestionAlignResult, error) {
	if kbID == "" {
		return nil, apperrors.NewBadRequestError("knowledge base ID cannot be empty")
	}
	kb, err := s.repo.GetKnowledgeBaseByID(ctx, kbID)
	if err != nil {
		return nil, err
	}
	if kb == nil {
		return nil, apperrors.NewNotFoundError("knowledge base not found")
	}
	if kb.Type == types.KnowledgeBaseTypeFAQ {
		return &types.GeneratedQuestionAlignResult{Enabled: false, IndexUpdated: true}, nil
	}

	active := types.QuestionGenerationActive(kb)
	out := &types.GeneratedQuestionAlignResult{Enabled: active}
	if store, ok := s.chunkRepo.(generatedQuestionMetadataStore); ok {
		n, err := store.SetGeneratedQuestionsInactive(ctx, kb.ID, !active)
		if err != nil {
			return nil, err
		}
		out.MetadataChunks = n
	}

	engine, err := retriever.CreateRetrieveEngineForKB(
		ctx, s.retrieveEngine, s.ownership, kb.TenantID, kb.VectorStoreID,
	)
	if err != nil {
		return out, err
	}
	n, err := engine.SetGeneratedQuestionEnabled(ctx, kb.ID, active)
	if errors.Is(err, retriever.ErrGeneratedQuestionIndexUnsupported) {
		logger.Warnf(ctx, "Retrieve engine cannot disable generated-question rows for knowledge base %s", kb.ID)
		return out, nil
	}
	if err != nil {
		return nil, err
	}
	out.IndexRows = n
	out.IndexUpdated = true
	return out, nil
}
