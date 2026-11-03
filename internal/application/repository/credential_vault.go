package repository

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"gorm.io/gorm"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

type credentialVaultRepository struct {
	db *gorm.DB
}

// NewCredentialVaultRepository creates a repository backed by GORM.
func NewCredentialVaultRepository(db *gorm.DB) interfaces.CredentialVaultRepository {
	return &credentialVaultRepository{db: db}
}

func (r *credentialVaultRepository) owned(ctx context.Context, tenantID uint64, userID string) *gorm.DB {
	return r.db.WithContext(ctx).Where("tenant_id = ? AND user_id = ?", tenantID, userID)
}

func (r *credentialVaultRepository) Create(ctx context.Context, item *types.CredentialVaultItem) error {
	if item == nil || item.ID == "" || item.TenantID == 0 || item.UserID == "" {
		return errors.New("credential vault item identity is required")
	}
	if err := r.db.WithContext(ctx).Create(item).Error; err != nil {
		if isCredentialUniqueViolation(err) {
			return types.ErrCredentialConflict
		}
		return fmt.Errorf("create credential vault item: %w", err)
	}
	return nil
}

func (r *credentialVaultRepository) first(q *gorm.DB) (*types.CredentialVaultItem, error) {
	var item types.CredentialVaultItem
	err := q.First(&item).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get credential vault item: %w", err)
	}
	return &item, nil
}

func (r *credentialVaultRepository) Get(
	ctx context.Context, tenantID uint64, userID, id string,
) (*types.CredentialVaultItem, error) {
	return r.first(r.owned(ctx, tenantID, userID).Where("id = ?", id))
}

func (r *credentialVaultRepository) FindBySiteUsername(
	ctx context.Context, tenantID uint64, userID, site, username string,
) (*types.CredentialVaultItem, error) {
	return r.first(r.owned(ctx, tenantID, userID).Where("site = ? AND username = ?", site, username))
}

func (r *credentialVaultRepository) List(
	ctx context.Context, tenantID uint64, userID, site string,
) ([]*types.CredentialVaultItem, error) {
	q := r.owned(ctx, tenantID, userID)
	if site != "" {
		q = q.Where("site = ?", site)
	}
	var items []*types.CredentialVaultItem
	if err := q.Order("site ASC, username ASC").Find(&items).Error; err != nil {
		return nil, fmt.Errorf("list credential vault items: %w", err)
	}
	return items, nil
}

func (r *credentialVaultRepository) Count(ctx context.Context, tenantID uint64, userID string) (int64, error) {
	var n int64
	err := r.owned(ctx, tenantID, userID).Model(&types.CredentialVaultItem{}).Count(&n).Error
	if err != nil {
		return 0, fmt.Errorf("count credential vault items: %w", err)
	}
	return n, nil
}

func (r *credentialVaultRepository) Update(ctx context.Context, item *types.CredentialVaultItem) error {
	res := r.owned(ctx, item.TenantID, item.UserID).Model(&types.CredentialVaultItem{}).
		Where("id = ?", item.ID).
		Select("title", "domains", "username", "secret_ciphertext", "updated_at").
		Updates(item)
	if res.Error != nil {
		if isCredentialUniqueViolation(res.Error) {
			return types.ErrCredentialConflict
		}
		return fmt.Errorf("update credential vault item: %w", res.Error)
	}
	if res.RowsAffected == 0 {
		return types.ErrCredentialNotFound
	}
	return nil
}

func (r *credentialVaultRepository) Delete(ctx context.Context, tenantID uint64, userID, id string) (bool, error) {
	res := r.owned(ctx, tenantID, userID).Where("id = ?", id).Delete(&types.CredentialVaultItem{})
	if res.Error != nil {
		return false, fmt.Errorf("delete credential vault item: %w", res.Error)
	}
	return res.RowsAffected > 0, nil
}

func (r *credentialVaultRepository) Touch(ctx context.Context, tenantID uint64, userID, id string, at time.Time) error {
	return r.owned(ctx, tenantID, userID).Model(&types.CredentialVaultItem{}).
		Where("id = ?", id).UpdateColumn("last_used_at", at).Error
}

// isCredentialUniqueViolation recognizes the (site, username) index on both
// dialects, translated or not.
func isCredentialUniqueViolation(err error) bool {
	if errors.Is(err, gorm.ErrDuplicatedKey) {
		return true
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "duplicate") || strings.Contains(msg, "unique constraint")
}
