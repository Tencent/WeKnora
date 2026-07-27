package service

import (
	"context"
	"encoding/json"
	"sort"

	"github.com/Tencent/WeKnora/internal/types"
)

type collectionStoredValue struct {
	value    any
	inactive bool
	exists   bool
	valid    bool
}

func (s *agentCollectionService) reconcile(
	ctx context.Context,
	input types.PrepareCollectionInput,
	profile *types.AgentCollectionProfile,
) (*types.PreparedCollection, error) {
	fields := append([]types.AgentCollectionField(nil), input.Config.CollectionFields...)
	sort.SliceStable(fields, func(i, j int) bool { return fields[i].Order < fields[j].Order })
	active := types.JSONMap{}
	changes := make([]types.AgentCollectionValueChange, 0)
	known := make(map[string]struct{}, len(fields))

	for _, field := range fields {
		known[field.Key] = struct{}{}
		stored := collectionStoredField(profile, field)
		visible := field.Enabled && len(types.VisibleCollectionFields([]types.AgentCollectionField{field}, active)) == 1
		changes = append(changes, collectionReconciliationChanges(field, stored, visible)...)
		if visible && stored.valid {
			active[field.Key] = map[string]any{"value": stored.value}
		}
	}
	changes = append(changes, unknownCollectionChanges(profile, known)...)
	visible := types.VisibleCollectionFields(fields, active)
	prepared := collectionProgress(profile, visible, active, input.Config.CollectionCollectOptionalDuringIntake)
	updated, err := s.repo.ApplyChanges(ctx, collectionApplyInput(
		input, changes, prepared.Profile.RequiredTotal, prepared.Profile.CompletedRequired,
	))
	if err != nil {
		return nil, err
	}
	prepared.Profile = updated
	return prepared, nil
}

func collectionStoredField(
	profile *types.AgentCollectionProfile,
	field types.AgentCollectionField,
) collectionStoredValue {
	raw, exists := profile.Values[field.Key]
	inactive := false
	if !exists {
		raw, exists = profile.InactiveValues[field.Key]
		inactive = exists
	}
	if !exists {
		return collectionStoredValue{}
	}
	value := collectionStoredRawValue(raw)
	return collectionStoredValue{
		value: value, inactive: inactive, exists: true,
		valid: types.ValidateCollectionValue(field, value) == nil,
	}
}

func collectionStoredRawValue(raw any) any {
	if entry, ok := raw.(map[string]any); ok {
		return entry["value"]
	}
	data, _ := json.Marshal(raw)
	entry := map[string]any{}
	if json.Unmarshal(data, &entry) == nil {
		if value, exists := entry["value"]; exists {
			return value
		}
	}
	return raw
}

func collectionReconciliationChanges(
	field types.AgentCollectionField,
	stored collectionStoredValue,
	visible bool,
) []types.AgentCollectionValueChange {
	if !stored.exists {
		return nil
	}
	change := types.AgentCollectionValueChange{
		FieldKey: field.Key, Value: stored.value, Source: types.CollectionSourceSchemaMigration,
	}
	if !stored.valid {
		change.Remove = true
		return []types.AgentCollectionValueChange{change}
	}
	wantInactive := !visible
	if stored.inactive == wantInactive {
		return nil
	}
	change.Inactive = wantInactive
	return []types.AgentCollectionValueChange{change}
}

func unknownCollectionChanges(
	profile *types.AgentCollectionProfile,
	known map[string]struct{},
) []types.AgentCollectionValueChange {
	changes := make([]types.AgentCollectionValueChange, 0)
	for key, raw := range profile.Values {
		if _, exists := known[key]; exists {
			continue
		}
		changes = append(changes, types.AgentCollectionValueChange{
			FieldKey: key, Value: collectionStoredRawValue(raw), Inactive: true,
			Source: types.CollectionSourceSchemaMigration,
		})
	}
	return changes
}

func collectionProgress(
	profile *types.AgentCollectionProfile,
	visible []types.AgentCollectionField,
	active types.JSONMap,
	collectOptional bool,
) *types.PreparedCollection {
	prepared := &types.PreparedCollection{Profile: profile, VisibleFields: visible}
	requiredTotal, completedRequired := 0, 0
	for _, field := range visible {
		_, hasValue := active[field.Key]
		if field.Required {
			requiredTotal++
			if hasValue {
				completedRequired++
			}
		}
		if !field.Required && !collectOptional {
			continue
		}
		if hasValue {
			prepared.CompletedCount++
		} else {
			prepared.MissingFields = append(prepared.MissingFields, field)
		}
	}
	prepared.RemainingCount = len(prepared.MissingFields)
	profile.RequiredTotal = requiredTotal
	profile.CompletedRequired = completedRequired
	profile.IsComplete = completedRequired == requiredTotal
	return prepared
}

func collectionApplyInput(
	input types.PrepareCollectionInput,
	changes []types.AgentCollectionValueChange,
	requiredTotal, completedRequired int,
) types.ApplyCollectionChangesInput {
	return types.ApplyCollectionChangesInput{
		TenantID: input.TenantID, AgentTenantID: input.AgentTenantID,
		AgentID: input.AgentID, UserID: input.UserID,
		SchemaVersion: input.Config.CollectionSchemaVersion,
		RequiredTotal: requiredTotal, CompletedRequired: completedRequired,
		IsComplete: completedRequired == requiredTotal, Changes: changes,
	}
}
