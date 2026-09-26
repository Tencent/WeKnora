// Package events delivers WeKnora's business events (a document ingested,
// an answer given) to the plugins that subscribed to them in
// permissions.events. Publishing enqueues one task per subscribed plugin
// that the workspace has switched on; the task calls the plugin's
// /v1/events and is retried with backoff while the plugin fails with a
// retryable error, keeping the event ID (at least once).
package events

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"github.com/hibiken/asynq"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/plugin/manifest"
	"github.com/Tencent/WeKnora/internal/plugin/registry"
	"github.com/Tencent/WeKnora/internal/plugin/tenancy"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/Tencent/WeKnora/pluginsdk/pluginapi"
)

// TaskType is the task that delivers one event to one plugin.
const TaskType = types.TypePluginEvent

// Delivery bounds: retries back off (asynq's default, or the Lite
// executor's), each attempt within its own timeout.
const (
	maxRetry        = 10
	deliveryTimeout = 2 * time.Minute
)

// payload is one queued delivery.
type payload struct {
	PluginID string                  `json:"pluginId"`
	TenantID uint64                  `json:"tenantId"`
	Event    pluginapi.EventDelivery `json:"event"`
}

// Caller makes a call to a plugin (activate.Invoker); an interface so
// services that publish events do not import the plugin runtime.
type Caller interface {
	Call(ctx context.Context, m *manifest.Manifest, path string, instance map[string]any, input, out any) error
}

// Dispatcher fans events out to subscribed plugins and delivers them.
type Dispatcher struct {
	registry *registry.Registry
	tenancy  *tenancy.Service
	enqueue  interfaces.TaskEnqueuer
	invoker  Caller
	now      func() time.Time
}

// NewDispatcher creates a Dispatcher.
func NewDispatcher(
	reg *registry.Registry, t *tenancy.Service, enq interfaces.TaskEnqueuer, iv Caller,
) *Dispatcher {
	return &Dispatcher{registry: reg, tenancy: t, enqueue: enq, invoker: iv, now: time.Now}
}

var current atomic.Pointer[Dispatcher]

// SetDefault makes d the dispatcher Publish uses.
func SetDefault(d *Dispatcher) { current.Store(d) }

// Publish sends an event of a workspace to the plugins subscribed to it. It
// does nothing until the plugin runtime starts, and never fails the caller:
// a delivery it cannot queue is logged.
func Publish(ctx context.Context, tenantID uint64, typ string, data any) {
	if d := current.Load(); d != nil && tenantID != 0 {
		d.Publish(ctx, tenantID, typ, data)
	}
}

// subscribed reports whether a loaded plugin wants an event and can take
// it (it has code).
func subscribed(m *manifest.Manifest, typ string) bool {
	rt := m.Runtime.Type
	return (rt == manifest.RuntimeHost || rt == manifest.RuntimeRemote) && slices.Contains(m.Permissions.Events, typ)
}

// Publish enqueues a delivery for every subscribed plugin the workspace has
// switched on.
func (d *Dispatcher) Publish(ctx context.Context, tenantID uint64, typ string, data any) {
	raw, err := json.Marshal(data)
	if err != nil {
		logger.Warnf(ctx, "[plugin] event %s: %v", typ, err)
		return
	}
	ev := pluginapi.EventDelivery{ID: uuid.NewString(), Type: typ, OccurredAt: d.now().UTC(), Data: raw}
	for _, m := range d.registry.Plugins() {
		if !subscribed(m, typ) {
			continue
		}
		if on, err := d.tenancy.PluginEnabled(ctx, tenantID, m.ID); err != nil || !on {
			if err != nil {
				logger.Warnf(ctx, "[plugin] event %s to %s: read switch: %v", typ, m.ID, err)
			}
			continue
		}
		body, _ := json.Marshal(payload{PluginID: m.ID, TenantID: tenantID, Event: ev})
		task := asynq.NewTask(TaskType, body)
		_, err := d.enqueue.Enqueue(task, asynq.Queue(types.QueueMaintenance), asynq.MaxRetry(maxRetry),
			asynq.Timeout(deliveryTimeout))
		if err != nil {
			logger.Warnf(ctx, "[plugin] event %s to %s: enqueue: %v", typ, m.ID, err)
		}
	}
}

// Handle delivers one event (the TaskType handler). An event is dropped when
// the plugin is gone, no longer subscribed or switched off, or refuses it
// for good; a retryable failure is returned so the task runs again.
func (d *Dispatcher) Handle(ctx context.Context, task *asynq.Task) error {
	var p payload
	if err := json.Unmarshal(task.Payload(), &p); err != nil {
		return fmt.Errorf("decode plugin event: %w: %w", err, asynq.SkipRetry)
	}
	m, ok := d.registry.Plugin(p.PluginID)
	if !ok || !subscribed(m, p.Event.Type) {
		return nil
	}
	if on, err := d.tenancy.PluginEnabled(ctx, p.TenantID, p.PluginID); err != nil {
		return err
	} else if !on {
		return nil
	}
	ev := p.Event
	ev.Attempt = 1
	if n, ok := asynq.GetRetryCount(ctx); ok {
		ev.Attempt = n + 1
	}
	ctx = context.WithValue(ctx, types.TenantIDContextKey, p.TenantID)
	err := d.invoker.Call(ctx, m, pluginapi.EventsPath, nil, ev, nil)
	if err == nil {
		return nil
	}
	if pe, ok := pluginapi.AsError(err); ok && !pe.Retryable {
		logger.Warnf(ctx, "[plugin] %s refused event %s (%s): %v", p.PluginID, ev.ID, ev.Type, err)
		return nil
	}
	logger.Infof(ctx, "[plugin] event %s to %s failed (attempt %d), will retry: %v", ev.ID, p.PluginID, ev.Attempt, err)
	return err
}
