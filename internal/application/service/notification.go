package service

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/url"
	"strings"
	"time"

	apperrors "github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"gorm.io/gorm"
)

// notificationRetention is how long a notification is kept.
const notificationRetention = 30 * 24 * time.Hour

// NotificationService keeps in-app notifications.
type NotificationService struct {
	db  *gorm.DB
	now func() time.Time
}

// NewNotificationService builds the service.
func NewNotificationService(db *gorm.DB) *NotificationService {
	return &NotificationService{db: db, now: time.Now}
}

// Notify records n. A failure is logged, not returned: a notification never
// decides whether the work it reports succeeded.
func (s *NotificationService) Notify(ctx context.Context, n *types.Notification) {
	if s == nil || n == nil || n.TenantID == 0 || n.UserID == "" {
		return
	}
	if r := []rune(n.Title); len(r) > 250 {
		n.Title = string(r[:250]) + "…"
	}
	if r := []rune(n.Body); len(r) > 2000 {
		n.Body = string(r[:2000]) + "…"
	}
	if err := s.db.WithContext(context.WithoutCancel(ctx)).Create(n).Error; err != nil {
		logger.Warnf(ctx, "Record notification %s: %v", n.Kind, err)
	}
}

// PendingNotification is actionable state, independent of notification read state.
// Its ID is the current prompt, so successive approvals in one run do not collide.
type PendingNotification struct {
	ID        string    `json:"id"`
	Title     string    `json:"title"`
	Kind      string    `json:"kind"`
	Link      string    `json:"link"`
	CreatedAt time.Time `json:"created_at"`
}

type NotificationPage struct {
	Items      []*types.Notification  `json:"items"`
	Pending    []*PendingNotification `json:"pending"`
	Unread     int64                  `json:"unread"`
	ReadCount  int64                  `json:"read_count"`
	NextCursor string                 `json:"next_cursor"`
}

type notificationCursor struct {
	CreatedAt time.Time `json:"created_at"`
	ID        string    `json:"id"`
}

// history excludes approval events: their live prompts are returned separately.
func (s *NotificationService) history(ctx context.Context) *gorm.DB {
	tenantID, userID := notificationOwner(ctx)
	return s.db.WithContext(ctx).Model(&types.Notification{}).
		Where("tenant_id = ? AND user_id = ? AND kind <> ?",
			tenantID, userID, types.NotificationAutomationAwaitingApproval)
}

// List pages history in stable descending order and always includes current work.
func (s *NotificationService) List(
	ctx context.Context, limit int, unreadOnly bool, cursor string,
) (*NotificationPage, error) {
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	query := s.history(ctx)
	if cursor != "" {
		if len(cursor) > 1024 {
			return nil, apperrors.NewBadRequestError("invalid notification cursor")
		}
		var c notificationCursor
		raw, err := base64.RawURLEncoding.DecodeString(cursor)
		if err != nil || json.Unmarshal(raw, &c) != nil || c.ID == "" || c.CreatedAt.IsZero() {
			return nil, apperrors.NewBadRequestError("invalid notification cursor")
		}
		query = query.Where("created_at < ? OR (created_at = ? AND id < ?)", c.CreatedAt, c.CreatedAt, c.ID)
	}
	if unreadOnly {
		query = query.Where("read_at IS NULL")
	}
	page := &NotificationPage{Items: []*types.Notification{}}
	if err := query.Order("created_at DESC").Order("id DESC").Limit(limit + 1).Find(&page.Items).Error; err != nil {
		return nil, err
	}
	if len(page.Items) > limit {
		page.Items = page.Items[:limit]
		last := page.Items[limit-1]
		raw, _ := json.Marshal(notificationCursor{CreatedAt: last.CreatedAt, ID: last.ID})
		page.NextCursor = base64.RawURLEncoding.EncodeToString(raw)
	}
	if err := s.history(ctx).Where("read_at IS NULL").Count(&page.Unread).Error; err != nil {
		return nil, err
	}
	if err := s.history(ctx).Where("read_at IS NOT NULL").Count(&page.ReadCount).Error; err != nil {
		return nil, err
	}
	pending, err := s.pending(ctx)
	if err != nil {
		return nil, err
	}
	page.Pending = pending
	return page, nil
}

func (s *NotificationService) pending(ctx context.Context) ([]*PendingNotification, error) {
	tenantID, userID := notificationOwner(ctx)
	now := s.now()
	var rows []struct {
		ID                     string
		Title                  string
		Kind                   string
		SessionID              string
		AutomationID           string
		RunID                  string
		CreatedAt              time.Time
		WaitingSince           time.Time
		ApprovalTimeoutMinutes int
	}
	// Scope every joined resource. A deleted/cancelled/answered/expired run is no
	// longer actionable even if its old notification has never been opened.
	err := s.db.WithContext(ctx).Table("automation_runs AS r").
		Select("p.id, a.name AS title, p.kind, r.session_id, a.id AS automation_id, r.id AS run_id, "+
			"p.created_at, r.updated_at AS waiting_since, a.approval_timeout_minutes").
		Joins("JOIN automations AS a ON a.id = r.automation_id AND a.tenant_id = r.tenant_id").
		Joins("JOIN agent_prompts AS p ON p.id = r.pending_prompt_id "+
			"AND p.tenant_id = r.tenant_id AND p.session_id = r.session_id").
		Where("a.tenant_id = ? AND a.owner_user_id = ? AND a.deleted_at IS NULL", tenantID, userID).
		Where("r.status = ? AND r.cancel_requested = ?", types.AutomationRunAwaitingApproval, false).
		Where("p.user_id = ? AND p.status IN ?", automationGrantUser(userID),
			[]string{types.AgentPromptPending, types.AgentPromptSuspended}).
		Where("p.expires_at IS NULL OR p.expires_at > ?", now).
		Order("p.created_at ASC").Order("p.id ASC").Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	pending := make([]*PendingNotification, 0, len(rows))
	for _, row := range rows {
		if !now.Before(row.WaitingSince.Add(time.Duration(row.ApprovalTimeoutMinutes) * time.Minute)) {
			continue
		}
		link := "/platform/automations/" + url.PathEscape(row.AutomationID) + "?run=" + url.QueryEscape(row.RunID)
		if row.SessionID != "" {
			link = "/platform/chat/" + url.PathEscape(row.SessionID)
		}
		pending = append(pending, &PendingNotification{
			ID: row.ID, Title: row.Title, Kind: row.Kind, Link: link, CreatedAt: row.CreatedAt,
		})
	}
	return pending, nil
}

// ClearRead removes only read history belonging to the caller. Pending work
// comes from live runs and prompts and is never affected by this operation.
func (s *NotificationService) ClearRead(ctx context.Context) error {
	return s.history(ctx).Where("read_at IS NOT NULL").Delete(&types.Notification{}).Error
}

// MarkRead marks the caller's history ids read, or all history when ids is empty.
// Live pending work is independent of read state.
func (s *NotificationService) MarkRead(ctx context.Context, ids []string) error {
	query := s.history(ctx).Where("read_at IS NULL")
	if len(ids) > 0 {
		query = query.Where("id IN ?", ids)
	}
	return query.Update("read_at", time.Now()).Error
}

// Prune drops notifications past retention.
func (s *NotificationService) Prune(ctx context.Context) error {
	return s.db.WithContext(ctx).
		Where("created_at < ?", time.Now().Add(-notificationRetention)).
		Delete(&types.Notification{}).Error
}

func notificationOwner(ctx context.Context) (uint64, string) {
	caller := types.CallerFromContext(ctx)
	return caller.TenantID, strings.TrimSpace(caller.UserID)
}
