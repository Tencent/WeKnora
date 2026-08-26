package repository

import (
	"context"
	"errors"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var (
	ErrUserNotFound                     = errors.New("user not found")
	ErrUserAlreadyExists                = errors.New("user already exists")
	ErrTokenNotFound                    = errors.New("token not found")
	ErrCannotRevokeSelf                 = errors.New("cannot revoke your own system admin privileges")
	ErrLastSystemAdmin                  = errors.New("cannot revoke the last remaining system administrator")
	ErrUserNotSystemAdmin               = errors.New("user is not a system administrator")
	ErrCannotRevokeOwnCrossTenantAccess = errors.New("cannot revoke your own cross-tenant access")
	ErrLastCrossTenantAccessManager     = errors.New("cannot revoke the last system administrator with cross-tenant access")
)

const (
	userSystemAdminColumn       = "is_system_admin"
	userCrossTenantAccessColumn = "can_access_all_tenants"
)

// userRepository implements user repository interface
type userRepository struct {
	db *gorm.DB
}

// NewUserRepository creates a new user repository
func NewUserRepository(db *gorm.DB) interfaces.UserRepository {
	return &userRepository{db: db}
}

// CreateUser creates a user
func (r *userRepository) CreateUser(ctx context.Context, user *types.User) error {
	// users.tenant_id is nullable in both PostgreSQL and SQLite. GORM would
	// otherwise serialise the uint64 zero value as 0, which violates the
	// PostgreSQL FK and loses the distinction between "not provisioned yet"
	// and a real tenant. Omitting the column stores SQL NULL; reads hydrate it
	// back as zero, the domain sentinel used by tenantless auth flows.
	if user != nil && user.TenantID == 0 {
		return r.db.WithContext(ctx).Omit("tenant_id").Create(user).Error
	}
	return r.db.WithContext(ctx).Create(user).Error
}

// GetUserByID gets a user by ID
func (r *userRepository) GetUserByID(ctx context.Context, id string) (*types.User, error) {
	var user types.User
	if err := r.db.WithContext(ctx).Where("id = ?", id).First(&user).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrUserNotFound
		}
		return nil, err
	}
	return &user, nil
}

// GetUsersByIDs batch-fetches users by id with a single SELECT … WHERE id IN (…)
// and projects the result into a map keyed by user id. Returns an empty
// map for an empty input slice. Missing ids are silently absent from
// the result (consistent with the interface contract used by tenant
// member hydration).
func (r *userRepository) GetUsersByIDs(ctx context.Context, ids []string) (map[string]*types.User, error) {
	out := make(map[string]*types.User, len(ids))
	if len(ids) == 0 {
		return out, nil
	}
	var users []*types.User
	if err := r.db.WithContext(ctx).Where("id IN ?", ids).Find(&users).Error; err != nil {
		return nil, err
	}
	for _, u := range users {
		out[u.ID] = u
	}
	return out, nil
}

// GetUserByEmail gets a user by email
func (r *userRepository) GetUserByEmail(ctx context.Context, email string) (*types.User, error) {
	var user types.User
	if err := r.db.WithContext(ctx).Where("email = ?", email).First(&user).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrUserNotFound
		}
		return nil, err
	}
	return &user, nil
}

// GetUserByUsername gets a user by username
func (r *userRepository) GetUserByUsername(ctx context.Context, username string) (*types.User, error) {
	var user types.User
	if err := r.db.WithContext(ctx).Where("username = ?", username).First(&user).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrUserNotFound
		}
		return nil, err
	}
	return &user, nil
}

// GetUserByTenantID gets the first user (owner) of a tenant
func (r *userRepository) GetUserByTenantID(ctx context.Context, tenantID uint64) (*types.User, error) {
	var user types.User
	if err := r.db.WithContext(ctx).Where("tenant_id = ?", tenantID).Order("created_at ASC").First(&user).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrUserNotFound
		}
		return nil, err
	}
	return &user, nil
}

// UpdateUser updates ordinary user fields while preserving platform
// privileges. Privilege changes must use their dedicated atomic methods so a
// stale user snapshot cannot silently grant or revoke administrative access.
func (r *userRepository) UpdateUser(ctx context.Context, user *types.User) error {
	if user != nil && user.TenantID == 0 {
		return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			// Preserve all-fields update semantics while keeping the nullable
			// tenant column out of the write, then explicitly store NULL. Writing
			// uint64(0) would violate the PostgreSQL tenant FK.
			if err := updateOrdinaryUserFields(tx, user, "tenant_id"); err != nil {
				return err
			}
			return tx.Model(&types.User{}).
				Where("id = ?", user.ID).
				UpdateColumn("tenant_id", nil).Error
		})
	}
	return updateOrdinaryUserFields(r.db.WithContext(ctx), user)
}

