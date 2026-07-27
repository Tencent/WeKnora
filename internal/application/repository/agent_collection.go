package repository

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var (
	ErrAgentCollectionProfileNotFound = errors.New("agent collection profile not found")
	ErrAgentCollectionExportNotFound  = errors.New("agent collection export not found")
	ErrAgentCollectionConflict        = errors.New("agent collection profile update conflict")
)

type agentCollectionRepository struct {
	db *gorm.DB
}

func NewAgentCollectionRepository(db *gorm.DB) interfaces.AgentCollectionRepository {
	return &agentCollectionRepository{db: db}
}

func (r *agentCollectionRepository) GetProfile(
	ctx context.Context,
	tenantID uint64,
	agentID, userID string,
) (*types.AgentCollectionProfile, error) {
	var profile types.AgentCollectionProfile
	err := r.db.WithContext(ctx).
		Where("tenant_id = ? AND agent_id = ? AND user_id = ?", tenantID, agentID, userID).
		First(&profile).Error
	return collectionProfileResult(&profile, err)
}

func (r *agentCollectionRepository) GetProfileByID(
	ctx context.Context,
	profileID string,
) (*types.AgentCollectionProfile, error) {
	var profile types.AgentCollectionProfile
	err := r.db.WithContext(ctx).Where("id = ?", profileID).First(&profile).Error
	return collectionProfileResult(&profile, err)
}

func collectionProfileResult(
	profile *types.AgentCollectionProfile,
	err error,
) (*types.AgentCollectionProfile, error) {
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrAgentCollectionProfileNotFound
	}
	if err != nil {
		return nil, err
	}
	return profile, nil
}

func (r *agentCollectionRepository) ApplyChanges(
	ctx context.Context,
	input types.ApplyCollectionChangesInput,
) (*types.AgentCollectionProfile, error) {
	if input.TenantID == 0 || input.AgentID == "" || input.UserID == "" {
		return nil, fmt.Errorf("tenant, agent, and user are required")
	}
	var lastErr error
	for attempt := 0; attempt < 2; attempt++ {
		profile, err := r.applyChangesOnce(ctx, input)
		if err == nil {
			return profile, nil
		}
		lastErr = err
		if !collectionRetryable(err) {
			break
		}
	}
	return nil, lastErr
}

func (r *agentCollectionRepository) applyChangesOnce(
	ctx context.Context,
	input types.ApplyCollectionChangesInput,
) (*types.AgentCollectionProfile, error) {
	var result *types.AgentCollectionProfile
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		profile, err := loadOrCreateCollectionProfile(tx, input)
		if err != nil {
			return err
		}
		changed, history, err := applyCollectionValueChanges(profile, input)
		if err != nil {
			return err
		}
		metricsChanged := applyCollectionMetrics(profile, input)
		if !changed && !metricsChanged {
			result = profile
			return nil
		}
		if err := persistCollectionProfile(tx, profile); err != nil {
			return err
		}
		if len(history) > 0 {
			if err := tx.Create(&history).Error; err != nil {
				return err
			}
		}
		result = profile
		return nil
	})
	return result, err
}

func loadOrCreateCollectionProfile(
	tx *gorm.DB,
	input types.ApplyCollectionChangesInput,
) (*types.AgentCollectionProfile, error) {
	var profile types.AgentCollectionProfile
	err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
		Where("tenant_id = ? AND agent_id = ? AND user_id = ?", input.TenantID, input.AgentID, input.UserID).
		First(&profile).Error
	if err == nil {
		ensureCollectionMaps(&profile)
		return &profile, nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	}
	profile = types.AgentCollectionProfile{
		ID: uuid.NewString(), TenantID: input.TenantID, AgentTenantID: collectionAgentTenantID(input),
		AgentID: input.AgentID, UserID: input.UserID,
		Values: types.JSONMap{}, InactiveValues: types.JSONMap{},
	}
	if err := tx.Create(&profile).Error; err != nil {
		return nil, err
	}
	return &profile, nil
}

