package service

import (
	"github.com/Tencent/WeKnora/internal/evaluation/metricregistry"
	"github.com/Tencent/WeKnora/internal/types"
)

// EvaluationMetricDefinitions returns detached registry metadata in stable
// key/version order.
func (e *EvaluationService) EvaluationMetricDefinitions() []types.EvaluationMetricDefinition {
	registry := e.metricRegistry
	if registry == nil {
		var err error
		registry, err = metricregistry.NewDefaultRegistry()
		if err != nil {
			return nil
		}
	}
	definitions := registry.Definitions()
	items := make([]types.EvaluationMetricDefinition, 0, len(definitions))
	for _, definition := range definitions {
		items = append(items, types.EvaluationMetricDefinition{
			Key: definition.Key, Version: definition.Version, Kind: string(definition.Kind),
			Description: definition.Description, DefaultConfig: definition.DefaultConfig,
			ConfigSchema: definition.ConfigSchema,
		})
	}
	return items
}
