package pluginsdk

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/Tencent/WeKnora/pluginsdk/pluginapi"
)

// WebSearcher is a web search provider. Its instance configuration is what
// the contribution's instanceSchema describes.
type WebSearcher interface {
	Search(ctx context.Context, call *Call, in pluginapi.SearchInput) (*pluginapi.SearchOutput, error)
}

// WebSearchFunc adapts a function to WebSearcher.
type WebSearchFunc func(ctx context.Context, call *Call, in pluginapi.SearchInput) (*pluginapi.SearchOutput, error)

// Search implements WebSearcher.
func (f WebSearchFunc) Search(
	ctx context.Context,
	call *Call,
	in pluginapi.SearchInput,
) (*pluginapi.SearchOutput, error) {
	return f(ctx, call, in)
}

// WebSearch registers web search provider id (contributes.webSearch[].id).
func (p *Plugin) WebSearch(id string, s WebSearcher) { p.webSearch[id] = s }

func (p *Plugin) routeWebSearch(mux *http.ServeMux) {
	mux.HandleFunc("POST /v1/websearch/{id}/search", func(w http.ResponseWriter, r *http.Request) {
		s, ok := p.webSearch[r.PathValue("id")]
		if !ok {
			writeError(w, pluginapi.Errorf(pluginapi.CodeNotFound, "no web search provider %q", r.PathValue("id")))
			return
		}
		p.unary(func(ctx context.Context, call *Call, raw json.RawMessage) (any, error) {
			in, err := decodeInput[pluginapi.SearchInput](raw)
			if err != nil {
				return nil, err
			}
			out, err := s.Search(ctx, call, in)
			if err != nil {
				return nil, err
			}
			if out == nil {
				out = &pluginapi.SearchOutput{}
			}
			if out.Results == nil {
				out.Results = []pluginapi.SearchResult{}
			}
			return out, nil
		})(w, r)
	})
}
