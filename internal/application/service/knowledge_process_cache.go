package service

import (
	"context"
	"encoding/json"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
)

type summaryCachePayload struct {
	Summary string `json:"summary"`
}

type questionCachePayload struct {
	Questions []string `json:"questions"`
}

func (s *knowledgeService) getSummaryCache(ctx context.Context, tenantID uint64, cacheKey string) (string, bool) {
	if s.cacheRepo == nil {
		return "", false
	}
	row, err := s.cacheRepo.Get(ctx, tenantID, types.ProcessingCacheStageSummary, cacheKey)
	if err != nil {
		logger.Warnf(ctx, "summary cache lookup failed: %v", err)
		return "", false
	}
	if row == nil {
		return "", false
	}
	var payload summaryCachePayload
	if err := json.Unmarshal(row.Payload, &payload); err != nil {
		logger.Warnf(ctx, "summary cache payload invalid: %v", err)
		return "", false
	}
	return payload.Summary, true
}

func (s *knowledgeService) putSummaryCache(ctx context.Context, tenantID uint64, cacheKey, summary, modelID string) {
	if s.cacheRepo == nil {
		return
	}
	payloadBytes, err := json.Marshal(summaryCachePayload{Summary: summary})
	if err != nil {
		logger.Warnf(ctx, "summary cache marshal failed: %v", err)
		return
	}
	metaBytes, _ := json.Marshal(map[string]string{
		"model_id":       modelID,
		"prompt_version": summaryPromptVersion,
	})
	if err := s.cacheRepo.Upsert(ctx, &types.ProcessingCache{
		TenantID: tenantID,
		Stage:    types.ProcessingCacheStageSummary,
		CacheKey: cacheKey,
		Payload:  types.JSON(payloadBytes),
		Metadata: types.JSON(metaBytes),
	}); err != nil {
		logger.Warnf(ctx, "summary cache write failed: %v", err)
	}
}

func (s *knowledgeService) getQuestionCache(ctx context.Context, tenantID uint64, cacheKey string) ([]string, bool) {
	if s.cacheRepo == nil {
		return nil, false
	}
	row, err := s.cacheRepo.Get(ctx, tenantID, types.ProcessingCacheStageQuestion, cacheKey)
	if err != nil {
		logger.Warnf(ctx, "question cache lookup failed: %v", err)
		return nil, false
	}
	if row == nil {
		return nil, false
	}
	var payload questionCachePayload
	if err := json.Unmarshal(row.Payload, &payload); err != nil {
		logger.Warnf(ctx, "question cache payload invalid: %v", err)
		return nil, false
	}
	if len(payload.Questions) == 0 {
		return nil, false
	}
	return append([]string(nil), payload.Questions...), true
}

func (s *knowledgeService) putQuestionCache(ctx context.Context, tenantID uint64, cacheKey, modelID string, questions []string) {
	if s.cacheRepo == nil || len(questions) == 0 {
		return
	}
	payloadBytes, err := json.Marshal(questionCachePayload{Questions: questions})
	if err != nil {
		logger.Warnf(ctx, "question cache marshal failed: %v", err)
		return
	}
	metaBytes, _ := json.Marshal(map[string]string{
		"model_id":       modelID,
		"prompt_version": questionPromptVersion,
	})
	if err := s.cacheRepo.Upsert(ctx, &types.ProcessingCache{
		TenantID: tenantID,
		Stage:    types.ProcessingCacheStageQuestion,
		CacheKey: cacheKey,
		Payload:  types.JSON(payloadBytes),
		Metadata: types.JSON(metaBytes),
	}); err != nil {
		logger.Warnf(ctx, "question cache write failed: %v", err)
	}
}
