package service

import (
	"context"
	"fmt"
	"strings"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// WikiRepairModelID returns the configured repair LLM for a KB, or "" if unset.
func WikiRepairModelID(kb *types.KnowledgeBase) string {
	if kb == nil || kb.WikiConfig == nil {
		return ""
	}
	return strings.TrimSpace(kb.WikiConfig.RepairModelID)
}

// ResolveWikiRepairModelID loads and validates the KB-scoped model used by the
// built-in wiki fixer agent. The builtin agent itself has no model_id; repair
// always runs against this KB setting.
func ResolveWikiRepairModelID(
	ctx context.Context,
	kbService interfaces.KnowledgeBaseService,
	modelService interfaces.ModelService,
	kbID string,
) (string, error) {
	if strings.TrimSpace(kbID) == "" {
		return "", fmt.Errorf("knowledge base id is required for wiki repair")
	}
	kb, err := kbService.GetKnowledgeBaseByID(ctx, kbID)
	if err != nil {
		return "", fmt.Errorf("load knowledge base for wiki repair: %w", err)
	}
	modelID := WikiRepairModelID(kb)
	if modelID == "" {
		return "", fmt.Errorf("wiki repair model is not configured for this knowledge base")
	}
	model, err := modelService.GetModelByID(ctx, modelID)
	if err != nil || model == nil {
		return "", fmt.Errorf("configured wiki repair model %s is unavailable", modelID)
	}
	if model.Type != types.ModelTypeKnowledgeQA {
		return "", fmt.Errorf("configured wiki repair model %s is not a chat model", modelID)
	}
	return modelID, nil
}
