package service

import (
	"context"
	"errors"
	"testing"
	"time"

	apprepo "github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"gorm.io/gorm"
)

// gormErrRecordNotFound is the sentinel the fake repo returns when an
// atomic helper is asked to touch a row that no longer exists, matching
// the real repo's behaviour. Kept as a package-level alias so the
// tests can reference it without re-importing gorm at every call site.
var gormErrRecordNotFound = gorm.ErrRecordNotFound

// fakeTenantMemberRepo is an in-memory implementation of
// interfaces.TenantMemberRepository for unit tests. It is intentionally
// not safe for concurrent use; tests should drive it sequentially.
type fakeTenantMemberRepo struct {
	rows []*types.TenantMember
	// nextID is incremented on Create to populate the surrogate PK.
	nextID uint64
	// failGet, failHasAny etc. let tests inject transient errors on the
	// matching method to exercise error paths.
	failGet           error
	failHasAny        error
	failCountOwners   error
	failUpdateRole    error
	failSoftDelete    error
	failCreate        error
}

func newFakeRepo() *fakeTenantMemberRepo { return &fakeTenantMemberRepo{} }

func (r *fakeTenantMemberRepo) Create(ctx context.Context, m *types.TenantMember) error {
	if r.failCreate != nil {
		return r.failCreate
	}
	r.nextID++
	m.ID = r.nextID
	// Mirror the partial unique index on (user_id, tenant_id) for
	// active rows so tests can exercise duplicate-insert behaviour.
	for _, e := range r.rows {
		if e.UserID == m.UserID && e.TenantID == m.TenantID && e.DeletedAt.Valid == false {
			return errors.New("duplicate active membership")
		}
	}
	cp := *m
	r.rows = append(r.rows, &cp)
	return nil
}

func (r *fakeTenantMemberRepo) Get(ctx context.Context, userID string, tenantID uint64) (*types.TenantMember, error) {
	if r.failGet != nil {
		return nil, r.failGet
	}
	for _, e := range r.rows {
		if e.UserID == userID && e.TenantID == tenantID && !e.DeletedAt.Valid {
			cp := *e
			return &cp, nil
		}
	}
	return nil, nil
}

func (r *fakeTenantMemberRepo) ListByUser(ctx context.Context, userID string) ([]*types.TenantMember, error) {
	var out []*types.TenantMember
	for _, e := range r.rows {
		if e.UserID == userID && !e.DeletedAt.Valid {
			cp := *e
			out = append(out, &cp)
		}
	}
	return out, nil
}

func (r *fakeTenantMemberRepo) ListByTenant(ctx context.Context, tenantID uint64) ([]*types.TenantMember, error) {
	var out []*types.TenantMember
	for _, e := range r.rows {
		if e.TenantID == tenantID && !e.DeletedAt.Valid {
			cp := *e
			out = append(out, &cp)
		}
	}
	return out, nil
}

func (r *fakeTenantMemberRepo) UpdateRole(ctx context.Context, userID string, tenantID uint64, role types.TenantRole) error {
	if r.failUpdateRole != nil {
		return r.failUpdateRole
	}
	for _, e := range r.rows {
		if e.UserID == userID && e.TenantID == tenantID && !e.DeletedAt.Valid {
			e.Role = role
			return nil
		}
	}
	return errors.New("not found")
}

func (r *fakeTenantMemberRepo) SoftDelete(ctx context.Context, userID string, tenantID uint64) error {
	if r.failSoftDelete != nil {
		return r.failSoftDelete
	}
	for _, e := range r.rows {
		if e.UserID == userID && e.TenantID == tenantID && !e.DeletedAt.Valid {
			e.DeletedAt.Valid = true
			return nil
		}
	}
	return errors.New("not found")
}

