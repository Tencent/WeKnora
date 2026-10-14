package repository

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// AgentPromptRepository implements interfaces.AgentPromptRepository.
type AgentPromptRepository struct {
	db *gorm.DB
}

// NewAgentPromptRepository creates a repository backed by GORM.
func NewAgentPromptRepository(db *gorm.DB) interfaces.AgentPromptRepository {
	return &AgentPromptRepository{db: db}
}

// Create inserts a new prompt row.
func (r *AgentPromptRepository) Create(ctx context.Context, row *types.AgentPrompt) error {
	if row == nil {
		return errors.New("agent prompt row is nil")
	}
	if err := r.db.WithContext(ctx).Create(row).Error; err != nil {
		return fmt.Errorf("create agent prompt: %w", err)
	}
	return nil
}

// Get returns the row, or nil when it does not exist.
func (r *AgentPromptRepository) Get(ctx context.Context, id string) (*types.AgentPrompt, error) {
	var row types.AgentPrompt
	err := r.db.WithContext(ctx).Where("id = ?", id).First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get agent prompt: %w", err)
	}
	return &row, nil
}

// Transition applies a compare-and-set state change.
func (r *AgentPromptRepository) Transition(
	ctx context.Context, id string, t types.AgentPromptTransition,
) (bool, error) {
	if len(t.From) == 0 || t.To == "" {
		return false, errors.New("transition requires from and to states")
	}
	// A map keeps empty values that a struct update would skip.
	updates := map[string]interface{}{
		"status":     t.To,
		"updated_at": time.Now(),
	}
	if len(t.Response) > 0 {
		updates["response"] = t.Response
	}
	if t.CloseReason != nil {
		updates["close_reason"] = *t.CloseReason
	}
	if t.ResolvedBy != nil {
		updates["resolved_by"] = *t.ResolvedBy
	}
	if t.ResolvedAt != nil {
		updates["resolved_at"] = *t.ResolvedAt
	}
	if t.ExpiresAt != nil {
		updates["expires_at"] = *t.ExpiresAt
	}
	if t.Owner != nil {
		updates["owner_instance"] = *t.Owner
	}
	if t.LeaseUntil != nil {
		updates["lease_until"] = *t.LeaseUntil
	}
	q := r.db.WithContext(ctx).Model(&types.AgentPrompt{}).
		Where("id = ? AND status IN ?", id, t.From)
	if t.UnexpiredAt != nil {
		q = q.Where("expires_at IS NULL OR expires_at > ?", *t.UnexpiredAt)
	}
	if t.ExpectedOwner != nil {
		q = q.Where("owner_instance = ?", *t.ExpectedOwner)
	}
	if t.LeaseBefore != nil {
		q = q.Where("lease_until < ?", *t.LeaseBefore)
	}
	res := q.Updates(updates)
	if res.Error != nil {
		return false, fmt.Errorf("transition agent prompt: %w", res.Error)
	}
	return res.RowsAffected == 1, nil
}

// leasedStatuses are the states whose row is held by an owner replica.
var leasedStatuses = []string{types.AgentPromptPending, types.AgentPromptAnswered, types.AgentPromptExecuting}

// RenewLease extends the lease of a leased row still held by owner.
func (r *AgentPromptRepository) RenewLease(
	ctx context.Context, id, owner string, until time.Time,
) (bool, error) {
	res := r.db.WithContext(ctx).Model(&types.AgentPrompt{}).
		Where("id = ? AND status IN ? AND owner_instance = ?", id, leasedStatuses, owner).
		Updates(map[string]interface{}{"lease_until": until, "updated_at": time.Now()})
	if res.Error != nil {
		return false, fmt.Errorf("renew agent prompt lease: %w", res.Error)
	}
	return res.RowsAffected == 1, nil
}

// ListStaleLeases returns rows in statuses whose owner stopped renewing.
func (r *AgentPromptRepository) ListStaleLeases(
	ctx context.Context, statuses []string, before time.Time, limit int,
) ([]*types.AgentPrompt, error) {
	var rows []*types.AgentPrompt
	err := r.db.WithContext(ctx).
		Where("status IN ? AND lease_until < ?", statuses, before).
		Order("lease_until").
		Limit(limit).
		Find(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("list stale agent prompts: %w", err)
	}
	return rows, nil
}

// ListExpired returns suspended rows past their expiry.
func (r *AgentPromptRepository) ListExpired(
	ctx context.Context, before time.Time, limit int,
) ([]*types.AgentPrompt, error) {
	var rows []*types.AgentPrompt
	err := r.db.WithContext(ctx).
		Where("status = ? AND expires_at < ?", types.AgentPromptSuspended, before).
		Order("expires_at").
		Limit(limit).
		Find(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("list expired agent prompts: %w", err)
	}
	return rows, nil
}

// ListBySession returns the session's rows in statuses.
func (r *AgentPromptRepository) ListBySession(
	ctx context.Context, sessionID string, statuses []string,
) ([]*types.AgentPrompt, error) {
	var rows []*types.AgentPrompt
	err := r.db.WithContext(ctx).
		Where("session_id = ? AND status IN ?", sessionID, statuses).
		Order("created_at").
		Find(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("list session agent prompts: %w", err)
	}
	return rows, nil
}

// AgentRunSuspensionRepository implements interfaces.AgentRunSuspensionRepository.
type AgentRunSuspensionRepository struct {
	db *gorm.DB
}

// NewAgentRunSuspensionRepository creates a repository backed by GORM.
func NewAgentRunSuspensionRepository(db *gorm.DB) interfaces.AgentRunSuspensionRepository {
	return &AgentRunSuspensionRepository{db: db}
}

// Save inserts or replaces the run state of a message.
func (r *AgentRunSuspensionRepository) Save(ctx context.Context, row *types.AgentRunSuspension) error {
	if row == nil || row.AssistantMessageID == "" {
		return errors.New("agent run suspension requires an assistant message id")
	}
	now := time.Now()
	if row.CreatedAt.IsZero() {
		row.CreatedAt = now
	}
	row.UpdatedAt = now
	err := r.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "assistant_message_id"}},
		DoUpdates: clause.AssignmentColumns([]string{
			"tenant_id", "session_id", "prompt_id", "snapshot_version", "snapshot", "updated_at",
		}),
	}).Create(row).Error
	if err != nil {
		return fmt.Errorf("save agent run suspension: %w", err)
	}
	return nil
}

// Get returns the run state of a message, or nil when there is none.
func (r *AgentRunSuspensionRepository) Get(
	ctx context.Context, assistantMessageID string,
) (*types.AgentRunSuspension, error) {
	var row types.AgentRunSuspension
	err := r.db.WithContext(ctx).Where("assistant_message_id = ?", assistantMessageID).First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get agent run suspension: %w", err)
	}
	return &row, nil
}

// Delete removes the run state while it still belongs to promptID.
func (r *AgentRunSuspensionRepository) Delete(ctx context.Context, assistantMessageID, promptID string) error {
	err := r.db.WithContext(ctx).
		Where("assistant_message_id = ? AND prompt_id = ?", assistantMessageID, promptID).
		Delete(&types.AgentRunSuspension{}).Error
	if err != nil {
		return fmt.Errorf("delete agent run suspension: %w", err)
	}
	return nil
}
