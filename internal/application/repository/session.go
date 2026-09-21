package repository

import (
	"context"
	stderrors "errors"
	"strings"
	"time"

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

// webSessionPredicate selects the "web" bucket of the session list: the
// user's own web chats. It excludes IM sessions (any im_channel_sessions
// mapping, including soft-deleted ones), embed-widget sessions (same IM-null
// row) and API-key sessions (surfaced only in the admin-only "api" bucket).
// The user_id NULL check keeps legacy tenant-level web rows visible, since
// "col NOT LIKE ?" is unknown (not true) for NULL.
//
// It expects sessions aliased as s and a
// "LEFT JOIN im_channel_sessions ics ON ics.session_id = s.id".
const webSessionPredicate = "ics.id IS NULL AND (s.description = '' OR s.description NOT LIKE ?) " +
	"AND (s.user_id IS NULL OR (s.user_id NOT LIKE ? AND s.user_id NOT LIKE ?))"

func webSessionPredicateArgs() []any {
	return []any{
		types.EmbedSessionMarkerPrefix + "%",
		types.SessionOwnerAPITenantKeyPrefix + "%",
		types.SessionOwnerAPIExternalUserPrefix + "%",
	}
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
	// Dialect-aware bits so the same query works on Postgres and SQLite (Lite build).
	isPostgres := r.db.Dialector.Name() == "postgres"
	titleLikeExpr := "LOWER(s.title) LIKE LOWER(?)"
	if isPostgres {
		titleLikeExpr = "s.title ILIKE ?"
	}
	// SQLite (the driver used by Lite) does not support NULLS LAST; its default
	// nulls ordering puts NULLs first for DESC, which is actually what we want
	// for pinned_at (rows with pinned_at=NULL are never pinned, so they get
	// filtered to the tail by the preceding is_pinned DESC anyway).
	orderClause := "s.is_pinned DESC, s.pinned_at DESC NULLS LAST, s.updated_at DESC"
	if !isPostgres {
		orderClause = "s.is_pinned DESC, s.pinned_at DESC, s.updated_at DESC"
	}

	// Base filter shared by count and list queries.
	applyBase := func(db *gorm.DB) *gorm.DB {
		db = db.Where("s.tenant_id = ? AND s.deleted_at IS NULL", q.TenantID)
		if q.UserID != "" {
			db = db.Where("(s.user_id = ? OR s.user_id IS NULL OR s.user_id = '')", q.UserID)
		}
		// Skill image maintenance runs in a real session so its transcript can
		// be read back, but it is not a conversation. Excluding it here rather
		// than in applySource is deliberate: a source branch only covers its
		// own bucket, and this row must be absent from all of them, including
		// the unfiltered listing.
		db = db.Where(
			"(s.description IS NULL OR s.description NOT LIKE ?)",
			types.SkillMaintenanceSessionMarker+"%",
		)
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
			// Tenant-wide view of API-key sessions. Requests without an
			// external identity use an api_tenant_key owner; requests with a
			// direct-header or signed-token identity use api_external_user.
			// The service layer already enforced Admin+ and cleared the
			// per-user scope for this source.
			return db.Where(
				"(s.user_id LIKE ? OR s.user_id LIKE ?)",
				types.SessionOwnerAPITenantKeyPrefix+"%",
				types.SessionOwnerAPIExternalUserPrefix+"%",
			)
		case "web":
			return db.Where(webSessionPredicate, webSessionPredicateArgs()...)
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

// CreateForked persists a forked session and its copied history atomically.
// A partially written fork would show up in the sidebar with a truncated or
// empty conversation, so both halves must land together.
func (r *sessionRepository) CreateForked(
	ctx context.Context, session *types.Session, messages []*types.Message,
) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		now := time.Now()
		session.CreatedAt = now
		session.UpdatedAt = now
		// Session.BeforeCreate would overwrite the ID the fork service already
		// assigned (the copied messages point at it), so write the row with
		// the hook skipped.
		if err := tx.Session(&gorm.Session{SkipHooks: true}).Create(session).Error; err != nil {
			return err
		}
		if len(messages) == 0 {
			return nil
		}
		if err := tx.Session(&gorm.Session{SkipHooks: true}).
			CreateInBatches(messages, 100).Error; err != nil {
			return err
		}
		return insertMessageArtifacts(tx, messages)
	})
}

// UpdateForkBootstrap overwrites a session's fork bootstrap. Passing nil clears
// it, which is how a failed or abandoned bootstrap is retired.
func (r *sessionRepository) UpdateForkBootstrap(
	ctx context.Context, sessionID string, b *types.ForkBootstrap,
) error {
	return r.db.WithContext(ctx).Unscoped().Model(&types.Session{}).
		Where("id = ?", sessionID).
		Update("fork_bootstrap", b).Error
}

