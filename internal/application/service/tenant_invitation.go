package service

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"strconv"
	"strings"
	"time"

	apprepo "github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// Sentinel errors returned by tenantInvitationService. Callers compare
// with errors.Is to render the appropriate HTTP responses.
var (
	// ErrInvitationNotFound is returned when no invitation row matches
	// the supplied id.
	ErrInvitationNotFound = errors.New("invitation not found")

	// ErrPendingInvitationExists is returned by Create when a pending
	// invitation for (tenant, invitee) is already in flight. The
	// handler maps this to 409.
	ErrPendingInvitationExists = errors.New("a pending invitation for this user already exists")

	// ErrAlreadyMember is returned by Create when the invitee is
	// already an active member of the tenant; sending an invite would
	// be a no-op at best and a confusing UX at worst.
	ErrAlreadyMember = errors.New("user is already an active member of the tenant")

	// ErrInvitationNotPending is returned by Accept / Decline / Revoke
	// when the row exists but has already been finalised. Maps to 409.
	ErrInvitationNotPending = errors.New("invitation is no longer pending")

	// ErrInvitationExpired is returned by Accept / Decline when the
	// pending row has aged past its expires_at. Maps to 410.
	ErrInvitationExpired = errors.New("invitation has expired")

	// ErrInvitationForbidden is returned by Accept / Decline when the
	// caller is not the invitee. Admin-driven Revoke is gated at the
	// route layer so this error only surfaces on the /me/ paths.
	ErrInvitationForbidden = errors.New("only the invitee can accept or decline this invitation")

	// ErrInvitationTokenInvalid is returned by LookupByToken /
	// AcceptByToken when the supplied plaintext token does not match
	// any active share-link row. The handler maps this to 410 Gone.
	// We deliberately collapse "unknown" / "expired" / "revoked" into
	// a single sentinel so an attacker can't probe which slots used
	// to exist.
	ErrInvitationTokenInvalid = errors.New("invitation token is invalid or has been revoked")

	// ErrOrgUnitRequired is returned when the tenant has an OrgUnit
	// hierarchy and the invitation did not specify which unit the
	// invitee should join.
	ErrOrgUnitRequired = errors.New("org_unit_id is required when the tenant has an organization hierarchy")

	// ErrInviterOrgUnitRequired is returned when hierarchy exists but
	// the inviter has no active OrgUnit (X-Org-Unit-ID / primary).
	ErrInviterOrgUnitRequired = errors.New("inviter must select a current organization before inviting")

	// ErrOrgUnitNotInviteable is returned when the target OrgUnit is
	// outside the inviter's own-unit + descendant scope.
	ErrOrgUnitNotInviteable = errors.New("org unit is outside the inviter's own unit or subordinate scope")

	// ErrOnlySystemAdminCanAssignOwner is returned when a non-system-admin
	// tries to invite or share-link with TenantRoleOwner. Product UI no
	// longer exposes owner; system admin may still assign for migration /
	// bootstrap. Self-serve registration still creates a personal-space
	// owner via EnsureOwner.
	ErrOnlySystemAdminCanAssignOwner = errors.New(
		"only system admin can assign the owner role",
	)
)

// defaultInvitationTTL is the lifetime of a pending invitation before
// the lazy sweep transitions it to expired. Operator override is via
// the WEKNORA_INVITATION_TTL env var (Go duration: "168h", "7d-ish");
// keeping this out of TenantConfig avoids a yaml migration for a knob
// that almost nobody is going to tweak.
const defaultInvitationTTL = 7 * 24 * time.Hour

// invitationTTL resolves the effective TTL at call time so an operator
// can hot-reload the override without restarting. The env var is parsed
// once per call; cost is negligible and beats a goroutine watching the
// environment.
func invitationTTL() time.Duration {
	raw := os.Getenv("WEKNORA_INVITATION_TTL")
	if raw == "" {
		return defaultInvitationTTL
	}
	if d, err := time.ParseDuration(raw); err == nil && d > 0 {
		return d
	}
	// Allow "604800" seconds for callers that don't like Go duration
	// syntax — same shape as the rest of our int env knobs.
	if secs, err := strconv.ParseInt(raw, 10, 64); err == nil && secs > 0 {
		return time.Duration(secs) * time.Second
	}
	return defaultInvitationTTL
}

