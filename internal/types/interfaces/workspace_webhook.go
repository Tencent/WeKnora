package interfaces

import (
	"context"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
)

// WorkspaceEventSink persists a domain event to the webhook outbox.
// Emit never rolls back the caller; insert failures are retried then logged.
// Emit skips outbox when the tenant has no enabled endpoint subscribed to
// the event type (see WebhookSubscriptionIndex).
type WorkspaceEventSink interface {
	Emit(ctx context.Context, ev types.WorkspaceEvent)
}

// WebhookSubscriptionIndex answers whether Emit should write an outbox row.
// Redis caches the union of enabled endpoint event types per tenant; DB is
// the source of truth. Invalidate after endpoint create/update/delete; Warm
// rebuilds the cache so the next Emit does not hit a negative-cache window.
type WebhookSubscriptionIndex interface {
	Subscribes(ctx context.Context, tenantID uint64, eventType string) bool
	Invalidate(ctx context.Context, tenantID uint64) error
	Warm(ctx context.Context, tenantID uint64)
}

type WebhookEndpointRepository interface {
	Create(ctx context.Context, ep *types.TenantWebhookEndpoint) error
	Update(ctx context.Context, ep *types.TenantWebhookEndpoint) error
	SoftDelete(ctx context.Context, tenantID uint64, id string) error
	GetByID(ctx context.Context, tenantID uint64, id string) (*types.TenantWebhookEndpoint, error)
	GetByIDUnscoped(ctx context.Context, id string) (*types.TenantWebhookEndpoint, error)
	ListByTenant(ctx context.Context, tenantID uint64) ([]*types.TenantWebhookEndpoint, error)
	ListEnabledByTenant(ctx context.Context, tenantID uint64) ([]*types.TenantWebhookEndpoint, error)
	CountByTenant(ctx context.Context, tenantID uint64) (int64, error)
	FindByTenantURL(ctx context.Context, tenantID uint64, url string) (*types.TenantWebhookEndpoint, error)
}

type WebhookOutboxRepository interface {
	Insert(ctx context.Context, row *types.TenantWebhookOutbox) error
	GetByEventID(ctx context.Context, eventID string) (*types.TenantWebhookOutbox, error)
	ListPending(ctx context.Context, limit int) ([]*types.TenantWebhookOutbox, error)
	MarkProcessed(ctx context.Context, eventID string) error
	MarkFailed(ctx context.Context, eventID, lastError string) error
	BumpAttempts(ctx context.Context, eventID, lastError string) error
	DeleteProcessedBefore(ctx context.Context, cutoff time.Time) (int64, error)
}

type WebhookDeliveryRepository interface {
	Claim(ctx context.Context, row *types.TenantWebhookDelivery) (created bool, existing *types.TenantWebhookDelivery, err error)
	GetByEventEndpoint(ctx context.Context, eventID, endpointID string) (*types.TenantWebhookDelivery, error)
	UpdateAttempt(ctx context.Context, deliveryID string, httpStatus, attempts, durationMs int, errMsg, status string, finished bool) error
	ListByEndpoint(ctx context.Context, endpointID string, limit int) ([]*types.TenantWebhookDelivery, error)
	ListEndpointIDs(ctx context.Context) ([]string, error)
	PruneEndpointKeepLatest(ctx context.Context, endpointID string, keep int) (int64, error)
	DeleteOlderThanDays(ctx context.Context, days int) (int64, error)
}

type WebhookDispatcher interface {
	Dispatch(ctx context.Context, eventID string) error
	DispatchTest(ctx context.Context, tenantID uint64, endpointID string) error
	SweepPending(ctx context.Context) error
	Prune(ctx context.Context) error
}

type WebhookDeliverer interface {
	Deliver(ctx context.Context, payload types.WebhookDeliverPayload) error
}

type WebhookEndpointService interface {
	List(ctx context.Context, tenantID uint64) ([]types.WebhookEndpointPublic, error)
	Create(ctx context.Context, tenantID uint64, in WebhookEndpointCreate) (*types.WebhookEndpointPublic, error)
	Update(ctx context.Context, tenantID uint64, hookID string, in WebhookEndpointUpdate) (*types.WebhookEndpointPublic, error)
	Delete(ctx context.Context, tenantID uint64, hookID string) error
	Test(ctx context.Context, tenantID uint64, hookID string) error
	ListDeliveries(ctx context.Context, tenantID uint64, hookID string, limit int) ([]*types.TenantWebhookDelivery, error)
	EventTypes() []string
}

type WebhookEndpointCreate struct {
	Name        string
	URL         string
	Secret      string
	Events      []string
	Enabled     *bool
	Description string
}

type WebhookEndpointUpdate struct {
	Name        *string
	URL         *string
	Secret      *string
	Events      []string
	Enabled     *bool
	Description *string
}
