package langfuse

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

// newTestManager builds a Manager wired to an in-memory span exporter via the
// SimpleSpanProcessor (synchronous export on span End), so tests can assert on
// exported spans deterministically without an HTTP server.
func newTestManager(t *testing.T) (*Manager, *tracetest.InMemoryExporter) {
	t.Helper()
	exp := tracetest.NewInMemoryExporter()
	m, err := Init(Config{
		Enabled:        true,
		Host:           "http://test",
		PublicKey:      "pk",
		SecretKey:      "sk",
		FlushAt:        1,
		FlushInterval:  1 * time.Second,
		QueueSize:      32,
		RequestTimeout: 2 * time.Second,
		SampleRate:     1.0,
		testExporter:   exp,
	})
	if err != nil {
		t.Fatalf("init: %v", err)
	}
	t.Cleanup(func() { _ = m.Shutdown(context.Background()) })
	return m, exp
}

// spanAttr returns the string value of a span attribute, or "" if absent.
func spanAttr(attrs []attribute.KeyValue, key string) string {
	for _, kv := range attrs {
		if string(kv.Key) == key {
			if v, ok := kv.Value.AsInterface().(string); ok {
				return v
			}
		}
	}
	return ""
}

// spanType returns the langfuse.observation.type of a span.
func spanType(s tracetest.SpanStub) string { return spanAttr(s.Attributes, attrObsType) }

// TestSpan_NestedHierarchy verifies nested StartSpan calls produce a
// trace → span → span → generation tree with correct OTel parent linking
// (parenting is automatic through trace.SpanFromContext, no manual ids).
func TestSpan_NestedHierarchy(t *testing.T) {
	m, exp := newTestManager(t)

	ctx, trace := m.StartTrace(context.Background(), TraceOptions{Name: "root"})
	ctx, outer := m.StartSpan(ctx, SpanOptions{Name: "outer"})
	ctx, inner := m.StartSpan(ctx, SpanOptions{Name: "inner"})
	_, gen := m.StartGeneration(ctx, GenerationOptions{Name: "llm", Model: "m"})

	gen.Finish("out", &TokenUsage{Input: 1, Output: 2, Total: 3}, nil)
	inner.Finish("inner-out", nil, nil)
	outer.Finish("outer-out", nil, nil)
	trace.Finish("root-out", nil)

	spans := exp.GetSpans()
	if len(spans) != 4 {
		t.Fatalf("expected 4 spans, got %d", len(spans))
	}
	byName := map[string]tracetest.SpanStub{}
	for _, s := range spans {
		byName[s.Name] = s
	}
	root, outerS, innerS, genS := byName["root"], byName["outer"], byName["inner"], byName["llm"]
	if root.Name == "" || outerS.Name == "" || innerS.Name == "" || genS.Name == "" {
		t.Fatalf("missing expected spans: %+v", byName)
	}
	// All spans share the trace id.
	if outerS.SpanContext.TraceID() != root.SpanContext.TraceID() ||
		innerS.SpanContext.TraceID() != root.SpanContext.TraceID() ||
		genS.SpanContext.TraceID() != root.SpanContext.TraceID() {
		t.Errorf("all spans must share the root trace id")
	}
	// Parent chain: outer → root, inner → outer, gen → inner.
	if outerS.Parent.SpanID() != root.SpanContext.SpanID() {
		t.Errorf("outer parent = %s, want root %s", outerS.Parent.SpanID(), root.SpanContext.SpanID())
	}
	if innerS.Parent.SpanID() != outerS.SpanContext.SpanID() {
		t.Errorf("inner parent = %s, want outer %s", innerS.Parent.SpanID(), outerS.SpanContext.SpanID())
	}
	if genS.Parent.SpanID() != innerS.SpanContext.SpanID() {
		t.Errorf("gen parent = %s, want inner %s", genS.Parent.SpanID(), innerS.SpanContext.SpanID())
	}
	// Observation types.
	if spanType(root) != obsTypeTrace {
		t.Errorf("root type = %q, want %q", spanType(root), obsTypeTrace)
	}
	if spanType(outerS) != obsTypeSpan || spanType(innerS) != obsTypeSpan {
		t.Errorf("outer/inner type not span: %q %q", spanType(outerS), spanType(innerS))
	}
	if spanType(genS) != obsTypeGeneration {
		t.Errorf("gen type = %q, want %q", spanType(genS), obsTypeGeneration)
	}
}

