package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/Tencent/WeKnora/internal/workspaceevent"
	"github.com/hibiken/asynq"
)

const webhookDeliverTimeout = 20 * time.Second

type webhookDispatcher struct {
	outbox    interfaces.WebhookOutboxRepository
	endpoints interfaces.WebhookEndpointRepository
	deliveries interfaces.WebhookDeliveryRepository
	enqueuer  interfaces.TaskEnqueuer
}

func NewWebhookDispatcher(
	outbox interfaces.WebhookOutboxRepository,
	endpoints interfaces.WebhookEndpointRepository,
	deliveries interfaces.WebhookDeliveryRepository,
	enqueuer interfaces.TaskEnqueuer,
) interfaces.WebhookDispatcher {
	return &webhookDispatcher{
		outbox:     outbox,
		endpoints:  endpoints,
		deliveries: deliveries,
		enqueuer:   enqueuer,
	}
}

func (d *webhookDispatcher) Dispatch(ctx context.Context, eventID string) error {
	if d == nil || d.outbox == nil {
		return nil
	}
	row, err := d.outbox.GetByEventID(ctx, eventID)
	if err != nil {
		return err
	}
	if row.Status != types.WebhookOutboxPending {
		return nil
	}
	return d.dispatchRow(ctx, row, "", false)
}

func (d *webhookDispatcher) DispatchTest(ctx context.Context, tenantID uint64, endpointID string) error {
	data := types.WebhookTestData{Resource: "webhook", OK: true}
	actor, _ := types.UserIDFromContext(ctx)
	req, _ := types.RequestIDFromContext(ctx)
	env, err := workspaceevent.BuildEnvelope(types.WorkspaceEvent{
		Type:        types.EventWebhookTest,
		TenantID:    tenantID,
		ActorUserID: actor,
		RequestID:   req,
		Data:        data,
	}, time.Now().UTC())
	if err != nil {
		return err
	}
	payload, err := json.Marshal(env)
	if err != nil {
		return err
	}
	row := &types.TenantWebhookOutbox{
		EventID:       env.ID,
		EventType:     env.Type,
		OwnerTenantID: tenantID,
		Payload:       payload,
		Status:        types.WebhookOutboxPending,
		CreatedAt:     time.Now().UTC(),
	}
	if err := d.outbox.Insert(ctx, row); err != nil {
		return err
	}
	return d.dispatchRow(ctx, row, endpointID, true)
}

func (d *webhookDispatcher) SweepPending(ctx context.Context) error {
	if d == nil || d.outbox == nil {
		return nil
	}
	rows, err := d.outbox.ListPending(ctx, 100)
	if err != nil {
		return err
	}
	var first error
	for _, row := range rows {
		if err := d.dispatchRow(ctx, row, "", false); err != nil && first == nil {
			first = err
		}
	}
	return first
}

func (d *webhookDispatcher) Prune(ctx context.Context) error {
	if d == nil {
		return nil
	}
	if d.outbox != nil {
		if _, err := d.outbox.DeleteProcessedBefore(ctx, time.Now().UTC().Add(-7*24*time.Hour)); err != nil {
			logger.Warnf(ctx, "webhook outbox prune: %v", err)
		}
	}
	if d.deliveries == nil {
		return nil
	}
	ids, err := d.deliveries.ListEndpointIDs(ctx)
	if err != nil {
		return err
	}
	for _, id := range ids {
		if _, err := d.deliveries.PruneEndpointKeepLatest(ctx, id, types.WebhookDeliveryKeepPerEndpoint); err != nil {
			logger.Warnf(ctx, "webhook delivery prune endpoint=%s: %v", id, err)
		}
	}
	_, err = d.deliveries.DeleteOlderThanDays(ctx, 30)
	return err
}

