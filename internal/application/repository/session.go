package repository

import (
	"context"
	stderrors "errors"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/database"
	apperrors "github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"gorm.io/gorm"
)

// sessionRepository implements the SessionRepository interface
type sessionRepository struct {
	db *gorm.DB
}

func applySessionUserScope(db *gorm.DB, userID string) *gorm.DB {
	if userID == "" {
		return db
	}
	// Empty user_id rows are legacy/API-created tenant-level sessions.
	return db.Where("(user_id = ? OR user_id IS NULL OR user_id = '')", userID)
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
func (r *sessionRepository) Get(ctx context.Context, tenantID uint64, userID string, id string) (*types.Session, error) {
	var session types.Session
	err := applySessionUserScope(
		r.db.WithContext(ctx).Where("tenant_id = ? AND id = ?", tenantID, id),
		userID,
	).First(&session).Error
	if err != nil {
		if stderrors.Is(err, gorm.ErrRecordNotFound) {
			return nil, apperrors.ErrSessionNotFound
		}
		return nil, err
	}
	return &session, nil
}

// GetByID retrieves a session by tenant and id without user scoping.
func (r *sessionRepository) GetByID(ctx context.Context, tenantID uint64, id string) (*types.Session, error) {
	var session types.Session
	err := r.db.WithContext(ctx).
		Where("tenant_id = ? AND id = ?", tenantID, id).
		First(&session).Error
	if err != nil {
		if stderrors.Is(err, gorm.ErrRecordNotFound) {
			return nil, apperrors.ErrSessionNotFound
		}
		return nil, err
	}
	return &session, nil
}

// GetIMPlatform returns the IM platform bound to a session, or "" when none.
// It intentionally ignores soft-deleted mappings' visibility rules used by
// QueryPaged: any mapping (active or cleared) marks the session as IM-origin.
func (r *sessionRepository) GetIMPlatform(
	ctx context.Context, tenantID uint64, sessionID string,
) (string, error) {
	var platform string
	err := r.db.WithContext(ctx).
		Table("im_channel_sessions AS ics").
		Joins("JOIN sessions AS s ON s.id = ics.session_id").
		Where("ics.session_id = ? AND s.tenant_id = ?", sessionID, tenantID).
		Limit(1).
		Pluck("ics.platform", &platform).Error
	if err != nil {
		return "", err
	}
	return platform, nil
}

// GetByTenantID retrieves all sessions for a tenant
func (r *sessionRepository) GetByTenantID(ctx context.Context, tenantID uint64, userID string) ([]*types.Session, error) {
	var sessions []*types.Session
	err := applySessionUserScope(
		r.db.WithContext(ctx).Where("tenant_id = ?", tenantID),
		userID,
	).Order("updated_at DESC").Find(&sessions).Error
	if err != nil {
		return nil, err
	}
	return sessions, nil
}

// GetPagedByTenantID retrieves sessions for a tenant with pagination
func (r *sessionRepository) GetPagedByTenantID(
	ctx context.Context, tenantID uint64, userID string, page *types.Pagination,
) ([]*types.Session, int64, error) {
	var sessions []*types.Session
	var total int64

	// First query the total count
	baseQ := applySessionUserScope(
		r.db.WithContext(ctx).Model(&types.Session{}).Where("tenant_id = ?", tenantID),
		userID,
	)
	err := baseQ.Count(&total).Error
	if err != nil {
		return nil, 0, err
	}

	// Then query the paginated data
	err = applySessionUserScope(
		r.db.WithContext(ctx).Where("tenant_id = ?", tenantID),
		userID,
	).
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
	dialect := r.db.Dialector.Name()
	isPostgres := dialect == "postgres"
	titleLikeExpr := database.CaseInsensitiveMatch(dialect, "s.title")
	// SQLite (the driver used by Lite) does not support NULLS LAST; its default
	// nulls ordering puts NULLs first for DESC, which is actually what we want
	// for pinned_at (rows with pinned_at=NULL are never pinned, so they get
	// filtered to the tail by the preceding is_pinned DESC anyway).
	orderClause := "s.is_pinned DESC, s.pinned_at DESC NULLS LAST, s.updated_at DESC"
	if dialect == "mysql" {
		orderClause = "s.is_pinned DESC, CASE WHEN s.pinned_at IS NULL THEN 1 ELSE 0 END, s.pinned_at DESC, s.updated_at DESC"
	} else if !isPostgres {
		orderClause = "s.is_pinned DESC, s.pinned_at DESC, s.updated_at DESC"
	}

	// Base filter shared by count and list queries.
	applyBase := func(db *gorm.DB) *gorm.DB {
		db = db.Where("s.tenant_id = ? AND s.deleted_at IS NULL", q.TenantID)
		if q.UserID != "" {
			db = db.Where("(s.user_id = ? OR s.user_id IS NULL OR s.user_id = '')", q.UserID)
		}
		if kw := strings.TrimSpace(q.Keyword); kw != "" {
			db = db.Where(titleLikeExpr, "%"+escapeLikeKeyword(kw)+"%")
		}
		return db
	}

	// LEFT JOIN IM mappings to surface origin fields and support source/agent filters.
	// Soft-deleted mappings are intentionally included: a session that was ever bound
	// to an IM channel belongs to that platform, not "web". /clear and session
	// recycling soft-delete the mapping (and start a fresh session), so filtering
	// deleted mappings out here would mis-bucket those past IM conversations into the
	// user's own web chats ("web" = ics.id IS NULL).
	// Safe from row fan-out because the IM flow only ever creates a *fresh* session
	// for a new mapping (never re-maps an existing one), so a session has at most one
	// mapping row. If that ever changes, this JOIN would need a one-row-per-session
	// guard (the unique index only constrains active mappings).
	joinClause := "LEFT JOIN im_channel_sessions ics ON ics.session_id = s.id"

	applySource := func(db *gorm.DB) *gorm.DB {
		src := strings.TrimSpace(q.Source)
		lower := strings.ToLower(src)
		embedPrefix := types.EmbedSessionMarkerPrefix
		switch lower {
		case "":
			return db
		case types.SessionSourceAPI:
			// Tenant-wide view of API-key sessions. Their owner id is
			// "api_tenant_key:<tenantID>:<keyID>", so a prefix match selects
			// every key's sessions. The service layer already enforced Admin+
			// and cleared the per-user scope for this source.
			return db.Where("s.user_id LIKE ?", types.SessionOwnerAPITenantKeyPrefix+"%")
		case "web":
			// User web chats only — exclude embed-widget sessions (same IM-null
			// row) and tenant API-key sessions (surfaced only in the admin-only
			// "api" bucket). The user_id NULL check keeps legacy tenant-level web
			// rows visible, since "col NOT LIKE ?" is unknown (not true) for NULL.
			return db.Where(
				"ics.id IS NULL AND (s.description = '' OR s.description NOT LIKE ?) "+
					"AND (s.user_id IS NULL OR s.user_id NOT LIKE ?)",
				embedPrefix+"%", types.SessionOwnerAPITenantKeyPrefix+"%",
			)
		case "embed":
			return db.Where("ics.id IS NULL AND s.description LIKE ?", embedPrefix+"%")
		default:
			if strings.HasPrefix(lower, "embed:") {
				channelID := strings.TrimSpace(src[len("embed:"):])
				if channelID != "" {
					return db.Where("ics.id IS NULL AND s.description = ?", embedPrefix+channelID)
				}
			}
			return db.Where("ics.platform = ?", lower)
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
		Order(orderClause).
		Offset((page - 1) * size).
		Limit(size)
	if err := rowsQ.Find(&items).Error; err != nil {
		return nil, 0, err
	}

	return items, total, nil
}

// SetPinned toggles is_pinned/pinned_at for a single session.
// Scope: must match tenant, and user_id (when provided) to prevent pinning
// other users' sessions. Legacy rows with user_id NULL/” remain mutable
// at the tenant level (same visibility rule as QueryPaged).
//
// Returns the number of rows affected so callers can distinguish "session
// doesn't exist / not visible to this user" (0) from a real DB error.
func (r *sessionRepository) SetPinned(
	ctx context.Context, tenantID uint64, userID string, id string, pinned bool,
) (int64, error) {
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
	res := q.Updates(updates)
	return res.RowsAffected, res.Error
}

// Update updates a session
func (r *sessionRepository) Update(ctx context.Context, session *types.Session, userID string) (int64, error) {
	session.UpdatedAt = time.Now()
	res := applySessionUserScope(r.db.WithContext(ctx).
		Model(&types.Session{}).
		Where("tenant_id = ? AND id = ?", session.TenantID, session.ID), userID).
		Updates(map[string]interface{}{
			"title":       session.Title,
			"description": session.Description,
			"updated_at":  session.UpdatedAt,
		})
	return res.RowsAffected, res.Error
}

// SetOwnerID assigns sessions.user_id for a tenant-scoped row.
func (r *sessionRepository) SetOwnerID(ctx context.Context, tenantID uint64, id, ownerID string) (int64, error) {
	res := r.db.WithContext(ctx).
		Model(&types.Session{}).
		Where("tenant_id = ? AND id = ?", tenantID, id).
		Updates(map[string]interface{}{
			"user_id":    ownerID,
			"updated_at": time.Now(),
		})
	return res.RowsAffected, res.Error
}

// UpdateLastRequestState writes only the agent_config column (used here to
// store SessionLastRequestState) and bumps updated_at. We deliberately bypass
// the regular Update path so the call doesn't perturb title/description and
// stays cheap (single-row UPDATE by PK).
func (r *sessionRepository) UpdateLastRequestState(
	ctx context.Context, tenantID uint64, userID string, sessionID string,
	state *types.SessionLastRequestState,
) (int64, error) {
	now := time.Now()
	var stateValue interface{}
	if state != nil {
		v, err := state.Value()
		if err != nil {
			return 0, err
		}
		stateValue = v
	}
	res := applySessionUserScope(r.db.WithContext(ctx).
		Model(&types.Session{}).
		Where("tenant_id = ? AND id = ?", tenantID, sessionID), userID).
		Updates(map[string]interface{}{
			"agent_config": stateValue,
			"updated_at":   now,
		})
	return res.RowsAffected, res.Error
}

// Delete deletes a session
func (r *sessionRepository) Delete(ctx context.Context, tenantID uint64, userID string, id string) (int64, error) {
	res := applySessionUserScope(
		r.db.WithContext(ctx).Where("tenant_id = ? AND id = ?", tenantID, id),
		userID,
	).Delete(&types.Session{})
	return res.RowsAffected, res.Error
}

// BatchDelete deletes multiple sessions by IDs
func (r *sessionRepository) BatchDelete(ctx context.Context, tenantID uint64, userID string, ids []string) (int64, error) {
	if len(ids) == 0 {
		return 0, nil
	}
	res := applySessionUserScope(
		r.db.WithContext(ctx).Where("tenant_id = ? AND id IN ?", tenantID, ids),
		userID,
	).Delete(&types.Session{})
	return res.RowsAffected, res.Error
}

// DeleteAllByTenantID deletes all sessions for a tenant
func (r *sessionRepository) DeleteAllByTenantID(ctx context.Context, tenantID uint64, userID string) (int64, error) {
	res := applySessionUserScope(
		r.db.WithContext(ctx).Where("tenant_id = ?", tenantID),
		userID,
	).Delete(&types.Session{})
	return res.RowsAffected, res.Error
}
