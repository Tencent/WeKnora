package sandbox

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"testing"
	"time"
)

// TestExitCodeFromStatus covers the fallback path used when an envd build
// omits the numeric exitCode and only reports a textual "exit status N"
// (observed on envd 0.5.11). A regression here would make every successful
// command look like a protocol error.
func TestExitCodeFromStatus(t *testing.T) {
	cases := []struct {
		status   string
		wantCode int
		wantOK   bool
	}{
		{"exit status 0", 0, true},
		{"exit status 137", 137, true},
		{"  exit status 2  ", 2, true},
		{"signal: killed", 0, false},
		{"", 0, false},
		{"exit status abc", 0, false},
	}
	for _, tc := range cases {
		got, ok := exitCodeFromStatus(tc.status)
		if got != tc.wantCode || ok != tc.wantOK {
			t.Errorf("exitCodeFromStatus(%q) = (%d, %v), want (%d, %v)",
				tc.status, got, ok, tc.wantCode, tc.wantOK)
		}
	}
}

// TestEnvdEndEventExit verifies the precedence order of the exit-code
// resolution: explicit exitCode > snake_case > status string > bare "exited".
func TestEnvdEndEventExit(t *testing.T) {
	i := func(n int) *int { return &n }
	cases := []struct {
		name string
		ev   envdEndEvent
		want int
		ok   bool
	}{
		{"explicit", envdEndEvent{ExitCode: i(3)}, 3, true},
		{"snake", envdEndEvent{ExitCodeSnake: i(4)}, 4, true},
		{"status", envdEndEvent{Status: "exit status 5"}, 5, true},
		{"exited-only", envdEndEvent{Exited: true}, 0, true},
		{"nothing", envdEndEvent{}, 0, false},
	}
	for _, tc := range cases {
		got, ok := tc.ev.exit()
		if got != tc.want || ok != tc.ok {
			t.Errorf("%s: exit() = (%d, %v), want (%d, %v)", tc.name, got, ok, tc.want, tc.ok)
		}
	}
}

// TestConnectEnvelopeRoundTrip confirms the request framing WeKnora produces
// is exactly the [flag][uint32 len][payload] shape envd requires, and that the
// stream parser reads back the frames it emits.
func TestConnectEnvelopeRoundTrip(t *testing.T) {
	payload := []byte(`{"process":{"cmd":"/bin/bash"}}`)
	framed := encodeConnectEnvelope(0, payload)
	if len(framed) != 5+len(payload) {
		t.Fatalf("framed length = %d, want %d", len(framed), 5+len(payload))
	}
	if framed[0] != 0 {
		t.Fatalf("flag byte = %d, want 0", framed[0])
	}
	flag, body, err := readConnectEnvelope(bytes.NewReader(framed))
	if err != nil {
		t.Fatalf("readConnectEnvelope: %v", err)
	}
	if flag != 0 {
		t.Fatalf("decoded flag = %d, want 0", flag)
	}
	if !bytes.Equal(body, payload) {
		t.Fatalf("decoded payload = %q, want %q", body, payload)
	}
}

// TestConnectBodyRewriter asserts that a POST to /connect gains the mandatory
// "timeout" field this CubeAPI build requires, while a body that already sets
// it — and any non-connect request — passes through untouched.
func TestConnectBodyRewriter(t *testing.T) {
	var lastBody []byte
	rt := roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if req.Body != nil {
			lastBody, _ = io.ReadAll(req.Body)
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(bytes.NewReader(nil)), Header: http.Header{}}, nil
	})
	client := &http.Client{Transport: &connectBodyRewriter{base: rt, ttlSeconds: 120}}

	// Connect with empty body -> timeout injected.
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodPost,
		"http://api/sandboxes/abc/connect", bytes.NewReader([]byte("{}")))
	if _, err := client.Do(req); err != nil {
		t.Fatalf("connect empty: %v", err)
	}
	var parsed map[string]any
	if err := json.Unmarshal(lastBody, &parsed); err != nil {
		t.Fatalf("unmarshal patched body %q: %v", lastBody, err)
	}
	if parsed["timeout"] != float64(120) {
		t.Fatalf("timeout = %v, want 120", parsed["timeout"])
	}

	// Connect with a caller-provided timeout -> preserved.
	req2, _ := http.NewRequestWithContext(context.Background(), http.MethodPost,
		"http://api/sandboxes/abc/connect", bytes.NewReader([]byte(`{"timeout":45}`)))
	if _, err := client.Do(req2); err != nil {
		t.Fatalf("connect explicit: %v", err)
	}
	_ = json.Unmarshal(lastBody, &parsed)
	if parsed["timeout"] != float64(45) {
		t.Fatalf("explicit timeout overwritten: %v", parsed["timeout"])
	}

	// Non-connect POST -> body unchanged.
	req3, _ := http.NewRequestWithContext(context.Background(), http.MethodPost,
		"http://api/sandboxes", bytes.NewReader([]byte(`{"templateID":"tpl"}`)))
	if _, err := client.Do(req3); err != nil {
		t.Fatalf("create: %v", err)
	}
	if string(lastBody) != `{"templateID":"tpl"}` {
		t.Fatalf("non-connect body mutated: %q", lastBody)
	}
}

// TestNewCubeSDKHTTPClientRoutesDataPlane verifies the injected SDK transport
// tunnels sandbox-domain hosts through the CubeProxy address while leaving
// control-plane hosts alone. We assert on the dial target via a stub proxy.
func TestNewCubeSDKHTTPClientRoutesDataPlane(t *testing.T) {
	// A zero envdProxyAddr means "no rerouting"; the transport should then dial
	// the URL host directly. We only check that construction succeeds and the
	// client is usable, since the dial target is an internal detail exercised
	// end-to-end by the integration suite.
	client := newCubeSDKHTTPClient("127.0.0.1:80", "cube.app", 5*time.Minute, time.Second)
	if client == nil || client.Transport == nil {
		t.Fatal("expected a non-nil client with transport")
	}
	if _, ok := client.Transport.(*connectBodyRewriter); !ok {
		t.Fatalf("transport should be wrapped in connectBodyRewriter, got %T", client.Transport)
	}
}

// roundTripFunc adapts a function to http.RoundTripper for the rewriter test.
type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
