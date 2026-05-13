package repository

import (
	"context"
	"errors"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// ErrLastOwner is returned by the atomic demote / remove repo helpers
// when the operation would leave the tenant without an active Owner.
// The service layer maps this to its own ErrLastOwner sentinel (same
// semantic; just kept separate so the repo doesn't import service).
var ErrLastOwner = errors.New("repository: last active owner")

// forUpdateClause returns the gorm SELECT ... FOR UPDATE clause. Kept
// in one place so we can swap it out for `clause.Locking{Strength: "UPDATE"}`
// on databases that don't support row-level locking (none in our matrix,
// but keeps the seam if SQLite-lite ever needs a no-op).
func forUpdateClause() clause.Expression {
	return clause.Locking{Strength: "UPDATE"}
}

// tenantMemberRepository implements interfaces.TenantMemberRepository.
type tenantMemberRepository struct {
	db *gorm.DB
}

// NewTenantMemberRepository creates a new tenant member repository.
func NewTenantMemberRepository(db *gorm.DB) interfaces.TenantMemberRepository {
	return &tenantMemberRepository{db: db}
}

// Create inserts a new active membership row. Status defaults to
// TenantMemberStatusActive when the caller leaves it blank, and JoinedAt
// defaults to the current time, matching service-layer expectations.
func (r *tenantMemberRepository) Create(ctx context.Context, member *types.TenantMember) error {
	if member.Status == "" {
		member.Status = types.TenantMemberStatusActive
	}
	if member.JoinedAt.IsZero() {
		member.JoinedAt = time.Now()
	}
	return r.db.WithContext(ctx).Create(member).Error
}

// Get returns the active membership for (userID, tenantID), or (nil, nil)
// if no such row exists. Errors are propagated unchanged for any other case.
func (r *tenantMemberRepository) Get(ctx context.Context, userID string, tenantID uint64) (*types.TenantMember, error) {
	var member types.TenantMember
	err := r.db.WithContext(ctx).
		Where("user_id = ? AND tenant_id = ?", userID, tenantID).
		First(&member).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &member, nil
}

// ListByUser returns every active membership owned by the user, ordered
// by joined_at ascending so the home tenant (created at registration)
// naturally appears first.
func (r *tenantMemberRepository) ListByUser(ctx context.Context, userID string) ([]*types.TenantMember, error) {
	var members []*types.TenantMember
	err := r.db.WithContext(ctx).
		Where("user_id = ?", userID).
		Order("joined_at ASC, id ASC").
		Find(&members).Error
	if err != nil {
		return nil, err
	}
	return members, nil
}

// ListByTenant returns every active membership inside the tenant.
func (r *tenantMemberRepository) ListByTenant(ctx context.Context, tenantID uint64) ([]*types.TenantMember, error) {
	var members []*types.TenantMember
	err := r.db.WithContext(ctx).
		Where("tenant_id = ?", tenantID).
		Order("joined_at ASC, id ASC").
		Find(&members).Error
	if err != nil {
		return nil, err
	}
	return members, nil
}

// UpdateRole changes the role of an existing active membership.
func (r *tenantMemberRepository) UpdateRole(ctx context.Context, userID string, tenantID uint64, role types.TenantRole) error {
	res := r.db.WithContext(ctx).
		Model(&types.TenantMember{}).
		Where("user_id = ? AND tenant_id = ?", userID, tenantID).
		Updates(map[string]any{
			"role":       role,
			"updated_at": time.Now(),
		})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return gorm.ErrRecordNotFound
	}
	return nil
}

// SoftDelete marks the membership row as deleted. GORM's soft-delete
// support populates DeletedAt automatically.
func (r *tenantMemberRepository) SoftDelete(ctx context.Context, userID string, tenantID uint64) error {
	return r.db.WithContext(ctx).
		Where("user_id = ? AND tenant_id = ?", userID, tenantID).
		Delete(&types.TenantMember{}).Error
}

