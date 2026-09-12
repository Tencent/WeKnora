package service

import (
	"context"
	"errors"
	"strings"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// ErrEvaluationSourceKnowledgeBaseNotFound hides an out-of-scope or missing
// source knowledge base from a knowledge-base-restricted API key.
var ErrEvaluationSourceKnowledgeBaseNotFound = errors.New("evaluation source knowledge base not found")

func authorizeEvaluationSourceKnowledgeBaseForAPIKey(ctx context.Context, sourceKnowledgeBaseID string) error {
	scope, ok := types.TenantAPIKeyScopeFromContext(ctx)
	if !ok || !scope.IsKnowledgeBaseRestricted() {
		return nil
	}
	if !scope.AllowsKnowledgeBase(strings.TrimSpace(sourceKnowledgeBaseID)) {
		return ErrEvaluationSourceKnowledgeBaseNotFound
	}
	return nil
}

// AuthorizeEvaluationTaskForAPIKey applies the API key knowledge-base
// allow-list to the source knowledge base frozen in the experiment manifest.
// Incomplete provenance and out-of-scope sources use the task not-found error.
func AuthorizeEvaluationTaskForAPIKey(ctx context.Context, entity *types.EvaluationTaskEntity) error {
	scope, ok := types.TenantAPIKeyScopeFromContext(ctx)
	if !ok || !scope.IsKnowledgeBaseRestricted() {
		return nil
	}
	if entity == nil {
		return interfaces.ErrEvaluationTaskNotFound
	}
	experiment, complete, err := decodeEvaluationExperiment(entity)
	if err != nil || !complete || experiment == nil || experiment.SourceKnowledgeBaseID == nil ||
		!scope.AllowsKnowledgeBase(*experiment.SourceKnowledgeBaseID) {
		return interfaces.ErrEvaluationTaskNotFound
	}
	return nil
}
