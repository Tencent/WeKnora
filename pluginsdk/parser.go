package pluginsdk

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/Tencent/WeKnora/pluginsdk/pluginapi"
)

// Parser turns a document into Markdown. Return a non-retryable error (such
// as pluginapi.CodeInvalidConfig or CodeNotFound) for a document that will
// never parse; retryable ones (CodeUnavailable, CodeRateLimited) make
// WeKnora try again later.
type Parser interface {
	Parse(ctx context.Context, call *Call, in pluginapi.ParseInput) (*pluginapi.ParseOutput, error)
}

// ParserFunc adapts a function to Parser.
type ParserFunc func(ctx context.Context, call *Call, in pluginapi.ParseInput) (*pluginapi.ParseOutput, error)

// Parse implements Parser.
func (f ParserFunc) Parse(ctx context.Context, call *Call, in pluginapi.ParseInput) (*pluginapi.ParseOutput, error) {
	return f(ctx, call, in)
}

// Parser registers parser id (contributes.parsers[].id).
func (p *Plugin) Parser(id string, parser Parser) { p.parsers[id] = parser }

func (p *Plugin) routeParsers(mux *http.ServeMux) {
	mux.HandleFunc("POST /v1/parsers/{id}/parse", func(w http.ResponseWriter, r *http.Request) {
		parser, ok := p.parsers[r.PathValue("id")]
		if !ok {
			writeError(w, pluginapi.Errorf(pluginapi.CodeNotFound, "no parser %q", r.PathValue("id")))
			return
		}
		p.unary(func(ctx context.Context, call *Call, raw json.RawMessage) (any, error) {
			in, err := decodeInput[pluginapi.ParseInput](raw)
			if err != nil {
				return nil, err
			}
			if len(in.Content) == 0 && in.URL == "" {
				return nil, pluginapi.Errorf(pluginapi.CodeBadRequest, "content or url is required")
			}
			out, err := parser.Parse(ctx, call, in)
			if err != nil {
				return nil, err
			}
			if out == nil {
				out = &pluginapi.ParseOutput{}
			}
			return out, nil
		})(w, r)
	})
}
