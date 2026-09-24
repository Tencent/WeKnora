package intentgate

import (
	"context"
	"fmt"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// fakePolicyRepo 是 interfaces.IntentPolicyRepository 的内存实现，
// 带 ListByTenant 调用计数，用于断言缓存命中/失效行为。
type fakePolicyRepo struct {
	mu        sync.Mutex
	policies  map[uint64][]*types.IntentPolicy // tenantID -> policies
	listCalls map[uint64]int                   // tenantID -> ListByTenant 调用次数
}

func newFakePolicyRepo() *fakePolicyRepo {
	return &fakePolicyRepo{policies: map[uint64][]*types.IntentPolicy{}, listCalls: map[uint64]int{}}
}

func (f *fakePolicyRepo) add(p *types.IntentPolicy) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.policies[p.TenantID] = append(f.policies[p.TenantID], p)
}

func (f *fakePolicyRepo) listCallCount(tenantID uint64) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.listCalls[tenantID]
}

func (f *fakePolicyRepo) Create(_ context.Context, p *types.IntentPolicy) error {
	f.add(p)
	return nil
}

func (f *fakePolicyRepo) GetByID(_ context.Context, tenantID uint64, id string) (*types.IntentPolicy, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, p := range f.policies[tenantID] {
		if p.ID == id {
			return p, nil
		}
	}
	return nil, fmt.Errorf("not found")
}

func (f *fakePolicyRepo) ListByTenant(_ context.Context, tenantID uint64) ([]*types.IntentPolicy, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.listCalls[tenantID]++
	return f.policies[tenantID], nil
}

func (f *fakePolicyRepo) SetEnabled(_ context.Context, tenantID uint64, id string, enabled bool) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, p := range f.policies[tenantID] {
		if p.ID == id {
			p.Enabled = enabled
			return nil
		}
	}
	return fmt.Errorf("not found")
}

func (f *fakePolicyRepo) Delete(_ context.Context, tenantID uint64, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	rows := f.policies[tenantID]
	for i, p := range rows {
		if p.ID == id {
			f.policies[tenantID] = append(rows[:i], rows[i+1:]...)
			return nil
		}
	}
	return fmt.Errorf("not found")
}

func mkPolicy(t *testing.T, tenantID uint64, scopeType, scopeRef string, version int) *types.IntentPolicy {
	t.Helper()
	p, err := types.NewIntentPolicy(types.IntentPolicyInput{
		TenantID:       tenantID,
		ScopeType:      scopeType,
		ScopeRef:       scopeRef,
		ConstraintText: "测试约束",
		Version:        version,
	})
	require.NoError(t, err)
	return p
}

func wikiQuery(tenantID uint64) ScopeQuery {
	return ScopeQuery{TenantID: tenantID, ServiceID: "svc_wiki", ToolName: "wiki_delete_page"}
}

// TestPolicyStoreResolve_SameLevelLatestVersion 验收矩阵第一格：
// 同级多条命中取 version 最新（设计 §8.3）。
func TestPolicyStoreResolve_SameLevelLatestVersion(t *testing.T) {
	repo := newFakePolicyRepo()
	repo.add(mkPolicy(t, 1, types.PolicyScopeTool, "svc_wiki:wiki_delete_page", 1))
	repo.add(mkPolicy(t, 1, types.PolicyScopeTool, "svc_wiki:wiki_delete_page", 3))
	repo.add(mkPolicy(t, 1, types.PolicyScopeTool, "svc_wiki:wiki_delete_page", 2))
	store := NewPolicyStore(repo)

	got, err := store.Resolve(context.Background(), wikiQuery(1))
	require.NoError(t, err)
	require.NotNil(t, got)
	require.Equal(t, 3, got.Version, "同级命中取 version 最新")
}

// TestPolicyStoreResolve_DisabledNewestFallsBack 同级最新 version 被停用时，
// 回落到仍启用的次新版本（enabled=false 的策略不参与解析）。
func TestPolicyStoreResolve_DisabledNewestFallsBack(t *testing.T) {
	repo := newFakePolicyRepo()
	repo.add(mkPolicy(t, 1, types.PolicyScopeTool, "svc_wiki:wiki_delete_page", 1))
	disabled := mkPolicy(t, 1, types.PolicyScopeTool, "svc_wiki:wiki_delete_page", 2)
	disabled.Enabled = false
	repo.add(disabled)
	store := NewPolicyStore(repo)

	got, err := store.Resolve(context.Background(), wikiQuery(1))
	require.NoError(t, err)
	require.NotNil(t, got)
	require.Equal(t, 1, got.Version)
}

