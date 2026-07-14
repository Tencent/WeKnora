package middleware

import (
	"context"
	"errors"
	"testing"

	"github.com/Tencent/WeKnora/internal/config"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// fakeMemberService is a hand-rolled stand-in for
// interfaces.TenantMemberService. It backs Get/HasAnyMembers/AddMember
// with two in-memory maps and lets each test seed exactly the rows it
// cares about. Other interface methods are stubbed because resolveTenantRole
// never touches them.
type fakeMemberService struct {
	members map[string]*types.TenantMember // key = userID + "|" + tenantID
	// addCalls records every AddMember invocation so tests can assert
	// that resolveTenantRole did (or didn't) attempt to auto-promote.
	addCalls []struct {
		UserID   string
		TenantID uint64
		Role     types.TenantRole
	}
	failGet    error
	failHasAny error
	failAdd    error
	// 阻止 auto-promote 把 hasAny 翻面：默认 AddMember 成功也会写入 members map。
}

func newFakeMemberService() *fakeMemberService {
	return &fakeMemberService{members: map[string]*types.TenantMember{}}
}

func memberKey(u string, t uint64) string {
	return u + "|" + uintToStr(t)
}

func uintToStr(t uint64) string {
	// 简单数字转字符串，避免引入额外依赖。
	if t == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for t > 0 {
		i--
		buf[i] = byte('0' + t%10)
		t /= 10
	}
	return string(buf[i:])
}

func (f *fakeMemberService) seedActive(userID string, tenantID uint64, role types.TenantRole) {
	f.members[memberKey(userID, tenantID)] = &types.TenantMember{
		UserID:   userID,
		TenantID: tenantID,
		Role:     role,
		Status:   types.TenantMemberStatusActive,
	}
}

func (f *fakeMemberService) AddMember(
	ctx context.Context, userID string, tenantID uint64, role types.TenantRole, invitedBy *string,
) (*types.TenantMember, error) {
	f.addCalls = append(f.addCalls, struct {
		UserID   string
		TenantID uint64
		Role     types.TenantRole
	}{userID, tenantID, role})
	if f.failAdd != nil {
		return nil, f.failAdd
	}
	m := &types.TenantMember{UserID: userID, TenantID: tenantID, Role: role, Status: types.TenantMemberStatusActive}
	f.members[memberKey(userID, tenantID)] = m
	return m, nil
}

func (f *fakeMemberService) EnsureOwner(
	ctx context.Context, userID string, tenantID uint64,
) (*types.TenantMember, error) {
	if existing, ok := f.members[memberKey(userID, tenantID)]; ok {
		return existing, nil
	}
	return f.AddMember(ctx, userID, tenantID, types.TenantRoleOwner, nil)
}

func (f *fakeMemberService) GetMembership(
	ctx context.Context, userID string, tenantID uint64,
) (*types.TenantMember, error) {
	if f.failGet != nil {
		return nil, f.failGet
	}
	m, ok := f.members[memberKey(userID, tenantID)]
	if !ok {
		return nil, nil
	}
	cp := *m
	return &cp, nil
}

func (f *fakeMemberService) ListByUser(ctx context.Context, userID string) ([]*types.TenantMember, error) {
	var out []*types.TenantMember
	for _, member := range f.members {
		if member.UserID == userID && member.Status == types.TenantMemberStatusActive {
			copy := *member
			out = append(out, &copy)
		}
	}
	return out, nil
}
func (f *fakeMemberService) ListByTenant(ctx context.Context, tenantID uint64) ([]*types.TenantMember, error) {
	return nil, nil
}
func (f *fakeMemberService) ListMembersPage(
	ctx context.Context, tenantID uint64, query string, page, pageSize int,
) ([]*types.TenantMember, int64, error) {
	return nil, 0, nil
}
func (f *fakeMemberService) HasAnyMembers(ctx context.Context, tenantID uint64) (bool, error) {
	if f.failHasAny != nil {
		return false, f.failHasAny
	}
	for _, m := range f.members {
		if m.TenantID == tenantID && m.Status == types.TenantMemberStatusActive {
			return true, nil
		}
	}
	return false, nil
}
func (f *fakeMemberService) UpdateRole(
	ctx context.Context, userID string, tenantID uint64, newRole types.TenantRole,
) error {
	return nil
}
func (f *fakeMemberService) RemoveMember(ctx context.Context, userID string, tenantID uint64) error {
	return nil
}

var _ interfaces.TenantMemberService = (*fakeMemberService)(nil)

func cfgWithRBAC(enabled bool) *config.Config {
	return &config.Config{Tenant: &config.TenantConfig{EnableRBAC: &enabled}}
}

func TestResolveTenantRole_ActiveMembershipWins(t *testing.T) {
	svc := newFakeMemberService()
	svc.seedActive("u1", 10, types.TenantRoleContributor)

	got, ok := resolveTenantRole(context.Background(), svc,
		&types.User{ID: "u1", TenantID: 10}, 10, false, cfgWithRBAC(true))
	if !ok || got != types.TenantRoleContributor {
		t.Fatalf("got (%v, %v), want (contributor, true)", got, ok)
	}
	if len(svc.addCalls) != 0 {
		t.Fatalf("must not auto-promote when membership exists, got %d AddMember calls", len(svc.addCalls))
	}
}

func TestResolveTenantRole_CrossTenantSuperuserGetsAdmin_NoAutoPromote(t *testing.T) {
	// 回归 H1：跨空间超管 switch 到他人空间时，绝对不能写入 tenant_members。
	svc := newFakeMemberService()
	user := &types.User{ID: "super", TenantID: 1, CanAccessAllTenants: true}

	got, ok := resolveTenantRole(context.Background(), svc, user, 99, true, cfgWithRBAC(true))
	if !ok || got != types.TenantRoleAdmin {
		t.Fatalf("got (%v, %v), want (admin, true)", got, ok)
	}
	if len(svc.addCalls) != 0 {
		t.Fatalf("cross-tenant superuser must not trigger auto-promote, got %+v", svc.addCalls)
	}
}

func TestResolveTenantRole_AutoPromoteRequiresHomeTenant(t *testing.T) {
	// 回归 H1：即便 target 是孤儿空间，只要不是用户自己的 home tenant，
	// 就不能 auto-promote 为 Owner。
	svc := newFakeMemberService() // 空 — 任何空间都是孤儿
	user := &types.User{ID: "u1", TenantID: 1, CanAccessAllTenants: true}

	got, ok := resolveTenantRole(context.Background(), svc, user, 42, true, cfgWithRBAC(true))
	if !ok || got != types.TenantRoleAdmin {
		t.Fatalf("cross-tenant superuser should still get visitor Admin, got (%v, %v)", got, ok)
	}
	if len(svc.addCalls) != 0 {
		t.Fatalf("auto-promote must skip cross-tenant target, got %+v", svc.addCalls)
	}
}

func TestResolveTenantRole_AutoPromoteHomeTenant(t *testing.T) {
	// home tenant + 孤儿空间 + 非 switch → 允许 auto-promote 为 Owner。
	svc := newFakeMemberService()
	user := &types.User{ID: "u1", TenantID: 7}

	got, ok := resolveTenantRole(context.Background(), svc, user, 7, false, cfgWithRBAC(true))
	if !ok || got != types.TenantRoleOwner {
		t.Fatalf("got (%v, %v), want (owner, true)", got, ok)
	}
	if len(svc.addCalls) != 1 || svc.addCalls[0].Role != types.TenantRoleOwner {
		t.Fatalf("expected exactly one Owner AddMember call, got %+v", svc.addCalls)
	}
}

func TestResolveTenantRole_AutoPromoteSkippedIfTenantHasMembers(t *testing.T) {
	svc := newFakeMemberService()
	// 同一 home tenant 已经有其它成员 — 不应自动晋升新登录者。
	svc.seedActive("other", 7, types.TenantRoleOwner)
	user := &types.User{ID: "u1", TenantID: 7}

	got, ok := resolveTenantRole(context.Background(), svc, user, 7, false, cfgWithRBAC(true))
	if ok {
		t.Fatalf("RBAC enabled + no membership for u1 should be rejected, got role=%v", got)
	}
	if len(svc.addCalls) != 0 {
		t.Fatalf("must not auto-promote into a tenant that already has members, got %+v", svc.addCalls)
	}
}

func TestResolveTenantRole_FailOpenAdminWhenRBACDisabled(t *testing.T) {
	svc := newFakeMemberService()
	user := &types.User{ID: "u1", TenantID: 7}
	// targetTenantID != home，所以不进 auto-promote 分支。
	got, ok := resolveTenantRole(context.Background(), svc, user, 8, false, cfgWithRBAC(false))
	if !ok || got != types.TenantRoleAdmin {
		t.Fatalf("EnableRBAC=false should fail open Admin, got (%v, %v)", got, ok)
	}
}

func TestResolveTenantRole_FailClosedWhenRBACEnabled(t *testing.T) {
	svc := newFakeMemberService()
	// 已有其它成员，自动晋升路径关闭；RBAC 启用 → 必须 403。
	svc.seedActive("other", 8, types.TenantRoleOwner)
	user := &types.User{ID: "u1", TenantID: 7}
	if _, ok := resolveTenantRole(context.Background(), svc, user, 8, false, cfgWithRBAC(true)); ok {
		t.Fatalf("EnableRBAC=true + no membership should be rejected")
	}
}

func TestResolveTenantRole_LookupErrorFailsOpenWhenRBACDisabled(t *testing.T) {
	// 短暂 DB 错误时，fail-open 模式不应锁死现有用户。这里 targetTenantID 故意
	// 选与 home 不同的值，避免进入 home-tenant auto-promote 分支。
	svc := newFakeMemberService()
	svc.failGet = errors.New("transient db failure")
	// 让 HasAnyMembers 返回 true，关闭孤儿空间自愈路径。
	svc.seedActive("placeholder", 8, types.TenantRoleAdmin)
	user := &types.User{ID: "u1", TenantID: 7}

	got, ok := resolveTenantRole(context.Background(), svc, user, 8, false, cfgWithRBAC(false))
	if !ok || got != types.TenantRoleAdmin {
		t.Fatalf("transient lookup error under RBAC=false should fail open Admin, got (%v, %v)", got, ok)
	}
}

func TestResolveTenantRole_DemotedUserCannotReclaimViaOrphan(t *testing.T) {
	// 边界场景：管理员人为软删全部成员后，被踢出的用户不应在登录自己 home tenant 时
	// 因 HasAnyMembers=false 而自动重新拿到 Owner。
	// 当前实现的策略是 "home tenant + 孤儿 => Owner"，这是设计选择；本测试为这条
	// 路径加锁，未来如果收紧策略需要同步更新。
	svc := newFakeMemberService()
	user := &types.User{ID: "demoted", TenantID: 5}
	got, ok := resolveTenantRole(context.Background(), svc, user, 5, false, cfgWithRBAC(true))
	if !ok || got != types.TenantRoleOwner {
		t.Fatalf("current policy allows orphan-tenant self-heal on home tenant, got (%v, %v)", got, ok)
	}
}
