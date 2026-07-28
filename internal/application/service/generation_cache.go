package service

import (
	"context"
	"encoding/json"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

const (
	generationCacheVLMOCRPromptVersion     = "vlm-ocr-v1"
	generationCacheVLMCaptionPromptVersion = "vlm-caption-v1"
	generationCacheWikiMapPromptVersion    = "wiki-map-v1"
	generationCacheGraphPromptVersion      = "graph-extract-v1"
	generationCacheQuestionPromptVersion   = "question-generation-v1"
)

func getGenerationCache[T any](
	ctx context.Context,
	repo interfaces.GenerationCacheRepository,
	tenantID uint64,
	namespace, scopeID, modelID, inputHash, promptVersion, promptHash string,
) (T, bool) {
	var zero T
	if repo == nil {
		return zero, false
	}
	row, ok, err := repo.Get(ctx, tenantID, namespace, scopeID, modelID, inputHash, promptVersion, promptHash)
	if err != nil {
		logger.Warnf(ctx, "generation cache lookup failed namespace=%s: %v", namespace, err)
		return zero, false
	}
	if !ok || row == nil {
		return zero, false
	}
	var out T
	if err := json.Unmarshal(row.Output, &out); err != nil {
		logger.Warnf(ctx, "generation cache decode failed namespace=%s: %v", namespace, err)
		return zero, false
	}
	return out, true
}

func putGenerationCache[T any](
	ctx context.Context,
	repo interfaces.GenerationCacheRepository,
	tenantID uint64,
	namespace, scopeID, modelID, inputHash, promptVersion, promptHash string,
	value T,
) {
	if repo == nil {
		return
	}
	raw, err := json.Marshal(value)
	if err != nil {
		logger.Warnf(ctx, "generation cache encode failed namespace=%s: %v", namespace, err)
		return
	}
	if err := repo.Upsert(ctx, &types.GenerationCache{
		TenantID:      tenantID,
		Namespace:     namespace,
		ScopeID:       scopeID,
		ModelID:       modelID,
		InputHash:     inputHash,
		PromptVersion: promptVersion,
		PromptHash:    promptHash,
		Output:        types.JSON(raw),
	}); err != nil {
		logger.Warnf(ctx, "generation cache upsert failed namespace=%s: %v", namespace, err)
	}
}
