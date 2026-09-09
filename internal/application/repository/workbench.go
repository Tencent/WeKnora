package repository

import (
	"context"
	"errors"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"gorm.io/gorm"
)

type workbenchAuthorizationRepository struct {
	db *gorm.DB
}

// NewWorkbenchAuthorizationRepository provides uncached, narrow identity reads.
func NewWorkbenchAuthorizationRepository(db *gorm.DB) interfaces.WorkbenchAuthorizationRepository {
	return &workbenchAuthorizationRepository{db: db}
}

func (r *workbenchAuthorizationRepository) GetAuthorization(
	ctx context.Context, tenantID uint64, userID string,
) (*types.WorkbenchAuthorization, error) {
	var row types.WorkbenchAuthorization
	err := r.db.WithContext(ctx).Table("users AS u").
		Select("u.id AS user_id, u.is_active AS user_active, t.id AS tenant_id, "+
			"t.status AS tenant_status, m.status AS member_status").
		Joins("JOIN tenant_members AS m ON m.user_id = u.id AND m.deleted_at IS NULL").
		Joins("JOIN tenants AS t ON t.id = m.tenant_id AND t.deleted_at IS NULL").
		Where("u.id = ? AND t.id = ? AND u.deleted_at IS NULL", userID, tenantID).
		Take(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &row, nil
}

func (r *workbenchAuthorizationRepository) HasIMSession(
	ctx context.Context, tenantID uint64, sessionID string,
) (bool, error) {
	var found int
	result := r.db.WithContext(ctx).Model(&types.Session{}).
		Select("1").
		Where("tenant_id = ? AND id = ?", tenantID, sessionID).
		Where("EXISTS (SELECT 1 FROM im_channel_sessions AS ics WHERE ics.session_id = sessions.id)").
		Limit(1).Scan(&found)
	return result.RowsAffected > 0, result.Error
}