// tenantInvitationService implements interfaces.TenantInvitationService.
type tenantInvitationService struct {
	repo           interfaces.TenantInvitationRepository
	memberSvc      interfaces.TenantMemberService
	orgUnitService interfaces.OrgUnitService // optional; nil ⇒ skip hierarchy binding
	audit          interfaces.AuditLogService // optional; nil ⇒ no audit, business ops still succeed
	now            func() time.Time           // injection seam for tests
}

// NewTenantInvitationService wires the dependencies. memberSvc is
// required (Accept must create the tenant_members row); orgUnitService
// and audit are optional.
func NewTenantInvitationService(
	repo interfaces.TenantInvitationRepository,
	memberSvc interfaces.TenantMemberService,
	orgUnitService interfaces.OrgUnitService,
	audit interfaces.AuditLogService,
) interfaces.TenantInvitationService {
	return &tenantInvitationService{
		repo:           repo,
		memberSvc:      memberSvc,
		orgUnitService: orgUnitService,
		audit:          audit,
		now:            time.Now,
	}
}

// emitAudit is the best-effort audit hook; mirrors tenantMemberService.
func (s *tenantInvitationService) emitAudit(ctx context.Context, entry *types.AuditLog) {
	if s.audit == nil {
		return
	}
	_ = s.audit.Log(ctx, entry)
}

// detailsFor packs the role + invitation id into the audit Details so a
// reader can reconstruct "Alice invited Bob as Admin (inv #42)" without
// joining back to the invitations table.
func detailsFor(invID uint64, role types.TenantRole, orgUnitID string) types.JSON {
	payload := map[string]any{
		"invitation_id": invID,
		"role":          string(role),
	}
	if orgUnitID != "" {
		payload["org_unit_id"] = orgUnitID
	}
	b, _ := json.Marshal(payload)
	return types.JSON(b)
}

// resolveOrgUnitID validates and normalises the OrgUnit binding for an
// invitation. When the tenant has no hierarchy the field is cleared.
// When hierarchy exists, a non-empty unit in the inviter's inviteable
// scope is required (本级/下级; Owner/Admin role grant → 下级 only).
func (s *tenantInvitationService) resolveOrgUnitID(
	ctx context.Context,
	tenantID uint64,
	orgUnitID string,
	role types.TenantRole,
) (string, error) {
	orgUnitID = strings.TrimSpace(orgUnitID)
	if s.orgUnitService == nil {
		return "", nil
	}
	has, err := s.orgUnitService.HasHierarchy(ctx, tenantID)
	if err != nil {
		return "", err
	}
	if !has {
		return "", nil
	}
	if orgUnitID == "" {
		return "", ErrOrgUnitRequired
	}
	actorOrgUnitID, _ := types.OrgUnitIDFromContext(ctx)
	actorOrgUnitID = strings.TrimSpace(actorOrgUnitID)
	if actorOrgUnitID == "" {
		// System admin / tenant Owner bootstrap: no current unit yet —
		// only require the target to exist in this tenant.
		if !isUnscopedOrgInviter(ctx) {
			return "", ErrInviterOrgUnitRequired
		}
		if _, err := s.orgUnitService.Get(ctx, tenantID, orgUnitID); err != nil {
			return "", err
		}
		return orgUnitID, nil
	}
	ok, err := s.orgUnitService.CanInviteToOrgUnit(
		ctx, tenantID, actorOrgUnitID, orgUnitID, role,
	)
	if err != nil {
		return "", err
	}
	if !ok {
		return "", ErrOrgUnitNotInviteable
	}
	return orgUnitID, nil
}

