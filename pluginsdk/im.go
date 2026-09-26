package pluginsdk

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/Tencent/WeKnora/pluginsdk/pluginapi"
)

// IMChannel connects an IM platform that calls back over HTTP. Callback
// sees each request the platform sends to the channel's callback URL
// (call.Config.Instance holds the channel's credentials) and returns what to
// answer and the user's message, if any; WeKnora answers it and calls Send
// with the reply.
type IMChannel interface {
	Callback(ctx context.Context, call *Call, req pluginapi.WebhookRequest) (pluginapi.IMCallbackOutput, error)
	Send(ctx context.Context, call *Call, in pluginapi.IMSendInput) error
}

// IMChannel registers IM channel id (contributes.imChannels[].id).
func (p *Plugin) IMChannel(id string, c IMChannel) { p.imChannels[id] = c }

func (p *Plugin) routeIM(mux *http.ServeMux) {
	channel := func(w http.ResponseWriter, r *http.Request) (IMChannel, bool) {
		c, ok := p.imChannels[r.PathValue("id")]
		if !ok {
			writeError(w, pluginapi.Errorf(pluginapi.CodeNotFound, "no IM channel %q", r.PathValue("id")))
		}
		return c, ok
	}
	mux.HandleFunc("POST /v1/im/{id}/callback", func(w http.ResponseWriter, r *http.Request) {
		c, ok := channel(w, r)
		if !ok {
			return
		}
		p.unary(func(ctx context.Context, call *Call, raw json.RawMessage) (any, error) {
			in, err := decodeInput[pluginapi.WebhookRequest](raw)
			if err != nil {
				return nil, err
			}
			return c.Callback(ctx, call, in)
		})(w, r)
	})
	mux.HandleFunc("POST /v1/im/{id}/send", func(w http.ResponseWriter, r *http.Request) {
		c, ok := channel(w, r)
		if !ok {
			return
		}
		p.unary(func(ctx context.Context, call *Call, raw json.RawMessage) (any, error) {
			in, err := decodeInput[pluginapi.IMSendInput](raw)
			if err != nil {
				return nil, err
			}
			return struct{}{}, c.Send(ctx, call, in)
		})(w, r)
	})
}