func updateOrdinaryUserFields(db *gorm.DB, user *types.User, omittedColumns ...string) error {
	columns := append([]string{"id", userSystemAdminColumn, userCrossTenantAccessColumn}, omittedColumns...)
	return db.Model(&types.User{}).
		Where("id = ?", user.ID).
		Select("*").
		Omit(columns...).
		Updates(user).Error
}

// DeleteUser deletes a user
func (r *userRepository) DeleteUser(ctx context.Context, id string) error {
	return r.db.WithContext(ctx).Where("id = ?", id).Delete(&types.User{}).Error
}

// ListUsers lists users with pagination
func (r *userRepository) ListUsers(ctx context.Context, offset, limit int) ([]*types.User, error) {
	var users []*types.User
	query := r.db.WithContext(ctx).Order("created_at DESC")

	if limit > 0 {
		query = query.Limit(limit)
	}

	if offset > 0 {
		query = query.Offset(offset)
	}

	if err := query.Find(&users).Error; err != nil {
		return nil, err
	}
	return users, nil
}

// ListSystemAdmins lists users where is_system_admin = true.
//
// Walks idx_users_is_system_admin (created in migration 000052), so the
// query stays cheap even on a large users table — only the small subset
// of system admins is scanned. Returns total count alongside the page so
// the management UI can render pagination without a second roundtrip.
//
// Ordered by created_at DESC for stable, newest-first listing; ties are
// further broken by id to keep paging deterministic across boundaries.
// limit <= 0 means "no limit" (matches ListUsers semantics); callers in
// production pass a sane page size.
func (r *userRepository) ListSystemAdmins(ctx context.Context, offset, limit int) ([]*types.User, int64, error) {
	var users []*types.User
	var total int64

	base := r.db.WithContext(ctx).Model(&types.User{}).Where("is_system_admin = ?", true)
	if err := base.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	query := base.Order("created_at DESC, id ASC")
	if limit > 0 {
		query = query.Limit(limit)
	}
	if offset > 0 {
		query = query.Offset(offset)
	}
	if err := query.Find(&users).Error; err != nil {
		return nil, 0, err
	}
	return users, total, nil
}

// CountActiveSystemAdmins returns the number of enabled system administrators.
// Bootstrap uses a dedicated count because the management list intentionally
// includes disabled accounts so operators can still inspect and revoke them.
func (r *userRepository) CountActiveSystemAdmins(ctx context.Context) (int64, error) {
	var total int64
	err := r.db.WithContext(ctx).Model(&types.User{}).
		Where("is_system_admin = TRUE AND is_active = TRUE").
		Count(&total).Error
	return total, err
}

// GrantSystemAdmin enables system-administrator access atomically.
// The changed result is false when the target already has the permission.
func (r *userRepository) GrantSystemAdmin(ctx context.Context, userID string) (*types.User, bool, error) {
	var updated *types.User
	changed := false
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var user types.User
		if err := withUpdateLock(tx).Where("id = ?", userID).First(&user).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrUserNotFound
			}
			return err
		}
		if user.IsSystemAdmin {
			updated = &user
			return nil
		}
		if err := tx.Model(&user).Update(userSystemAdminColumn, true).Error; err != nil {
			return err
		}
		user.IsSystemAdmin = true
		updated = &user
		changed = true
		return nil
	})
	if err != nil {
		return nil, false, err
	}
	return updated, changed, nil
}

// ListCrossTenantAccessUsers lists users where can_access_all_tenants is true.
func (r *userRepository) ListCrossTenantAccessUsers(
	ctx context.Context, cursor *types.UserListCursor, limit int,
) ([]*types.User, *types.UserListCursor, error) {
	var users []*types.User

	// Keep the indexed predicate literal. PostgreSQL cannot always prove that
	// a parameterized boolean predicate implies a partial-index predicate when
	// it switches to a generic prepared plan.
	query := r.db.WithContext(ctx).Model(&types.User{}).
		Where("can_access_all_tenants = TRUE")
	if cursor != nil {
		query = query.Where(
			"created_at < ? OR (created_at = ? AND id > ?)",
			cursor.CreatedAt, cursor.CreatedAt, cursor.ID,
		)
	}
	query = query.Order("created_at DESC, id ASC")
	if limit > 0 {
		query = query.Limit(limit + 1)
	}
	if err := query.Find(&users).Error; err != nil {
		return nil, nil, err
	}
	if limit <= 0 || len(users) <= limit {
		return users, nil, nil
	}

	users = users[:limit]
	last := users[len(users)-1]
	nextCursor := &types.UserListCursor{CreatedAt: last.CreatedAt, ID: last.ID}
	return users, nextCursor, nil
}

