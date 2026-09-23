package interfaces

import (
	"context"

	"github.com/Tencent/WeKnora/internal/types"
)

// IntentVerdictRepository 持久化 IntentGate 的判定记录（intent_verdicts
// 表，设计文档 §6.2）。写入方是 VerdictWriter（T11 异步落库）；读取方是
// 策略运营报表与数据飞轮。
type IntentVerdictRepository interface {
	// Create 写入一条判定记录。
	Create(ctx context.Context, rec *types.VerdictRecord) error
	// GetByID 按主键读取；不存在时返回 repository.ErrIntentVerdictNotFound。
	GetByID(ctx context.Context, tenantID uint64, id string) (*types.VerdictRecord, error)
	// ListBySession 按会话查询，created_at 升序；limit<=0 表示不限。
	ListBySession(ctx context.Context, tenantID uint64, sessionID string, limit int) ([]*types.VerdictRecord, error)
	// ListByPolicy 按策略查询（跨 version，供策略生命周期对账），
	// created_at 升序；limit<=0 表示不限。
	ListByPolicy(ctx context.Context, tenantID uint64, policyID string, limit int) ([]*types.VerdictRecord, error)
	// ListByTenant 全租户最近 limit 条（created_at 降序；报表页下钻列表），
	// limit<=0 表示不限（上限 1000 防失控）。
	ListByTenant(ctx context.Context, tenantID uint64, limit int) ([]*types.VerdictRecord, error)
	// CountByVerdictGrouped 按 verdict 分组计数（T51 报表页分布数字的
	// 数据源），policyID 为空时统计租户内全部 verdict（含 baseline 的
	// policy_id NULL 行）。返回 verdict 取值 → 计数。
	CountByVerdictGrouped(ctx context.Context, tenantID uint64, policyID string) (map[string]int64, error)
	// UpdateHumanOverride 记录人工后续动作（飞轮关键字段）。
	UpdateHumanOverride(ctx context.Context, tenantID uint64, id string, override string) error
	// Delete 删除一条记录。
	Delete(ctx context.Context, tenantID uint64, id string) error
}
