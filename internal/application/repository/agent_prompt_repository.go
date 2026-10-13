package repository

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"gorm.io/gorm"
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
	res := r.db.WithContext(ctx).Model(&types.AgentPrompt{}).
		Where("id = ? AND status IN ?", id, t.From).
		Updates(updates)
	if res.Error != nil {
		return false, fmt.Errorf("transition agent prompt: %w", res.Error)
	}
	return res.RowsAffected == 1, nil
}

// RenewLease extends the lease of a pending row still held by owner.
func (r *AgentPromptRepository) RenewLease(
	ctx context.Context, id, owner string, until time.Time,
) (bool, error) {
	res := r.db.WithContext(ctx).Model(&types.AgentPrompt{}).
		Where("id = ? AND status = ? AND owner_instance = ?", id, types.AgentPromptPending, owner).
		Updates(map[string]interface{}{"lease_until": until, "updated_at": time.Now()})
	if res.Error != nil {
		return false, fmt.Errorf("renew agent prompt lease: %w", res.Error)
	}
	return res.RowsAffected == 1, nil
}