// TestPolicyStoreResolve_MostSpecificWins 验收矩阵第二格：跨级取最具体，
// 顺序 tool > service > agent > workspace > tenant（设计 §8.3）。
func TestPolicyStoreResolve_MostSpecificWins(t *testing.T) {
	q := ScopeQuery{
		TenantID:    1,
		ServiceID:   "svc_wiki",
		ToolName:    "wiki_delete_page",
		AgentID:     "agent_ops",
		WorkspaceID: "ws_wiki",
	}
	levels := []struct {
		scopeType string
		scopeRef  string
	}{
		{types.PolicyScopeTool, "svc_wiki:wiki_delete_page"},
		{types.PolicyScopeService, "svc_wiki"},
		{types.PolicyScopeAgent, "agent_ops"},
		{types.PolicyScopeTenant, ""},
	}
	// 从最具体到最一般逐级加策略：每加一条更具体的，胜者就应换成它。
	repo := newFakePolicyRepo()
	store := NewPolicyStore(repo)
	for i := len(levels) - 1; i >= 0; i-- {
		want := mkPolicy(t, 1, levels[i].scopeType, levels[i].scopeRef, 1)
		repo.add(want)
		store.InvalidateTenant(1)
		got, err := store.Resolve(context.Background(), q)
		require.NoError(t, err)
		require.NotNil(t, got)
		require.Equal(t, want.ID, got.ID,
			"存在 %s 级策略时，更低级别不得胜出", levels[i].scopeType)
	}
}

// TestPolicyStoreResolve_NoMatch 无任何策略命中时返回 (nil, nil)，
// 交由 Gate 走 baseline 判定（设计 §6.2 policy_id NULL 语义）。
func TestPolicyStoreResolve_NoMatch(t *testing.T) {
	repo := newFakePolicyRepo()
	repo.add(mkPolicy(t, 1, types.PolicyScopeService, "svc_other", 1))
	repo.add(mkPolicy(t, 1, types.PolicyScopeAgent, "agent_other", 1))
	store := NewPolicyStore(repo)

	got, err := store.Resolve(context.Background(), wikiQuery(1))
	require.NoError(t, err)
	require.Nil(t, got)
}

// TestPolicyStoreResolve_ToolRefWildcard tool 级 scope_ref 支持
// `service_id:tool_name` 精确匹配与 `*:wiki_*` 前缀通配（设计 §6.1）。
func TestPolicyStoreResolve_ToolRefWildcard(t *testing.T) {
	cases := []struct {
		name      string
		ref       string
		serviceID string
		toolName  string
		want      bool
	}{
		{"exact", "svc_wiki:wiki_delete_page", "svc_wiki", "wiki_delete_page", true},
		{"service mismatch", "svc_other:wiki_delete_page", "svc_wiki", "wiki_delete_page", false},
		{"wildcard service", "*:wiki_delete_page", "svc_wiki", "wiki_delete_page", true},
		{"prefix wildcard tool", "svc_wiki:wiki_*", "svc_wiki", "wiki_delete_page", true},
		{"prefix wildcard no match", "svc_wiki:wiki_*", "svc_wiki", "kb_search", false},
		{"both wildcard", "*:wiki_*", "svc_wiki", "wiki_create_page", true},
		{"builtin tool no service", ":kb_search", "", "kb_search", true},
		{"tool name only matches any service", "kb_search", "svc_any", "kb_search", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			repo := newFakePolicyRepo()
			repo.add(mkPolicy(t, 1, types.PolicyScopeTool, tc.ref, 1))
			store := NewPolicyStore(repo)
			got, err := store.Resolve(context.Background(), ScopeQuery{
				TenantID:  1,
				ServiceID: tc.serviceID,
				ToolName:  tc.toolName,
			})
			require.NoError(t, err)
			if tc.want {
				require.NotNil(t, got)
			} else {
				require.Nil(t, got)
			}
		})
	}
}

// TestPolicyStoreResolve_TenantIsolation 验收矩阵第三格：租户间隔离。
// tenant A 的策略绝不为 tenant B 解析出结果（设计 §14 场景 4：
// scope 缓存必须按 tenant 隔离）。
func TestPolicyStoreResolve_TenantIsolation(t *testing.T) {
	repo := newFakePolicyRepo()
	repo.add(mkPolicy(t, 1, types.PolicyScopeTool, "svc_wiki:wiki_delete_page", 1))
	repo.add(mkPolicy(t, 1, types.PolicyScopeTenant, "", 1))
	store := NewPolicyStore(repo)

	got, err := store.Resolve(context.Background(), wikiQuery(2))
	require.NoError(t, err)
	require.Nil(t, got, "tenant B 不得命中 tenant A 的任何策略")

	got, err = store.Resolve(context.Background(), wikiQuery(1))
	require.NoError(t, err)
	require.NotNil(t, got)
	require.Equal(t, uint64(1), got.TenantID)
}

