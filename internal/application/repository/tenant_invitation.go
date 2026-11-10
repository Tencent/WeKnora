package repository

import (
	"context"
	"errors"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"gorm.io/gorm"
)

// ErrPendingInvitationExists is returned by Create when a pending
// invitation for (tenant_id, invitee_user_id) already exists. The
// service layer maps it to its own sentinel for the handler.
var ErrPendingInvitationExists = errors.New("repository: pending invitation already exists")

// tenantInvitationRepository implements interfaces.TenantInvitationRepository.
type tenantInvitationRepository struct {
	db *gorm.DB
}

// NewTenantInvitationRepository constructs the repo against db. Wired
// up via the dig container alongside other repositories.
func NewTenantInvitationRepository(db *gorm.DB) interfaces.TenantInvitationRepository {
	return &tenantInvitationRepository{db: db}
}

// Create inserts a new pending invitation row. The partial unique
// index on (tenant_id, invitee_user_id) WHERE status='pending' is the
// authoritative guard against duplicates, but we ALSO pre-check via
// GetPendingByPair inside a tiny transaction so the typical "click
// twice fast" case returns a clean sentinel instead of a raw 23505 /
// SQLITE_CONSTRAINT error string the handler would have to parse.
//
// The pre-check is best-effort: two concurrent inserts can still race
// past it, in which case the underlying database error surfaces. The
// service layer treats any insert error containing the index name
// (idx_tenant_invitations_unique_pending) as the conflict sentinel —
// see invitationService.Create.
//
// Share-link rows (invitee_user_id == "") skip the pre-check: the
// partial unique index on (tenant_id, invitee_user_id) deliberately
// excludes empty values so multiple share links can coexist on a
// tenant; pre-checking would falsely reject every additional one.
func (r *tenantInvitationRepository) Create(
	ctx context.Context,
	inv *types.TenantInvitation,
) error {
	if inv.Status == "" {
		inv.Status = types.TenantInvitationStatusPending
	}
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if inv.InviteeUserID != "" {
			var probe types.TenantInvitation
			err := tx.
				Where("tenant_id = ? AND invitee_user_id = ? AND status = ?",
					inv.TenantID, inv.InviteeUserID, types.TenantInvitationStatusPending).
				First(&probe).Error
			switch {
			case errors.Is(err, gorm.ErrRecordNotFound):
				// fallthrough — clean to insert.
			case err != nil:
				return err
			default:
				return ErrPendingInvitationExists
			}
		}
		return tx.Create(inv).Error
	})
}

// GetByID returns the invitation row (regardless of status) or
// (nil, nil) when missing. Used by Accept/Decline/Revoke flows that
// must inspect status before transitioning.
func (r *tenantInvitationRepository) GetByID(
	ctx context.Context,
	id uint64,
) (*types.TenantInvitation, error) {
	var inv types.TenantInvitation
	err := r.db.WithContext(ctx).Where("id = ?", id).First(&inv).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &inv, nil
}

// GetPendingByPair returns the in-flight pending invitation, if any.
func (r *tenantInvitationRepository) GetPendingByPair(
	ctx context.Context,
	tenantID uint64,
	inviteeUserID string,
) (*types.TenantInvitation, error) {
	var inv types.TenantInvitation
	err := r.db.WithContext(ctx).
		Where("tenant_id = ? AND invitee_user_id = ? AND status = ?",
			tenantID, inviteeUserID, types.TenantInvitationStatusPending).
		First(&inv).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &inv, nil
}

// GetActiveByToken returns the share-link row matching token, or
// (nil, nil) if none. Empty token returns (nil, nil) defensively so
// a malformed caller doesn't accidentally match the legacy "" sentinel
// rows that have token=”.
func (r *tenantInvitationRepository) GetActiveByToken(
	ctx context.Context,
	token string,
) (*types.TenantInvitation, error) {
	if token == "" {
		return nil, nil
	}
	var inv types.TenantInvitation
	err := r.db.WithContext(ctx).
		Where("token = ? AND status = ?",
			token, types.TenantInvitationStatusPending).
		First(&inv).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &inv, nil
}

// listQuery centralises the "newest-first" ordering and includeTerminal
// filter so ListByTenant / ListByInvitee stay short.
func (r *tenantInvitationRepository) listQuery(includeTerminal bool) *gorm.DB {
	q := r.db.Order("id DESC")
	if !includeTerminal {
		q = q.Where("status = ?", types.TenantInvitationStatusPending)
	}
	return q
}

// tenantInvitationTenantScope applies tenant_id, optional pending-only
// filter, and id DESC ordering — shared by ListByTenant and paging helpers.
func (r *tenantInvitationRepository) tenantInvitationTenantScope(
	ctx context.Context,
	tenantID uint64,
	includeTerminal bool,
) *gorm.DB {
	q := r.db.WithContext(ctx).
		Model(&types.TenantInvitation{}).
		Where("tenant_id = ?", tenantID).
		Order("id DESC")
	if !includeTerminal {
		q = q.Where("status = ?", types.TenantInvitationStatusPending)
	}
	return q
}

