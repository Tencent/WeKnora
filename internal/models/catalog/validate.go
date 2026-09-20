package catalog

import (
	"fmt"

	"github.com/Tencent/WeKnora/internal/models/api"
	"github.com/Tencent/WeKnora/internal/types"
)

// ValidateRow checks that a stored model row resolves against the catalog.
//
// It is the one gate shared by every write path — the REST create/update
// handlers and the declarative builtin_models.yaml loader — so an unknown
// protocol, a bad compat key or an invalid thinking level is rejected where
// it is written instead of surfacing as a failed chat call much later.
//
// Only chat and VLM rows carry catalog parameters; everything else resolves
// trivially and returns nil.
func ValidateRow(modelName string, modelType types.ModelType, params *types.ModelParameters) error {
	if params == nil || (modelType != types.ModelTypeKnowledgeQA && modelType != types.ModelTypeVLLM) {
		return nil
	}
	if params.Spec != nil {
		for level := range params.Spec.ThinkingLevels {
			if _, ok := api.ParseReasoningEffort(level); !ok || level == "" {
				return fmt.Errorf("spec.thinking_levels: unknown level %q", level)
			}
		}
	}
	if _, err := Resolve(Ref{
		Provider:  params.Provider,
		Model:     modelName,
		BaseURL:   params.BaseURL,
		ModelType: modelType,
		Extra:     params.ExtraConfig,
		Override:  params.Spec,
	}); err != nil {
		return fmt.Errorf("model parameters: %w", err)
	}
	return nil
}
