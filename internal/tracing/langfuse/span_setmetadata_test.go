package langfuse

import (
	"context"
	"encoding/json"
	"testing"

	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

// spanMetadataAttr decodes the langfuse.observation.metadata JSON attribute
// of an exported span.
func spanMetadataAttr(t *testing.T, s tracetest.SpanStub) map[string]interface{} {
	t.Helper()
	raw := spanAttr(s.Attributes, attrObsMetadata)
	if raw == "" {
		t.Fatalf("span %q has no %s attribute", s.Name, attrObsMetadata)
	}
	var md map[string]interface{}
	if err := json.Unmarshal([]byte(raw), &md); err != nil {
		t.Fatalf("metadata attribute is not JSON: %v (raw=%q)", err, raw)
	}
	return md
}

func findSpanByName(t *testing.T, spans []tracetest.SpanStub, name string) tracetest.SpanStub {
	t.Helper()
	for _, s := range spans {
		if s.Name == name {
			return s
		}
	}
	t.Fatalf("span %q not exported; got %d span(s)", name, len(spans))
	return tracetest.SpanStub{}
}

// TestSpanSetMetadataLandsOnExportedSpan verifies that metadata set after
// StartSpan via SetMetadata is merged into the span metadata exported at
// Finish, alongside the open-time and finish-time keys.
func TestSpanSetMetadataLandsOnExportedSpan(t *testing.T) {
	m, exp := newTestManager(t)
	_, span := m.StartSpan(context.Background(), SpanOptions{
		Name:     "agent.tool.shell_exec",
		Metadata: map[string]interface{}{"iteration": 0, "tool_call_id": "call-1"},
	})
	span.SetMetadata(map[string]interface{}{
		"intent.verdict":    "deny",
		"intent.layer":      "rule",
		"intent.policy_id":  "pol-1",
		"intent.latency_ms": int64(3),
	})
	span.Finish(nil, map[string]interface{}{"success": true}, nil)

	md := spanMetadataAttr(t, findSpanByName(t, exp.GetSpans(), "agent.tool.shell_exec"))
	// SetMetadata keys must all be present with exact values.
	if md["intent.verdict"] != "deny" {
		t.Fatalf("intent.verdict = %#v", md["intent.verdict"])
	}
	if md["intent.layer"] != "rule" {
		t.Fatalf("intent.layer = %#v", md["intent.layer"])
	}
	if md["intent.policy_id"] != "pol-1" {
		t.Fatalf("intent.policy_id = %#v", md["intent.policy_id"])
	}
	if md["intent.latency_ms"] != float64(3) {
		t.Fatalf("intent.latency_ms = %#v", md["intent.latency_ms"])
	}
	// Open-time and finish-time keys survive the merge.
	if md["iteration"] != float64(0) || md["tool_call_id"] != "call-1" {
		t.Fatalf("open-time metadata lost: %v", md)
	}
	if md["success"] != true {
		t.Fatalf("finish-time metadata lost: %v", md)
	}
}

// TestSpanSetMetadataMergesAcrossCalls verifies repeated SetMetadata calls
// accumulate, and a later call wins on key conflict.
func TestSpanSetMetadataMergesAcrossCalls(t *testing.T) {
	m, exp := newTestManager(t)
	_, span := m.StartSpan(context.Background(), SpanOptions{Name: "agent.tool.x"})
	span.SetMetadata(map[string]interface{}{"intent.verdict": "uncertain"})
	span.SetMetadata(map[string]interface{}{"intent.verdict": "allow", "intent.latency_ms": int64(1)})
	span.Finish(nil, nil, nil)

	md := spanMetadataAttr(t, findSpanByName(t, exp.GetSpans(), "agent.tool.x"))
	if md["intent.verdict"] != "allow" {
		t.Fatalf("later SetMetadata must win: intent.verdict = %#v", md["intent.verdict"])
	}
	if md["intent.latency_ms"] != float64(1) {
		t.Fatalf("accumulated key missing: %v", md)
	}
}

// TestSpanSetMetadataDisabledAndNilSafe verifies SetMetadata is a safe no-op
// when tracing is disabled and on a nil span, so callers can wire it
// unconditionally (same contract as Finish).
func TestSpanSetMetadataDisabledAndNilSafe(t *testing.T) {
	m, err := Init(Config{Enabled: false})
	if err != nil {
		t.Fatalf("init: %v", err)
	}
	t.Cleanup(func() { _ = m.Shutdown(context.Background()) })

	_, span := m.StartSpan(context.Background(), SpanOptions{Name: "agent.tool.x"})
	span.SetMetadata(map[string]interface{}{"intent.verdict": "deny"})
	span.Finish(nil, nil, nil)

	var nilSpan *Span
	nilSpan.SetMetadata(map[string]interface{}{"intent.verdict": "deny"})
	nilSpan.SetMetadata(nil)
	span.SetMetadata(nil)
}
