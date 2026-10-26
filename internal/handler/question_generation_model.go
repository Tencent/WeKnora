package handler

import (
	"context"

	"github.com/Tencent/WeKnora/internal/application/service"
	"github.com/Tencent/WeKnora/internal/types"
)

func (h *InitializationHandler) resolveQuestionGenerationModel(
	ctx context.Context, requested *string, previous *types.QuestionGenerationConfig,
) (string, error) {
	// Old clients omit this field when saving unrelated knowledge-base settings.
	if requested == nil {
		if previous != nil {
			return previous.ModelID, nil
		}
		return "", nil
	}
	return service.ValidateQuestionGenerationModel(ctx, h.modelService, *requested)
}