// assignOrgUnitMembership places the user into the invitation's OrgUnit
// via TransferMember (single-membership path). Covers re-invite / data
// repair when the user already belongs elsewhere. Best-effort after
// tenant membership succeeds — failure is logged so Accept still
// returns the membership.
func (s *tenantInvitationService) assignOrgUnitMembership(
	ctx context.Context,
	tenantID uint64,
	userID string,
	orgUnitID string,
) {
	if s.orgUnitService == nil || orgUnitID == "" || userID == "" {
		return
	}
	if _, err := s.orgUnitService.TransferMember(
		ctx, tenantID, userID, orgUnitID,
	); err != nil {
		logger.Errorf(ctx,
			"invitation accepted but org_unit membership failed: tenant=%d user=%s unit=%s err=%v",
			tenantID, userID, orgUnitID, err)
	}
}

// sweep transitions overdue pending rows to expired before any List/
// Accept/Decline/Count read. Failures are logged and swallowed — a
// transient sweep error must not block a user from reading their
// inbox; the next call will sweep again.
func (s *tenantInvitationService) sweep(ctx context.Context) {
	if _, err := s.repo.SweepExpired(ctx, s.now()); err != nil {
		logger.Warnf(ctx, "tenant_invitation lazy sweep failed: %v", err)
	}
}

// Create issues a new pending invitation. Returns the standard
// conflict sentinels for the duplicate-pending and already-member
// cases.
func (s *tenantInvitationService) Create(
	ctx context.Context,
	tenantID uint64,
	inviteeUserID string,
	role types.TenantRole,
	invitedBy *string,
	message string,
	orgUnitID string,
) (*types.TenantInvitation, error) {
	if !role.IsValid() {
		return nil, ErrInvalidTenantRole
	}
	if err := rejectAPIKeyOwnerAssignment(ctx, role); err != nil {
		return nil, err
	}
	if role == types.TenantRoleOwner && !types.IsSystemAdminActor(ctx) {
		return nil, ErrOnlySystemAdminCanAssignOwner
	}
	resolvedUnit, err := s.resolveOrgUnitID(ctx, tenantID, orgUnitID, role)
	if err != nil {
		return nil, err
	}
	// Reject early if the invitee is already an active member; the
	// handler renders this as "they're already in" rather than the
	// generic conflict.
	existing, err := s.memberSvc.GetMembership(ctx, inviteeUserID, tenantID)
	if err != nil {
		return nil, err
	}
	if existing != nil && existing.Status == types.TenantMemberStatusActive {
		return nil, ErrAlreadyMember
	}

	now := s.now()
	inv := &types.TenantInvitation{
		TenantID:      tenantID,
		InviteeUserID: inviteeUserID,
		InvitedBy:     invitedBy,
		Role:          role,
		OrgUnitID:     resolvedUnit,
		Status:        types.TenantInvitationStatusPending,
		Message:       message,
		ExpiresAt:     now.Add(invitationTTL()),
	}
	if err := s.repo.Create(ctx, inv); err != nil {
		if errors.Is(err, apprepo.ErrPendingInvitationExists) {
			return nil, ErrPendingInvitationExists
		}
		return nil, err
	}

	s.emitAudit(ctx, &types.AuditLog{
		TenantID:     tenantID,
		ActorUserID:  auditActor(ctx),
		ActorRole:    auditActorRole(ctx),
		Action:       types.AuditActionInvitationSent,
		TargetType:   "tenant_invitation",
		TargetID:     strconv.FormatUint(inv.ID, 10),
		TargetUserID: inviteeUserID,
		Outcome:      types.AuditOutcomeSuccess,
		Details:      detailsFor(inv.ID, role, resolvedUnit),
	})
	return inv, nil
}

