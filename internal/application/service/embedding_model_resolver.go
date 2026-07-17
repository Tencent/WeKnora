package service

import (
	"context"
	"errors"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/models/embedding"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

func getEmbeddingModelForKB(
	ctx context.Context,
	modelService interfaces.ModelService,
	kb *types.KnowledgeBase,
) (embedding.Embedder, error) {
	if kb == nil {
		return nil, errors.New("knowledge base cannot be nil")
	}
	return getEmbeddingModelForTenant(ctx, modelService, kb.EmbeddingModelID, kb.TenantID)
}

func getEmbeddingModelForKnowledge(
	ctx context.Context,
	modelService interfaces.ModelService,
	knowledge *types.Knowledge,
) (embedding.Embedder, error) {
	if knowledge == nil {
		return nil, errors.New("knowledge cannot be nil")
	}
	return getEmbeddingModelForTenant(ctx, modelService, knowledge.EmbeddingModelID, knowledge.TenantID)
}

func getEmbeddingModelForTenant(
	ctx context.Context,
	modelService interfaces.ModelService,
	modelID string,
	modelTenantID uint64,
) (embedding.Embedder, error) {
	currentTenantID, ok := types.TenantIDFromContext(ctx)
	if ok && modelTenantID != 0 && modelTenantID != currentTenantID {
		logger.Infof(ctx,
			"Cross-tenant embedding model lookup, model ID: %s, owner tenant: %d, current tenant: %d",
			modelID, modelTenantID, currentTenantID)
		return modelService.GetEmbeddingModelForTenant(ctx, modelID, modelTenantID)
	}
	return modelService.GetEmbeddingModel(ctx, modelID)
}
