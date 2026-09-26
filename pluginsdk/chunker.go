package pluginsdk

import (
	"context"
	"encoding/json"
	"net/http"
	"unicode/utf8"

	"github.com/Tencent/WeKnora/pluginsdk/pluginapi"
)

// Chunker cuts a document into chunks for a knowledge base whose chunking
// strategy is this chunker. It returns spans of the text; offsets count
// characters (runes), not bytes.
type Chunker interface {
	Split(ctx context.Context, call *Call, in pluginapi.ChunkInput) ([]pluginapi.ChunkSpan, error)
}

// ChunkerFunc adapts a function to Chunker.
type ChunkerFunc func(ctx context.Context, call *Call, in pluginapi.ChunkInput) ([]pluginapi.ChunkSpan, error)

// Split implements Chunker.
func (f ChunkerFunc) Split(ctx context.Context, call *Call, in pluginapi.ChunkInput) ([]pluginapi.ChunkSpan, error) {
	return f(ctx, call, in)
}

// Chunker registers chunker id (contributes.chunkers[].id).
func (p *Plugin) Chunker(id string, c Chunker) { p.chunkers[id] = c }

func (p *Plugin) routeChunkers(mux *http.ServeMux) {
	mux.HandleFunc("POST /v1/chunkers/{id}/split", func(w http.ResponseWriter, r *http.Request) {
		c, ok := p.chunkers[r.PathValue("id")]
		if !ok {
			writeError(w, pluginapi.Errorf(pluginapi.CodeNotFound, "no chunker %q", r.PathValue("id")))
			return
		}
		p.unary(func(ctx context.Context, call *Call, raw json.RawMessage) (any, error) {
			in, err := decodeInput[pluginapi.ChunkInput](raw)
			if err != nil {
				return nil, err
			}
			spans, err := c.Split(ctx, call, in)
			if err != nil {
				return nil, err
			}
			n := utf8.RuneCountInString(in.Text)
			for _, s := range spans {
				if s.Start < 0 || s.End <= s.Start || s.End > n {
					return nil, pluginapi.Errorf(pluginapi.CodeInternal,
						"chunk [%d, %d) is outside the %d-character text", s.Start, s.End, n)
				}
			}
			if spans == nil {
				spans = []pluginapi.ChunkSpan{}
			}
			return pluginapi.ChunkOutput{Chunks: spans}, nil
		})(w, r)
	})
}