// Accept transitions a pending invitation into accepted AND creates
// the active tenant_members row in the same flow. We do NOT wrap both
// writes in a single DB transaction because TenantMemberService.AddMember
// owns its own write + audit emit and reaching across services here
// would force the audit-log writes to commit/rollback in lockstep.
// Instead, we order operations so the user-visible failure mode is
// "you couldn't accept the invitation"; if the membership insert fails
// AFTER the invitation transition committed (rare; collision on the
// tenant_members unique index would be the only realistic case), the
// row already in tenant_members wins and a subsequent Accept call sees
// ErrInvitationNotPending which the handler renders as 409.
func (s *tenantInvitationService) Accept(
	ctx context.Context,
	invID uint64,
	callerUserID string,
) (*types.TenantMember, error) {
	s.sweep(ctx)

	inv, err := s.repo.GetByID(ctx, invID)
	if err != nil {
		return nil, err
	}
	if inv == nil {
		return nil, ErrInvitationNotFound
	}
	if inv.InviteeUserID != callerUserID {
		return nil, ErrInvitationForbidden
	}
	if inv.Status != types.TenantInvitationStatusPending {
		return nil, ErrInvitationNotPending
	}
	if inv.IsExpired(s.now()) {
		// The sweep above should have flipped it already, but a row
		// can age past expires_at between the sweep and this read.
		// Treat it as expired regardless.
		return nil, ErrInvitationExpired
	}

	now := s.now()
	if err := s.repo.MarkStatusIfPending(ctx, invID, types.TenantInvitationStatusAccepted, now); err != nil {
		// Another goroutine (concurrent click) won the race. Honour
		// the state machine.
		return nil, ErrInvitationNotPending
	}

	// Create the actual tenant_members row. Cross-service hop: the
	// member service handles its own audit (rbac.member_added) and
	// also enforces the (user, tenant) uniqueness invariant via the
	// repo. If it fails here the invitation is already accepted —
	// see comment above for why we don't rollback the invitation.
	member, err := s.memberSvc.AddMember(ctx, inv.InviteeUserID, inv.TenantID, inv.Role, inv.InvitedBy)
	if err != nil {
		// Special-case "already a member": that's the idempotent
		// outcome we want. Return the existing membership instead of
		// bubbling the error up to the invitee.
		if errors.Is(err, ErrMembershipAlreadyExists) {
			existing, getErr := s.memberSvc.GetMembership(ctx, inv.InviteeUserID, inv.TenantID)
			if getErr == nil && existing != nil {
				s.assignOrgUnitMembership(ctx, inv.TenantID, inv.InviteeUserID, inv.OrgUnitID)
				s.emitInvitationAccepted(ctx, inv)
				return existing, nil
			}
		}
		logger.Errorf(ctx,
			"invitation %d accepted but tenant_members insert failed: %v",
			invID, err)
		return nil, err
	}

	s.assignOrgUnitMembership(ctx, inv.TenantID, inv.InviteeUserID, inv.OrgUnitID)
	s.emitInvitationAccepted(ctx, inv)
	return member, nil
}

// MarkPendingAcceptedIfExists reconciles a stale per-user pending row when
// tenant.auto_accept_invitation joins the invitee directly. member_added
// audit from AddMember is the authoritative trail; we only flip status here.
func (s *tenantInvitationService) MarkPendingAcceptedIfExists(
	ctx context.Context,
	tenantID uint64,
	inviteeUserID string,
) error {
	s.sweep(ctx)
	inv, err := s.repo.GetPendingByPair(ctx, tenantID, inviteeUserID)
	if err != nil {
		return err
	}
	if inv == nil {
		return nil
	}
	return s.repo.MarkStatusIfPending(ctx, inv.ID, types.TenantInvitationStatusAccepted, s.now())
}

// reconcilePendingInvitation closes a per-user invitation after another
// successful join path (for example a multi-use share link) has already
// established the membership. This keeps the invitation inbox consistent
// without turning a bookkeeping failure into a failed join after the member
// row has already been committed.
func (s *tenantInvitationService) reconcilePendingInvitation(
	ctx context.Context,
	tenantID uint64,
	inviteeUserID string,
) {
	if err := s.MarkPendingAcceptedIfExists(ctx, tenantID, inviteeUserID); err != nil {
		logger.Warnf(ctx,
			"failed to reconcile pending invitation for tenant %d user %s: %v",
			tenantID, inviteeUserID, err)
	}
}

