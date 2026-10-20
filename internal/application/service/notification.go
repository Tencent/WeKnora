package service

import (
	"context"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"gorm.io/gorm"
)

// notificationRetention is how long a notification is kept.
const notificationRetention = 30 * 24 * time.Hour

// NotificationService keeps in-app notifications.
type NotificationService struct {
	db *gorm.DB
}

// NewNotificationService builds the service.
func NewNotificationService(db *gorm.DB) *NotificationService {
	return &NotificationService{db: db}
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

// NotificationPage is one page of the caller's notifications.
type NotificationPage struct {
	Items  []*types.Notification `json:"items"`
	Unread int64                 `json:"unread"`
}

// List returns the caller's newest notifications, at most limit, and how
// many are unread. unreadOnly leaves out the read ones.
func (s *NotificationService) List(ctx context.Context, limit int, unreadOnly bool) (*NotificationPage, error) {
	tenantID, userID := notificationOwner(ctx)
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	mine := func() *gorm.DB {
		return s.db.WithContext(ctx).Model(&types.Notification{}).
			Where("tenant_id = ? AND user_id = ?", tenantID, userID)
	}
	page := &NotificationPage{Items: []*types.Notification{}}
	query := mine()
	if unreadOnly {
		query = query.Where("read_at IS NULL")
	}
	if err := query.Order("created_at DESC").Limit(limit).Find(&page.Items).Error; err != nil {
		return nil, err
	}
	if err := mine().Where("read_at IS NULL").Count(&page.Unread).Error; err != nil {
		return nil, err
	}
	return page, nil
}

// MarkRead marks the caller's notifications ids read, or all of them when
// ids is empty.
func (s *NotificationService) MarkRead(ctx context.Context, ids []string) error {
	tenantID, userID := notificationOwner(ctx)
	query := s.db.WithContext(ctx).Model(&types.Notification{}).
		Where("tenant_id = ? AND user_id = ? AND read_at IS NULL", tenantID, userID)
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