// TestPolicyStoreCache_PerTenantInvalidation 验收标准第二条：
// 策略变更后缓存按 tenant 失效，其他租户缓存不受影响（设计 §8.3）。
func TestPolicyStoreCache_PerTenantInvalidation(t *testing.T) {
	repo := newFakePolicyRepo()
	v1 := mkPolicy(t, 1, types.PolicyScopeTool, "svc_wiki:wiki_delete_page", 1)
	repo.add(v1)
	bPolicy := mkPolicy(t, 2, types.PolicyScopeTool, "svc_wiki:wiki_delete_page", 1)
	repo.add(bPolicy)
	store := NewPolicyStore(repo)
	ctx := context.Background()

	// 首轮解析：两个租户各自落缓存。
	gotA, err := store.Resolve(ctx, wikiQuery(1))
	require.NoError(t, err)
	require.Equal(t, 1, gotA.Version)
	gotB, err := store.Resolve(ctx, wikiQuery(2))
	require.NoError(t, err)
	require.Equal(t, bPolicy.ID, gotB.ID)
	require.Equal(t, 1, repo.listCallCount(1))
	require.Equal(t, 1, repo.listCallCount(2))

	// tenant A 策略变更（version+1 新行，设计 §3.2）：失效前解析仍走缓存。
	repo.add(mkPolicy(t, 1, types.PolicyScopeTool, "svc_wiki:wiki_delete_page", 2))
	gotA, err = store.Resolve(ctx, wikiQuery(1))
	require.NoError(t, err)
	require.Equal(t, 1, gotA.Version, "失效前返回缓存结果")
	require.Equal(t, 1, repo.listCallCount(1), "缓存命中不得再查库")

	// 按 tenant 失效：A 重新解析看到 v2；B 的缓存不受影响。
	store.InvalidateTenant(1)
	gotA, err = store.Resolve(ctx, wikiQuery(1))
	require.NoError(t, err)
	require.Equal(t, 2, gotA.Version, "失效后必须解析到新版本")
	require.Equal(t, 2, repo.listCallCount(1))

	gotB, err = store.Resolve(ctx, wikiQuery(2))
	require.NoError(t, err)
	require.Equal(t, bPolicy.ID, gotB.ID)
	require.Equal(t, 1, repo.listCallCount(2), "tenant A 失效不得波及 tenant B 缓存")
}

// TestPolicyStoreCache_NegativeResultCached 无策略命中的解析结果同样
// 按 (tenant, query) 缓存，避免每次工具调用都打库（设计 §11 预算）。
func TestPolicyStoreCache_NegativeResultCached(t *testing.T) {
	repo := newFakePolicyRepo()
	store := NewPolicyStore(repo)
	ctx := context.Background()

	for i := 0; i < 3; i++ {
		got, err := store.Resolve(ctx, wikiQuery(7))
		require.NoError(t, err)
		require.Nil(t, got)
	}
	require.Equal(t, 1, repo.listCallCount(7), "空结果也应缓存")
}

// slowListRepo 阻塞 ListByTenant 直到测试放行——制造「读 miss → 读库 →
// 回填」窗口，供 InvalidateTenant 插入（review 修复：失效与回填的竞态）。
type slowListRepo struct {
	interfaces.IntentPolicyRepository
	policies []*types.IntentPolicy
	release  chan struct{}
	calls    int32
}

func (s *slowListRepo) ListByTenant(_ context.Context, _ uint64) ([]*types.IntentPolicy, error) {
	atomic.AddInt32(&s.calls, 1)
	<-s.release
	return s.policies, nil
}

// TestCachedPolicyStoreInvalidateDuringMissDiscardsStale（review 修复）：
// 读 miss 并发失效时，回填不得把旧策略集重新写进缓存——否则该租户要
// 等到下一次失效才纠正，期间判定一直用旧策略。
func TestCachedPolicyStoreInvalidateDuringMissDiscardsStale(t *testing.T) {
	old := mkPolicy(t, 1, types.PolicyScopeTenant, "", 1)
	repo := &slowListRepo{policies: []*types.IntentPolicy{old}, release: make(chan struct{})}
	store := NewPolicyStore(repo)

	q := ScopeQuery{TenantID: 7, ToolName: "web_search"}
	done := make(chan struct{})
	go func() {
		_, _ = store.Resolve(context.Background(), q)
		close(done)
	}()

	// 等读库开始（短轮询 + Gosched，避免 sleep 定死时序）。
	for atomic.LoadInt32(&repo.calls) == 0 {
		runtime.Gosched()
	}
	store.InvalidateTenant(7)
	close(repo.release)
	<-done

	// 世代守卫应丢弃过期回填：桶已被 InvalidateTenant 删除且未被重建，
	// 下一次 Resolve 必须重新读库（calls=2），而不是吃到旧结果。
	stale, err := store.Resolve(context.Background(), q)
	require.NoError(t, err)
	require.Equal(t, old, stale, "本次调用的返回值不受后至失效影响（语义不变）")
	require.EqualValues(t, 2, atomic.LoadInt32(&repo.calls),
		"失效期间的回填必须被丢弃：缓存不得保留旧策略集")
}
