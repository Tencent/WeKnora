package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strings"
	"time"

	apperrors "github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	secutils "github.com/Tencent/WeKnora/internal/utils"
	"github.com/Tencent/WeKnora/internal/workspaceevent"
	"github.com/google/uuid"
)

var (
	ErrWebhookEndpointLimit  = errors.New("workspace already has the maximum number of webhook endpoints")
	ErrWebhookURLDuplicate   = errors.New("this callback URL is already configured")
	ErrWebhookEventsRequired = errors.New("events must be a non-empty list of known types")
	ErrWebhookSecretTooShort = errors.New("secret must be at least 16 characters")
	ErrWebhookSecretRequired = errors.New("secret is required")
	ErrWebhookHTTPSRequired  = errors.New("callback URL must use https")
)

type webhookEndpointService struct {
	endpoints     interfaces.WebhookEndpointRepository
	deliveries    interfaces.WebhookDeliveryRepository
	dispatcher    interfaces.WebhookDispatcher
	subscriptions interfaces.WebhookSubscriptionIndex
	audit         interfaces.AuditLogService
}

func NewWebhookEndpointService(
	endpoints interfaces.WebhookEndpointRepository,
	deliveries interfaces.WebhookDeliveryRepository,
	dispatcher interfaces.WebhookDispatcher,
	subscriptions interfaces.WebhookSubscriptionIndex,
	audit interfaces.AuditLogService,
) interfaces.WebhookEndpointService {
	return &webhookEndpointService{
		endpoints:     endpoints,
		deliveries:    deliveries,
		dispatcher:    dispatcher,
		subscriptions: subscriptions,
		audit:         audit,
	}
}

func (s *webhookEndpointService) EventTypes() []string {
	return types.P0WebhookEventTypes()
}