// emitInvitationAccepted writes the rbac.invitation_accepted audit row.
// Actor is the invitee (acting on their own inbox); target is the same
// user since the action is self-directed.
func (s *tenantInvitationService) emitInvitationAccepted(ctx context.Context, inv *types.TenantInvitation) {
	s.emitAudit(ctx, &types.AuditLog{
		TenantID:     inv.TenantID,
		ActorUserID:  auditActor(ctx),
		ActorRole:    auditActorRole(ctx),
		Action:       types.AuditActionInvitationAccepted,
		TargetType:   "tenant_invitation",
		TargetID:     strconv.FormatUint(inv.ID, 10),
		TargetUserID: inv.InviteeUserID,
		Outcome:      types.AuditOutcomeSuccess,
		Details:      detailsFor(inv.ID, inv.Role, inv.OrgUnitID),
	})
}

// Decline transitions the pending row into declined. Only the invitee
// themselves can call this.
func (s *tenantInvitationService) Decline(
	ctx context.Context,
	invID uint64,
	callerUserID string,
) error {
	s.sweep(ctx)

	inv, err := s.repo.GetByID(ctx, invID)
	if err != nil {
		return err
	}
	if inv == nil {
		return ErrInvitationNotFound
	}
	if inv.InviteeUserID != callerUserID {
		return ErrInvitationForbidden
	}
	if inv.Status != types.TenantInvitationStatusPending {
		return ErrInvitationNotPending
	}
	if inv.IsExpired(s.now()) {
		return ErrInvitationExpired
	}

	if err := s.repo.MarkStatusIfPending(ctx, invID, types.TenantInvitationStatusDeclined, s.now()); err != nil {
		return ErrInvitationNotPending
	}

	s.emitAudit(ctx, &types.AuditLog{
		TenantID:     inv.TenantID,
		ActorUserID:  auditActor(ctx),
		ActorRole:    auditActorRole(ctx),
		Action:       types.AuditActionInvitationDeclined,
		TargetType:   "tenant_invitation",
		TargetID:     strconv.FormatUint(inv.ID, 10),
		TargetUserID: inv.InviteeUserID,
		Outcome:      types.AuditOutcomeSuccess,
		Details:      detailsFor(inv.ID, inv.Role, inv.OrgUnitID),
	})
	return nil
}

// Revoke transitions the pending row into revoked. Route-layer Admin+
// gate guarantees the caller is allowed to act on this tenant; this
// method does not re-check role.
func (s *tenantInvitationService) Revoke(ctx context.Context, invID uint64) error {
	s.sweep(ctx)

	inv, err := s.repo.GetByID(ctx, invID)
	if err != nil {
		return err
	}
	if inv == nil {
		return ErrInvitationNotFound
	}
	if inv.Status != types.TenantInvitationStatusPending {
		return ErrInvitationNotPending
	}

	if err := s.repo.MarkStatusIfPending(ctx, invID, types.TenantInvitationStatusRevoked, s.now()); err != nil {
		return ErrInvitationNotPending
	}

	s.emitAudit(ctx, &types.AuditLog{
		TenantID:     inv.TenantID,
		ActorUserID:  auditActor(ctx),
		ActorRole:    auditActorRole(ctx),
		Action:       types.AuditActionInvitationRevoked,
		TargetType:   "tenant_invitation",
		TargetID:     strconv.FormatUint(inv.ID, 10),
		TargetUserID: inv.InviteeUserID,
		Outcome:      types.AuditOutcomeSuccess,
		Details:      detailsFor(inv.ID, inv.Role, inv.OrgUnitID),
	})
	return nil
}