// CountActiveOwners reports the number of active owner rows in the tenant.
func (r *tenantMemberRepository) CountActiveOwners(ctx context.Context, tenantID uint64) (int64, error) {
	var count int64
	err := r.db.WithContext(ctx).
		Model(&types.TenantMember{}).
		Where("tenant_id = ? AND role = ? AND status = ?",
			tenantID, types.TenantRoleOwner, types.TenantMemberStatusActive).
		Count(&count).Error
	return count, err
}

// DemoteOwnerAtomically transitions an Owner row to a non-Owner role
// while holding an UPDATE lock on the tenant's other Owner rows. This
// closes the TOCTOU window in the old "Get → CountActiveOwners → Update"
// sequence where two concurrent demotions of two different Owners could
// each read count=2, then both commit, leaving the tenant ownerless.
//
// Returns:
//   - ErrLastOwner when there is no other active Owner.
//   - gorm.ErrRecordNotFound when the row isn't there anymore (race
//     between concurrent removes); callers map this to ErrMembershipNotFound.
//   - any other error verbatim for the caller to log / surface.
//
// The caller is responsible for verifying the *current* role is Owner
// before invoking this; the method is purposely narrow (it only handles
// the dangerous demotion path) so other UpdateRole transitions can keep
// using the cheap single-statement UpdateRole above.
func (r *tenantMemberRepository) DemoteOwnerAtomically(
	ctx context.Context,
	userID string,
	tenantID uint64,
	newRole types.TenantRole,
) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// Lock every other active Owner row in the tenant. Locking ONLY
		// owners (not the row being demoted) is enough: a concurrent
		// demote of the locked row will block on this same SELECT.
		var locked []types.TenantMember
		err := tx.
			Clauses(forUpdateClause()).
			Where("tenant_id = ? AND user_id <> ? AND role = ? AND status = ?",
				tenantID, userID, types.TenantRoleOwner, types.TenantMemberStatusActive).
			Find(&locked).Error
		if err != nil {
			return err
		}
		if len(locked) == 0 {
			return ErrLastOwner
		}
		res := tx.
			Model(&types.TenantMember{}).
			Where("user_id = ? AND tenant_id = ?", userID, tenantID).
			Updates(map[string]any{
				"role":       newRole,
				"updated_at": time.Now(),
			})
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 0 {
			return gorm.ErrRecordNotFound
		}
		return nil
	})
}

// RemoveOwnerAtomically soft-deletes an Owner row under the same lock
// as DemoteOwnerAtomically. Same return semantics.
func (r *tenantMemberRepository) RemoveOwnerAtomically(
	ctx context.Context,
	userID string,
	tenantID uint64,
) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var locked []types.TenantMember
		err := tx.
			Clauses(forUpdateClause()).
			Where("tenant_id = ? AND user_id <> ? AND role = ? AND status = ?",
				tenantID, userID, types.TenantRoleOwner, types.TenantMemberStatusActive).
			Find(&locked).Error
		if err != nil {
			return err
		}
		if len(locked) == 0 {
			return ErrLastOwner
		}
		res := tx.
			Where("user_id = ? AND tenant_id = ?", userID, tenantID).
			Delete(&types.TenantMember{})
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 0 {
			return gorm.ErrRecordNotFound
		}
		return nil
	})
}

// HasAnyMembers reports whether the tenant has at least one active
// membership row. Uses a LIMIT 1 SELECT (instead of COUNT(*)) so the query
// short-circuits after the first match — important because this is on the
// auth middleware's hot path for users without a cached membership.
func (r *tenantMemberRepository) HasAnyMembers(ctx context.Context, tenantID uint64) (bool, error) {
	var probe struct {
		ID uint64
	}
	err := r.db.WithContext(ctx).
		Model(&types.TenantMember{}).
		Select("id").
		Where("tenant_id = ? AND status = ?", tenantID, types.TenantMemberStatusActive).
		Limit(1).
		Take(&probe).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}
