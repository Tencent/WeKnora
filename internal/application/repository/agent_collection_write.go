package repository

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

func collectionJSONEqual(left, right any) bool {
	leftJSON, leftErr := json.Marshal(left)
	rightJSON, rightErr := json.Marshal(right)
	return leftErr == nil && rightErr == nil && bytes.Equal(leftJSON, rightJSON)
}

func newCollectionHistory(
	profile *types.AgentCollectionProfile,
	schemaVersion int64,
	change types.AgentCollectionValueChange,
	oldValue, newValue any,
) types.AgentCollectionHistory {
	oldJSON, _ := json.Marshal(oldValue)
	newJSON, _ := json.Marshal(newValue)
	return types.AgentCollectionHistory{
		ID: uuid.NewString(), ProfileID: profile.ID, TenantID: profile.TenantID,
		AgentID: profile.AgentID, UserID: profile.UserID, FieldKey: change.FieldKey,
		SchemaVersion: schemaVersion, OldValue: oldJSON, NewValue: newJSON, Source: change.Source,
		Confidence: change.Confidence, SourceMessageID: change.SourceMessageID,
		SourceMessageAt: change.SourceMessageAt, ActorUserID: change.ActorUserID,
		ChangeReason: change.ChangeReason, CreatedAt: time.Now().UTC(),
	}
}

func applyCollectionMetrics(profile *types.AgentCollectionProfile, input types.ApplyCollectionChangesInput) bool {
	changed := profile.SchemaVersion != input.SchemaVersion || profile.RequiredTotal != input.RequiredTotal ||
		profile.CompletedRequired != input.CompletedRequired || profile.IsComplete != input.IsComplete
	profile.SchemaVersion = input.SchemaVersion
	profile.RequiredTotal = input.RequiredTotal
	profile.CompletedRequired = input.CompletedRequired
	profile.IsComplete = input.IsComplete
	return changed
}

func persistCollectionProfile(tx *gorm.DB, profile *types.AgentCollectionProfile) error {
	oldVersion := profile.LockVersion
	profile.LockVersion++
	profile.UpdatedAt = time.Now().UTC()
	result := tx.Model(&types.AgentCollectionProfile{}).
		Where("id = ? AND lock_version = ?", profile.ID, oldVersion).
		Updates(map[string]any{
			"schema_version": profile.SchemaVersion, "values": profile.Values,
			"inactive_values": profile.InactiveValues, "required_total": profile.RequiredTotal,
			"completed_required": profile.CompletedRequired, "is_complete": profile.IsComplete,
			"lock_version": profile.LockVersion, "updated_at": profile.UpdatedAt,
		})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return ErrAgentCollectionConflict
	}
	return nil
}

func collectionRetryable(err error) bool {
	if errors.Is(err, ErrAgentCollectionConflict) {
		return true
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "unique") || strings.Contains(message, "duplicate")
}