func (d *webhookDispatcher) dispatchRow(ctx context.Context, row *types.TenantWebhookOutbox, onlyEndpointID string, bypassEvents bool) error {
	endpoints, err := d.endpoints.ListEnabledByTenant(ctx, row.OwnerTenantID)
	if err != nil {
		_ = d.outbox.BumpAttempts(ctx, row.EventID, err.Error())
		d.maybeFailOutbox(ctx, row.EventID)
		return err
	}
	if onlyEndpointID != "" {
		filtered := endpoints[:0]
		for _, ep := range endpoints {
			if ep.ID == onlyEndpointID {
				filtered = append(filtered, ep)
			}
		}
		if len(filtered) == 0 {
			ep, getErr := d.endpoints.GetByID(ctx, row.OwnerTenantID, onlyEndpointID)
			if getErr != nil {
				return getErr
			}
			filtered = []*types.TenantWebhookEndpoint{ep}
		}
		endpoints = filtered
	}

	matched := make([]*types.TenantWebhookEndpoint, 0, len(endpoints))
	for _, ep := range endpoints {
		if bypassEvents || ep.Events.Contains(row.EventType) {
			matched = append(matched, ep)
		}
	}
	if len(matched) == 0 {
		return d.outbox.MarkProcessed(ctx, row.EventID)
	}

	allQueued := true
	var lastErr error
	for _, ep := range matched {
		if err := d.claimAndEnqueue(ctx, row, ep); err != nil {
			allQueued = false
			lastErr = err
			logger.Warnf(ctx, "webhook enqueue failed event_id=%s endpoint=%s err=%v", row.EventID, ep.ID, err)
		}
	}
	if !allQueued {
		_ = d.outbox.BumpAttempts(ctx, row.EventID, fmt.Sprintf("%v", lastErr))
		d.maybeFailOutbox(ctx, row.EventID)
		return lastErr
	}
	return d.outbox.MarkProcessed(ctx, row.EventID)
}

func (d *webhookDispatcher) maybeFailOutbox(ctx context.Context, eventID string) {
	row, err := d.outbox.GetByEventID(ctx, eventID)
	if err != nil || row == nil {
		return
	}
	if row.Attempts >= types.WebhookOutboxSweepMaxAttempts {
		_ = d.outbox.MarkFailed(ctx, eventID, row.LastError)
	}
}

func (d *webhookDispatcher) claimAndEnqueue(ctx context.Context, row *types.TenantWebhookOutbox, ep *types.TenantWebhookEndpoint) error {
	deliveryID := workspaceevent.NewDeliveryID()
	claimed, existing, err := d.deliveries.Claim(ctx, &types.TenantWebhookDelivery{
		DeliveryID: deliveryID,
		EndpointID: ep.ID,
		TenantID:   row.OwnerTenantID,
		EventID:    row.EventID,
		EventType:  row.EventType,
		Status:     types.WebhookDeliveryPending,
		CreatedAt:  time.Now().UTC(),
	})
	if err != nil {
		return err
	}
	target := existing
	if claimed {
		target = existing
	}
	if target == nil {
		return errors.New("webhook delivery claim returned empty row")
	}
	if target.Status == types.WebhookDeliverySuccess || target.Status == types.WebhookDeliveryFailed {
		return nil
	}
	payload, err := json.Marshal(types.WebhookDeliverPayload{
		EventID:    row.EventID,
		EndpointID: ep.ID,
		TenantID:   row.OwnerTenantID,
		DeliveryID: target.DeliveryID,
	})
	if err != nil {
		return err
	}
	task := asynq.NewTask(types.TypeWebhookDeliver, payload)
	_, err = d.enqueuer.Enqueue(task,
		asynq.Queue(types.QueueWebhook),
		asynq.TaskID("wh:"+row.EventID+":"+ep.ID),
		asynq.MaxRetry(5),
		asynq.Timeout(webhookDeliverTimeout),
	)
	if err != nil && (errors.Is(err, asynq.ErrTaskIDConflict) || errors.Is(err, asynq.ErrDuplicateTask)) {
		return nil
	}
	return err
}
