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
	ID                  string
	TraceID             string
	ParentObservationID string
	manager             *Manager
	sampled             bool
	startTime           time.Time
	model               string
	name                string
}

// Span represents a logical unit of work that isn't itself an LLM call — for
// example an asynq task execution, a pipeline stage, or a document-processing
// step. Generations and nested spans attach to it via parentObservationId.
type Span struct {
	ID                  string
	TraceID             string
	ParentObservationID string
	manager             *Manager
	sampled             bool
	startTime           time.Time
	name                string
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

// SpanOptions configures a new SPAN observation.
type SpanOptions struct {
	Name     string
	Input    interface{}
	Metadata map[string]interface{}
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

// ResumeTrace reconstructs a *Trace handle from an upstream-provided trace id
// (and optional parent observation id), without emitting a new trace-create
// event. Used by the asynq middleware to graft async work onto the HTTP-level
// trace that originated it: the HTTP layer already issued trace-create, and
// the worker only needs to add child observations.
//
// When traceID is empty the returned *Trace is nil, signalling the caller
// should fall back to StartTrace if it wants a standalone root.
func (m *Manager) ResumeTrace(ctx context.Context, traceID, parentObservationID string) (context.Context, *Trace) {
	if m == nil || !m.cfg.Enabled || traceID == "" {
		return ctx, nil
	}
	t := &Trace{ID: traceID, manager: m, sampled: true}
	ctx = withTrace(ctx, t)
	if parentObservationID != "" {
		ctx = withParentObservation(ctx, parentObservationID)
	}
	return ctx, t
}

// StartSpan opens a SPAN observation under the trace (and optional parent
// observation) carried by ctx. When no trace is present, a shallow trace is
// auto-created, mirroring StartGeneration's behaviour. Returns a ctx whose
// parentObservationId is set to this span's id, so any nested span or
// generation will properly attach as a child.
func (m *Manager) StartSpan(ctx context.Context, opts SpanOptions) (context.Context, *Span) {
	if m == nil || !m.cfg.Enabled {
		return ctx, &Span{}
	}
	trace, ok := traceFromCtx(ctx)
	if !ok || trace == nil {
		newCtx, t := m.StartTrace(ctx, TraceOptions{Name: opts.Name})
		ctx = newCtx
		trace = t
	}
	if !trace.sampled {
		return ctx, &Span{}
	}
	parent, _ := parentObservationFromCtx(ctx)
	now := time.Now()
	s := &Span{
		ID:                  newID(),
		TraceID:             trace.ID,
		ParentObservationID: parent,
		manager:             m,
		sampled:             true,
		startTime:           now,
		name:                opts.Name,
	}
	body := observationBody{
		ID:                  s.ID,
		TraceID:             s.TraceID,
		ParentObservationID: parent,
		Type:                "SPAN",
		Name:                opts.Name,
		StartTime:           isoTime(now),
		Input:               opts.Input,
		Metadata:            opts.Metadata,
	}
	m.enqueue(ingestionEvent{
		ID:        newID(),
		Timestamp: isoTime(now),
		Type:      "span-create",
		Body:      body,
	})
	ctx = withParentObservation(ctx, s.ID)
	return ctx, s
}

// Finish updates a span with its final output, extra metadata and any error.
// A non-nil err marks the span as ERROR level in Langfuse.
func (s *Span) Finish(output interface{}, metadata map[string]interface{}, err error) {
	if s == nil || s.manager == nil || !s.sampled {
		return
	}
	level := "DEFAULT"
	var statusMsg string
	if err != nil {
		level = "ERROR"
		statusMsg = err.Error()
	}
	body := observationBody{
		ID:            s.ID,
		TraceID:       s.TraceID,
		Type:          "SPAN",
		EndTime:       isoTime(time.Now()),
		Output:        output,
		Metadata:      metadata,
		Level:         level,
		StatusMessage: statusMsg,
	}
	s.manager.enqueue(ingestionEvent{
		ID:        newID(),
		Timestamp: isoTime(time.Now()),
		Type:      "span-update",
		Body:      body,
	})
}

// StartGeneration opens a generation observation under the trace carried by
// ctx (or a newly auto-created trace if none is present). If a parent span
// is present on ctx, the generation attaches under it via parentObservationId
// so the Langfuse tree shows: trace → span → generation.
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
	parent, _ := parentObservationFromCtx(ctx)
	now := time.Now()
	g := &Generation{
		ID:                  newID(),
		TraceID:             trace.ID,
		ParentObservationID: parent,
		manager:             m,
		sampled:             true,
		startTime:           now,
		model:               opts.Model,
		name:                opts.Name,
	}
	body := observationBody{
		ID:                  g.ID,
		TraceID:             g.TraceID,
		ParentObservationID: parent,
		Type:                "GENERATION",
		Name:                opts.Name,
		StartTime:           isoTime(now),
		Input:               opts.Input,
		Metadata:            opts.Metadata,
		Model:               opts.Model,
		ModelParameters:     opts.ModelParameters,
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
