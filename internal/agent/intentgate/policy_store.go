package intentgate

import (
	"context"
	"strings"
	"sync"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// ScopeQuery 是 scope 解析的输入：一次工具调用可能命中的全部层级
// （设计 §8.3）。空字段表示该层级不参与解析（如内置工具无 ServiceID）。
type ScopeQuery struct {
	TenantID    uint64
	ToolName    string
	ServiceID   string // MCP 工具才有
	AgentID     string
	WorkspaceID string
}

// PolicyStore 是 intent_policy 的读取接口（设计 §7 Gate.store），
// 带 per-tenant 缓存：解析结果按 (tenant_id, 查询键) 缓存，策略变更时
// 按 tenant 失效（设计 §8.3）。
type PolicyStore interface {
	// Resolve 解析一次工具调用命中的最具体策略；无命中返回 (nil, nil)，
	// 交由 Gate 走 baseline 判定（policy_id NULL，设计 §6.2）。
	Resolve(ctx context.Context, q ScopeQuery) (*types.IntentPolicy, error)
	// InvalidateTenant 使一个租户的全部缓存解析结果失效。任何策略变更
	// （新版本 Create / SetEnabled / Delete）都必须调用；其他租户的缓存
	// 不受影响（设计 §14 场景 4：不能全局缓存）。
	InvalidateTenant(tenantID uint64)
}

// scope 层级特异度：tool 最具体，tenant 最一般（设计 §8.3）。
var scopeRank = map[string]int{
	types.PolicyScopeTool:    0,
	types.PolicyScopeService: 1,
	types.PolicyScopeAgent:   2,
	types.PolicyScopeTenant:  3,
}

// cachedResolution 是一次解析结果的缓存项。policy 为 nil 表示"无命中"
// 的负缓存——否则每次工具调用都要打库（设计 §11 预算）。
type cachedResolution struct {
	policy *types.IntentPolicy
}

// CachedPolicyStore implements PolicyStore：内存解析 + per-tenant 缓存。
// 缓存结构与并发安全：外层 map 按 tenantID 分桶，失效时整桶丢弃，
// 天然保证租户间隔离；单桶内按查询键存结果。
//
// 世代守卫（review 修复）：读 miss → 读库 → 回填 之间可能插入
// InvalidateTenant。gens 记录每租户世代，miss 时先取 gen0，回填前
// 复查——世代已变说明策略刚被改/删，丢弃本次解析结果，绝不让旧
// 数据重新进缓存（否则要等到下一次失效才纠正）。
type CachedPolicyStore struct {
	repo  interfaces.IntentPolicyRepository
	mu    sync.RWMutex
	cache map[uint64]map[string]*cachedResolution
	gens  map[uint64]uint64
}

// NewPolicyStore 创建带 per-tenant 缓存的 PolicyStore。
func NewPolicyStore(repo interfaces.IntentPolicyRepository) PolicyStore {
	return &CachedPolicyStore{
		repo:  repo,
		cache: map[uint64]map[string]*cachedResolution{},
		gens:  map[uint64]uint64{},
	}
}

// cacheKey 把一次解析查询编码为缓存键（对应设计 §8.3 的
// (tenant_id, scope_type, scope_ref) 粒度——查询键即各层 scope_ref 的
// 组合）。\x1f 是不会出现在任一字段里的分隔符。
func cacheKey(q ScopeQuery) string {
	return strings.Join([]string{q.ToolName, q.ServiceID, q.AgentID, q.WorkspaceID}, "\x1f")
}

// Resolve implements PolicyStore.
func (s *CachedPolicyStore) Resolve(ctx context.Context, q ScopeQuery) (*types.IntentPolicy, error) {
	key := cacheKey(q)
	s.mu.RLock()
	if bucket, ok := s.cache[q.TenantID]; ok {
		if hit, ok := bucket[key]; ok {
			s.mu.RUnlock()
			return hit.policy, nil
		}
	}
	gen0 := s.gens[q.TenantID]
	s.mu.RUnlock()

	policies, err := s.repo.ListByTenant(ctx, q.TenantID)
	if err != nil {
		return nil, err
	}
	resolved := resolveMostSpecific(policies, q)

	s.mu.Lock()
	if s.gens[q.TenantID] != gen0 {
		// 失效发生在读库期间：丢弃过期解析结果，下一次调用自然重读。
		s.mu.Unlock()
		return resolved, nil
	}
	bucket, ok := s.cache[q.TenantID]
	if !ok {
		bucket = map[string]*cachedResolution{}
		s.cache[q.TenantID] = bucket
	}
	bucket[key] = &cachedResolution{policy: resolved}
	s.mu.Unlock()
	return resolved, nil
}

// InvalidateTenant implements PolicyStore.
func (s *CachedPolicyStore) InvalidateTenant(tenantID uint64) {
	s.mu.Lock()
	s.gens[tenantID]++
	delete(s.cache, tenantID)
	s.mu.Unlock()
}

// resolveMostSpecific 实现 §8.3：取最具体层级（tool>service>agent>
// workspace>tenant）有命中的策略；同级多条命中取 version 最新
// （决胜：created_at 后、id 字典序，保证确定性）。disabled 不参与解析。
func resolveMostSpecific(policies []*types.IntentPolicy, q ScopeQuery) *types.IntentPolicy {
	var best *types.IntentPolicy
	bestRank := len(scopeRank)
	for _, p := range policies {
		if p == nil || !p.Enabled {
			continue
		}
		rank, ok := scopeRank[p.ScopeType]
		if !ok || rank > bestRank {
			continue
		}
		if !matchScope(p, q) {
			continue
		}
		if rank < bestRank {
			best, bestRank = p, rank
			continue
		}
		// 同级：取 version 最新。
		if p.Version > best.Version ||
			(p.Version == best.Version && p.CreatedAt.After(best.CreatedAt)) ||
			(p.Version == best.Version && p.CreatedAt.Equal(best.CreatedAt) && p.ID > best.ID) {
			best = p
		}
	}
	return best
}

// matchScope 判定一条策略是否命中本次查询的对应层级。
func matchScope(p *types.IntentPolicy, q ScopeQuery) bool {
	switch p.ScopeType {
	case types.PolicyScopeTool:
		return matchToolRef(p.ScopeRef, q.ServiceID, q.ToolName)
	case types.PolicyScopeService:
		return q.ServiceID != "" && p.ScopeRef == q.ServiceID
	case types.PolicyScopeAgent:
		return q.AgentID != "" && p.ScopeRef == q.AgentID
	case types.PolicyScopeTenant:
		return true
	}
	return false
}

// matchToolRef 匹配 tool 级 scope_ref（设计 §6.1）：格式
// `service_id:tool_name`，service 段支持 `*` 通配，tool 段支持
// `wiki_*` 前缀通配。无 `:` 时整段按 tool 名匹配任意 service
// （内置工具的便捷写法）；内置工具查询（ServiceID 为空）可写作
// `:tool_name`。
func matchToolRef(ref, serviceID, toolName string) bool {
	if toolName == "" {
		return false
	}
	servicePart, toolPart := "*", ref
	if i := strings.Index(ref, ":"); i >= 0 {
		servicePart, toolPart = ref[:i], ref[i+1:]
	}
	if servicePart != "*" && servicePart != serviceID {
		return false
	}
	return matchWildcard(toolPart, toolName)
}

// matchWildcard 支持精确匹配与前缀通配（尾部 `*`）。
func matchWildcard(pattern, value string) bool {
	if pattern == "*" {
		return true
	}
	if strings.HasSuffix(pattern, "*") {
		return strings.HasPrefix(value, strings.TrimSuffix(pattern, "*"))
	}
	return pattern == value
}
