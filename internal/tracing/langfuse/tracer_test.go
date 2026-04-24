package langfuse

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

// newTestServer spins up a fake Langfuse ingestion endpoint and returns the
// collected events + a cleanup func.
func newTestServer(t *testing.T) (*httptest.Server, func() []ingestionEvent) {
	t.Helper()
	var mu sync.Mutex
	var batches []ingestionRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var req ingestionRequest
		_ = json.Unmarshal(body, &req)
		mu.Lock()
		batches = append(batches, req)
		mu.Unlock()
		w.WriteHeader(200)
		_, _ = w.Write([]byte(`{}`))
	}))
	drain := func() []ingestionEvent {
		mu.Lock()
		defer mu.Unlock()
		var out []ingestionEvent
		for _, b := range batches {
			out = append(out, b.Batch...)
		}
		return out
	}
	return srv, drain
}

func newTestManager(t *testing.T, host string) *Manager {
	t.Helper()
	m, err := Init(Config{
		Enabled:        true,
		Host:           host,
		PublicKey:      "pk",
		SecretKey:      "sk",
		FlushAt:        1,
		FlushInterval:  5 * time.Millisecond,
		QueueSize:      32,
		RequestTimeout: 2 * time.Second,
		SampleRate:     1.0,
	})
	if err != nil {
		t.Fatalf("init: %v", err)
	}
	return m
}

// TestSpan_NestedHierarchy verifies that nested StartSpan calls produce a
// trace → span₁ → span₂ → generation hierarchy, with parentObservationId
// pointing to the direct ancestor at each level.
func TestSpan_NestedHierarchy(t *testing.T) {
	srv, drain := newTestServer(t)
	defer srv.Close()
	m := newTestManager(t, srv.URL)

	ctx, trace := m.StartTrace(context.Background(), TraceOptions{Name: "root"})
	ctx, outer := m.StartSpan(ctx, SpanOptions{Name: "outer"})
	ctx, inner := m.StartSpan(ctx, SpanOptions{Name: "inner"})
	_, gen := m.StartGeneration(ctx, GenerationOptions{Name: "llm", Model: "m"})

	gen.Finish("out", &TokenUsage{Input: 1, Output: 2, Total: 3}, nil)
	inner.Finish("inner-out", nil, nil)
	outer.Finish("outer-out", nil, nil)
	trace.Finish("root-out", nil)

	if err := m.Shutdown(context.Background()); err != nil {
		t.Fatalf("shutdown: %v", err)
	}

	events := drain()
	parentOf := map[string]string{}
	traceOf := map[string]string{}
	kindOf := map[string]string{}
	for _, ev := range events {
		if ev.Type != "span-create" && ev.Type != "generation-create" {
			continue
		}
		b, _ := json.Marshal(ev.Body)
		var ob observationBody
		_ = json.Unmarshal(b, &ob)
		parentOf[ob.ID] = ob.ParentObservationID
		traceOf[ob.ID] = ob.TraceID
		kindOf[ob.ID] = ob.Type
	}

	if traceOf[outer.ID] != trace.ID || parentOf[outer.ID] != "" {
		t.Errorf("outer span should sit directly under the trace, got parent=%q trace=%q", parentOf[outer.ID], traceOf[outer.ID])
	}
	if parentOf[inner.ID] != outer.ID {
		t.Errorf("inner span parent mismatch: got %q want %q", parentOf[inner.ID], outer.ID)
	}
	if parentOf[gen.ID] != inner.ID {
		t.Errorf("generation should nest under inner span, got parent=%q want %q", parentOf[gen.ID], inner.ID)
	}
	if kindOf[outer.ID] != "SPAN" || kindOf[inner.ID] != "SPAN" {
		t.Errorf("expected both wrappers to be SPAN observations, got outer=%q inner=%q", kindOf[outer.ID], kindOf[inner.ID])
	}
	if kindOf[gen.ID] != "GENERATION" {
		t.Errorf("expected generation kind, got %q", kindOf[gen.ID])
	}
}

// TestSpan_FinishWithError records an error status on the span so failures in
// asynq handlers surface as red observations in Langfuse.
func TestSpan_FinishWithError(t *testing.T) {
	srv, drain := newTestServer(t)
	defer srv.Close()
	m := newTestManager(t, srv.URL)

	ctx, _ := m.StartTrace(context.Background(), TraceOptions{Name: "root"})
	_, span := m.StartSpan(ctx, SpanOptions{Name: "boom"})
	span.Finish(nil, nil, errors.New("kaboom"))

	if err := m.Shutdown(context.Background()); err != nil {
		t.Fatalf("shutdown: %v", err)
	}

	var sawError bool
	for _, ev := range drain() {
		if ev.Type != "span-update" {
			continue
		}
		b, _ := json.Marshal(ev.Body)
		var ob observationBody
		_ = json.Unmarshal(b, &ob)
		if ob.Level == "ERROR" && ob.StatusMessage == "kaboom" {
			sawError = true
		}
	}
	if !sawError {
		t.Fatal("expected span-update with ERROR level and status message")
	}
}

// TestResumeTrace_NoTraceCreateEvent verifies that ResumeTrace does NOT
// emit a trace-create event — the originating HTTP handler already did,
// and a duplicate would register as an orphan root in the Langfuse UI.
func TestResumeTrace_NoTraceCreateEvent(t *testing.T) {
	srv, drain := newTestServer(t)
	defer srv.Close()
	m := newTestManager(t, srv.URL)

	ctx, trace := m.ResumeTrace(context.Background(), "upstream-trace", "upstream-span")
	if trace == nil || trace.ID != "upstream-trace" {
		t.Fatalf("expected resumed trace with id upstream-trace, got %+v", trace)
	}
	if pid, ok := parentObservationFromCtx(ctx); !ok || pid != "upstream-span" {
		t.Errorf("expected parent observation upstream-span on ctx, got %q (ok=%v)", pid, ok)
	}

	// Emit one child so there's *something* to flush, proving only child
	// observations reach the wire, not a trace-create for the resume.
	_, span := m.StartSpan(ctx, SpanOptions{Name: "child"})
	span.Finish(nil, nil, nil)

	if err := m.Shutdown(context.Background()); err != nil {
		t.Fatalf("shutdown: %v", err)
	}

	for _, ev := range drain() {
		if ev.Type == "trace-create" {
			t.Fatalf("ResumeTrace must not emit trace-create events, got %+v", ev)
		}
	}
}

// TestResumeTrace_DisabledIsSafe guards against nil deref when Langfuse is
// off: ResumeTrace should return a nil *Trace and the original ctx unchanged.
func TestResumeTrace_DisabledIsSafe(t *testing.T) {
	m, err := Init(Config{Enabled: false})
	if err != nil {
		t.Fatalf("init: %v", err)
	}
	ctx, trace := m.ResumeTrace(context.Background(), "x", "y")
	if trace != nil {
		t.Errorf("expected nil trace when disabled, got %+v", trace)
	}
	if _, ok := parentObservationFromCtx(ctx); ok {
		t.Error("disabled ResumeTrace should not attach parent observation")
	}
}