// TestSpan_FinishWithError records an error status on the span so failures in
// asynq handlers surface as red observations in Langfuse.
func TestSpan_FinishWithError(t *testing.T) {
	m, exp := newTestManager(t)

	ctx, _ := m.StartTrace(context.Background(), TraceOptions{Name: "root"})
	_, span := m.StartSpan(ctx, SpanOptions{Name: "boom"})
	span.Finish(nil, nil, errors.New("kaboom"))

	for _, s := range exp.GetSpans() {
		if s.Name != "boom" {
			continue
		}
		if s.Status.Code != codes.Error {
			t.Errorf("span status = %v, want Error", s.Status.Code)
		}
		if s.Status.Description != "kaboom" {
			t.Errorf("status description = %q, want kaboom", s.Status.Description)
		}
		return
	}
	t.Fatal("boom span not exported")
}

// TestManager_FullRoundTrip asserts a generation carries the model name and
// usage_details attribute (JSON with token counts), and shares the trace id
// of the root span.
func TestManager_FullRoundTrip(t *testing.T) {
	m, exp := newTestManager(t)

	ctx, trace := m.StartTrace(context.Background(), TraceOptions{Name: "test.trace", UserID: "user-42"})
	_, gen := m.StartGeneration(ctx, GenerationOptions{
		Name:  "chat.completion",
		Model: "gpt-test",
		Input: []map[string]string{{"role": "user", "content": "hi"}},
	})
	gen.Finish("hello", &TokenUsage{Input: 10, Output: 20, Total: 30, Unit: "TOKENS"}, nil)
	trace.Finish("hello", nil)

	for _, s := range exp.GetSpans() {
		if s.Name != "chat.completion" {
			continue
		}
		if spanAttr(s.Attributes, attrObsModel) != "gpt-test" {
			t.Errorf("model = %q, want gpt-test", spanAttr(s.Attributes, attrObsModel))
		}
		usage := spanAttr(s.Attributes, attrObsUsageDetails)
		if !strings.Contains(usage, `"total":30`) {
			t.Errorf("usage_details = %q, want total:30", usage)
		}
		if s.SpanContext.TraceID().String() != trace.ID {
			t.Errorf("gen trace id = %s, want %s", s.SpanContext.TraceID(), trace.ID)
		}
		return
	}
	t.Fatal("generation span not exported")
}

// TestSpan_FinishMetadataMerged verifies that metadata supplied at Finish is
// merged into (not discarded, nor overwriting) the metadata set at StartSpan.
// Regression guard: several call sites only know key fields (outcome,
// duration_ms, …) at completion and rely on Finish metadata being reported.
func TestSpan_FinishMetadataMerged(t *testing.T) {
	m, exp := newTestManager(t)

	ctx, tr := m.StartTrace(context.Background(), TraceOptions{Name: "root"})
	_, span := m.StartSpan(ctx, SpanOptions{
		Name:     "work",
		Metadata: map[string]interface{}{"stage": "ingest", "task_type": "manual"},
	})
	span.Finish("out", map[string]interface{}{"outcome": "success", "duration_ms": 42}, nil)
	tr.Finish(nil, nil)

	for _, s := range exp.GetSpans() {
		if s.Name != "work" {
			continue
		}
		meta := spanAttr(s.Attributes, attrObsMetadata)
		for _, want := range []string{`"stage":"ingest"`, `"task_type":"manual"`, `"outcome":"success"`, `"duration_ms":42`} {
			if !strings.Contains(meta, want) {
				t.Errorf("span metadata %q missing %q", meta, want)
			}
		}
		return
	}
	t.Fatal("work span not exported")
}