func (r *fakeTenantMemberRepo) CountActiveOwners(ctx context.Context, tenantID uint64) (int64, error) {
	if r.failCountOwners != nil {
		return 0, r.failCountOwners
	}
	var n int64
	for _, e := range r.rows {
		if e.TenantID == tenantID && !e.DeletedAt.Valid &&
			e.Role == types.TenantRoleOwner && e.Status == types.TenantMemberStatusActive {
			n++
		}
	}
	return n, nil
}

func (r *fakeTenantMemberRepo) HasAnyMembers(ctx context.Context, tenantID uint64) (bool, error) {
	if r.failHasAny != nil {
		return false, r.failHasAny
	}
	for _, e := range r.rows {
		if e.TenantID == tenantID && !e.DeletedAt.Valid && e.Status == types.TenantMemberStatusActive {
			return true, nil
		}
	}
	return false, nil
}

// DemoteOwnerAtomically and RemoveOwnerAtomically mimic the production
// repo's transactional invariants: count other active Owners, fail
// closed when there are none, otherwise apply the role change /
// soft-delete in-memory. The fake doesn't simulate row-level locks
// because every Go test is single-goroutine here; the production
// transaction is exercised via the repo tests against a real DB.
func (r *fakeTenantMemberRepo) DemoteOwnerAtomically(
	ctx context.Context, userID string, tenantID uint64, newRole types.TenantRole,
) error {
	others := int64(0)
	var target *types.TenantMember
	for _, e := range r.rows {
		if e.TenantID != tenantID || e.DeletedAt.Valid || e.Status != types.TenantMemberStatusActive {
			continue
		}
		if e.Role == types.TenantRoleOwner && e.UserID != userID {
			others++
		}
		if e.UserID == userID {
			target = e
		}
	}
	if others == 0 {
		return apprepo.ErrLastOwner
	}
	if target == nil {
		return gormErrRecordNotFound
	}
	target.Role = newRole
	target.UpdatedAt = time.Now()
	return nil
}

func (r *fakeTenantMemberRepo) RemoveOwnerAtomically(
	ctx context.Context, userID string, tenantID uint64,
) error {
	others := int64(0)
	var target *types.TenantMember
	for _, e := range r.rows {
		if e.TenantID != tenantID || e.DeletedAt.Valid || e.Status != types.TenantMemberStatusActive {
			continue
		}
		if e.Role == types.TenantRoleOwner && e.UserID != userID {
			others++
		}
		if e.UserID == userID {
			target = e
		}
	}
	if others == 0 {
		return apprepo.ErrLastOwner
	}
	if target == nil {
		return gormErrRecordNotFound
	}
	target.DeletedAt.Time = time.Now()
	target.DeletedAt.Valid = true
	return nil
}

// Compile-time guard so the test stays in sync with the interface.
var _ interfaces.TenantMemberRepository = (*fakeTenantMemberRepo)(nil)

func newServiceWithRepo() (interfaces.TenantMemberService, *fakeTenantMemberRepo) {
	r := newFakeRepo()
	return NewTenantMemberService(r), r
}

func TestTenantMemberService_AddMember_RejectsInvalidRole(t *testing.T) {
	svc, _ := newServiceWithRepo()
	_, err := svc.AddMember(context.Background(), "u1", 1, types.TenantRole("nonsense"), nil)
	if !errors.Is(err, ErrInvalidTenantRole) {
		t.Fatalf("want ErrInvalidTenantRole, got %v", err)
	}
}

func TestTenantMemberService_AddMember_RejectsDuplicate(t *testing.T) {
	svc, _ := newServiceWithRepo()
	ctx := context.Background()
	if _, err := svc.AddMember(ctx, "u1", 1, types.TenantRoleContributor, nil); err != nil {
		t.Fatalf("first AddMember: %v", err)
	}
	_, err := svc.AddMember(ctx, "u1", 1, types.TenantRoleContributor, nil)
	if !errors.Is(err, ErrMembershipAlreadyExists) {
		t.Fatalf("want ErrMembershipAlreadyExists, got %v", err)
	}
}

