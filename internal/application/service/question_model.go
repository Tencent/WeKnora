package service

import (
	"context"
	"strings"

	werrors "github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// ValidateQuestionGenerationModel checks an explicit question-model binding
// against the execution workspace. Empty bindings inherit the summary model.
func ValidateQuestionGenerationModel(
	ctx context.Context, models interfaces.ModelService, id string,
) (string, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return "", nil
	}
	if models == nil {
		return "", werrors.NewBadRequestError("question model service is unavailable")
	}
	model, err := models.GetModelByID(ctx, id)
	if err != nil || model == nil || model.Type != types.ModelTypeKnowledgeQA ||
		model.Status != types.ModelStatusActive {
		return "", werrors.NewBadRequestError("question model must be an active chat model in this workspace")
	}
	return id, nil
}

func resolveQuestionGenerationModel(
	ctx context.Context, models interfaces.ModelService,
	kb *types.KnowledgeBase, overrides *types.KnowledgeProcessOverrides,
) (string, error) {
	id := ResolveProcessConfig(kb, overrides).QuestionGenerationConfig.ModelID
	explicit := kb.QuestionGenerationConfig != nil && strings.TrimSpace(kb.QuestionGenerationConfig.ModelID) != ""
	if overrides != nil && overrides.QuestionGenerationConfig != nil {
		explicit = explicit || strings.TrimSpace(overrides.QuestionGenerationConfig.ModelID) != ""
	}
	if !explicit {
		return id, nil
	}
	return ValidateQuestionGenerationModel(ctx, models, id)
}
