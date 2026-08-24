package repository

import (
	"context"
	"errors"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type messageSuggestionRepository struct {
	db *gorm.DB
}

func NewMessageSuggestionRepository(db *gorm.DB) interfaces.MessageSuggestionRepository {
	return &messageSuggestionRepository{db: db}
}

func (r *messageSuggestionRepository) GetByCacheKey(
	ctx context.Context,
	tenantID uint64,
	assistantMessageID string,
	placement string,
	configHash string,
	locale string,
) (*types.MessageSuggestionSet, error) {
	var set types.MessageSuggestionSet
	err := r.db.WithContext(ctx).
		Where(
			"tenant_id = ? AND assistant_message_id = ? AND placement = ? AND config_hash = ? AND locale = ?",
			tenantID, assistantMessageID, placement, configHash, locale,
		).
		First(&set).Error
	if err != nil {
		return nil, err
	}
	return &set, nil
}

func (r *messageSuggestionRepository) GetByID(
	ctx context.Context,
	tenantID uint64,
	sessionID string,
	id string,
) (*types.MessageSuggestionSet, error) {
	var set types.MessageSuggestionSet
	err := r.db.WithContext(ctx).
		Where("id = ? AND tenant_id = ? AND session_id = ?", id, tenantID, sessionID).
		First(&set).Error
	if err != nil {
		return nil, err
	}
	return &set, nil
}

func (r *messageSuggestionRepository) AcquireGeneration(
	ctx context.Context,
	candidate *types.MessageSuggestionSet,
	regenerate bool,
) (*types.MessageSuggestionSet, bool, error) {
	now := time.Now()
	leaseUntil := now.Add(3 * time.Minute)
	candidate.Status = types.SuggestionStatusGenerating
	candidate.LeaseUntil = &leaseUntil
	candidate.Questions = types.SuggestionItems{}

	result := r.db.WithContext(ctx).
		Clauses(clause.OnConflict{DoNothing: true}).
		Create(candidate)
	if result.Error != nil {
		return nil, false, result.Error
	}
	if result.RowsAffected == 1 {
		return candidate, true, nil
	}

	existing, err := r.GetByCacheKey(
		ctx,
		candidate.TenantID,
		candidate.AssistantMessageID,
		candidate.Placement,
		candidate.ConfigHash,
		candidate.Locale,
	)
	if err != nil {
		return nil, false, err
	}
	if existing.Status == types.SuggestionStatusReady && !regenerate {
		return existing, false, nil
	}
	if existing.Status == types.SuggestionStatusSuppressed && !regenerate {
		return existing, false, nil
	}
	if existing.Status == types.SuggestionStatusGenerating && existing.LeaseUntil != nil && existing.LeaseUntil.After(now) {
		return existing, false, nil
	}

	query := r.db.WithContext(ctx).Model(&types.MessageSuggestionSet{}).
		Where("id = ?", existing.ID).
		Where("status <> ? OR lease_until IS NULL OR lease_until < ?", types.SuggestionStatusGenerating, now)
	if existing.Status == types.SuggestionStatusReady && regenerate {
		query = r.db.WithContext(ctx).Model(&types.MessageSuggestionSet{}).
			Where("id = ? AND status = ?", existing.ID, types.SuggestionStatusReady)
	}
	updates := map[string]interface{}{
		"status":             types.SuggestionStatusGenerating,
		"lease_until":        leaseUntil,
		"suppression_reason": "",
		"questions":          types.SuggestionItems{},
		"error_code":         "",
		"generated_at":       nil,
		"updated_at":         now,
	}
	result = query.Updates(updates)
	if result.Error != nil {
		return nil, false, result.Error
	}
	if result.RowsAffected == 0 {
		current, getErr := r.GetByCacheKey(
			ctx,
			candidate.TenantID,
			candidate.AssistantMessageID,
			candidate.Placement,
			candidate.ConfigHash,
			candidate.Locale,
		)
		return current, false, getErr
	}
	existing.Status = types.SuggestionStatusGenerating
	existing.LeaseUntil = &leaseUntil
	existing.Questions = types.SuggestionItems{}
	return existing, true, nil
}

func (r *messageSuggestionRepository) Save(ctx context.Context, set *types.MessageSuggestionSet) error {
	if set == nil {
		return errors.New("message suggestion set is nil")
	}
	return r.db.WithContext(ctx).Save(set).Error
}

func (r *messageSuggestionRepository) CreateEvent(
	ctx context.Context,
	event *types.MessageSuggestionEvent,
) error {
	return r.db.WithContext(ctx).Create(event).Error
}

func (r *messageSuggestionRepository) DeleteByMessageID(
	ctx context.Context,
	tenantID uint64,
	sessionID string,
	messageID string,
) error {
	return r.db.WithContext(ctx).
		Where("tenant_id = ? AND session_id = ? AND assistant_message_id = ?", tenantID, sessionID, messageID).
		Delete(&types.MessageSuggestionSet{}).Error
}

func (r *messageSuggestionRepository) DeleteBySessionID(
	ctx context.Context,
	tenantID uint64,
	sessionID string,
) error {
	return r.db.WithContext(ctx).
		Where("tenant_id = ? AND session_id = ?", tenantID, sessionID).
		Delete(&types.MessageSuggestionSet{}).Error
}
