// Package repository persists sandbox backend configuration.
//
// Every read and write is scoped by tenant_id. Sandbox configs carry provider
// credentials, so a query that forgets the scope is a cross-workspace credential
// leak, not merely a bug.
package repository

import (
	"context"
	"errors"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"gorm.io/gorm"
)

// TenantSandboxConfigRepository persists named sandbox backend configs.
type TenantSandboxConfigRepository interface {
	Create(ctx context.Context, e *types.TenantSandboxConfigEntity) error
	GetByID(ctx context.Context, tenantID uint64, id string) (*types.TenantSandboxConfigEntity, error)
	ListByTenant(ctx context.Context, tenantID uint64) ([]*types.TenantSandboxConfigEntity, error)
	// ListAll is the housekeeping scan. Unlike every other method it is not
	// tenant-scoped: the stuck-run reaper and snapshot reconcile have to walk
	// every workspace. Do not use it on a request path.
	ListAll(ctx context.Context) ([]*types.TenantSandboxConfigEntity, error)
	Update(ctx context.Context, e *types.TenantSandboxConfigEntity) error
	SoftDelete(ctx context.Context, tenantID uint64, id string) error
	SetCordon(ctx context.Context, tenantID uint64, id string, at time.Time) error
	ClearCordon(ctx context.Context, tenantID uint64, id string) error
}

type tenantSandboxConfigRepository struct {
	db *gorm.DB
}

// NewTenantSandboxConfigRepository returns a GORM-backed implementation.
func NewTenantSandboxConfigRepository(db *gorm.DB) TenantSandboxConfigRepository {
	return &tenantSandboxConfigRepository{db: db}
}

func (r *tenantSandboxConfigRepository) Create(
	ctx context.Context, e *types.TenantSandboxConfigEntity,
) error {
	return r.db.WithContext(ctx).Create(e).Error
}

// GetByID returns nil (no error) when the config does not exist or belongs to
// another workspace, so callers can render a 404 without inspecting errors.
func (r *tenantSandboxConfigRepository) GetByID(
	ctx context.Context, tenantID uint64, id string,
) (*types.TenantSandboxConfigEntity, error) {
	var e types.TenantSandboxConfigEntity
	err := r.db.WithContext(ctx).
		Where("tenant_id = ? AND id = ?", tenantID, id).
		First(&e).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &e, nil
}

func (r *tenantSandboxConfigRepository) ListByTenant(
	ctx context.Context, tenantID uint64,
) ([]*types.TenantSandboxConfigEntity, error) {
	var list []*types.TenantSandboxConfigEntity
	err := r.db.WithContext(ctx).
		Where("tenant_id = ?", tenantID).
		Order("created_at ASC").
		Find(&list).Error
	if err != nil {
		return nil, err
	}
	return list, nil
}

func (r *tenantSandboxConfigRepository) ListAll(
	ctx context.Context,
) ([]*types.TenantSandboxConfigEntity, error) {
	var list []*types.TenantSandboxConfigEntity
	err := r.db.WithContext(ctx).
		Order("tenant_id ASC, created_at ASC").
		Find(&list).Error
	if err != nil {
		return nil, err
	}
	return list, nil
}

// Update writes the mutable columns. Select is explicit so a zero-valued
// CordonedAt on the passed entity cannot silently release someone else's lease.
func (r *tenantSandboxConfigRepository) Update(
	ctx context.Context, e *types.TenantSandboxConfigEntity,
) error {
	return r.db.WithContext(ctx).
		Model(&types.TenantSandboxConfigEntity{}).
		Where("tenant_id = ? AND id = ?", e.TenantID, e.ID).
		Select("name", "description", "sandbox_type", "config", "updated_at").
		Updates(map[string]any{
			"name":         e.Name,
			"description":  e.Description,
			"sandbox_type": e.SandboxType,
			"config":       e.Config,
			"updated_at":   time.Now(),
		}).Error
}

func (r *tenantSandboxConfigRepository) SoftDelete(
	ctx context.Context, tenantID uint64, id string,
) error {
	return r.db.WithContext(ctx).
		Where("tenant_id = ? AND id = ?", tenantID, id).
		Delete(&types.TenantSandboxConfigEntity{}).Error
}

// ErrSandboxConfigCordoned is returned by SetCordon when another request
// already holds a fresh cordon lease on the same config row.
var ErrSandboxConfigCordoned = errors.New("sandbox config is being modified by another request")

// SetCordon must be committed before the caller lists provider sandboxes:
// resolution paths only stop creating sandboxes once they can see it.
//
// The update is a conditional CAS: it refuses to overwrite a cordon that is
// still within the lease window, so two concurrent identity-change requests
// cannot race past each other.
func (r *tenantSandboxConfigRepository) SetCordon(
	ctx context.Context, tenantID uint64, id string, at time.Time,
) error {
	result := r.db.WithContext(ctx).
		Model(&types.TenantSandboxConfigEntity{}).
		Where("tenant_id = ? AND id = ?", tenantID, id).
		Where("cordoned_at IS NULL OR cordoned_at < ?", at.Add(-types.SandboxCordonLease)).
		Update("cordoned_at", at)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return ErrSandboxConfigCordoned
	}
	return nil
}

func (r *tenantSandboxConfigRepository) ClearCordon(
	ctx context.Context, tenantID uint64, id string,
) error {
	return r.db.WithContext(ctx).
		Model(&types.TenantSandboxConfigEntity{}).
		Where("tenant_id = ? AND id = ?", tenantID, id).
		Update("cordoned_at", nil).Error
}