// GetByID returns the row or (nil, nil) without sweeping. The handler
// uses this for narrow per-row checks where running a full sweep just
// to read one row would be wasted work; List* paths still sweep.
func (s *tenantInvitationService) GetByID(
	ctx context.Context,
	invID uint64,
) (*types.TenantInvitation, error) {
	return s.repo.GetByID(ctx, invID)
}

// ListByTenant sweeps then returns. The tenant-side management UI
// expects expired rows to surface correctly; running the sweep here
// guarantees the page reflects reality even if no other code path has
// touched the table recently.
func (s *tenantInvitationService) ListByTenant(
	ctx context.Context,
	tenantID uint64,
	includeTerminal bool,
) ([]*types.TenantInvitation, error) {
	s.sweep(ctx)
	return s.repo.ListByTenant(ctx, tenantID, includeTerminal)
}

// ListTenantInvitationsPage sweeps then returns a page plus total rows
// matching the same filter as ListByTenant. Scoped admins only see
// invitations whose target OrgUnit is their home unit or a descendant
// (本级发出/管理范围内的待接受邀请).
func (s *tenantInvitationService) ListTenantInvitationsPage(
	ctx context.Context,
	tenantID uint64,
	includeTerminal bool,
	page, pageSize int,
) ([]*types.TenantInvitation, int64, error) {
	const (
		defSize = 20
		maxSize = 100
	)
	s.sweep(ctx)
	if page < 1 {
		page = 1
	}
	if pageSize < 1 {
		pageSize = defSize
	}
	if pageSize > maxSize {
		pageSize = maxSize
	}
	offset := (page - 1) * pageSize

	orgUnitIDs, restricted, scopeErr := s.resolveInvitationListOrgUnitIDs(ctx, tenantID)
	if scopeErr != nil {
		return nil, 0, scopeErr
	}
	if restricted {
		total, err := s.repo.CountByTenantListInOrgUnits(
			ctx, tenantID, includeTerminal, orgUnitIDs,
		)
		if err != nil {
			return nil, 0, err
		}
		rows, err := s.repo.ListByTenantPageInOrgUnits(
			ctx, tenantID, includeTerminal, offset, pageSize, orgUnitIDs,
		)
		if err != nil {
			return nil, 0, err
		}
		return rows, total, nil
	}

	total, err := s.repo.CountByTenantList(ctx, tenantID, includeTerminal)
	if err != nil {
		return nil, 0, err
	}
	rows, err := s.repo.ListByTenantPage(ctx, tenantID, includeTerminal, offset, pageSize)
	if err != nil {
		return nil, 0, err
	}
	return rows, total, nil
}

// resolveInvitationListOrgUnitIDs returns OrgUnit IDs whose invitations
// a scoped actor may see (home + descendants). restricted=false means
// no filter (Owner / system admin / no hierarchy).
func (s *tenantInvitationService) resolveInvitationListOrgUnitIDs(
	ctx context.Context,
	tenantID uint64,
) ([]string, bool, error) {
	if s.orgUnitService == nil || isUnscopedOrgInviter(ctx) {
		return nil, false, nil
	}
	has, err := s.orgUnitService.HasHierarchy(ctx, tenantID)
	if err != nil {
		return nil, false, err
	}
	if !has {
		return nil, false, nil
	}
	// ListFlat is already scoped to the actor's home subtree.
	units, err := s.orgUnitService.ListFlat(ctx, tenantID)
	if err != nil {
		return nil, false, err
	}
	ids := make([]string, 0, len(units))
	for _, unit := range units {
		if unit != nil && strings.TrimSpace(unit.ID) != "" {
			ids = append(ids, unit.ID)
		}
	}
	return ids, true, nil
}

// ListByInvitee sweeps then returns. Same reasoning as ListByTenant.
func (s *tenantInvitationService) ListByInvitee(
	ctx context.Context,
	inviteeUserID string,
	includeTerminal bool,
) ([]*types.TenantInvitation, error) {
	s.sweep(ctx)
	return s.repo.ListByInvitee(ctx, inviteeUserID, includeTerminal)
}