// ListUnconsumedForks returns fork bootstraps the snapshot reaper should
// try to retire: unopened forks older than olderThan, plus consumed forks
// that still name a snapshot (Cube keeps runtime refs until both sandboxes
// exit, so AfterCreate often cannot delete immediately).
//
// Soft-deleted sessions are included: deleting an unopened fork hides the
// row from the default GORM scope, and this listing is the reaper's fallback
// when delete-path snapshot cleanup did not run.
func (r *sessionRepository) ListUnconsumedForks(
	ctx context.Context, olderThan time.Time,
) ([]*types.Session, error) {
	var sessions []*types.Session
	// Only the columns the reaper needs. The full session row includes large
	// JSONB (last_request_state) that this listing never reads.
	if err := r.db.WithContext(ctx).Unscoped().
		Model(&types.Session{}).
		Select("id", "tenant_id", "sandbox_config_id", "fork_bootstrap", "deleted_at").
		Where("fork_bootstrap IS NOT NULL").
		Find(&sessions).Error; err != nil {
		return nil, err
	}
	pending := make([]*types.Session, 0, len(sessions))
	for _, s := range sessions {
		if s == nil || s.ForkBootstrap == nil {
			continue
		}
		if strings.TrimSpace(s.ForkBootstrap.SnapshotID) == "" {
			continue
		}
		if s.ForkBootstrap.Consumed() {
			pending = append(pending, s)
			continue
		}
		if s.ForkBootstrap.CreatedAt.After(olderThan) {
			continue
		}
		pending = append(pending, s)
	}
	return pending, nil
}

func forkBootstrapJSONText(db *gorm.DB, key string) string {
	if db.Name() == "postgres" {
		return "fork_bootstrap ->> '" + key + "'"
	}
	return "json_extract(fork_bootstrap, '$." + key + "')"
}

func (r *sessionRepository) unconsumedForkSnapshotQuery(ctx context.Context, snapshotID string) *gorm.DB {
	snapExpr := forkBootstrapJSONText(r.db, "snapshot_id")
	consumedExpr := forkBootstrapJSONText(r.db, "consumed_at")
	// Matches idx_sessions_unconsumed_fork on Postgres: snapshot_id equality
	// under (fork_bootstrap IS NOT NULL AND consumed_at IS NULL).
	return r.db.WithContext(ctx).
		Model(&types.Session{}).
		Where("fork_bootstrap IS NOT NULL").
		Where(snapExpr+" = ?", snapshotID).
		Where(consumedExpr + " IS NULL")
}

func (r *sessionRepository) unconsumedForkSnapshotHolders(
	ctx context.Context, snapshotID string,
) ([]string, error) {
	snapshotID = strings.TrimSpace(snapshotID)
	if snapshotID == "" {
		return nil, nil
	}
	var ids []string
	if err := r.unconsumedForkSnapshotQuery(ctx, snapshotID).
		Pluck("id", &ids).Error; err != nil {
		return nil, err
	}
	return ids, nil
}

// HasOtherUnconsumedForkSnapshot reports whether another session still needs
// snapshotID to provision. Nested unused forks share one snapshot; deleting it
// when the first sibling boots would strand the rest.
func (r *sessionRepository) HasOtherUnconsumedForkSnapshot(
	ctx context.Context, snapshotID, excludeSessionID string,
) (bool, error) {
	snapshotID = strings.TrimSpace(snapshotID)
	if snapshotID == "" {
		return false, nil
	}
	q := r.unconsumedForkSnapshotQuery(ctx, snapshotID)
	if excludeSessionID != "" {
		q = q.Where("id <> ?", excludeSessionID)
	}
	var ids []string
	if err := q.Limit(1).Pluck("id", &ids).Error; err != nil {
		return false, err
	}
	return len(ids) > 0, nil
}

// UnconsumedForkSnapshotHolders returns session IDs that still need snapshotID
// to boot.
func (r *sessionRepository) UnconsumedForkSnapshotHolders(
	ctx context.Context, snapshotID string,
) ([]string, error) {
	return r.unconsumedForkSnapshotHolders(ctx, snapshotID)
}

func (r *sessionRepository) CreateForkSnapshotLease(
	ctx context.Context, lease *types.ForkSnapshotLease,
) error {
	if lease == nil || strings.TrimSpace(lease.SnapshotID) == "" {
		return nil
	}
	if lease.CreatedAt.IsZero() {
		lease.CreatedAt = time.Now().UTC()
	}
	return r.db.WithContext(ctx).Create(lease).Error
}

func (r *sessionRepository) DeleteForkSnapshotLease(ctx context.Context, snapshotID string) error {
	snapshotID = strings.TrimSpace(snapshotID)
	if snapshotID == "" {
		return nil
	}
	return r.db.WithContext(ctx).
		Where("snapshot_id = ?", snapshotID).
		Delete(&types.ForkSnapshotLease{}).Error
}

func (r *sessionRepository) ListStaleForkSnapshotLeases(
	ctx context.Context, olderThan time.Time,
) ([]*types.ForkSnapshotLease, error) {
	var leases []*types.ForkSnapshotLease
	if err := r.db.WithContext(ctx).
		Where("created_at <= ?", olderThan).
		Find(&leases).Error; err != nil {
		return nil, err
	}
	return leases, nil
}
