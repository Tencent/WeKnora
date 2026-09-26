package pluginsdk

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/Tencent/WeKnora/pluginsdk/pluginapi"
)

// UIHandler answers the requests of the plugin's pages (contributes.pages,
// settingsSections, kbTabs), which reach it through the WeKnora bridge.
type UIHandler func(ctx context.Context, call *Call, req pluginapi.UIRequest) (*pluginapi.UIResponse, error)

// UI registers the handler behind the plugin's pages.
func (p *Plugin) UI(h UIHandler) { p.ui = h }

// UIJSON is a UIResponse carrying v as JSON.
func UIJSON(status int, v any) (*pluginapi.UIResponse, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	return &pluginapi.UIResponse{Status: status, Body: b}, nil
}

func (p *Plugin) routeUI(mux *http.ServeMux) {
	mux.HandleFunc("POST /v1/ui/request", func(w http.ResponseWriter, r *http.Request) {
		if p.ui == nil {
			writeError(w, pluginapi.Errorf(pluginapi.CodeNotFound, "this plugin's pages make no requests"))
			return
		}
		p.unary(func(ctx context.Context, call *Call, raw json.RawMessage) (any, error) {
			in, err := decodeInput[pluginapi.UIRequest](raw)
			if err != nil {
				return nil, err
			}
			out, err := p.ui(ctx, call, in)
			if err != nil {
				return nil, err
			}
			if out == nil {
				out = &pluginapi.UIResponse{}
			}
			if out.Status == 0 {
				out.Status = http.StatusOK
			}
			return out, nil
		})(w, r)
	})
}
