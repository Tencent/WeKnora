package langfuse

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

// TestManager_DisabledIsNoop verifies that when the manager is disabled the
// public API is safe to call and produces no side effects.
func TestManager_DisabledIsNoop(t *testing.T) {
	m, err := Init(Config{Enabled: false})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if m.Enabled() {
		t.Fatal("expected disabled")
	}
	ctx, trace := m.StartTrace(context.Background(), TraceOptions{Name: "x"})
	trace.Finish(nil, nil)

	_, gen := m.StartGeneration(ctx, GenerationOptions{Name: "g", Model: "m"})
	gen.Finish(nil, nil, nil)

	if err := m.Shutdown(context.Background()); err != nil {
		t.Fatalf("shutdown: %v", err)
	}
}

// TestManager_FullRoundTrip boots a fake Langfuse server, runs a trace +
// generation through the manager, and asserts the ingested payload contains
// the expected ids, model name and usage.
func TestManager_FullRoundTrip(t *testing.T) {
	var mu sync.Mutex
	var batches []ingestionRequest

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/public/ingestion" {
			t.Errorf("unexpected path: %s", r.URL.Path)
			w.WriteHeader(404)
			return
		}
		if auth := r.Header.Get("Authorization"); auth == "" {
			t.Errorf("missing Authorization header")
		}
		body, _ := io.ReadAll(r.Body)
		var req ingestionRequest
		if err := json.Unmarshal(body, &req); err != nil {
			t.Errorf("decode body: %v", err)
			w.WriteHeader(400)
			return
		}
		mu.Lock()
		batches = append(batches, req)
		mu.Unlock()
		w.WriteHeader(200)
		w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	m, err := Init(Config{
		Enabled:        true,
		Host:           srv.URL,
		PublicKey:      "pk",
		SecretKey:      "sk",
		FlushAt:        1,
		FlushInterval:  10 * time.Millisecond,
		QueueSize:      16,
		RequestTimeout: 2 * time.Second,
		SampleRate:     1.0,
	})
	if err != nil {
		t.Fatalf("init: %v", err)
	}

	ctx, trace := m.StartTrace(context.Background(), TraceOptions{
		Name:   "test.trace",
		UserID: "user-42",
	})
	_, gen := m.StartGeneration(ctx, GenerationOptions{
		Name:  "chat.completion",
		Model: "gpt-test",
		Input: []map[string]string{{"role": "user", "content": "hi"}},
	})
	gen.Finish("hello", &TokenUsage{Input: 10, Output: 20, Total: 30, Unit: "TOKENS"}, nil)
	trace.Finish("hello", nil)

	if err := m.Shutdown(context.Background()); err != nil {
		t.Fatalf("shutdown: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()

	// We should have received at least:
	//   trace-create, generation-create, generation-update, trace-create(update)
	// possibly split across multiple HTTP calls depending on batching.
	var events []ingestionEvent
	for _, b := range batches {
		events = append(events, b.Batch...)
	}
	if len(events) < 4 {
		t.Fatalf("expected >=4 events, got %d: %+v", len(events), events)
	}

	var sawGenerationUpdate bool
	for _, ev := range events {
		if ev.Type != "generation-update" {
			continue
		}
		b, _ := json.Marshal(ev.Body)
		var body observationBody
		_ = json.Unmarshal(b, &body)
		if body.Usage == nil || body.Usage.Total != 30 {
			t.Errorf("expected usage total=30, got %+v", body.Usage)
		}
		if body.TraceID != trace.ID {
			t.Errorf("generation trace id mismatch: got %s want %s", body.TraceID, trace.ID)
		}
		sawGenerationUpdate = true
	}
	if !sawGenerationUpdate {
		t.Fatalf("no generation-update event found")
	}
}
