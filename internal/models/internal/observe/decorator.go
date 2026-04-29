// Package observe holds the Langfuse + LLM-debug decorator machinery that is
// shared across the LLM sub-packages (chat / embedding / rerank / vlm / asr).
//
// Before this package existed, every sub-package had its own langfuse_wrapper.go
// and llm_debug[_wrapper].go with ~80% identical shape:
//
//	func (d *Wrapper) Op(ctx, in) (out, error) {
//	    start := time.Now()
//	    genCtx, gen := manager.StartGeneration(...)
//	    out, err := d.inner.Op(genCtx, in)
//	    gen.Finish(output, usage, err)
//	    logDebugRecord(ctx, ..., time.Since(start))
//	    return out, err
//	}
//
// This package centralizes the above. Each sub-package now only writes a
// small adapter (~40 lines) that:
//   - provides a toLangfuseInput / toLangfuseOutput / approxUsage func
//   - provides a buildDebugRecord func
// and calls Wrap / WrapStream generic helpers.
package observe

import (
	"context"
	"maps"
	"time"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/tracing/langfuse"
)

// Call captures what to record for a single non-streaming LLM operation.
// Req is the input DTO; Resp is the response DTO.
type Call[Req, Resp any] struct {
	// Name appears as the Langfuse generation name, e.g. "chat.completion".
	Name string

	// Model / ModelID pulled from the underlying impl's GetModelName/GetModelID.
	Model   string
	ModelID string

	// LangfuseInput returns the structured input to report. Called only when
	// Langfuse is enabled, so the adapter can do work unconditionally.
	LangfuseInput func(req Req) any

	// LangfuseOutput returns the structured output to report on Finish.
	LangfuseOutput func(req Req, resp Resp, err error) any

	// LangfuseMetadata returns request-level metadata (e.g. model_id, streaming,
	// has_tools). Called once at the start of the call.
	LangfuseMetadata func(req Req) map[string]any

	// LangfuseParams maps options to Langfuse model parameters (temperature,
	// top_p, max_tokens, …). Optional.
	LangfuseParams func(req Req) map[string]any

	// Usage returns the token usage associated with the call. Called after
	// the inner function returns. May be nil.
	Usage func(req Req, resp Resp) *langfuse.TokenUsage

	// DebugRecord builds the LLM-debug record. Called only when
	// logger.LLMDebugEnabled() is true.
	DebugRecord func(req Req, resp Resp, err error, dur time.Duration) *logger.LLMCallRecord
}

// Wrap runs the inner function under the Langfuse generation + debug log
// decorators, both of which are no-ops when their respective subsystems are
// disabled. The ctx passed to fn is the Langfuse-annotated one so nested
// spans chain correctly.
func Wrap[Req, Resp any](ctx context.Context, c Call[Req, Resp], req Req, fn func(context.Context, Req) (Resp, error)) (Resp, error) {
	start := time.Now()

	mgr := langfuse.GetManager()
	var gen *langfuse.Generation
	callCtx := ctx
	if mgr.Enabled() {
		opts := langfuse.GenerationOptions{
			Name:  c.Name,
			Model: c.Model,
		}
		if c.LangfuseInput != nil {
			opts.Input = c.LangfuseInput(req)
		}
		if c.LangfuseMetadata != nil {
			opts.Metadata = convertAnyMap(c.LangfuseMetadata(req))
		}
		if c.LangfuseParams != nil {
			opts.ModelParameters = convertAnyMap(c.LangfuseParams(req))
		}
		callCtx, gen = mgr.StartGeneration(ctx, opts)
	}

	resp, err := fn(callCtx, req)

	if gen != nil {
		var out any
		if c.LangfuseOutput != nil {
			out = c.LangfuseOutput(req, resp, err)
		}
		var usage *langfuse.TokenUsage
		if c.Usage != nil {
			usage = c.Usage(req, resp)
		}
		gen.Finish(out, usage, err)
	}

	if logger.LLMDebugEnabled() && c.DebugRecord != nil {
		if rec := c.DebugRecord(req, resp, err, time.Since(start)); rec != nil {
			logger.LLMDebugLog(ctx, rec)
		}
	}

	return resp, err
}

// convertAnyMap turns map[string]any into the map[string]any shape that
// langfuse expects.
func convertAnyMap(in map[string]any) map[string]any {
	if in == nil {
		return nil
	}
	out := make(map[string]any, len(in))
	maps.Copy(out, in)
	return out
}
