package interfaces

import (
	"context"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
)

// TenantInvitationRepository persists tenant_invitations rows that
// capture the "pending invite" intent before it becomes a real
// tenant_members row. All read methods skip GORM soft-deleted rows.
type TenantInvitationRepository interface {
	// Create inserts a new pending invitation. Returns the conflict
	// error sentinel (ErrPendingInvitationExists) if the (tenant_id,
	// invitee) partial unique index rejects the insert.
	Create(ctx context.Context, inv *types.TenantInvitation) error

	// GetByID fetches an invitation by surrogate id, or (nil, nil) if
	// missing. Used by Accept/Decline/Revoke handlers.
	GetByID(ctx context.Context, id uint64) (*types.TenantInvitation, error)

	// GetPendingByPair returns the in-flight pending invitation for
	// (tenant, invitee) or (nil, nil) when none exists. Used by the
	// service layer to short-circuit duplicate Create calls before
	// they hit the unique index.
	GetPendingByPair(ctx context.Context, tenantID uint64, inviteeUserID string) (*types.TenantInvitation, error)

	// ListByTenant returns invitations for the tenant ordered by
	// id DESC (newest first). includeTerminal controls whether
	// non-pending rows (accepted/declined/revoked/expired) are
	// returned — the management UI shows pending by default, with
	// a "history" toggle for the rest.
	ListByTenant(ctx context.Context, tenantID uint64, includeTerminal bool) ([]*types.TenantInvitation, error)

	// CountByTenantList counts invitation rows matching the same filter as
	// ListByTenant (tenant_id + optional pending-only vs include-terminal).
	CountByTenantList(ctx context.Context, tenantID uint64, includeTerminal bool) (int64, error)

	// ListByTenantPage returns invitations for the tenant with id DESC
	// paging, same filtering semantics as ListByTenant.
	ListByTenantPage(ctx context.Context, tenantID uint64, includeTerminal bool, offset, limit int) ([]*types.TenantInvitation, error)

	// ListByInvitee returns invitations addressed to the user across
	// all tenants. Ordered by id DESC. Used both by the inbox page
	// (includeTerminal=false) and by /me/invitations/history if/when
	// we expose history (includeTerminal=true).
	ListByInvitee(ctx context.Context, inviteeUserID string, includeTerminal bool) ([]*types.TenantInvitation, error)

	// CountPendingByInvitee returns the number of pending invitations
	// for the user. Used by the avatar-row badge so the UI doesn't
	// have to fetch the full list just to render a count.
	CountPendingByInvitee(ctx context.Context, inviteeUserID string) (int64, error)

	// MarkStatusIfPending atomically transitions a row from pending
	// to the supplied status. respondedAt is recorded as the
	// transition time. Returns gorm.ErrRecordNotFound when the row
	// does not exist or is already in a non-pending state, which the
	// service layer maps to ErrInvitationNotPending — that error is
	// the "stale state machine" signal callers care about.
	MarkStatusIfPending(
		ctx context.Context,
		id uint64,
		status types.TenantInvitationStatus,
		respondedAt time.Time,
	) error

	// SweepExpired transitions all pending rows whose expires_at is
	// before `now` into the expired state. Returns the affected row
	// count so callers can decide whether to emit per-row audit
	// events (we don't — the volume could be high and the audit-log
	// table already records the original invitation_sent rows).
	SweepExpired(ctx context.Context, now time.Time) (int64, error)
}
