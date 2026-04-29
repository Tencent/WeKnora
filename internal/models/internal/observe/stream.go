package observe

import (
	"context"
	"time"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/tracing/langfuse"
)

// StreamCall describes how to observe a streaming LLM operation that returns
// a channel of events. It generalizes chat.ChatStream so the same decorator
// is used for every future stream-returning call.
//
// EventT is the event type sent over the channel (e.g. types.StreamResponse).
// AggT is the accumulator kept across events; the adapter provides a Reducer
// that merges each event into AggT, and after the channel closes the helper
// calls Finish{Langfuse,Debug} with the final AggT.
type StreamCall[Req, EventT, AggT any] struct {
	Name    string
	Model   string
	ModelID string

	LangfuseInput    func(req Req) any
	LangfuseMetadata func(req Req) map[string]any
	LangfuseParams   func(req Req) map[string]any

	// NewAgg returns the initial aggregator. Called once per stream.
	NewAgg func() AggT

	// Reduce is called for every event just before it's forwarded to the
	// caller. It may mark first-token time via the callback.
	Reduce func(agg *AggT, ev EventT, markFirstToken func())

	// LangfuseOutput builds the Langfuse output from the final aggregator.
	LangfuseOutput func(req Req, agg AggT, err error) any

	// Usage builds the token usage from the final aggregator.
	Usage func(req Req, agg AggT) *langfuse.TokenUsage

	// DebugRecord builds the LLM-debug record from the final aggregator.
	DebugRecord func(req Req, agg AggT, err error, dur time.Duration) *logger.LLMCallRecord
}

// WrapStream runs the inner function (which returns a channel) under the
// Langfuse + debug decorators, aggregating events through the adapter-provided
// reducer. The returned channel receives the exact events produced by fn, in
// order.
//
// If fn returns an error before producing a channel, Langfuse receives a
// finished generation with nil output and the debug record is flushed
// immediately. Otherwise the decorators run on a goroutine that drains the
// inner channel and forwards to a wrapped channel.
func WrapStream[Req, EventT, AggT any](
	ctx context.Context,
	c StreamCall[Req, EventT, AggT],
	req Req,
	fn func(context.Context, Req) (<-chan EventT, error),
) (<-chan EventT, error) {
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

	inner, err := fn(callCtx, req)
	if err != nil {
		if gen != nil {
			gen.Finish(nil, nil, err)
		}
		if logger.LLMDebugEnabled() && c.DebugRecord != nil {
			var zero AggT
			if rec := c.DebugRecord(req, zero, err, time.Since(start)); rec != nil {
				logger.LLMDebugLog(ctx, rec)
			}
		}
		return inner, err
	}
	if inner == nil {
		if gen != nil {
			gen.Finish(nil, nil, nil)
		}
		return nil, nil
	}

	debugEnabled := logger.LLMDebugEnabled() && c.DebugRecord != nil
	if gen == nil && !debugEnabled {
		return inner, nil
	}

	out := make(chan EventT)
	go func() {
		defer close(out)
		var agg AggT
		if c.NewAgg != nil {
			agg = c.NewAgg()
		}
		firstTokenMarked := false
		markFirstToken := func() {
			if firstTokenMarked {
				return
			}
			firstTokenMarked = true
			if gen != nil {
				gen.MarkCompletionStart(time.Now())
			}
		}

		for ev := range inner {
			if c.Reduce != nil {
				c.Reduce(&agg, ev, markFirstToken)
			}
			out <- ev
		}

		if gen != nil {
			var output any
			if c.LangfuseOutput != nil {
				output = c.LangfuseOutput(req, agg, nil)
			}
			var usage *langfuse.TokenUsage
			if c.Usage != nil {
				usage = c.Usage(req, agg)
			}
			gen.Finish(output, usage, nil)
		}

		if debugEnabled {
			if rec := c.DebugRecord(req, agg, nil, time.Since(start)); rec != nil {
				logger.LLMDebugLog(ctx, rec)
			}
		}
	}()

	return out, nil
}
