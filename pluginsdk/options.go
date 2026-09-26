package pluginsdk

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/Tencent/WeKnora/pluginsdk/pluginapi"
)

// OptionsHandler lists a field's choices. call.Config holds what the form
// holds now; decode it with call.DecodeInstance for instance forms.
type OptionsHandler func(ctx context.Context, call *Call, in pluginapi.OptionsInput) ([]pluginapi.Option, error)

// Options registers the choices behind x-options {name: name}. Return
// pluginapi.InvalidConfig when the form lacks what the list needs (a token
// not entered yet): the form shows the message by the field.
func (p *Plugin) Options(name string, h OptionsHandler) { p.options[name] = h }

func (p *Plugin) routeOptions(mux *http.ServeMux) {
	mux.HandleFunc("POST /v1/options/{name}", func(w http.ResponseWriter, r *http.Request) {
		h, ok := p.options[r.PathValue("name")]
		if !ok {
			writeError(w, pluginapi.Errorf(pluginapi.CodeNotFound, "no options %q", r.PathValue("name")))
			return
		}
		p.unary(func(ctx context.Context, call *Call, raw json.RawMessage) (any, error) {
			in, err := decodeInput[pluginapi.OptionsInput](raw)
			if err != nil {
				return nil, err
			}
			opts, err := h(ctx, call, in)
			if err != nil {
				return nil, err
			}
			if opts == nil {
				opts = []pluginapi.Option{}
			}
			return pluginapi.OptionsOutput{Options: opts}, nil
		})(w, r)
	})
}