// TestTrace_FinishMetadataMerged verifies the same merge behaviour on a trace:
// open-time correlation fields (request_id) survive alongside finish outcome.
func TestTrace_FinishMetadataMerged(t *testing.T) {
	m, exp := newTestManager(t)

	_, tr := m.StartTrace(context.Background(), TraceOptions{
		Name:     "root",
		Metadata: map[string]interface{}{"request_id": "req-1"},
	})
	tr.Finish("done", map[string]interface{}{"status": 200})

	for _, s := range exp.GetSpans() {
		if s.Name != "root" {
			continue
		}
		meta := spanAttr(s.Attributes, attrTraceMetadata)
		if !strings.Contains(meta, `"request_id":"req-1"`) || !strings.Contains(meta, `"status":200`) {
			t.Errorf("trace metadata %q missing merged fields", meta)
		}
		return
	}
	t.Fatal("root span not exported")
}

// TestStartGeneration_AutoRootExported guards the regression where a
// generation started with no active trace auto-opened a root span that was
// never ended and therefore never exported — leaving the generation's parent
// dangling. After the fix the auto root is exported and shares the trace id.
func TestStartGeneration_AutoRootExported(t *testing.T) {
	m, exp := newTestManager(t)

	_, gen := m.StartGeneration(context.Background(), GenerationOptions{Name: "llm", Model: "m"})
	gen.Finish("out", nil, nil)

	var root, generation tracetest.SpanStub
	for _, s := range exp.GetSpans() {
		switch spanType(s) {
		case obsTypeTrace:
			root = s
		case obsTypeGeneration:
			generation = s
		}
	}
	if root.Name == "" {
		t.Fatal("auto-created root trace span was not exported")
	}
	if generation.Name == "" {
		t.Fatal("generation span was not exported")
	}
	if generation.SpanContext.TraceID() != root.SpanContext.TraceID() {
		t.Errorf("generation trace id %s != root trace id %s", generation.SpanContext.TraceID(), root.SpanContext.TraceID())
	}
	if generation.Parent.SpanID() != root.SpanContext.SpanID() {
		t.Errorf("generation parent %s != root span %s (dangling parent)", generation.Parent.SpanID(), root.SpanContext.SpanID())
	}
}

// TestStartSpan_AutoRootExported is the span counterpart of the above.
func TestStartSpan_AutoRootExported(t *testing.T) {
	m, exp := newTestManager(t)

	_, span := m.StartSpan(context.Background(), SpanOptions{Name: "orphan"})
	span.Finish("out", nil, nil)

	var sawRoot, sawSpan bool
	for _, s := range exp.GetSpans() {
		switch spanType(s) {
		case obsTypeTrace:
			sawRoot = true
		case obsTypeSpan:
			sawSpan = true
		}
	}
	if !sawRoot {
		t.Error("auto-created root trace span was not exported")
	}
	if !sawSpan {
		t.Error("span was not exported")
	}
}

// TestTraceparentPropagation is the sop3 correlation core test: an incoming
// W3C traceparent (as injected by an upstream caller like sop3) is extracted,
// and the WeKnora root span inherits the upstream trace id — so in LiteFuse
// the WeKnora trace and the upstream caller's trace are the same trace.
func TestTraceparentPropagation(t *testing.T) {
	m, exp := newTestManager(t)

	// Simulate an upstream caller carrying a traceparent.
	upstreamCtx, upstreamSpan := m.Tracer().Start(context.Background(), "upstream-caller")
	remoteTraceID := upstreamSpan.SpanContext().TraceID()
	carrier := propagation.MapCarrier{}
	propagator.Inject(upstreamCtx, carrier)
	if carrier["traceparent"] == "" {
		t.Fatal("no traceparent injected")
	}

	// The HTTP middleware extracts the traceparent into the request context.
	httpCtx := propagator.Extract(context.Background(), carrier)
	_, trace := m.StartTrace(httpCtx, TraceOptions{Name: "weknora-root"})
	trace.Finish(nil, nil)

	for _, s := range exp.GetSpans() {
		if s.Name != "weknora-root" {
			continue
		}
		if s.SpanContext.TraceID() != remoteTraceID {
			t.Errorf("weknora root trace id = %s, want upstream %s (traceparent not inherited)",
				s.SpanContext.TraceID(), remoteTraceID)
		}
		return
	}
	t.Fatal("weknora-root span not exported")
}