func TestTenantMemberService_EnsureOwner_Idempotent(t *testing.T) {
	svc, repo := newServiceWithRepo()
	ctx := context.Background()
	first, err := svc.EnsureOwner(ctx, "u1", 1)
	if err != nil {
		t.Fatalf("first EnsureOwner: %v", err)
	}
	second, err := svc.EnsureOwner(ctx, "u1", 1)
	if err != nil {
		t.Fatalf("second EnsureOwner: %v", err)
	}
	if first.ID != second.ID {
		t.Fatalf("EnsureOwner not idempotent: %d vs %d", first.ID, second.ID)
	}
	if len(repo.rows) != 1 {
		t.Fatalf("want exactly 1 row after idempotent EnsureOwner, got %d", len(repo.rows))
	}
}

func TestTenantMemberService_UpdateRole_BlocksDemotingLastOwner(t *testing.T) {
	svc, _ := newServiceWithRepo()
	ctx := context.Background()
	if _, err := svc.EnsureOwner(ctx, "owner", 1); err != nil {
		t.Fatalf("seed: %v", err)
	}
	err := svc.UpdateRole(ctx, "owner", 1, types.TenantRoleAdmin)
	if !errors.Is(err, ErrLastOwner) {
		t.Fatalf("want ErrLastOwner when demoting last owner, got %v", err)
	}
}

func TestTenantMemberService_UpdateRole_AllowsDemotionWhenOtherOwnerExists(t *testing.T) {
	svc, _ := newServiceWithRepo()
	ctx := context.Background()
	if _, err := svc.EnsureOwner(ctx, "owner1", 1); err != nil {
		t.Fatalf("seed1: %v", err)
	}
	if _, err := svc.AddMember(ctx, "owner2", 1, types.TenantRoleOwner, nil); err != nil {
		t.Fatalf("seed2: %v", err)
	}
	if err := svc.UpdateRole(ctx, "owner1", 1, types.TenantRoleAdmin); err != nil {
		t.Fatalf("UpdateRole: %v", err)
	}
}

func TestTenantMemberService_UpdateRole_NoopOnSameRole(t *testing.T) {
	svc, _ := newServiceWithRepo()
	ctx := context.Background()
	if _, err := svc.EnsureOwner(ctx, "owner", 1); err != nil {
		t.Fatalf("seed: %v", err)
	}
	// 把"还是 Owner"作为 no-op 处理，必须不触发 ErrLastOwner（同一角色不算降级）。
	if err := svc.UpdateRole(ctx, "owner", 1, types.TenantRoleOwner); err != nil {
		t.Fatalf("UpdateRole same role should be a no-op, got %v", err)
	}
}

func TestTenantMemberService_UpdateRole_RejectsInvalidRole(t *testing.T) {
	svc, _ := newServiceWithRepo()
	ctx := context.Background()
	if _, err := svc.EnsureOwner(ctx, "owner", 1); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := svc.UpdateRole(ctx, "owner", 1, types.TenantRole("nope")); !errors.Is(err, ErrInvalidTenantRole) {
		t.Fatalf("want ErrInvalidTenantRole, got %v", err)
	}
}

func TestTenantMemberService_UpdateRole_ReturnsNotFound(t *testing.T) {
	svc, _ := newServiceWithRepo()
	if err := svc.UpdateRole(context.Background(), "ghost", 1, types.TenantRoleAdmin); !errors.Is(err, ErrMembershipNotFound) {
		t.Fatalf("want ErrMembershipNotFound, got %v", err)
	}
}

func TestTenantMemberService_RemoveMember_BlocksLastOwner(t *testing.T) {
	svc, _ := newServiceWithRepo()
	ctx := context.Background()
	if _, err := svc.EnsureOwner(ctx, "owner", 1); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := svc.RemoveMember(ctx, "owner", 1); !errors.Is(err, ErrLastOwner) {
		t.Fatalf("want ErrLastOwner, got %v", err)
	}
}

