package interfaces

import (
	"context"

	"github.com/Tencent/WeKnora/internal/types"
)

// IntentPolicyRepository 持久化 IntentGate 的策略表（intent_policies，
// 设计文档 §6.1）。读取方是 intentgate.PolicyStore（scope 解析 +
// per-tenant 缓存，设计 §8.3）；写入方是策略运营接口（后续 ticket）。
//
// 版本化语义：策略修改 = 插入同 (tenant_id, scope_type, scope_ref) 的
// 新行（version+1），旧版本保留（设计 §3.2）；本接口不提供原地 Update。
type IntentPolicyRepository interface {
	// Create 写入一条策略（或某谱系的新版本）。
	Create(ctx context.Context, p *types.IntentPolicy) error
	// GetByID 按主键读取；不存在时返回 repository.ErrIntentPolicyNotFound。
	GetByID(ctx context.Context, tenantID uint64, id string) (*types.IntentPolicy, error)
	// ListByTenant 列出租户全部策略（跨 scope、跨 version、含 disabled），
	// 供 scope 解析器在内存中做最具体匹配。按 (scope_type, scope_ref,
	// version) 升序返回，保证消费方行为确定。
	ListByTenant(ctx context.Context, tenantID uint64) ([]*types.IntentPolicy, error)
	// SetEnabled 启用/停用一条策略。属策略变更，调用方须触发对应租户的
	// 缓存失效（intentgate.PolicyStore.InvalidateTenant，设计 §8.3）。
	SetEnabled(ctx context.Context, tenantID uint64, id string, enabled bool) error
	// Delete 删除一条策略。版本化策略原则上只增不删，此方法为测试与
	// 租户级数据清理预留。
	Delete(ctx context.Context, tenantID uint64, id string) error
}
