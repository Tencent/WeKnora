package service

import (
	"context"
	"errors"
	"sort"
	"testing"
	"time"

	apprepo "github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// fakeInvitationRepo is an in-memory stand-in for
// interfaces.TenantInvitationRepository. The implementation is
// deliberately tiny — these tests focus on the service-layer state
// machine (Accept / Decline / Revoke / lazy sweep / duplicate guard)
// rather than the SQL surface. The production repo gets exercised via
// the real DB in repo tests if/when those are added.
type fakeInvitationRepo struct {
	rows   []*types.TenantInvitation
	nextID uint64
}

func newFakeInvitationRepo() *fakeInvitationRepo { return &fakeInvitationRepo{} }

func (r *fakeInvitationRepo) Create(ctx context.Context, inv *types.TenantInvitation) error {
	for _, e := range r.rows {
		if e.TenantID == inv.TenantID &&
			e.InviteeUserID == inv.InviteeUserID &&
			e.Status == types.TenantInvitationStatusPending {
			return apprepo.ErrPendingInvitationExists
		}
	}
	r.nextID++
	inv.ID = r.nextID
	inv.CreatedAt = time.Now()
	inv.UpdatedAt = inv.CreatedAt
	cp := *inv
	r.rows = append(r.rows, &cp)
	return nil
}

func (r *fakeInvitationRepo) GetByID(ctx context.Context, id uint64) (*types.TenantInvitation, error) {
	for _, e := range r.rows {
		if e.ID == id {
			cp := *e
			return &cp, nil
		}
	}
	return nil, nil
}

func (r *fakeInvitationRepo) GetPendingByPair(
	ctx context.Context, tenantID uint64, inviteeUserID string,
) (*types.TenantInvitation, error) {
	for _, e := range r.rows {
		if e.TenantID == tenantID &&
			e.InviteeUserID == inviteeUserID &&
			e.Status == types.TenantInvitationStatusPending {
			cp := *e
			return &cp, nil
		}
	}
	return nil, nil
}

func (r *fakeInvitationRepo) GetActiveByToken(
	ctx context.Context, token string,
) (*types.TenantInvitation, error) {
	if token == "" {
		return nil, nil
	}
	for _, e := range r.rows {
		if e.Token == token &&
			e.Status == types.TenantInvitationStatusPending {
			cp := *e
			return &cp, nil
		}
	}
	return nil, nil
}

func (r *fakeInvitationRepo) filteredTenantRows(
	tenantID uint64, includeTerminal bool,
) []*types.TenantInvitation {
	var out []*types.TenantInvitation
	for _, e := range r.rows {
		if e.TenantID != tenantID {
			continue
		}
		if !includeTerminal && e.Status != types.TenantInvitationStatusPending {
			continue
		}
		cp := *e
		out = append(out, &cp)
	}
	sort.SliceStable(out, func(i, j int) bool {
		return out[i].ID > out[j].ID
	})
	return out
}

func (r *fakeInvitationRepo) ListByTenant(
	ctx context.Context, tenantID uint64, includeTerminal bool,
) ([]*types.TenantInvitation, error) {
	return r.filteredTenantRows(tenantID, includeTerminal), nil
}

func (r *fakeInvitationRepo) CountByTenantList(
	ctx context.Context,
	tenantID uint64,
	includeTerminal bool,
) (int64, error) {
	return int64(len(r.filteredTenantRows(tenantID, includeTerminal))), nil
}

func (r *fakeInvitationRepo) ListByTenantPage(
	ctx context.Context,
	tenantID uint64,
	includeTerminal bool,
	offset, limit int,
) ([]*types.TenantInvitation, error) {
	all := r.filteredTenantRows(tenantID, includeTerminal)
	if offset >= len(all) {
		return []*types.TenantInvitation{}, nil
	}
	end := offset + limit
	if end > len(all) {
		end = len(all)
	}
	return append([]*types.TenantInvitation(nil), all[offset:end]...), nil
}

func (r *fakeInvitationRepo) ListByInvitee(
	ctx context.Context, inviteeUserID string, includeTerminal bool,
) ([]*types.TenantInvitation, error) {
	var out []*types.TenantInvitation
	for _, e := range r.rows {
		if e.InviteeUserID != inviteeUserID {
			continue
		}
		if !includeTerminal && e.Status != types.TenantInvitationStatusPending {
			continue
		}
		cp := *e
		out = append(out, &cp)
	}
	return out, nil
}

func (r *fakeInvitationRepo) CountPendingByInvitee(
	ctx context.Context, inviteeUserID string,
) (int64, error) {
	var n int64
	for _, e := range r.rows {
		if e.InviteeUserID == inviteeUserID &&
			e.Status == types.TenantInvitationStatusPending {
			n++
		}
	}
	return n, nil
}

func (r *fakeInvitationRepo) MarkStatusIfPending(
	ctx context.Context,
	id uint64,
	status types.TenantInvitationStatus,
	respondedAt time.Time,
) error {
	for _, e := range r.rows {
		if e.ID == id && e.Status == types.TenantInvitationStatusPending {
			e.Status = status
			e.RespondedAt = &respondedAt
			e.UpdatedAt = time.Now()
			return nil
		}
	}
	return gormErrRecordNotFound
}

func (r *fakeInvitationRepo) SweepExpired(
	ctx context.Context, now time.Time,
) (int64, error) {
	var n int64
	for _, e := range r.rows {
		if e.Status == types.TenantInvitationStatusPending && e.ExpiresAt.Before(now) {
			e.Status = types.TenantInvitationStatusExpired
			e.RespondedAt = &now
			e.UpdatedAt = time.Now()
			n++
		}
	}
	return n, nil
}

func (r *fakeInvitationRepo) IncrementAcceptedCount(
	ctx context.Context, id uint64,
) error {
	for _, e := range r.rows {
		if e.ID == id {
			e.AcceptedCount++
			e.UpdatedAt = time.Now()
			return nil
		}
	}
	return gormErrRecordNotFound
}

var _ interfaces.TenantInvitationRepository = (*fakeInvitationRepo)(nil)

// newInvitationSvc returns a service wired against in-memory fakes for
// both the invitation repo and the member service. Audit is nil so
// the lazy emitAudit short-circuit keeps the test surface focused.
func newInvitationSvc() (
	interfaces.TenantInvitationService,
	*fakeInvitationRepo,
	interfaces.TenantMemberService,
) {
	invRepo := newFakeInvitationRepo()
	memberSvc, _ := newServiceWithRepo()
	svc := NewTenantInvitationService(invRepo, memberSvc, nil)
	return svc, invRepo, memberSvc
}

func TestInvitationService_Create_RejectsInvalidRole(t *testing.T) {
	svc, _, _ := newInvitationSvc()
	_, err := svc.Create(context.Background(), 1, "u-bob", types.TenantRole("magician"), nil, "")
	if !errors.Is(err, ErrInvalidTenantRole) {
		t.Fatalf("want ErrInvalidTenantRole, got %v", err)
	}
}

func TestInvitationService_Create_RejectsAlreadyActiveMember(t *testing.T) {
	svc, _, memberSvc := newInvitationSvc()
	ctx := context.Background()
	if _, err := memberSvc.AddMember(ctx, "u-bob", 1, types.TenantRoleContributor, nil); err != nil {
		t.Fatalf("seed: %v", err)
	}
	_, err := svc.Create(ctx, 1, "u-bob", types.TenantRoleContributor, nil, "")
	if !errors.Is(err, ErrAlreadyMember) {
		t.Fatalf("want ErrAlreadyMember, got %v", err)
	}
}

func TestInvitationService_Create_DedupsPending(t *testing.T) {
	svc, _, _ := newInvitationSvc()
	ctx := context.Background()
	if _, err := svc.Create(ctx, 1, "u-bob", types.TenantRoleContributor, nil, ""); err != nil {
		t.Fatalf("first invite: %v", err)
	}
	_, err := svc.Create(ctx, 1, "u-bob", types.TenantRoleContributor, nil, "")
	if !errors.Is(err, ErrPendingInvitationExists) {
		t.Fatalf("want ErrPendingInvitationExists, got %v", err)
	}
}

func TestInvitationService_Accept_OnlyByInvitee(t *testing.T) {
	svc, _, _ := newInvitationSvc()
	ctx := context.Background()
	inv, err := svc.Create(ctx, 1, "u-bob", types.TenantRoleViewer, nil, "")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := svc.Accept(ctx, inv.ID, "u-eve"); !errors.Is(err, ErrInvitationForbidden) {
		t.Fatalf("non-invitee must be forbidden, got %v", err)
	}
}

func TestInvitationService_Accept_HappyPath_CreatesMembership(t *testing.T) {
	svc, _, memberSvc := newInvitationSvc()
	ctx := context.Background()
	inv, err := svc.Create(ctx, 1, "u-bob", types.TenantRoleAdmin, nil, "")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	mb, err := svc.Accept(ctx, inv.ID, "u-bob")
	if err != nil {
		t.Fatalf("accept: %v", err)
	}
	if mb == nil || mb.UserID != "u-bob" || mb.Role != types.TenantRoleAdmin {
		t.Fatalf("unexpected membership: %+v", mb)
	}
	// Re-acceptance must be a state-machine rejection, not silent
	// success — otherwise a duplicate click double-creates audit rows.
	if _, err := svc.Accept(ctx, inv.ID, "u-bob"); !errors.Is(err, ErrInvitationNotPending) {
		t.Fatalf("re-accept must reject as ErrInvitationNotPending, got %v", err)
	}
	// And the membership service must see the row exactly once.
	got, _ := memberSvc.GetMembership(ctx, "u-bob", 1)
	if got == nil {
		t.Fatalf("membership row missing after accept")
	}
}

func TestInvitationService_Accept_IdempotentWhenAlreadyMember(t *testing.T) {
	// If the invitee somehow became an active member between Create
	// and Accept (e.g. a parallel direct-add via POST /members), the
	// Accept call must not 500 — it should fold gracefully into "you
	// are already in" and flip the invitation to accepted for audit.
	svc, invRepo, memberSvc := newInvitationSvc()
	ctx := context.Background()
	inv, err := svc.Create(ctx, 1, "u-bob", types.TenantRoleContributor, nil, "")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	// Side-effect: mint the membership directly.
	if _, err := memberSvc.AddMember(ctx, "u-bob", 1, types.TenantRoleViewer, nil); err != nil {
		t.Fatalf("side-effect AddMember: %v", err)
	}
	mb, err := svc.Accept(ctx, inv.ID, "u-bob")
	if err != nil {
		t.Fatalf("accept must be idempotent, got %v", err)
	}
	if mb == nil || mb.Role != types.TenantRoleViewer {
		// Returns the pre-existing membership; the role on the
		// invitation does NOT clobber a real membership row.
		t.Fatalf("unexpected membership after idempotent accept: %+v", mb)
	}
	// Invitation row must reflect accepted state so the inbox no
	// longer surfaces it as pending.
	row, _ := invRepo.GetByID(ctx, inv.ID)
	if row == nil || row.Status != types.TenantInvitationStatusAccepted {
		t.Fatalf("invitation must be accepted after idempotent accept, got %+v", row)
	}
}

func TestInvitationService_Decline_MarksDeclined(t *testing.T) {
	svc, invRepo, _ := newInvitationSvc()
	ctx := context.Background()
	inv, err := svc.Create(ctx, 1, "u-bob", types.TenantRoleViewer, nil, "")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := svc.Decline(ctx, inv.ID, "u-bob"); err != nil {
		t.Fatalf("decline: %v", err)
	}
	row, _ := invRepo.GetByID(ctx, inv.ID)
	if row.Status != types.TenantInvitationStatusDeclined {
		t.Fatalf("want declined status, got %s", row.Status)
	}
	if err := svc.Decline(ctx, inv.ID, "u-bob"); !errors.Is(err, ErrInvitationNotPending) {
		t.Fatalf("double-decline must reject, got %v", err)
	}
}

func TestInvitationService_Revoke_MarksRevoked(t *testing.T) {
	svc, invRepo, _ := newInvitationSvc()
	ctx := context.Background()
	inv, err := svc.Create(ctx, 1, "u-bob", types.TenantRoleAdmin, nil, "")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := svc.Revoke(ctx, inv.ID); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	row, _ := invRepo.GetByID(ctx, inv.ID)
	if row.Status != types.TenantInvitationStatusRevoked {
		t.Fatalf("want revoked status, got %s", row.Status)
	}
	// Revoked rows can be re-invited via a fresh Create — the
	// partial unique index only guards PENDING.
	if _, err := svc.Create(ctx, 1, "u-bob", types.TenantRoleContributor, nil, ""); err != nil {
		t.Fatalf("re-invite after revoke must succeed, got %v", err)
	}
}

func TestInvitationService_LazySweepExpires(t *testing.T) {
	// Drop a row directly into the repo with an ExpiresAt in the past,
	// then call ListByInvitee — the sweep must flip it to expired
	// before the read returns.
	svc, invRepo, _ := newInvitationSvc()
	now := time.Now()
	stale := &types.TenantInvitation{
		TenantID:      1,
		InviteeUserID: "u-bob",
		Role:          types.TenantRoleViewer,
		Status:        types.TenantInvitationStatusPending,
		ExpiresAt:     now.Add(-time.Hour),
	}
	if err := invRepo.Create(context.Background(), stale); err != nil {
		t.Fatalf("seed stale: %v", err)
	}

	rows, err := svc.ListByInvitee(context.Background(), "u-bob", true)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(rows) != 1 || rows[0].Status != types.TenantInvitationStatusExpired {
		t.Fatalf("expected expired row after sweep, got %+v", rows)
	}

	// And accepting an expired invitation must fail loudly rather than
	// quietly creating the membership.
	if _, err := svc.Accept(context.Background(), stale.ID, "u-bob"); err == nil {
		t.Fatalf("accept on expired invitation must fail")
	}
}

func TestInvitationService_CountPending(t *testing.T) {
	svc, _, _ := newInvitationSvc()
	ctx := context.Background()
	if _, err := svc.Create(ctx, 1, "u-bob", types.TenantRoleViewer, nil, ""); err != nil {
		t.Fatalf("seed1: %v", err)
	}
	if _, err := svc.Create(ctx, 2, "u-bob", types.TenantRoleAdmin, nil, ""); err != nil {
		t.Fatalf("seed2: %v", err)
	}
	n, err := svc.CountPendingByInvitee(ctx, "u-bob")
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 2 {
		t.Fatalf("want 2 pending, got %d", n)
	}
	// Decline one; count drops.
	rows, _ := svc.ListByInvitee(ctx, "u-bob", false)
	if err := svc.Decline(ctx, rows[0].ID, "u-bob"); err != nil {
		t.Fatalf("decline: %v", err)
	}
	n, _ = svc.CountPendingByInvitee(ctx, "u-bob")
	if n != 1 {
		t.Fatalf("want 1 pending after decline, got %d", n)
	}
}

// --- share-link tests ---------------------------------------------------

func TestInvitationService_CreateShareLink_PersistsToken(t *testing.T) {
	svc, repo, _ := newInvitationSvc()
	inv, plain, err := svc.CreateShareLink(
		context.Background(), 1, types.TenantRoleContributor, nil, "")
	if err != nil {
		t.Fatalf("create-share-link: %v", err)
	}
	if plain == "" {
		t.Fatalf("plain token must be returned for the URL")
	}
	row, _ := repo.GetByID(context.Background(), inv.ID)
	if row == nil {
		t.Fatalf("row missing")
	}
	if row.Token != plain {
		t.Fatalf("DB must store plaintext token (multi-use, short-lived), got %q vs %q", row.Token, plain)
	}
	if row.InviteeUserID != "" {
		t.Fatalf("share-link rows must have empty invitee_user_id, got %q", row.InviteeUserID)
	}
	if row.Status != types.TenantInvitationStatusPending {
		t.Fatalf("status must be pending, got %s", row.Status)
	}
}

func TestInvitationService_CreateShareLink_RejectsInvalidRole(t *testing.T) {
	svc, _, _ := newInvitationSvc()
	_, _, err := svc.CreateShareLink(
		context.Background(), 1, types.TenantRole("magician"), nil, "")
	if !errors.Is(err, ErrInvalidTenantRole) {
		t.Fatalf("want ErrInvalidTenantRole, got %v", err)
	}
}

func TestInvitationService_LookupByToken_RejectsUnknownToken(t *testing.T) {
	svc, _, _ := newInvitationSvc()
	if _, err := svc.LookupByToken(context.Background(), "nonexistent"); !errors.Is(err, ErrInvitationTokenInvalid) {
		t.Fatalf("want ErrInvitationTokenInvalid, got %v", err)
	}
}

func TestInvitationService_LookupByToken_RejectsExpired(t *testing.T) {
	svc, repo, _ := newInvitationSvc()
	_, plain, err := svc.CreateShareLink(context.Background(), 1, types.TenantRoleViewer, nil, "")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	for _, r := range repo.rows {
		r.ExpiresAt = time.Now().Add(-time.Hour)
	}
	if _, err := svc.LookupByToken(context.Background(), plain); !errors.Is(err, ErrInvitationTokenInvalid) {
		t.Fatalf("expired token must reject, got %v", err)
	}
}

func TestInvitationService_AcceptByToken_HappyPath(t *testing.T) {
	svc, repo, memberSvc := newInvitationSvc()
	ctx := context.Background()
	_, plain, err := svc.CreateShareLink(ctx, 1, types.TenantRoleAdmin, nil, "")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	mb, err := svc.AcceptByToken(ctx, plain, "u-alice")
	if err != nil {
		t.Fatalf("accept-by-token: %v", err)
	}
	if mb == nil || mb.UserID != "u-alice" || mb.Role != types.TenantRoleAdmin {
		t.Fatalf("unexpected membership: %+v", mb)
	}
	if got, _ := memberSvc.GetMembership(ctx, "u-alice", 1); got == nil {
		t.Fatalf("membership row missing")
	}
	// Multi-use: row stays pending.
	rows, _ := repo.ListByTenant(ctx, 1, true)
	if len(rows) == 0 || rows[0].Status != types.TenantInvitationStatusPending {
		t.Fatalf("share-link row must remain pending after accept, got %+v", rows)
	}
}

func TestInvitationService_AcceptByToken_AllowsMultipleUsers(t *testing.T) {
	svc, _, memberSvc := newInvitationSvc()
	ctx := context.Background()
	_, plain, err := svc.CreateShareLink(ctx, 1, types.TenantRoleViewer, nil, "")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := svc.AcceptByToken(ctx, plain, "u-alice"); err != nil {
		t.Fatalf("accept alice: %v", err)
	}
	if _, err := svc.AcceptByToken(ctx, plain, "u-bob"); err != nil {
		t.Fatalf("accept bob: %v", err)
	}
	a, _ := memberSvc.GetMembership(ctx, "u-alice", 1)
	b, _ := memberSvc.GetMembership(ctx, "u-bob", 1)
	if a == nil || b == nil {
		t.Fatalf("both users should have membership: alice=%v bob=%v", a, b)
	}
}

func TestInvitationService_AcceptByToken_RevokedTokenRejected(t *testing.T) {
	svc, _, _ := newInvitationSvc()
	ctx := context.Background()
	inv, plain, err := svc.CreateShareLink(ctx, 1, types.TenantRoleViewer, nil, "")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := svc.Revoke(ctx, inv.ID); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	if _, err := svc.AcceptByToken(ctx, plain, "u-alice"); !errors.Is(err, ErrInvitationTokenInvalid) {
		t.Fatalf("revoked token must reject, got %v", err)
	}
}

func TestInvitationService_AcceptByToken_RequiresUserID(t *testing.T) {
	svc, _, _ := newInvitationSvc()
	ctx := context.Background()
	_, plain, err := svc.CreateShareLink(ctx, 1, types.TenantRoleViewer, nil, "")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := svc.AcceptByToken(ctx, plain, ""); err == nil {
		t.Fatalf("empty user id must be rejected")
	}
}