func TestTenantMemberService_RemoveMember_AllowsContributorRemoval(t *testing.T) {
	svc, _ := newServiceWithRepo()
	ctx := context.Background()
	if _, err := svc.EnsureOwner(ctx, "owner", 1); err != nil {
		t.Fatalf("seed owner: %v", err)
	}
	if _, err := svc.AddMember(ctx, "contrib", 1, types.TenantRoleContributor, nil); err != nil {
		t.Fatalf("seed contributor: %v", err)
	}
	if err := svc.RemoveMember(ctx, "contrib", 1); err != nil {
		t.Fatalf("RemoveMember: %v", err)
	}
	got, _ := svc.GetMembership(ctx, "contrib", 1)
	if got != nil {
		t.Fatalf("contributor should be soft-deleted, got %+v", got)
	}
}

func TestTenantMemberService_RemoveMember_ReturnsNotFound(t *testing.T) {
	svc, _ := newServiceWithRepo()
	if err := svc.RemoveMember(context.Background(), "ghost", 1); !errors.Is(err, ErrMembershipNotFound) {
		t.Fatalf("want ErrMembershipNotFound, got %v", err)
	}
}

// The TOCTOU race the atomic helpers were introduced for: two Owner
// rows, demoting both must keep at least one. Sequentially via the
// service the second call must observe the post-first-demote state
// and refuse with ErrLastOwner. (True concurrent demotes are
// exercised at the repo layer with a real DB; here we pin the
// service-level contract.)
func TestTenantMemberService_UpdateRole_AtomicDemoteRejectsSecondLastOwner(t *testing.T) {
	svc, repo := newServiceWithRepo()
	ctx := context.Background()
	const tenantID uint64 = 7
	for _, uid := range []string{"a", "b"} {
		repo.rows = append(repo.rows, &types.TenantMember{
			ID: uint64(len(repo.rows) + 1), UserID: uid, TenantID: tenantID,
			Role: types.TenantRoleOwner, Status: types.TenantMemberStatusActive,
		})
	}
	if err := svc.UpdateRole(ctx, "a", tenantID, types.TenantRoleViewer); err != nil {
		t.Fatalf("first demote should succeed, got %v", err)
	}
	if err := svc.UpdateRole(ctx, "b", tenantID, types.TenantRoleViewer); !errors.Is(err, ErrLastOwner) {
		t.Fatalf("second demote must hit ErrLastOwner, got %v", err)
	}
}

func TestTenantMemberService_RemoveMember_AtomicRemoveRejectsSecondLastOwner(t *testing.T) {
	svc, repo := newServiceWithRepo()
	ctx := context.Background()
	const tenantID uint64 = 7
	for _, uid := range []string{"a", "b"} {
		repo.rows = append(repo.rows, &types.TenantMember{
			ID: uint64(len(repo.rows) + 1), UserID: uid, TenantID: tenantID,
			Role: types.TenantRoleOwner, Status: types.TenantMemberStatusActive,
		})
	}
	if err := svc.RemoveMember(ctx, "a", tenantID); err != nil {
		t.Fatalf("first remove should succeed, got %v", err)
	}
	if err := svc.RemoveMember(ctx, "b", tenantID); !errors.Is(err, ErrLastOwner) {
		t.Fatalf("second remove must hit ErrLastOwner, got %v", err)
	}
}

func TestTenantRole_HasPermission(t *testing.T) {
	cases := []struct {
		caller   types.TenantRole
		required types.TenantRole
		want     bool
	}{
		{types.TenantRoleOwner, types.TenantRoleAdmin, true},
		{types.TenantRoleAdmin, types.TenantRoleOwner, false},
		{types.TenantRoleContributor, types.TenantRoleViewer, true},
		{types.TenantRoleViewer, types.TenantRoleContributor, false},
		{types.TenantRole("bogus"), types.TenantRoleViewer, false},
	}
	for _, c := range cases {
		if got := c.caller.HasPermission(c.required); got != c.want {
			t.Errorf("HasPermission(%s, %s) = %v, want %v", c.caller, c.required, got, c.want)
		}
	}
}