// CountPendingByInvitee sweeps then counts. The avatar-row badge polls
// this endpoint so a stale sweep would manifest as a phantom "1" on
// the bell icon for the full polling interval; sweeping inline is
// worth the extra UPDATE.
func (s *tenantInvitationService) CountPendingByInvitee(
	ctx context.Context,
	inviteeUserID string,
) (int64, error) {
	s.sweep(ctx)
	return s.repo.CountPendingByInvitee(ctx, inviteeUserID)
}

// invitationTokenBytes is the raw entropy length for share-link
// tokens before base64url encoding. 32 bytes -> 256 bits, well above
// the 128-bit floor for unguessable opaque tokens.
const invitationTokenBytes = 32

// generateShareLinkToken returns a freshly-randomised plaintext token
// (base64url, no padding) for a new share-link invitation.
func generateShareLinkToken() (string, error) {
	buf := make([]byte, invitationTokenBytes)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

// CreateShareLink issues a single-use share-link invitation. The token
// is generated server-side and persisted plaintext on the row so the
// management UI can re-display it while the row is still pending.
// Per-user invitation constraints (already-member, duplicate-pending)
// do NOT apply here: share-link rows have no specific invitee. Once
// one member joins via AcceptByToken the row flips to accepted and
// the token stops working.
func (s *tenantInvitationService) CreateShareLink(
	ctx context.Context,
	tenantID uint64,
	role types.TenantRole,
	invitedBy *string,
	message string,
	orgUnitID string,
) (*types.TenantInvitation, string, error) {
	if !role.IsValid() {
		return nil, "", ErrInvalidTenantRole
	}
	if err := rejectAPIKeyOwnerAssignment(ctx, role); err != nil {
		return nil, "", err
	}
	if role == types.TenantRoleOwner && !types.IsSystemAdminActor(ctx) {
		return nil, "", ErrOnlySystemAdminCanAssignOwner
	}
	resolvedUnit, err := s.resolveOrgUnitID(ctx, tenantID, orgUnitID, role)
	if err != nil {
		return nil, "", err
	}
	token, err := generateShareLinkToken()
	if err != nil {
		return nil, "", err
	}
	now := s.now()
	inv := &types.TenantInvitation{
		TenantID:      tenantID,
		InviteeUserID: "", // share-link rows have no specific invitee
		Token:         token,
		InvitedBy:     invitedBy,
		Role:          role,
		OrgUnitID:     resolvedUnit,
		Status:        types.TenantInvitationStatusPending,
		Message:       message,
		ExpiresAt:     now.Add(invitationTTL()),
	}
	if err := s.repo.Create(ctx, inv); err != nil {
		return nil, "", err
	}
	s.emitAudit(ctx, &types.AuditLog{
		TenantID:    tenantID,
		ActorUserID: auditActor(ctx),
		ActorRole:   auditActorRole(ctx),
		Action:      types.AuditActionInvitationSent,
		TargetType:  "tenant_invitation",
		TargetID:    strconv.FormatUint(inv.ID, 10),
		// TargetUserID intentionally empty — share-link has no invitee yet.
		Outcome: types.AuditOutcomeSuccess,
		Details: detailsFor(inv.ID, role, resolvedUnit),
	})
	return inv, token, nil
}

// LookupByToken resolves a plaintext share-link token to its row.
// Sweeps overdue rows first so an expired link is reflected as
// expired rather than letting the registration page accept it for a
// few extra seconds. Accepted / revoked / expired tokens reject.
func (s *tenantInvitationService) LookupByToken(
	ctx context.Context,
	plainToken string,
) (*types.TenantInvitation, error) {
	plainToken = strings.TrimSpace(plainToken)
	if plainToken == "" {
		return nil, ErrInvitationTokenInvalid
	}
	s.sweep(ctx)
	inv, err := s.repo.GetActiveByToken(ctx, plainToken)
	if err != nil {
		return nil, err
	}
	if inv == nil {
		return nil, ErrInvitationTokenInvalid
	}
	if inv.IsExpired(s.now()) {
		return nil, ErrInvitationTokenInvalid
	}
	return inv, nil
}

// AcceptByToken adds newUserID to the share-link's tenant + role, then
// consumes the link (status → accepted) so it cannot be reused. The
// claim is atomic via MarkStatusIfPending to avoid two concurrent
// registrations sharing one link. If the user already has membership,
// the existing row is returned and the link is still consumed when it
// was still pending (single-use even for re-entry).
func (s *tenantInvitationService) AcceptByToken(
	ctx context.Context,
	plainToken string,
	newUserID string,
) (*types.TenantMember, error) {
	if newUserID == "" {
		return nil, errors.New("newUserID is required")
	}
	inv, err := s.LookupByToken(ctx, plainToken)
	if err != nil {
		return nil, err
	}

	// Single-use: claim the pending row before creating membership so
	// concurrent acceptors cannot both succeed on the same token.
	now := s.now()
	if err := s.repo.MarkStatusIfPending(
		ctx, inv.ID, types.TenantInvitationStatusAccepted, now,
	); err != nil {
		return nil, ErrInvitationTokenInvalid
	}

	member, err := s.memberSvc.AddMember(ctx, newUserID, inv.TenantID, inv.Role, inv.InvitedBy)
	if err != nil {
		if errors.Is(err, ErrMembershipAlreadyExists) {
			existing, getErr := s.memberSvc.GetMembership(ctx, newUserID, inv.TenantID)
			if getErr == nil && existing != nil {
				s.assignOrgUnitMembership(ctx, inv.TenantID, newUserID, inv.OrgUnitID)
				if incErr := s.repo.IncrementAcceptedCount(ctx, inv.ID); incErr != nil {
					logger.Warnf(ctx,
						"share-link %d accepted_count bump failed: %v",
						inv.ID, incErr)
				}
				s.emitAudit(ctx, &types.AuditLog{
					TenantID:     inv.TenantID,
					ActorUserID:  auditActor(ctx),
					ActorRole:    auditActorRole(ctx),
					Action:       types.AuditActionInvitationAccepted,
					TargetType:   "tenant_invitation",
					TargetID:     strconv.FormatUint(inv.ID, 10),
					TargetUserID: newUserID,
					Outcome:      types.AuditOutcomeSuccess,
					Details:      detailsFor(inv.ID, inv.Role, inv.OrgUnitID),
				})
				s.reconcilePendingInvitation(ctx, inv.TenantID, newUserID)
				return existing, nil
			}
		}
		logger.Errorf(ctx,
			"share-link %d accept failed for user %s: %v",
			inv.ID, newUserID, err)
		return nil, err
	}
	s.assignOrgUnitMembership(ctx, inv.TenantID, newUserID, inv.OrgUnitID)
	if incErr := s.repo.IncrementAcceptedCount(ctx, inv.ID); incErr != nil {
		logger.Warnf(ctx,
			"share-link %d accepted_count bump failed (membership still created): %v",
			inv.ID, incErr)
	}
	// A user may have received a direct invitation and then joined through
	// a share link first. Close that direct invitation now so its notification
	// cannot be accepted a second time and produce a misleading audit event.
	s.reconcilePendingInvitation(ctx, inv.TenantID, newUserID)
	s.emitAudit(ctx, &types.AuditLog{
		TenantID:     inv.TenantID,
		ActorUserID:  auditActor(ctx),
		ActorRole:    auditActorRole(ctx),
		Action:       types.AuditActionInvitationAccepted,
		TargetType:   "tenant_invitation",
		TargetID:     strconv.FormatUint(inv.ID, 10),
		TargetUserID: newUserID,
		Outcome:      types.AuditOutcomeSuccess,
		Details:      detailsFor(inv.ID, inv.Role, inv.OrgUnitID),
	})
	return member, nil
}