func (s *webhookEndpointService) List(ctx context.Context, tenantID uint64) ([]types.WebhookEndpointPublic, error) {
	rows, err := s.endpoints.ListByTenant(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	out := make([]types.WebhookEndpointPublic, 0, len(rows))
	for _, row := range rows {
		out = append(out, row.Public())
	}
	return out, nil
}

func (s *webhookEndpointService) Create(ctx context.Context, tenantID uint64, in interfaces.WebhookEndpointCreate) (*types.WebhookEndpointPublic, error) {
	n, err := s.endpoints.CountByTenant(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	if n >= int64(types.WebhookMaxEndpointsPerTenant) {
		return nil, apperrors.NewBadRequestError(ErrWebhookEndpointLimit.Error())
	}
	events, err := normalizeWebhookEvents(in.Events)
	if err != nil {
		return nil, apperrors.NewValidationError(err.Error())
	}
	target, err := validateWebhookTargetURL(in.URL)
	if err != nil {
		return nil, apperrors.NewValidationError(err.Error())
	}
	if existing, _ := s.endpoints.FindByTenantURL(ctx, tenantID, target); existing != nil {
		return nil, apperrors.NewConflictError(ErrWebhookURLDuplicate.Error())
	}
	secret := strings.TrimSpace(in.Secret)
	if secret == "" {
		return nil, apperrors.NewValidationError(ErrWebhookSecretRequired.Error())
	}
	if len(secret) < types.WebhookMinSecretLength {
		return nil, apperrors.NewValidationError(ErrWebhookSecretTooShort.Error())
	}
	enc, err := encryptWebhookSecret(secret)
	if err != nil {
		return nil, err
	}
	enabled := true
	if in.Enabled != nil {
		enabled = *in.Enabled
	}
	actor, _ := types.UserIDFromContext(ctx)
	now := time.Now().UTC()
	ep := &types.TenantWebhookEndpoint{
		ID:          uuid.NewString(),
		TenantID:    tenantID,
		Name:        strings.TrimSpace(in.Name),
		URL:         target,
		SecretEnc:   enc,
		Events:      events,
		Enabled:     enabled,
		Description: strings.TrimSpace(in.Description),
		CreatedBy:   actor,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	if err := s.endpoints.Create(ctx, ep); err != nil {
		return nil, err
	}
	s.invalidateSubscriptions(ctx, tenantID)
	s.emitAudit(ctx, types.AuditActionWebhookEndpointCreated, ep)
	pub := ep.Public()
	return &pub, nil
}

func (s *webhookEndpointService) Update(ctx context.Context, tenantID uint64, hookID string, in interfaces.WebhookEndpointUpdate) (*types.WebhookEndpointPublic, error) {
	ep, err := s.endpoints.GetByID(ctx, tenantID, hookID)
	if err != nil {
		return nil, err
	}
	if in.Name != nil {
		ep.Name = strings.TrimSpace(*in.Name)
	}
	if in.Description != nil {
		ep.Description = strings.TrimSpace(*in.Description)
	}
	if in.Enabled != nil {
		ep.Enabled = *in.Enabled
	}
	if in.Events != nil {
		events, err := normalizeWebhookEvents(in.Events)
		if err != nil {
			return nil, apperrors.NewValidationError(err.Error())
		}
		ep.Events = events
	}
	if in.URL != nil {
		target, err := validateWebhookTargetURL(*in.URL)
		if err != nil {
			return nil, apperrors.NewValidationError(err.Error())
		}
		if existing, _ := s.endpoints.FindByTenantURL(ctx, tenantID, target); existing != nil && existing.ID != ep.ID {
			return nil, apperrors.NewConflictError(ErrWebhookURLDuplicate.Error())
		}
		ep.URL = target
	}
	if in.Secret != nil && strings.TrimSpace(*in.Secret) != "" {
		secret := strings.TrimSpace(*in.Secret)
		if len(secret) < types.WebhookMinSecretLength {
			return nil, apperrors.NewValidationError(ErrWebhookSecretTooShort.Error())
		}
		enc, err := encryptWebhookSecret(secret)
		if err != nil {
			return nil, err
		}
		ep.SecretEnc = enc
	}
	ep.UpdatedAt = time.Now().UTC()
	if err := s.endpoints.Update(ctx, ep); err != nil {
		return nil, err
	}
	s.invalidateSubscriptions(ctx, tenantID)
	s.emitAudit(ctx, types.AuditActionWebhookEndpointUpdated, ep)
	pub := ep.Public()
	return &pub, nil
}

func (s *webhookEndpointService) Delete(ctx context.Context, tenantID uint64, hookID string) error {
	ep, err := s.endpoints.GetByID(ctx, tenantID, hookID)
	if err != nil {
		return err
	}
	if err := s.endpoints.SoftDelete(ctx, tenantID, hookID); err != nil {
		return err
	}
	s.invalidateSubscriptions(ctx, tenantID)
	s.emitAudit(ctx, types.AuditActionWebhookEndpointDeleted, ep)
	return nil
}

func (s *webhookEndpointService) Test(ctx context.Context, tenantID uint64, hookID string) error {
	if _, err := s.endpoints.GetByID(ctx, tenantID, hookID); err != nil {
		return err
	}
	if s.dispatcher == nil {
		return errors.New("webhook dispatcher unavailable")
	}
	return s.dispatcher.DispatchTest(ctx, tenantID, hookID)
}

func (s *webhookEndpointService) ListDeliveries(ctx context.Context, tenantID uint64, hookID string, limit int) ([]*types.TenantWebhookDelivery, error) {
	if _, err := s.endpoints.GetByID(ctx, tenantID, hookID); err != nil {
		return nil, err
	}
	if limit <= 0 || limit > types.WebhookDeliveryKeepPerEndpoint {
		limit = types.WebhookDeliveryKeepPerEndpoint
	}
	return s.deliveries.ListByEndpoint(ctx, hookID, limit)
}

func (s *webhookEndpointService) emitAudit(ctx context.Context, action types.AuditAction, ep *types.TenantWebhookEndpoint) {
	if s.audit == nil || ep == nil {
		return
	}
	details, _ := json.Marshal(map[string]any{
		"name":    ep.Name,
		"url":     ep.URL,
		"enabled": ep.Enabled,
	})
	_ = s.audit.Log(ctx, &types.AuditLog{
		TenantID:    ep.TenantID,
		ActorUserID: auditActor(ctx),
		ActorRole:   auditActorRole(ctx),
		Action:      action,
		TargetType:  "webhook_endpoint",
		TargetID:    ep.ID,
		Outcome:     types.AuditOutcomeSuccess,
		Details:     details,
	})
}

func (s *webhookEndpointService) invalidateSubscriptions(ctx context.Context, tenantID uint64) {
	if s == nil || s.subscriptions == nil || tenantID == 0 {
		return
	}
	_ = s.subscriptions.Invalidate(ctx, tenantID)
	s.subscriptions.Warm(ctx, tenantID)
}

func normalizeWebhookEvents(events []string) (types.WebhookEvents, error) {
	if len(events) == 0 {
		return nil, ErrWebhookEventsRequired
	}
	seen := map[string]struct{}{}
	out := make(types.WebhookEvents, 0, len(events))
	for _, raw := range events {
		t := strings.TrimSpace(raw)
		if t == "" {
			continue
		}
		if !workspaceevent.IsKnownEventType(t) || t == types.EventWebhookTest {
			return nil, fmt.Errorf("unknown event type %q", t)
		}
		if _, ok := seen[t]; ok {
			continue
		}
		seen[t] = struct{}{}
		out = append(out, t)
	}
	if len(out) == 0 {
		return nil, ErrWebhookEventsRequired
	}
	return out, nil
}

func encryptWebhookSecret(secret string) (string, error) {
	key := secutils.GetAESKey()
	if key == nil {
		return "", apperrors.NewInternalServerError("SYSTEM_AES_KEY is not configured")
	}
	return secutils.EncryptAESGCM(secret, key)
}

func validateWebhookTargetURL(raw string) (string, error) {
	trimmed := strings.TrimSpace(raw)
	parsed, err := url.Parse(trimmed)
	if err != nil || parsed.Host == "" {
		return "", fmt.Errorf("callback URL must be a valid http(s) URL")
	}
	host := parsed.Hostname()
	loopback := isLoopbackHost(host)
	switch parsed.Scheme {
	case "https":
	case "http":
		if !loopback {
			return "", ErrWebhookHTTPSRequired
		}
	default:
		return "", fmt.Errorf("callback URL must use http or https")
	}
	if loopback {
		return trimmed, nil
	}
	if err := secutils.ValidateURLForSSRF(trimmed); err != nil {
		if hint := secutils.FormatSSRFError("Webhook URL", trimmed, err); hint != "" {
			return "", errors.New(hint)
		}
		return "", err
	}
	return trimmed, nil
}

func isLoopbackHost(host string) bool {
	h := strings.ToLower(strings.TrimSpace(host))
	if h == "localhost" || h == "127.0.0.1" || h == "::1" {
		return true
	}
	ip := net.ParseIP(h)
	return ip != nil && ip.IsLoopback()
}
