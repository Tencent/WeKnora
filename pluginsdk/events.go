package pluginsdk

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/Tencent/WeKnora/pluginsdk/pluginapi"
)

// EventHandler handles one event delivery. Deliveries repeat (at least
// once): make it idempotent on ev.ID. Return a retryable error (unavailable,
// rate_limited) to be called again later; any other error drops the event.
// Ignore event types you do not know.
type EventHandler func(ctx context.Context, call *Call, ev pluginapi.EventDelivery) error

// OnEvent registers the handler of the events the plugin subscribed to in
// permissions.events.
func (p *Plugin) OnEvent(h EventHandler) { p.events = h }

// WebhookHandler answers one inbound call to a webhook.
type WebhookHandler func(
	ctx context.Context, call *Call, req pluginapi.WebhookRequest,
) (*pluginapi.WebhookResponse, error)

// Webhook registers webhook id (contributes.webhooks[].id). call carries the
// workspace the URL belongs to and its configuration, so the handler can
// verify the sender's signature with the workspace's secret.
func (p *Plugin) Webhook(id string, h WebhookHandler) { p.webhooks[id] = h }

func (p *Plugin) routeEvents(mux *http.ServeMux) {
	mux.HandleFunc("POST /v1/events", func(w http.ResponseWriter, r *http.Request) {
		if p.events == nil {
			writeError(w, pluginapi.Errorf(pluginapi.CodeNotFound, "this plugin handles no events"))
			return
		}
		p.unary(func(ctx context.Context, call *Call, raw json.RawMessage) (any, error) {
			ev, err := decodeInput[pluginapi.EventDelivery](raw)
			if err != nil {
				return nil, err
			}
			return struct{}{}, p.events(ctx, call, ev)
		})(w, r)
	})
	mux.HandleFunc("POST /v1/webhooks/{id}", func(w http.ResponseWriter, r *http.Request) {
		h, ok := p.webhooks[r.PathValue("id")]
		if !ok {
			writeError(w, pluginapi.Errorf(pluginapi.CodeNotFound, "no webhook %q", r.PathValue("id")))
			return
		}
		p.unary(func(ctx context.Context, call *Call, raw json.RawMessage) (any, error) {
			in, err := decodeInput[pluginapi.WebhookRequest](raw)
			if err != nil {
				return nil, err
			}
			out, err := h(ctx, call, in)
			if err != nil {
				return nil, err
			}
			if out == nil {
				out = &pluginapi.WebhookResponse{}
			}
			if out.Status == 0 {
				out.Status = http.StatusOK
			}
			return out, nil
		})(w, r)
	})
}
