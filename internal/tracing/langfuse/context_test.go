package langfuse

import (
	"context"
	"encoding/json"
	"testing"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

// exportedTraceMetadata extracts the decoded trace metadata map from the
// single exported span, failing the test when the attribute is absent.
func exportedTraceMetadata(t *testing.T, exp *tracetest.InMemoryExporter) map[string]interface{} {
	t.Helper()
	spans := exp.GetSpans()
	if len(spans) != 1 {
		t.Fatalf("expected 1 exported span, got %d", len(spans))
	}
	var raw string
	for _, attr := range spans[0].Attributes {
		if attr.Key == attribute.Key(attrTraceMetadata) {
			raw = attr.Value.AsString()
		}
	}
	if raw == "" {
		t.Fatal("trace metadata attribute missing from exported span")
	}
	md := map[string]interface{}{}
	if err := json.Unmarshal([]byte(raw), &md); err != nil {
		t.Fatalf("metadata is not valid JSON: %v", err)
	}
	return md
}

func TestSetTraceMetadataMergesIntoFinishedTrace(t *testing.T) {
	m, exp := newTestManager(t)
	ctx, trace := m.StartTrace(context.Background(), TraceOptions{
		Name: "POST /api/v1/knowledge-chat/:session_id",
		Metadata: map[string]interface{}{
			"http.method": "POST",
			"request_id":  "req-1",
		},
	})

	SetTraceMetadata(ctx, map[string]interface{}{
		"channel":    "web",
		"agent_id":   "agent-42",
		"agent_mode": "smart-reasoning",
		"language":   "zh-CN",
	})
	trace.Finish(map[string]interface{}{"status": 200}, nil)

	md := exportedTraceMetadata(t, exp)
	for k, want := range map[string]interface{}{
		"http.method": "POST",
		"request_id":  "req-1",
		"channel":     "web",
		"agent_id":    "agent-42",
		"agent_mode":  "smart-reasoning",
		"language":    "zh-CN",
	} {
		if got, ok := md[k]; !ok || got != want {
			t.Errorf("metadata[%q] = %v, want %v", k, got, want)
		}
	}
	// GinMiddleware passes its status map as the Finish *output* argument, so
	// it must not leak into the trace metadata.
	if _, exists := md["status"]; exists {
		t.Error("finish output must not leak into trace metadata")
	}
}

func TestSetTraceMetadataPreservesExistingKeys(t *testing.T) {
	m, exp := newTestManager(t)
	ctx, trace := m.StartTrace(context.Background(), TraceOptions{
		Name: "POST /api/v1/agent-chat/:session_id",
		Metadata: map[string]interface{}{
			"http.path":  "/api/v1/agent-chat/:session_id",
			"channel":    "im", // set by the IM caller before the handler ran
			"request_id": "req-2",
		},
	})

	SetTraceMetadata(ctx, map[string]interface{}{
		"channel":  "web", // must not shadow the already-recorded value
		"language": "en-US",
	})
	trace.Finish(nil, nil)

	md := exportedTraceMetadata(t, exp)
	if md["channel"] != "im" {
		t.Errorf("channel = %v, want im (existing keys must win)", md["channel"])
	}
	if md["language"] != "en-US" {
		t.Errorf("language = %v, want en-US", md["language"])
	}
}

func TestSetTraceMetadataSkipsNonScalarAndEmptyEntries(t *testing.T) {
	m, exp := newTestManager(t)
	ctx, trace := m.StartTrace(context.Background(), TraceOptions{Name: "trace"})

	SetTraceMetadata(ctx, map[string]interface{}{
		"ok":     "kept",
		"nested": map[string]interface{}{"a": 1}, // object: skipped
		"list":   []string{"x"},                  // array: skipped
		"nil":    nil,                            // nil: skipped
		"":       "empty key",                    // empty key: skipped
		"num":    7,                              // typed Go int: kept
		"flag":   true,
	})
	trace.Finish(nil, nil)

	md := exportedTraceMetadata(t, exp)
	if md["ok"] != "kept" || md["num"] != float64(7) || md["flag"] != true {
		t.Errorf("scalars lost: %v", md)
	}
	for _, k := range []string{"nested", "list", "nil", ""} {
		if _, exists := md[k]; exists {
			t.Errorf("key %q should have been skipped", k)
		}
	}
}

func TestSetTraceMetadataNoopWithoutTraceOrDisabled(t *testing.T) {
	// No trace on ctx.
	SetTraceMetadata(context.Background(), map[string]interface{}{"channel": "web"})

	// Disabled manager.
	exp := tracetest.NewInMemoryExporter()
	m, err := Init(Config{Enabled: false, testExporter: exp})
	if err != nil {
		t.Fatalf("init disabled: %v", err)
	}
	ctx, trace := m.StartTrace(context.Background(), TraceOptions{Name: "t"})
	SetTraceMetadata(ctx, map[string]interface{}{"channel": "web"})
	trace.Finish(nil, nil)
	if spans := exp.GetSpans(); len(spans) != 0 {
		t.Errorf("disabled manager exported %d spans", len(spans))
	}

	// Degenerate inputs. A nil Context must no-op without panicking.
	//nolint:staticcheck // deliberate nil ctx
	SetTraceMetadata(nil, map[string]interface{}{"channel": "web"})
	SetTraceMetadata(context.Background(), nil)
}

func TestSetTraceMetadataIgnoresMismatchedContext(t *testing.T) {
	m, exp := newTestManager(t)
	ctx, trace := m.StartTrace(context.Background(), TraceOptions{Name: "resumed"})
	SetTraceMetadata(context.Background(), map[string]interface{}{"agent_id": "x"}) // wrong ctx: ignored
	SetTraceMetadata(ctx, map[string]interface{}{"tenant_name": "acme"})
	trace.Finish(nil, nil)

	md := exportedTraceMetadata(t, exp)
	if md["tenant_name"] != "acme" {
		t.Errorf("tenant_name = %v, want acme", md["tenant_name"])
	}
	if _, exists := md["agent_id"]; exists {
		t.Error("agent_id from a mismatched ctx must be ignored")
	}
}