func collectionAgentTenantID(input types.ApplyCollectionChangesInput) uint64 {
	if input.AgentTenantID != 0 {
		return input.AgentTenantID
	}
	return input.TenantID
}

func ensureCollectionMaps(profile *types.AgentCollectionProfile) {
	if profile.Values == nil {
		profile.Values = types.JSONMap{}
	}
	if profile.InactiveValues == nil {
		profile.InactiveValues = types.JSONMap{}
	}
}

func applyCollectionValueChanges(
	profile *types.AgentCollectionProfile,
	input types.ApplyCollectionChangesInput,
) (bool, []types.AgentCollectionHistory, error) {
	changed := false
	history := make([]types.AgentCollectionHistory, 0, len(input.Changes))
	for _, change := range input.Changes {
		oldEntry, oldValue, oldInactive, exists := findCollectionValue(profile, change.FieldKey)
		if collectionMessageIsOlder(change, oldEntry) {
			continue
		}
		if change.Remove {
			if !exists {
				continue
			}
			delete(profile.Values, change.FieldKey)
			delete(profile.InactiveValues, change.FieldKey)
			changed = true
			history = append(history, newCollectionHistory(profile, input.SchemaVersion, change, oldValue, nil))
			continue
		}
		if exists && collectionJSONEqual(oldValue, change.Value) {
			if oldInactive != change.Inactive {
				moveCollectionEntry(profile, change.FieldKey, oldEntry, change.Inactive)
				changed = true
			}
			continue
		}
		entry := collectionEntryMap(change)
		moveCollectionEntry(profile, change.FieldKey, entry, change.Inactive)
		changed = true
		history = append(history, newCollectionHistory(profile, input.SchemaVersion, change, oldValue, change.Value))
	}
	return changed, history, nil
}

func findCollectionValue(
	profile *types.AgentCollectionProfile,
	fieldKey string,
) (map[string]any, any, bool, bool) {
	if raw, exists := profile.Values[fieldKey]; exists {
		entry := collectionEntry(raw)
		return entry, entry["value"], false, true
	}
	if raw, exists := profile.InactiveValues[fieldKey]; exists {
		entry := collectionEntry(raw)
		return entry, entry["value"], true, true
	}
	return nil, nil, false, false
}

func collectionEntry(raw any) map[string]any {
	if entry, ok := raw.(map[string]any); ok {
		return entry
	}
	data, _ := json.Marshal(raw)
	entry := map[string]any{}
	_ = json.Unmarshal(data, &entry)
	return entry
}

func collectionEntryMap(change types.AgentCollectionValueChange) map[string]any {
	entry := map[string]any{
		"value": change.Value, "updated_at": time.Now().UTC(), "source": change.Source,
	}
	if change.SourceMessageID != "" {
		entry["source_message_id"] = change.SourceMessageID
	}
	if change.SourceMessageAt != nil {
		entry["source_message_at"] = change.SourceMessageAt.UTC()
	}
	return entry
}

func moveCollectionEntry(
	profile *types.AgentCollectionProfile,
	fieldKey string,
	entry map[string]any,
	inactive bool,
) {
	delete(profile.Values, fieldKey)
	delete(profile.InactiveValues, fieldKey)
	if inactive {
		profile.InactiveValues[fieldKey] = entry
	} else {
		profile.Values[fieldKey] = entry
	}
}

func collectionMessageIsOlder(
	change types.AgentCollectionValueChange,
	entry map[string]any,
) bool {
	if change.Source != types.CollectionSourceMessageExtraction || change.SourceMessageAt == nil || entry == nil {
		return false
	}
	stored, ok := collectionEntryTime(entry["source_message_at"])
	return ok && change.SourceMessageAt.Before(stored)
}

func collectionEntryTime(value any) (time.Time, bool) {
	switch typed := value.(type) {
	case time.Time:
		return typed, true
	case string:
		parsed, err := time.Parse(time.RFC3339Nano, typed)
		return parsed, err == nil
	default:
		return time.Time{}, false
	}
}
