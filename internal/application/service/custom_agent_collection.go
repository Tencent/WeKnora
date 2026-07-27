package service

import (
	"fmt"
	"reflect"

	"github.com/Tencent/WeKnora/internal/types"
)

func prepareNewAgentCollectionConfig(config *types.CustomAgentConfig) error {
	types.NormalizeAgentCollectionConfig(config)
	if err := types.ValidateAgentCollectionConfig(*config); err != nil {
		return err
	}
	if len(config.CollectionFields) == 0 {
		config.CollectionSchemaVersion = 0
		return nil
	}
	config.CollectionSchemaVersion = 1
	return nil
}

func prepareUpdatedAgentCollectionConfig(
	previous types.CustomAgentConfig,
	next *types.CustomAgentConfig,
) error {
	types.NormalizeAgentCollectionConfig(&previous)
	types.NormalizeAgentCollectionConfig(next)
	if err := validatePublishedCollectionFields(previous.CollectionFields, next.CollectionFields); err != nil {
		return err
	}
	if err := types.ValidateAgentCollectionConfig(*next); err != nil {
		return err
	}
	next.CollectionSchemaVersion = nextCollectionSchemaVersion(previous, *next)
	return nil
}

func validatePublishedCollectionFields(
	previous []types.AgentCollectionField,
	next []types.AgentCollectionField,
) error {
	nextByKey := collectionFieldsByKey(next)
	for _, oldField := range previous {
		newField, exists := nextByKey[oldField.Key]
		if !exists {
			continue
		}
		if oldField.Type != newField.Type {
			return fmt.Errorf("published collection field %q type cannot be changed", oldField.Key)
		}
	}
	return nil
}

func collectionFieldsByKey(fields []types.AgentCollectionField) map[string]types.AgentCollectionField {
	result := make(map[string]types.AgentCollectionField, len(fields))
	for _, field := range fields {
		result[field.Key] = field
	}
	return result
}

func nextCollectionSchemaVersion(previous, next types.CustomAgentConfig) int64 {
	if reflect.DeepEqual(previous.CollectionFields, next.CollectionFields) {
		return previous.CollectionSchemaVersion
	}
	if previous.CollectionSchemaVersion < 1 {
		return 1
	}
	return previous.CollectionSchemaVersion + 1
}
