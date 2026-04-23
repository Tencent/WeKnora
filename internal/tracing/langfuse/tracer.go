package langfuse

import (
	"context"
	"time"
)

// Trace represents an active root observation. A Trace is conceptually one
// "request" (e.g. a chat turn). Generations and spans attached to it roll up
// as children in the Langfuse UI.
type Trace struct {
	ID      string
	manager *Manager
	sampled bool
}

// Generation represents a single model invocation (LLM / embedding / VLM).
type Generation struct {
	ID        string
	TraceID   string
	manager   *Manager
	sampled   bool
	startTime time.Time
	model     string
	name      string
}

// TraceOptions configures a new trace.
type TraceOptions struct {
	Name        string
	UserID      string
	SessionID   string
	Input       interface{}
	Metadata    map[string]interface{}
	Tags        []string
	Environment string
	Release     string
}

// GenerationOptions configures a new generation observation.
type GenerationOptions struct {
	Name            string
	Model           string
	Input           interface{}
	Metadata        map[string]interface{}
	ModelParameters map[string]interface{}
}

// StartTrace opens a new trace, stores its ID in the returned ctx, and returns
// a handle callers can finish with FinishTrace. When the manager is disabled
// or sampling excludes the trace, the returned *Trace is non-nil but all
// methods are no-ops so callers don't need nil checks.
func (m *Manager) StartTrace(ctx context.Context, opts TraceOptions) (context.Context, *Trace) {
	if m == nil || !m.cfg.Enabled {
		return ctx, &Trace{}
	}
	sampled := m.sample()
	id := newID()
	t := &Trace{ID: id, manager: m, sampled: sampled}

	if sampled {
		env := opts.Environment
		if env == "" {
			env = m.cfg.Environment
		}
		release := opts.Release
		if release == "" {
			release = m.cfg.Release
		}
		body := traceBody{
			ID:          id,
			Timestamp:   isoTime(time.Now()),
			Name:        opts.Name,
			UserID:      opts.UserID,
			SessionID:   opts.SessionID,
			Input:       opts.Input,
			Metadata:    opts.Metadata,
			Tags:        opts.Tags,
			Environment: env,
			Release:     release,
		}
		m.enqueue(ingestionEvent{
			ID:        newID(),
			Timestamp: isoTime(time.Now()),
			Type:      "trace-create",
			Body:      body,
		})
	}
	return withTrace(ctx, t), t
}

// Finish updates the trace with its final output. Safe to call on a disabled
// trace (no-op).
func (t *Trace) Finish(output interface{}, metadata map[string]interface{}) {
	if t == nil || t.manager == nil || !t.sampled {
		return
	}
	// "trace-create" events are also used to update traces in Langfuse —
	// the server merges repeated events by ID. See the ingestion API docs.
	body := traceBody{
		ID:       t.ID,
		Output:   output,
		Metadata: metadata,
	}
	t.manager.enqueue(ingestionEvent{
		ID:        newID(),
		Timestamp: isoTime(time.Now()),
		Type:      "trace-create",
		Body:      body,
	})
}

// StartGeneration opens a generation observation under the trace carried by
// ctx (or a newly auto-created trace if none is present).
func (m *Manager) StartGeneration(ctx context.Context, opts GenerationOptions) (context.Context, *Generation) {
	if m == nil || !m.cfg.Enabled {
		return ctx, &Generation{}
	}
	// If the caller hasn't opened a trace yet, create a shallow auto-trace so
	// the generation has a parent. This keeps single-shot internal callers
	// (e.g. test connections) observable.
	trace, ok := traceFromCtx(ctx)
	if !ok || trace == nil {
		newCtx, t := m.StartTrace(ctx, TraceOptions{Name: opts.Name})
		ctx = newCtx
		trace = t
	}
	if !trace.sampled {
		return ctx, &Generation{}
	}
	now := time.Now()
	g := &Generation{
		ID:        newID(),
		TraceID:   trace.ID,
		manager:   m,
		sampled:   true,
		startTime: now,
		model:     opts.Model,
		name:      opts.Name,
	}
	body := observationBody{
		ID:              g.ID,
		TraceID:         g.TraceID,
		Type:            "GENERATION",
		Name:            opts.Name,
		StartTime:       isoTime(now),
		Input:           opts.Input,
		Metadata:        opts.Metadata,
		Model:           opts.Model,
		ModelParameters: opts.ModelParameters,
	}
	m.enqueue(ingestionEvent{
		ID:        newID(),
		Timestamp: isoTime(now),
		Type:      "generation-create",
		Body:      body,
	})
	return ctx, g
}

// Finish updates a generation with its final output, token usage and any
// error. A non-nil err marks the observation as ERROR level in Langfuse.
func (g *Generation) Finish(output interface{}, usage *TokenUsage, err error) {
	if g == nil || g.manager == nil || !g.sampled {
		return
	}
	level := "DEFAULT"
	var statusMsg string
	if err != nil {
		level = "ERROR"
		statusMsg = err.Error()
	}
	body := observationBody{
		ID:            g.ID,
		TraceID:       g.TraceID,
		Type:          "GENERATION",
		EndTime:       isoTime(time.Now()),
		Output:        output,
		Usage:         usage,
		Level:         level,
		StatusMessage: statusMsg,
	}
	g.manager.enqueue(ingestionEvent{
		ID:        newID(),
		Timestamp: isoTime(time.Now()),
		Type:      "generation-update",
		Body:      body,
	})
}

// MarkCompletionStart records the time at which the first token was received
// in a streaming generation. Langfuse surfaces this as time-to-first-token.
func (g *Generation) MarkCompletionStart(t time.Time) {
	if g == nil || g.manager == nil || !g.sampled {
		return
	}
	body := observationBody{
		ID:              g.ID,
		TraceID:         g.TraceID,
		Type:            "GENERATION",
		CompletionStart: isoTime(t),
	}
	g.manager.enqueue(ingestionEvent{
		ID:        newID(),
		Timestamp: isoTime(time.Now()),
		Type:      "generation-update",
		Body:      body,
	})
}
