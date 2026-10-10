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

// questionIndexStatusOnly reports whether err is only the partial or
// unsupported alignment status, with no separate engine failure inside it.
func questionIndexStatusOnly(err error) bool {
	if err == nil {
		return true
	}
	if joined, ok := err.(interface{ Unwrap() []error }); ok {
		parts := joined.Unwrap()
		if len(parts) == 0 {
			return false
		}
		for _, part := range parts {
			if !questionIndexStatusOnly(part) {
				return false
			}
		}
		return true
	}
	return errors.Is(err, retriever.ErrGeneratedQuestionIndexPartial) ||
		errors.Is(err, retriever.ErrGeneratedQuestionIndexUnsupported)
}

// markSkipGeneratedQuestions sets SkipGeneratedQuestions from the live switch
// so rerank passages omit historical questions before metadata is aligned.
// A lookup failure leaves the flag unset.
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
	if err != nil {
		out.IndexRows = n
		out.IndexUpdated = false
		if questionIndexStatusOnly(err) && errors.Is(err, retriever.ErrGeneratedQuestionIndexPartial) {
			logger.Warnf(ctx, "Generated-question alignment was partial for knowledge base %s: %v", kb.ID, err)
			return out, apperrors.NewBadRequestError(
				"部分检索引擎不能按行停用或恢复预生成问题，问句向量仍会参与召回",
			).WithDetails("generated_question_index_partial")
		}
		if questionIndexStatusOnly(err) && errors.Is(err, retriever.ErrGeneratedQuestionIndexUnsupported) {
			logger.Warnf(ctx, "Retrieve engine cannot update generated-question rows for knowledge base %s", kb.ID)
			return out, apperrors.NewBadRequestError(
				"当前检索引擎不能按行停用或恢复预生成问题，问句向量仍会参与召回",
			).WithDetails("generated_question_index_unsupported")
		}
		return nil, err
	}
	out.IndexRows = n
	out.IndexUpdated = true
	return out, nil
}
