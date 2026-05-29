package interfaces

import (
	"context"

	"github.com/Tencent/WeKnora/internal/types"
)

// TenantInvitationService is the business-logic layer over
// TenantInvitationRepository. It enforces the lifecycle invariants
// described in TenantInvitationStatus, runs the lazy expiry sweep, and
// brokers the accept-time hand-off into TenantMemberService so that
// "accept" produces both the invitation transition AND the
// tenant_members row in a single audit story.
type TenantInvitationService interface {
	// Create issues a new pending invitation for (tenantID, inviteeUserID)
	// with the given role. Caller passes the inviter user id (may be
	// nil for service-internal flows). Returns the freshly created row
	// or a sentinel error (ErrPendingInvitationExists / ErrAlreadyMember /
	// ErrInvalidTenantRole) for the common conflicts.
	Create(
		ctx context.Context,
		tenantID uint64,
		inviteeUserID string,
		role types.TenantRole,
		invitedBy *string,
		message string,
	) (*types.TenantInvitation, error)

	// Accept transitions the pending row into accepted AND creates the
	// active tenant_members row in the same transaction. The callerUserID
	// MUST equal inv.InviteeUserID — the service rejects anyone else.
	// Returns the freshly created membership for the response shape.
	Accept(ctx context.Context, invID uint64, callerUserID string) (*types.TenantMember, error)

	// Decline transitions the pending row into declined. Same caller
	// rule as Accept: only the invitee themselves.
	Decline(ctx context.Context, invID uint64, callerUserID string) error

	// Revoke transitions the pending row into revoked. Callable by an
	// Owner of the tenant the invitation belongs to (the handler
	// applies the Owner gate via g.Owner() route middleware, so this
	// method does not re-check role).
	Revoke(ctx context.Context, invID uint64) error

	// GetByID returns an invitation or (nil, nil). Used by handlers to
	// authorise per-row operations (e.g. is the caller the invitee?).
	GetByID(ctx context.Context, invID uint64) (*types.TenantInvitation, error)

	// ListByTenant returns invitations for a tenant. Always runs the
	// lazy expiry sweep before reading so stale-pending rows surface
	// as expired in the UI.
	ListByTenant(ctx context.Context, tenantID uint64, includeTerminal bool) ([]*types.TenantInvitation, error)

	// ListTenantInvitationsPage paginates invitations for the tenant
	// management UI after the lazy sweep (same filtering as ListByTenant).
	ListTenantInvitationsPage(ctx context.Context, tenantID uint64, includeTerminal bool, page, pageSize int) ([]*types.TenantInvitation, int64, error)

	// ListByInvitee returns invitations addressed to the user across
	// all tenants. Always runs the lazy sweep first.
	ListByInvitee(ctx context.Context, inviteeUserID string, includeTerminal bool) ([]*types.TenantInvitation, error)

	// CountPendingByInvitee returns the user's pending invitation count
	// after running the lazy sweep, so the inbox badge can poll a
	// lightweight endpoint without paginating the full list.
	CountPendingByInvitee(ctx context.Context, inviteeUserID string) (int64, error)

	// CreateShareLink generates a multi-use share-link invitation for
	// the tenant. The plaintext token is persisted on the row and also
	// returned for the handler to compose the registration URL — the
	// token stays available via list/get for as long as the row is
	// pending so Owners can re-share without "copy now or lose it"
	// pressure. Multiple share-link rows can coexist on the same
	// tenant (different roles, or just multiple campaigns).
	CreateShareLink(
		ctx context.Context,
		tenantID uint64,
		role types.TenantRole,
		invitedBy *string,
		message string,
	) (inv *types.TenantInvitation, plainToken string, err error)

	// LookupByToken resolves a plaintext token to its active share-link
	// row. Returns ErrInvitationTokenInvalid for unknown / expired /
	// revoked tokens to avoid leaking which tokens used to exist. The
	// row is NOT consumed; the same token can be looked up arbitrarily
	// many times until it expires or the Owner revokes it.
	LookupByToken(ctx context.Context, plainToken string) (*types.TenantInvitation, error)

	// AcceptByToken creates a tenant_members row binding newUserID to
	// the share-link's tenant + role. Unlike Accept, this does NOT flip
	// the invitation row into a terminal state — share-link rows stay
	// pending so subsequent invitees can also register through the
	// same link. Idempotent: if the user already has membership, the
	// existing row is returned.
	AcceptByToken(ctx context.Context, plainToken string, newUserID string) (*types.TenantMember, error)
}
