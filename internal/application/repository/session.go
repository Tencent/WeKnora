package repository

import (
	"context"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"gorm.io/gorm"
)

// sessionRepository implements the SessionRepository interface
type sessionRepository struct {
	db *gorm.DB
}

// NewSessionRepository creates a new session repository instance
func NewSessionRepository(db *gorm.DB) interfaces.SessionRepository {
	return &sessionRepository{db: db}
}

// Create creates a new session
func (r *sessionRepository) Create(ctx context.Context, session *types.Session) (*types.Session, error) {
	session.CreatedAt = time.Now()
	session.UpdatedAt = time.Now()
	if err := r.db.WithContext(ctx).Create(session).Error; err != nil {
		return nil, err
	}
	// Return the session with generated ID
	return session, nil
}

// Get retrieves a session by ID
func (r *sessionRepository) Get(ctx context.Context, tenantID uint64, id string) (*types.Session, error) {
	var session types.Session
	err := r.db.WithContext(ctx).Where("tenant_id = ?", tenantID).First(&session, "id = ?", id).Error
	if err != nil {
		return nil, err
	}
	return &session, nil
}

// GetByTenantID retrieves all sessions for a tenant
func (r *sessionRepository) GetByTenantID(ctx context.Context, tenantID uint64) ([]*types.Session, error) {
	var sessions []*types.Session
	err := r.db.WithContext(ctx).Where("tenant_id = ?", tenantID).Order("created_at DESC").Find(&sessions).Error
	if err != nil {
		return nil, err
	}
	return sessions, nil
}

// GetPagedByTenantID retrieves sessions for a tenant with pagination
func (r *sessionRepository) GetPagedByTenantID(
	ctx context.Context, tenantID uint64, page *types.Pagination,
) ([]*types.Session, int64, error) {
	var sessions []*types.Session
	var total int64

	// First query the total count
	err := r.db.WithContext(ctx).Model(&types.Session{}).Where("tenant_id = ?", tenantID).Count(&total).Error
	if err != nil {
		return nil, 0, err
	}

	// Then query the paginated data
	err = r.db.WithContext(ctx).
		Where("tenant_id = ?", tenantID).
		Order("created_at DESC").
		Offset(page.Offset()).
		Limit(page.Limit()).
		Find(&sessions).Error
	if err != nil {
		return nil, 0, err
	}

	return sessions, total, nil
}

// Update updates a session
func (r *sessionRepository) Update(ctx context.Context, session *types.Session) error {
	session.UpdatedAt = time.Now()
	return r.db.WithContext(ctx).Where("tenant_id = ?", session.TenantID).Save(session).Error
}

// Delete deletes a session
func (r *sessionRepository) Delete(ctx context.Context, tenantID uint64, id string) error {
	return r.db.WithContext(ctx).Where("tenant_id = ?", tenantID).Delete(&types.Session{}, "id = ?", id).Error
}

// BatchDelete deletes multiple sessions by IDs
func (r *sessionRepository) BatchDelete(ctx context.Context, tenantID uint64, ids []string) error {
	if len(ids) == 0 {
		return nil
	}
	return r.db.WithContext(ctx).Where("tenant_id = ? AND id IN ?", tenantID, ids).Delete(&types.Session{}).Error
}