// CountCrossTenantAccessManagers returns the number of active users that hold
// both privileges required to manage cross-tenant access. The bootstrap path
// uses this to establish the first manager without weakening the HTTP guard.
func (r *userRepository) CountCrossTenantAccessManagers(ctx context.Context) (int64, error) {
	var total int64
	err := r.db.WithContext(ctx).Model(&types.User{}).
		Where("is_system_admin = TRUE AND can_access_all_tenants = TRUE AND is_active = TRUE").
		Count(&total).Error
	return total, err
}

// GrantCrossTenantAccess enables cross-tenant access atomically.
// The changed result is false when the target already has the permission.
func (r *userRepository) GrantCrossTenantAccess(ctx context.Context, userID string) (*types.User, bool, error) {
	return r.updateCrossTenantAccess(ctx, userID, true)
}

// RevokeCrossTenantAccess disables cross-tenant access atomically.
// Callers cannot revoke themselves because doing so would immediately remove
// their ability to repair the platform-level permission set.
func (r *userRepository) RevokeCrossTenantAccess(
	ctx context.Context, userID, actorID string,
) (*types.User, bool, error) {
	if userID == actorID {
		return nil, false, ErrCannotRevokeOwnCrossTenantAccess
	}
	return r.updateCrossTenantAccess(ctx, userID, false)
}

func (r *userRepository) updateCrossTenantAccess(
	ctx context.Context, userID string, enabled bool,
) (*types.User, bool, error) {
	var updated *types.User
	changed := false
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var admins []types.User
		if !enabled {
			var err error
			admins, err = lockSystemAdmins(tx)
			if err != nil {
				return err
			}
		}

		var user types.User
		if err := withUpdateLock(tx).Where("id = ?", userID).First(&user).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrUserNotFound
			}
			return err
		}
		if user.CanAccessAllTenants == enabled {
			updated = &user
			return nil
		}
		if !enabled && user.IsSystemAdmin && countCrossTenantAccessManagers(admins) <= 1 {
			return ErrLastCrossTenantAccessManager
		}

		if err := tx.Model(&user).Update("can_access_all_tenants", enabled).Error; err != nil {
			return err
		}
		user.CanAccessAllTenants = enabled
		updated = &user
		changed = true
		return nil
	})
	if err != nil {
		return nil, false, err
	}
	return updated, changed, nil
}

// RevokeSystemAdmin revokes system-admin privileges inside a transaction.
// It locks the current admin rows before counting so concurrent revokes
// cannot both observe "two admins" and leave the platform with zero.
//
// Return contract:
//   - (user, nil): revoke actually happened; user.IsSystemAdmin == false
//   - (user, ErrUserNotSystemAdmin): target was already not an admin;
//     no row was written. Caller should treat as idempotent success but
//     MUST distinguish it from a real revoke for audit purposes — the
//     surfaced `user` is the unchanged DB row.
//   - (nil, ErrCannotRevokeSelf | ErrLastSystemAdmin | ErrUserNotFound | …):
//     hard rejection; no row written.
func (r *userRepository) RevokeSystemAdmin(ctx context.Context, userID, actorID string) (*types.User, error) {
	if userID == actorID {
		return nil, ErrCannotRevokeSelf
	}

	var revoked *types.User
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		admins, err := lockSystemAdmins(tx)
		if err != nil {
			return err
		}

		var user *types.User
		for i := range admins {
			if admins[i].ID == userID {
				user = &admins[i]
				break
			}
		}
		if user == nil {
			var target types.User
			if err := withUpdateLock(tx).Where("id = ?", userID).First(&target).Error; err != nil {
				if errors.Is(err, gorm.ErrRecordNotFound) {
					return ErrUserNotFound
				}
				return err
			}
			revoked = &target
			return ErrUserNotSystemAdmin
		}

		if len(admins) <= 1 {
			return ErrLastSystemAdmin
		}
		if user.CanAccessAllTenants && countCrossTenantAccessManagers(admins) <= 1 {
			return ErrLastCrossTenantAccessManager
		}

		if err := tx.Model(&types.User{}).
			Where("id = ?", user.ID).
			Update("is_system_admin", false).Error; err != nil {
			return err
		}
		user.IsSystemAdmin = false
		revoked = user
		return nil
	})
	// Propagate ErrUserNotSystemAdmin up to the handler alongside the
	// (unchanged) user row. The handler treats it as idempotent success
	// but emits an audit row with changed=false so a probing pattern
	// ("revoke every random user id we know") still leaves a trail.
	if errors.Is(err, ErrUserNotSystemAdmin) {
		return revoked, err
	}
	if err != nil {
		return nil, err
	}
	return revoked, nil
}

