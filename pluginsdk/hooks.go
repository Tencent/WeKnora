package pluginsdk

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/Tencent/WeKnora/pluginsdk/pluginapi"
)

// PipelineHook joins stages of the knowledge Q&A pipeline. Set the stages
// it handles; plugin.yaml lists the same ones. WeKnora waits a few seconds
// at most and carries on without the hook if it fails.
type PipelineHook struct {
	RewriteQuery func(ctx context.Context, call *Call,
		in pluginapi.RewriteQueryInput) (pluginapi.RewriteQueryOutput, error)
	FilterResults func(ctx context.Context, call *Call,
		in pluginapi.FilterResultsInput) (pluginapi.FilterResultsOutput, error)
	Answer func(ctx context.Context, call *Call, in pluginapi.AnswerInput) (pluginapi.AnswerOutput, error)
}

// PipelineHook registers pipeline hook id (contributes.pipelineHooks[].id).
func (p *Plugin) PipelineHook(id string, h PipelineHook) { p.hooks[id] = h }

func (p *Plugin) routeHooks(mux *http.ServeMux) {
	mux.HandleFunc("POST /v1/hooks/{id}/{stage}", func(w http.ResponseWriter, r *http.Request) {
		h, ok := p.hooks[r.PathValue("id")]
		if !ok {
			writeError(w, pluginapi.Errorf(pluginapi.CodeNotFound, "no pipeline hook %q", r.PathValue("id")))
			return
		}
		var fn unaryFunc
		switch stage := r.PathValue("stage"); {
		case stage == pluginapi.StageRewriteQuery && h.RewriteQuery != nil:
			fn = hookStage(h.RewriteQuery)
		case stage == pluginapi.StageFilterResults && h.FilterResults != nil:
			fn = hookStage(h.FilterResults)
		case stage == pluginapi.StageAnswer && h.Answer != nil:
			fn = hookStage(h.Answer)
		default:
			writeError(w, pluginapi.Errorf(pluginapi.CodeNotFound, "pipeline hook %q has no stage %q",
				r.PathValue("id"), stage))
			return
		}
		p.unary(fn)(w, r)
	})
}

func hookStage[In, Out any](f func(context.Context, *Call, In) (Out, error)) unaryFunc {
	return func(ctx context.Context, call *Call, raw json.RawMessage) (any, error) {
		in, err := decodeInput[In](raw)
		if err != nil {
			return nil, err
		}
		return f(ctx, call, in)
	}
}
