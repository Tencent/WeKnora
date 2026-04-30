package repository

import (
	"context"
	"strings"
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
	err := r.db.WithContext(ctx).Where("tenant_id = ?", tenantID).Order("updated_at DESC").Find(&sessions).Error
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
		Order("updated_at DESC").
		Offset(page.Offset()).
		Limit(page.Limit()).
		Find(&sessions).Error
	if err != nil {
		return nil, 0, err
	}

	return sessions, total, nil
}

// QueryPaged lists sessions for tenant/user with keyword/source/agent filters,
// pin-aware ordering, and IM origin fields from a LEFT JOIN.
func (r *sessionRepository) QueryPaged(
	ctx context.Context, q *types.SessionListQuery,
) ([]*types.SessionListItem, int64, error) {
	// Base filter shared by count and list queries.
	applyBase := func(db *gorm.DB) *gorm.DB {
		db = db.Where("s.tenant_id = ? AND s.deleted_at IS NULL", q.TenantID)
		if q.UserID != "" {
			db = db.Where("(s.user_id = ? OR s.user_id IS NULL OR s.user_id = '')", q.UserID)
		}
		if kw := strings.TrimSpace(q.Keyword); kw != "" {
			db = db.Where("s.title ILIKE ?", "%"+kw+"%")
		}
		return db
	}

	// LEFT JOIN IM mappings to surface origin fields and support source/agent filters.
	joinClause := "LEFT JOIN im_channel_sessions ics ON ics.session_id = s.id AND ics.deleted_at IS NULL"

	applySource := func(db *gorm.DB) *gorm.DB {
		switch strings.ToLower(strings.TrimSpace(q.Source)) {
		case "":
			return db
		case "web":
			return db.Where("ics.id IS NULL")
		default:
			return db.Where("ics.platform = ?", strings.ToLower(q.Source))
		}
	}
	applyAgent := func(db *gorm.DB) *gorm.DB {
		if q.AgentID != "" {
			return db.Where("ics.agent_id = ?", q.AgentID)
		}
		return db
	}

	// Count distinct sessions to guard against fan-out from the join.
	var total int64
	countQ := applyAgent(applySource(applyBase(
		r.db.WithContext(ctx).Table("sessions AS s").Joins(joinClause),
	)))
	if err := countQ.Distinct("s.id").Count(&total).Error; err != nil {
		return nil, 0, err
	}

	page := q.Page
	if page < 1 {
		page = 1
	}
	size := q.PageSize
	if size < 1 {
		size = 20
	}

	items := make([]*types.SessionListItem, 0)
	rowsQ := applyAgent(applySource(applyBase(
		r.db.WithContext(ctx).Table("sessions AS s").Joins(joinClause),
	))).
		Select(`s.*,
			ics.platform       AS im_platform,
			ics.chat_id        AS im_chat_id,
			ics.thread_id      AS im_thread_id,
			ics.user_id        AS im_user_id,
			ics.agent_id       AS im_agent_id,
			ics.im_channel_id  AS im_channel_id`).
		Order("s.is_pinned DESC, s.pinned_at DESC NULLS LAST, s.updated_at DESC").
		Offset((page - 1) * size).
		Limit(size)
	if err := rowsQ.Find(&items).Error; err != nil {
		return nil, 0, err
	}

	return items, total, nil
}

// SetPinned toggles is_pinned/pinned_at for a single session.
// Scope: must match tenant, and user_id (when provided) to prevent pinning
// other users' sessions. Legacy rows with user_id NULL/'' remain mutable
// at the tenant level (same visibility rule as QueryPaged).
func (r *sessionRepository) SetPinned(
	ctx context.Context, tenantID uint64, userID string, id string, pinned bool,
) error {
	now := time.Now()
	updates := map[string]interface{}{
		"is_pinned":  pinned,
		"updated_at": now,
	}
	if pinned {
		updates["pinned_at"] = now
	} else {
		updates["pinned_at"] = nil
	}

	q := r.db.WithContext(ctx).
		Model(&types.Session{}).
		Where("tenant_id = ? AND id = ?", tenantID, id)
	if userID != "" {
		q = q.Where("(user_id = ? OR user_id IS NULL OR user_id = '')", userID)
	}
	return q.Updates(updates).Error
}

// Update updates a session
func (r *sessionRepository) Update(ctx context.Context, session *types.Session) error {
	session.UpdatedAt = time.Now()
	return r.db.WithContext(ctx).
		Model(&types.Session{}).
		Where("tenant_id = ? AND id = ?", session.TenantID, session.ID).
		Updates(map[string]interface{}{
			"title":       session.Title,
			"description": session.Description,
			"updated_at":  session.UpdatedAt,
		}).Error
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

// DeleteAllByTenantID deletes all sessions for a tenant
func (r *sessionRepository) DeleteAllByTenantID(ctx context.Context, tenantID uint64) error {
	return r.db.WithContext(ctx).Where("tenant_id = ?", tenantID).Delete(&types.Session{}).Error
}