// ListByTenant returns invitations for the tenant ordered by id DESC.
func (r *tenantInvitationRepository) ListByTenant(
	ctx context.Context,
	tenantID uint64,
	includeTerminal bool,
) ([]*types.TenantInvitation, error) {
	var rows []*types.TenantInvitation
	err := r.tenantInvitationTenantScope(ctx, tenantID, includeTerminal).Find(&rows).Error
	if err != nil {
		return nil, err
	}
	return rows, nil
}

// CountByTenantList counts invitations matching ListByTenant filters.
func (r *tenantInvitationRepository) CountByTenantList(
	ctx context.Context,
	tenantID uint64,
	includeTerminal bool,
) (int64, error) {
	var n int64
	err := r.tenantInvitationTenantScope(ctx, tenantID, includeTerminal).
		Count(&n).Error
	return n, err
}

// ListByTenantPage returns a page of invitations (id DESC).
func (r *tenantInvitationRepository) ListByTenantPage(
	ctx context.Context,
	tenantID uint64,
	includeTerminal bool,
	offset, limit int,
) ([]*types.TenantInvitation, error) {
	var rows []*types.TenantInvitation
	err := r.tenantInvitationTenantScope(ctx, tenantID, includeTerminal).
		Offset(offset).
		Limit(limit).
		Find(&rows).Error
	if err != nil {
		return nil, err
	}
	return rows, nil
}

// ListByInvitee returns invitations addressed to the user across all
// tenants.
func (r *tenantInvitationRepository) ListByInvitee(
	ctx context.Context,
	inviteeUserID string,
	includeTerminal bool,
) ([]*types.TenantInvitation, error) {
	var rows []*types.TenantInvitation
	err := r.listQuery(includeTerminal).
		WithContext(ctx).
		Where("invitee_user_id = ?", inviteeUserID).
		Find(&rows).Error
	if err != nil {
		return nil, err
	}
	return rows, nil
}

// CountPendingByInvitee returns the pending invitation count for the user.
func (r *tenantInvitationRepository) CountPendingByInvitee(
	ctx context.Context,
	inviteeUserID string,
) (int64, error) {
	var count int64
	err := r.db.WithContext(ctx).
		Model(&types.TenantInvitation{}).
		Where("invitee_user_id = ? AND status = ?",
			inviteeUserID, types.TenantInvitationStatusPending).
		Count(&count).Error
	return count, err
}

// MarkStatusIfPending atomically transitions a row from pending to
// the supplied terminal status. The WHERE filter on status='pending'
// is what makes this safe under concurrent accept/revoke clicks —
// only the first writer succeeds; the second sees RowsAffected=0 and
// receives gorm.ErrRecordNotFound (which the service maps to a
// "already finalised" sentinel).
func (r *tenantInvitationRepository) MarkStatusIfPending(
	ctx context.Context,
	id uint64,
	status types.TenantInvitationStatus,
	respondedAt time.Time,
) error {
	res := r.db.WithContext(ctx).
		Model(&types.TenantInvitation{}).
		Where("id = ? AND status = ?", id, types.TenantInvitationStatusPending).
		Updates(map[string]any{
			"status":       status,
			"responded_at": respondedAt,
			"updated_at":   time.Now(),
		})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return gorm.ErrRecordNotFound
	}
	return nil
}

// SweepExpired transitions all overdue pending rows to expired in a
// single batch. We intentionally don't emit per-row audit events from
// here — the original rbac.invitation_sent already lives in the audit
// log, and a sweep can touch many rows on a sleepy weekend (an audit
// fanout would dwarf the original event volume).
func (r *tenantInvitationRepository) SweepExpired(
	ctx context.Context,
	now time.Time,
) (int64, error) {
	res := r.db.WithContext(ctx).
		Model(&types.TenantInvitation{}).
		Where("status = ? AND expires_at < ?", types.TenantInvitationStatusPending, now).
		Updates(map[string]any{
			"status":       types.TenantInvitationStatusExpired,
			"responded_at": now,
			"updated_at":   time.Now(),
		})
	if res.Error != nil {
		return 0, res.Error
	}
	return res.RowsAffected, nil
}

// IncrementAcceptedCount bumps accepted_count by 1 for the given row.
// Implemented as a single UPDATE expression so concurrent accepts
// don't race on a read-modify-write loop. We deliberately don't gate
// on status='pending' here: AcceptByToken is the only caller, and
// share-link rows stay pending across accepts — so the gate would
// reject zero rows in steady state but introduce a subtle ordering
// dependency on MarkStatusIfPending for per-user accepts.
func (r *tenantInvitationRepository) IncrementAcceptedCount(
	ctx context.Context,
	id uint64,
) error {
	res := r.db.WithContext(ctx).
		Model(&types.TenantInvitation{}).
		Where("id = ?", id).
		UpdateColumn("accepted_count", gorm.Expr("accepted_count + 1"))
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return gorm.ErrRecordNotFound
	}
	return nil
}