// lockSystemAdmins serializes both privilege-revocation paths on the same
// ordered row set. This prevents concurrent changes to either flag from
// removing every user who can still manage cross-tenant access.
func lockSystemAdmins(tx *gorm.DB) ([]types.User, error) {
	var admins []types.User
	err := withUpdateLock(tx).
		Where("is_system_admin = ?", true).
		Order("id ASC").
		Find(&admins).Error
	return admins, err
}

func withUpdateLock(tx *gorm.DB) *gorm.DB {
	switch tx.Dialector.Name() {
	case "postgres", "mysql":
		return tx.Clauses(clause.Locking{Strength: "UPDATE"})
	default:
		return tx
	}
}

func countCrossTenantAccessManagers(admins []types.User) int {
	count := 0
	for i := range admins {
		if admins[i].IsActive && admins[i].CanAccessAllTenants {
			count++
		}
	}
	return count
}

// SearchUsers searches users by username or email
func (r *userRepository) SearchUsers(ctx context.Context, query string, limit int) ([]*types.User, error) {
	var users []*types.User
	searchPattern := "%" + query + "%"

	dbQuery := r.db.WithContext(ctx).
		Where("username ILIKE ? OR email ILIKE ?", searchPattern, searchPattern).
		Where("is_active = ?", true).
		Order("username ASC")

	if limit > 0 {
		dbQuery = dbQuery.Limit(limit)
	} else {
		dbQuery = dbQuery.Limit(20) // default limit
	}

	if err := dbQuery.Find(&users).Error; err != nil {
		return nil, err
	}
	return users, nil
}

// authTokenRepository implements auth token repository interface
type authTokenRepository struct {
	db *gorm.DB
}

// NewAuthTokenRepository creates a new auth token repository
func NewAuthTokenRepository(db *gorm.DB) interfaces.AuthTokenRepository {
	return &authTokenRepository{db: db}
}

// CreateToken creates an auth token
func (r *authTokenRepository) CreateToken(ctx context.Context, token *types.AuthToken) error {
	return r.db.WithContext(ctx).Create(token).Error
}

// GetTokenByValue gets a token by its value
func (r *authTokenRepository) GetTokenByValue(ctx context.Context, tokenValue string) (*types.AuthToken, error) {
	var token types.AuthToken
	if err := r.db.WithContext(ctx).Where("token = ?", tokenValue).First(&token).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrTokenNotFound
		}
		return nil, err
	}
	return &token, nil
}

// GetTokensByUserID gets all tokens for a user
func (r *authTokenRepository) GetTokensByUserID(ctx context.Context, userID string) ([]*types.AuthToken, error) {
	var tokens []*types.AuthToken
	if err := r.db.WithContext(ctx).Where("user_id = ?", userID).Find(&tokens).Error; err != nil {
		return nil, err
	}
	return tokens, nil
}

// UpdateToken updates a token
func (r *authTokenRepository) UpdateToken(ctx context.Context, token *types.AuthToken) error {
	return r.db.WithContext(ctx).Save(token).Error
}

// DeleteToken deletes a token
func (r *authTokenRepository) DeleteToken(ctx context.Context, id string) error {
	return r.db.WithContext(ctx).Where("id = ?", id).Delete(&types.AuthToken{}).Error
}

// DeleteExpiredTokens deletes all expired tokens
func (r *authTokenRepository) DeleteExpiredTokens(ctx context.Context) error {
	return r.db.WithContext(ctx).Where("expires_at < NOW()").Delete(&types.AuthToken{}).Error
}

// RevokeTokensByUserID revokes all tokens for a user
func (r *authTokenRepository) RevokeTokensByUserID(ctx context.Context, userID string) error {
	return r.db.WithContext(ctx).Model(&types.AuthToken{}).Where("user_id = ?", userID).Update("is_revoked", true).Error
}
