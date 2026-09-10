// Package invoketest provides an in-process fake chat adapter for tests of
// invoke entry callers (P1c caller sweep: agent engine, compaction,
// chat_pipeline, memory, sessions). No real HTTP leaves the process and no
// vendor adapter is involved: the fake adapter's BuildChatRequest serializes
// the ChatOptions verbatim as the request body, and the httptest server
// replays queued results.
//
// Wire protocol of the fake:
//   - request body: JSON-encoded *invoke.ChatOptions
//   - non-stream response: one JSON-encoded invoke.ChatResponse
//   - stream response: one JSON-encoded invoke.StreamEvent per NDJSON line
package invoketest

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/Tencent/WeKnora/internal/models/invoke"
	"github.com/Tencent/WeKnora/internal/models/provider"
	secutils "github.com/Tencent/WeKnora/internal/utils"
	"github.com/stretchr/testify/require"
)

// Recorded is one captured call: the decoded ChatOptions the entry dispatched.
type Recorded struct {
	Opts invoke.ChatOptions
}

// Fake is the per-test invocation seam.
type Fake struct {
	mu     sync.Mutex
	srv    *httptest.Server
	cfg    *invoke.ModelConfig
	stream []queued
	calls  []Recorded
}

type queued struct {
	stream    bool
	events    []invoke.StreamEvent
	resp      *invoke.ChatResponse
	errStatus int
	errBody   string
}

// New starts the fake server, registers the fake adapter in a fresh invoke
// registry (restored at cleanup), and opens the SSRF gate for the loopback.
func New(t *testing.T) *Fake {
	t.Helper()
	t.Setenv("SSRF_WHITELIST", "127.0.0.1,::1,localhost")
	secutils.ResetSSRFWhitelistForTest()
	secutils.ResetSSRFOutboundValidationCacheForTest()
	t.Cleanup(func() {
		secutils.ResetSSRFWhitelistForTest()
		secutils.ResetSSRFOutboundValidationCacheForTest()
	})

	f := &Fake{}
	f.srv = httptest.NewServer(http.HandlerFunc(f.handle))
	t.Cleanup(f.srv.Close)

	old := invoke.Default
	r := &invoke.Registry{}
	require.NoError(t, r.Register(fakeAdapter{fake: f}))
	invoke.Default = r
	t.Cleanup(func() { invoke.Default = old })

	f.cfg = &invoke.ModelConfig{
		Provider:  "fake",
		ModelID:   "m-1",
		ModelName: "fake-model",
		BaseURL:   f.srv.URL,
	}
	return f
}

// Config returns the ModelConfig to hand the caller under test.
func (f *Fake) Config() *invoke.ModelConfig { return f.cfg }

// BaseURL returns the fake server URL.
func (f *Fake) BaseURL() string { return f.srv.URL }

// EnqueueStream queues one streaming call replayed as NDJSON StreamEvents.
func (f *Fake) EnqueueStream(events ...invoke.StreamEvent) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.stream = append(f.stream, queued{stream: true, events: events})
}

// EnqueueResponse queues one non-stream ChatResponse.
func (f *Fake) EnqueueResponse(resp invoke.ChatResponse) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.stream = append(f.stream, queued{resp: &resp})
}

// EnqueueError queues one non-stream call that fails with the given HTTP
// status and body (the executor classifies it into a *invoke.ProviderError).
func (f *Fake) EnqueueError(status int, body string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.stream = append(f.stream, queued{errStatus: status, errBody: body})
}

// Calls returns the recorded calls (decoded ChatOptions, one per call).
func (f *Fake) Calls() []Recorded {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]Recorded, len(f.calls))
	copy(out, f.calls)
	return out
}

func (f *Fake) pop() queued {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.stream) == 0 {
		return queued{}
	}
	q := f.stream[0]
	f.stream = f.stream[1:]
	return q
}

func (f *Fake) record(opts *invoke.ChatOptions) {
	f.mu.Lock()
	defer f.mu.Unlock()
	rec := Recorded{}
	if opts != nil {
		rec.Opts = *opts
	}
	f.calls = append(f.calls, rec)
}

func (f *Fake) handle(w http.ResponseWriter, r *http.Request) {
	var opts invoke.ChatOptions
	_ = json.NewDecoder(r.Body).Decode(&opts)
	f.record(&opts)
	q := f.pop()

	if q.errStatus != 0 {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(q.errStatus)
		_, _ = w.Write([]byte(q.errBody))
		return
	}
	if !q.stream {
		body, _ := json.Marshal(q.resp)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(body)
		return
	}
	w.Header().Set("Content-Type", "application/x-ndjson")
	flusher := w.(http.Flusher)
	for _, ev := range q.events {
		line, _ := json.Marshal(ev)
		_, _ = w.Write(append(line, '\n'))
		flusher.Flush()
	}
}

// fakeAdapter is the ChatAdapter facet registered for the "fake" provider.
type fakeAdapter struct{ fake *Fake }

func (a fakeAdapter) Provider() string { return "fake" }

func (a fakeAdapter) Capabilities() provider.Capabilities {
	return provider.Capabilities{Chat: &provider.ChatCaps{}}
}

func (a fakeAdapter) BuildChatRequest(
	ep invoke.Endpoint, _ string, opts *invoke.ChatOptions,
) (*invoke.Request, error) {
	body, err := json.Marshal(opts)
	if err != nil {
		return nil, err
	}
	return &invoke.Request{
		Method: http.MethodPost,
		URL:    ep.BaseURL + "/v1/chat/completions",
		Header: http.Header{"Content-Type": []string{"application/json"}},
		Body:   body,
		Stream: opts != nil && opts.Stream,
	}, nil
}

func (a fakeAdapter) ParseChatResponse(_ int, _ http.Header, body []byte) (*invoke.ChatResponse, error) {
	var resp invoke.ChatResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

func (a fakeAdapter) TranslateStreamEvent(
	_ *invoke.StreamBridgeState, chunk invoke.StreamChunk,
) (*invoke.StreamEvent, error) {
	var ev invoke.StreamEvent
	if err := json.Unmarshal(chunk.Data, &ev); err != nil {
		return &invoke.StreamEvent{
			Kind:  invoke.StreamKindError,
			Delta: &invoke.ContentDelta{Text: err.Error()},
			Done:  &invoke.FinishInfo{},
		}, nil
	}
	if ev.Kind == "" {
		return nil, nil
	}
	return &ev, nil
}

var (
	_ invoke.ChatAdapter = fakeAdapter{}
	_                    = context.Background
)
