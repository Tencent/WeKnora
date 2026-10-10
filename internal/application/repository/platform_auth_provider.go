package repository

import (
	"context"
	"errors"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// ErrPlatformAuthProviderNotFound is returned when no row exists for a kind.
var ErrPlatformAuthProviderNotFound = errors.New("platform auth provider not configured")

// platformAuthProviderRepository persists the platform-wide auth provider rows
// (one row per kind: oidc / ldap).
type platformAuthProviderRepository struct {
	db *gorm.DB
}

// NewPlatformAuthProviderRepository creates the repository.
func NewPlatformAuthProviderRepository(db *gorm.DB) interfaces.PlatformAuthProviderRepository {
	return &platformAuthProviderRepository{db: db}
}

// GetByKind loads the row for `kind`. Returns (nil, nil) when the deployment
// has never saved that provider, so callers can fall back to environment
// defaults without treating the miss as an error.
func (r *platformAuthProviderRepository) GetByKind(
	ctx context.Context, kind types.PlatformAuthProviderKind,
) (*types.PlatformAuthProvider, error) {
	var row types.PlatformAuthProvider
	err := r.db.WithContext(ctx).Where("kind = ?", string(kind)).First(&row).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &row, nil
}

// Upsert writes the row, inserting on first save and updating the mutable
// columns afterwards. kind is the conflict target (unique index), so a kind is
// never duplicated even under concurrent saves from two admins.
func (r *platformAuthProviderRepository) Upsert(
	ctx context.Context, provider *types.PlatformAuthProvider,
) error {
	if provider == nil {
		return errors.New("nil platform auth provider")
	}
	return r.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "kind"}},
		DoUpdates: clause.AssignmentColumns([]string{
			"enabled", "config", "secret", "updated_by", "updated_at",
		}),
	}).Create(provider).Error
}
