package repository

import (
	"context"
	"errors"
	"fmt"
	"time"

	"gorm.io/gorm"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// ErrIntentPolicyNotFound 是按 ID/条件操作不到目标策略时的哨兵错误。
var ErrIntentPolicyNotFound = errors.New("intent policy record not found")

// IntentPolicyRepository implements interfaces.IntentPolicyRepository.
//
// 所有查询都带 tenant_id 谓词：intent_policies 按租户隔离（CONTEXT.md
// IntentPolicy 条目），跨租户读取/修改一律视为 not found。版本化语义
// （修改 = 插入 version+1 新行）由调用方与 types.NewIntentPolicy 保证，
// 本实现不提供原地 Update。
type IntentPolicyRepository struct {
	db *gorm.DB
}

// NewIntentPolicyRepository creates a repository backed by GORM.
func NewIntentPolicyRepository(db *gorm.DB) interfaces.IntentPolicyRepository {
	return &IntentPolicyRepository{db: db}
}

// Create 写入一条策略。最低字段校验在这里兜底：枚举非法或缺 scope_ref
// 的行无法参与 scope 解析（设计 §8.3），属于脏数据，拒绝写入。
func (r *IntentPolicyRepository) Create(ctx context.Context, p *types.IntentPolicy) error {
	if p == nil {
		return errors.New("intentgate: intent policy is nil")
	}
	if p.ID == "" {
		return errors.New("intentgate: policy id is required")
	}
	if !types.ValidPolicyScope(p.ScopeType) {
		return fmt.Errorf("intentgate: unknown policy scope_type %q", p.ScopeType)
	}
	if p.ScopeType != types.PolicyScopeTenant && p.ScopeRef == "" {
		return fmt.Errorf("intentgate: scope_ref is required for scope_type %q", p.ScopeType)
	}
	if !types.ValidRiskTier(p.RiskTier) {
		return fmt.Errorf("intentgate: unknown risk_tier %q", p.RiskTier)
	}
	if !types.ValidVerdictMode(p.Mode) {
		return fmt.Errorf("intentgate: unknown policy mode %q", p.Mode)
	}
	if err := r.db.WithContext(ctx).Create(p).Error; err != nil {
		return fmt.Errorf("create intent policy: %w", err)
	}
	return nil
}

// GetByID 按主键读取一条策略。
func (r *IntentPolicyRepository) GetByID(ctx context.Context, tenantID uint64, id string) (*types.IntentPolicy, error) {
	var p types.IntentPolicy
	err := r.db.WithContext(ctx).
		Where("tenant_id = ? AND id = ?", tenantID, id).
		First(&p).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrIntentPolicyNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get intent policy: %w", err)
	}
	return &p, nil
}

// ListByTenant 列出租户全部策略（跨 scope、跨 version、含 disabled）。
// scope 解析器在内存中做 enabled 过滤与最具体匹配（设计 §8.3），因此
// 这里不做任何过滤，只保证确定性排序。
func (r *IntentPolicyRepository) ListByTenant(ctx context.Context, tenantID uint64) ([]*types.IntentPolicy, error) {
	var rows []*types.IntentPolicy
	if err := r.db.WithContext(ctx).
		Where("tenant_id = ?", tenantID).
		Order("scope_type ASC, scope_ref ASC, version ASC, id ASC").
		Find(&rows).Error; err != nil {
		return nil, fmt.Errorf("list intent policies by tenant: %w", err)
	}
	return rows, nil
}

// SetEnabled 启用/停用一条策略（updated_at 随之刷新）。属策略变更，
// 调用方须触发该租户的缓存失效（设计 §8.3）。
func (r *IntentPolicyRepository) SetEnabled(ctx context.Context, tenantID uint64, id string, enabled bool) error {
	res := r.db.WithContext(ctx).Model(&types.IntentPolicy{}).
		Where("tenant_id = ? AND id = ?", tenantID, id).
		Updates(map[string]any{"enabled": enabled, "updated_at": time.Now().UTC()})
	if res.Error != nil {
		return fmt.Errorf("set intent policy enabled: %w", res.Error)
	}
	if res.RowsAffected == 0 {
		return ErrIntentPolicyNotFound
	}
	return nil
}

// Delete 删除一条策略。版本化策略原则上只增不删（旧版本保留是对账
// 前提，设计 §3.2），此方法为测试与租户级数据清理预留。
func (r *IntentPolicyRepository) Delete(ctx context.Context, tenantID uint64, id string) error {
	res := r.db.WithContext(ctx).
		Where("tenant_id = ? AND id = ?", tenantID, id).
		Delete(&types.IntentPolicy{})
	if res.Error != nil {
		return fmt.Errorf("delete intent policy: %w", res.Error)
	}
	if res.RowsAffected == 0 {
		return ErrIntentPolicyNotFound
	}
	return nil
}
