package langfuse

import (
	"context"
	"encoding/json"
	"time"

	"github.com/Tencent/WeKnora/internal/logger"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

// Trace represents an active root observation. A Trace is conceptually one
// "request" (e.g. a chat turn). Generations and spans attached to it roll up
// as children in the Langfuse UI. It wraps an OpenTelemetry root span; its
// ID is the OTel trace id (W3C 32-hex), which — when the request carried a
// traceparent header — is the upstream caller's trace id (sop3 correlation).
type Trace struct {
	ID      string
	span    trace.Span
	manager *Manager
	// metadata holds the metadata set at StartTrace so Finish can merge (not
	// overwrite) the finish-time metadata into it before serializing.
	metadata map[string]interface{}
}

// Generation represents a single model invocation (LLM / embedding / VLM / ASR).
type Generation struct {
	ID      string
	span    trace.Span
	manager *Manager
	model   string
	name    string
	// autoTrace is a non-nil root trace this generation implicitly opened
	// because ctx carried none; Finish must End it so the root is exported.
	autoTrace *Trace
}

// Span represents a logical unit of work that isn't itself an LLM call — for
// example an asynq task execution, a pipeline stage, or a document-processing
// step. Generations and nested spans attach as children via the OTel span
// context (parenting is automatic through trace.SpanFromContext).
type Span struct {
	ID      string
	span    trace.Span
	manager *Manager
	name    string
	// metadata holds the metadata set at StartSpan so Finish can merge (not
	// overwrite) the finish-time metadata into it before serializing.
	metadata map[string]interface{}
	// autoTrace is a non-nil root trace this span implicitly opened because
	// ctx carried none; Finish must End it so the root is exported.
	autoTrace *Trace
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

// StartTrace opens a root span. When ctx carries a remote SpanContext (from a
// W3C traceparent extracted by GinMiddleware), the root span inherits the
// upstream trace id — this is what makes a sop3 run and its WeKnora call land
// under the same trace in LiteFuse. The returned *Trace is non-nil even when
// disabled (methods are no-ops), so callers don't need nil checks.
func (m *Manager) StartTrace(ctx context.Context, opts TraceOptions) (context.Context, *Trace) {
	if !m.Enabled() {
		return ctx, &Trace{manager: m}
	}
	name := opts.Name
	attrs := []attribute.KeyValue{attribute.String(attrObsType, obsTypeTrace)}
	if opts.Name != "" {
		attrs = append(attrs, attribute.String(attrTraceName, opts.Name))
	}
	if opts.UserID != "" {
		attrs = append(attrs, attribute.String(attrUserID, opts.UserID))
	}
	if opts.SessionID != "" {
		attrs = append(attrs, attribute.String(attrSessionID, opts.SessionID))
	}
	env := opts.Environment
	if env == "" {
		env = m.cfg.Environment
	}
	if env != "" {
		attrs = append(attrs, attribute.String(attrEnvironment, env))
	}
	rel := opts.Release
	if rel == "" {
		rel = m.cfg.Release
	}
	if rel != "" {
		attrs = append(attrs, attribute.String(attrRelease, rel))
	}
	attrs = append(attrs, jsonAttr(attrTraceInput, opts.Input))
	attrs = append(attrs, jsonAttr(attrTraceMetadata, opts.Metadata))
	if len(opts.Tags) > 0 {
		attrs = append(attrs, jsonAttr(attrTraceTags, opts.Tags))
	}
	ctx, span := m.tracer.Start(ctx, name, trace.WithTimestamp(time.Now()), trace.WithAttributes(attrs...))
	t := &Trace{ID: span.SpanContext().TraceID().String(), span: span, manager: m, metadata: opts.Metadata}
	return withTrace(ctx, t), t
}

// Finish updates the trace with its final output and merges any finish-time
// metadata into the metadata set at StartTrace. Safe to call on a disabled
// trace (no-op). Finish keys are merged on top of the open-time correlation
// fields (request_id, http.method, etc.) rather than overwriting them, so
// both the open's correlation and the finish outcome survive.
func (t *Trace) Finish(output interface{}, metadata map[string]interface{}) {
	if t == nil || t.manager == nil || !t.manager.Enabled() || t.span == nil {
		return
	}
	attrs := []attribute.KeyValue{jsonAttr(attrTraceOutput, output)}
	if merged := mergeMetadata(t.metadata, metadata); merged != nil {
		attrs = append(attrs, jsonAttr(attrTraceMetadata, merged))
	}
	t.span.SetAttributes(attrs...)
	t.span.End()
}

// ResumeTrace reconstructs a *Trace handle from an externally-provided W3C
// trace id (and optional parent span id), without creating a new root span —
// the originating process (e.g. an HTTP request that already opened a trace)
// owns the root. Used to graft async work onto an existing trace: it sets a
// remote SpanContext on ctx so any child span/generation started under it
// inherits the upstream trace id. When traceID is empty the returned *Trace
// is nil, signalling the caller should fall back to StartTrace.
func (m *Manager) ResumeTrace(ctx context.Context, traceID, parentSpanID string) (context.Context, *Trace) {
	if m == nil || !m.Enabled() || traceID == "" {
		return ctx, nil
	}
	tid, err := trace.TraceIDFromHex(traceID)
	if err != nil {
		// Not a W3C 32-hex trace id (legacy UUID, etc.); cannot resume.
		return ctx, nil
	}
	var sid trace.SpanID
	if parentSpanID != "" {
		if s, err := trace.SpanIDFromHex(parentSpanID); err == nil {
			sid = s
		}
	}
	sc := trace.NewSpanContext(trace.SpanContextConfig{
		TraceID:    tid,
		SpanID:     sid,
		TraceFlags: trace.FlagsSampled,
		Remote:     true,
	})
	ctx = trace.ContextWithRemoteSpanContext(ctx, sc)
	t := &Trace{ID: traceID, manager: m}
	return withTrace(ctx, t), t
}

// reestablishParentSpan re-injects the active trace's root span as the OTel
// parent when ctx carries a *Trace but no active OTel span. This happens when
// a context rebuild drops the OTel span while the *Trace handle survives on
// the exported key (e.g. a background goroutine derived from a non-request
// context, or a CloneContext that predates/missed the span fix). Without
// this, child spans (e.g. a summary generation) start a fresh root and orphan
// off the HTTP trace.
func (m *Manager) reestablishParentSpan(ctx context.Context) context.Context {
	if !m.Enabled() {
		return ctx
	}
	if sp := trace.SpanFromContext(ctx); sp.IsRecording() {
		return ctx // already has an active span
	}
	if t, ok := traceFromCtx(ctx); ok && t != nil && t.span != nil {
		return trace.ContextWithSpan(ctx, t.span)
	}
	return ctx
}

// StartSpan opens a child span under the trace/span carried by ctx. When no
// trace is present, OTel creates a fresh root (mirroring StartGeneration's
// auto-trace behaviour). Returns a ctx whose active span is this span.
func (m *Manager) StartSpan(ctx context.Context, opts SpanOptions) (context.Context, *Span) {
	if !m.Enabled() {
		return ctx, &Span{manager: m}
	}
	ctx = m.reestablishParentSpan(ctx)
	var autoTrace *Trace
	if _, ok := traceFromCtx(ctx); !ok {
		// No active trace: open a shallow root so the span isn't orphaned.
		// Hold the handle so Finish can End it — otherwise the root span is
		// never exported and this span's parent points at a missing span.
		ctx, autoTrace = m.StartTrace(ctx, TraceOptions{Name: opts.Name})
	}
	attrs := []attribute.KeyValue{
		attribute.String(attrObsType, obsTypeSpan),
		jsonAttr(attrObsInput, opts.Input),
		jsonAttr(attrObsMetadata, opts.Metadata),
	}
	ctx, span := m.tracer.Start(ctx, opts.Name, trace.WithTimestamp(time.Now()), trace.WithAttributes(attrs...))
	return ctx, &Span{
		ID:        span.SpanContext().SpanID().String(),
		span:      span,
		manager:   m,
		name:      opts.Name,
		metadata:  opts.Metadata,
		autoTrace: autoTrace,
	}
}

// Finish updates a span with its final output, extra metadata and any error.
// A non-nil err marks the span as ERROR. Finish-time metadata is merged on top
// of the metadata set at StartSpan (finish keys win) rather than discarded, so
// fields only known at completion (outcome, duration_ms, tool_calls, …) are
// reported. If this span implicitly opened a root trace, that root is ended
// last so it is exported.
func (s *Span) Finish(output interface{}, metadata map[string]interface{}, err error) {
	if s == nil || s.manager == nil || !s.manager.Enabled() || s.span == nil {
		return
	}
	attrs := []attribute.KeyValue{jsonAttr(attrObsOutput, output)}
	if merged := mergeMetadata(s.metadata, metadata); merged != nil {
		attrs = append(attrs, jsonAttr(attrObsMetadata, merged))
	}
	s.span.SetAttributes(attrs...)
	if err != nil {
		s.span.RecordError(err)
		s.span.SetStatus(codes.Error, err.Error())
	}
	s.span.End()
	if s.autoTrace != nil {
		s.autoTrace.Finish(nil, nil)
	}
}

// StartGeneration opens a generation observation under the trace carried by
// ctx (or a newly auto-created trace). If a parent span is present on ctx,
// the generation attaches under it via the OTel span context.
func (m *Manager) StartGeneration(ctx context.Context, opts GenerationOptions) (context.Context, *Generation) {
	if !m.Enabled() {
		return ctx, &Generation{manager: m, model: opts.Model, name: opts.Name}
	}
	ctx = m.reestablishParentSpan(ctx)
	var autoTrace *Trace
	if _, ok := traceFromCtx(ctx); !ok {
		// No active trace: open a root so the generation isn't orphaned, and
		// hold the handle so Finish can End it (otherwise the root span never
		// gets exported and this generation's parent points at nothing).
		ctx, autoTrace = m.StartTrace(ctx, TraceOptions{Name: opts.Name})
	}
	attrs := []attribute.KeyValue{
		attribute.String(attrObsType, obsTypeGeneration),
		attribute.String(attrObsModel, opts.Model),
		jsonAttr(attrObsInput, opts.Input),
		jsonAttr(attrObsMetadata, opts.Metadata),
		jsonAttr(attrObsModelParams, opts.ModelParameters),
	}
	ctx, span := m.tracer.Start(ctx, opts.Name, trace.WithTimestamp(time.Now()), trace.WithAttributes(attrs...))
	g := &Generation{
		ID:        span.SpanContext().SpanID().String(),
		span:      span,
		manager:   m,
		model:     opts.Model,
		name:      opts.Name,
		autoTrace: autoTrace,
	}
	return ctx, g
}

// Finish updates a generation with its final output, token usage and any
// error. A non-nil err marks the observation as ERROR.
func (g *Generation) Finish(output interface{}, usage *TokenUsage, err error) {
	if g == nil || g.manager == nil || !g.manager.Enabled() || g.span == nil {
		return
	}
	attrs := []attribute.KeyValue{jsonAttr(attrObsOutput, output)}
	if usage != nil {
		attrs = append(attrs, jsonAttr(attrObsUsageDetails, usage))
	}
	g.span.SetAttributes(attrs...)
	if err != nil {
		g.span.RecordError(err)
		g.span.SetStatus(codes.Error, err.Error())
	}
	g.span.End()
	if g.autoTrace != nil {
		g.autoTrace.Finish(nil, nil)
	}
}

// MarkCompletionStart records the time at which the first token was received
// in a streaming generation. Langfuse surfaces this as time-to-first-token.
func (g *Generation) MarkCompletionStart(t time.Time) {
	if g == nil || g.manager == nil || !g.manager.Enabled() || g.span == nil {
		return
	}
	g.span.SetAttributes(attribute.String(attrObsCompletionStart, isoTime(t)))
}

// mergeMetadata combines the metadata captured when an observation opened
// with the metadata supplied at Finish. Finish keys win on conflict (they
// reflect the final outcome), while open-time keys (correlation fields such
// as request_id / http.method) are preserved. Returns nil when both inputs
// are empty so callers can skip writing an empty attribute.
func mergeMetadata(start, finish map[string]interface{}) map[string]interface{} {
	if len(start) == 0 && len(finish) == 0 {
		return nil
	}
	merged := make(map[string]interface{}, len(start)+len(finish))
	for k, v := range start {
		merged[k] = v
	}
	for k, v := range finish {
		merged[k] = v
	}
	return merged
}

// jsonAttr serializes v to a compact JSON string and wraps it as a string
// OTel attribute — matching how langfuse-python stores structured fields
// (input/output/metadata/usage) on spans. nil/zero values return an empty
// KeyValue (harmless on SetAttributes).
func jsonAttr(key string, v interface{}) attribute.KeyValue {
	if v == nil {
		return attribute.KeyValue{Key: attribute.Key(key)}
	}
	b, err := json.Marshal(v)
	if err != nil || len(b) == 0 || string(b) == "null" {
		logger.Warnf(context.Background(), "[Langfuse] marshal attr %s failed: %v", key, err)
		return attribute.KeyValue{Key: attribute.Key(key)}
	}
	return attribute.String(key, string(b))
}
