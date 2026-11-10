package cmdutil

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

// stubTransport returns a scripted sequence of responses (and optional
// errors). The N-th request gets the N-th entry; running off the end is a
// test failure.
type stubTransport struct {
	t      *testing.T
	resps  []*http.Response
	errs   []error
	bodies []string // captured body of each request, for assertions
	authz  []string // captured Authorization header of each request
	paths  []string // captured req URL paths
	idx    atomic.Int32
}

func (s *stubTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	i := s.idx.Add(1) - 1
	if int(i) >= len(s.resps) {
		s.t.Fatalf("stubTransport: more requests than scripted (req #%d, scripted %d)", i+1, len(s.resps))
	}
	// Capture body. http.NewRequest with bytes.Buffer body sets GetBody, so
	// req.Body is readable each call.
	var bodyStr string
	if req.Body != nil {
		b, _ := io.ReadAll(req.Body)
		bodyStr = string(b)
		_ = req.Body.Close()
	}
	s.bodies = append(s.bodies, bodyStr)
	s.authz = append(s.authz, req.Header.Get("Authorization"))
	s.paths = append(s.paths, req.URL.Path)

	var err error
	if int(i) < len(s.errs) {
		err = s.errs[i]
	}
	return s.resps[i], err
}

func resp(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Body:       io.NopCloser(strings.NewReader(body)),
		Header:     make(http.Header),
	}
}

// newReq builds a POST with a replayable body so retries can be verified.
func newReq(t *testing.T, method, url, body string) *http.Request {
	t.Helper()
	var rdr io.Reader
	if body != "" {
		rdr = bytes.NewBufferString(body)
	}
	req, err := http.NewRequest(method, url, rdr)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Authorization", "Bearer old-token")
	return req
}

func TestAuthRetry_PassThrough_200(t *testing.T) {
	base := &stubTransport{t: t, resps: []*http.Response{resp(200, "ok")}}
	rt := NewAuthRetryTransport(base, "old-token", func(context.Context) (string, error) {
		t.Fatal("refresh must NOT be called on 200 response")
		return "", nil
	})

	r, err := rt.RoundTrip(newReq(t, "GET", "http://example.test/api/v1/kbs", ""))
	if err != nil {
		t.Fatalf("roundtrip: %v", err)
	}
	if r.StatusCode != 200 {
		t.Errorf("status=%d, want 200", r.StatusCode)
	}
}

func TestAuthRetry_PassThrough_NonAuthError(t *testing.T) {
	base := &stubTransport{t: t, resps: []*http.Response{resp(500, "boom")}}
	rt := NewAuthRetryTransport(base, "old-token", func(context.Context) (string, error) {
		t.Fatal("refresh must NOT be called on non-401 errors")
		return "", nil
	})
	r, _ := rt.RoundTrip(newReq(t, "GET", "http://example.test/api/v1/kbs", ""))
	if r.StatusCode != 500 {
		t.Errorf("status=%d, want 500", r.StatusCode)
	}
}

func TestAuthRetry_401_RefreshAndReplay(t *testing.T) {
	base := &stubTransport{t: t,
		resps: []*http.Response{resp(401, "expired"), resp(200, "ok-after-retry")},
	}
	refreshCalls := atomic.Int32{}
	rt := NewAuthRetryTransport(base, "old-token", func(context.Context) (string, error) {
		refreshCalls.Add(1)
		return "new-token", nil
	})

	r, err := rt.RoundTrip(newReq(t, "POST", "http://example.test/api/v1/sessions", `{"q":"hi"}`))
	if err != nil {
		t.Fatalf("roundtrip: %v", err)
	}
	if r.StatusCode != 200 {
		t.Errorf("status=%d, want 200", r.StatusCode)
	}
	if refreshCalls.Load() != 1 {
		t.Errorf("refresh called %d times, want 1", refreshCalls.Load())
	}
	if len(base.authz) != 2 {
		t.Fatalf("expected 2 requests through base, got %d", len(base.authz))
	}
	if base.authz[0] != "Bearer old-token" {
		t.Errorf("first req Authorization=%q, want old-token", base.authz[0])
	}
	if base.authz[1] != "Bearer new-token" {
		t.Errorf("replay Authorization=%q, want new-token", base.authz[1])
	}
	if base.bodies[1] != `{"q":"hi"}` {
		t.Errorf("replay body=%q, want original", base.bodies[1])
	}
}

func TestAuthRetry_401_RefreshFails(t *testing.T) {
	base := &stubTransport{t: t, resps: []*http.Response{resp(401, "expired")}}
	rt := NewAuthRetryTransport(base, "old-token", func(context.Context) (string, error) {
		return "", errors.New("refresh rejected")
	})
	_, err := rt.RoundTrip(newReq(t, "GET", "http://example.test/api/v1/kbs", ""))
	if err == nil {
		t.Fatal("expected refresh error")
	}
	if !strings.Contains(err.Error(), "refresh rejected") {
		t.Errorf("error should surface refresh failure, got %q", err.Error())
	}
}

func TestAuthRetry_SkipAuthEndpoints(t *testing.T) {
	cases := []string{
		"/api/v1/auth/login",
		"/api/v1/auth/refresh",
	}
	for _, path := range cases {
		t.Run(path, func(t *testing.T) {
			base := &stubTransport{t: t, resps: []*http.Response{resp(401, "")}}
			rt := NewAuthRetryTransport(base, "old-token", func(context.Context) (string, error) {
				t.Fatalf("refresh must NOT be triggered by a 401 on %s", path)
				return "", nil
			})
			r, _ := rt.RoundTrip(newReq(t, "POST", "http://example.test"+path, ""))
			if r.StatusCode != 401 {
				t.Errorf("status=%d, want 401 (passthrough)", r.StatusCode)
			}
		})
	}
}

func TestAuthRetry_NoTokenConfigured_DoesNotInjectAuthz(t *testing.T) {
	// API-key contexts construct the transport with an empty initial token;
	// authretry should pass through 401s untouched (no refresh callback exists
	// for api-key - they're rejected at the auth-refresh layer).
	base := &stubTransport{t: t, resps: []*http.Response{resp(401, "")}}
	refreshed := false
	rt := NewAuthRetryTransport(base, "", func(context.Context) (string, error) {
		refreshed = true
		return "", nil
	})
	req, _ := http.NewRequest("GET", "http://example.test/api/v1/kbs", nil)
	r, _ := rt.RoundTrip(req)
	if r.StatusCode != 401 {
		t.Errorf("status=%d, want 401", r.StatusCode)
	}
	if refreshed {
		t.Errorf("must not call refresh when initial token is empty (api-key context)")
	}
}

func TestAuthRetry_ConcurrentRefresh_SingleFlight(t *testing.T) {
	// 4 parallel 401s should trigger exactly 1 refresh; all 4 then retry with
	// the new token. Use a server that 401s first request from each unique
	// path then 200s on retry - except we use one shared transport with
	// scripted responses, ordering matters. To keep determinism, build a
	// transport that always returns 401 on first call per request, 200 on
	// second, and gate the refresh callback.
	var (
		refreshCalls atomic.Int32
		wg           sync.WaitGroup
	)

	// We'll use a single mux'd base that counts 401-then-200 per goroutine
	// via an httptest server, which is the cleanest model for concurrency.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") == "Bearer new-token" {
			_, _ = w.Write([]byte("ok"))
			return
		}
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer server.Close()

	rt := NewAuthRetryTransport(http.DefaultTransport, "old-token", func(context.Context) (string, error) {
		refreshCalls.Add(1)
		return "new-token", nil
	})

	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			req, _ := http.NewRequest("GET", server.URL+"/api/v1/x", nil)
			req.Header.Set("Authorization", "Bearer old-token")
			r, err := rt.RoundTrip(req)
			if err != nil {
				t.Errorf("roundtrip: %v", err)
				return
			}
			if r.StatusCode != 200 {
				t.Errorf("status=%d, want 200", r.StatusCode)
			}
			_ = r.Body.Close()
		}()
	}
	wg.Wait()

	if got := refreshCalls.Load(); got != 1 {
		t.Errorf("refresh called %d times, want 1 (singleflight)", got)
	}
}

func TestAuthRetry_NonReplayableBody_NoRetry(t *testing.T) {
	// io.Reader that isn't *bytes.Buffer / Reader / strings.Reader → http
	// does NOT set req.GetBody. Confirm we don't lose the response and
	// don't attempt a retry.
	base := &stubTransport{t: t, resps: []*http.Response{resp(401, "")}}
	rt := NewAuthRetryTransport(base, "old-token", func(context.Context) (string, error) {
		t.Fatal("must not retry without GetBody")
		return "", nil
	})

	// Use a custom Reader the stdlib won't recognize.
	type unknownReader struct{ io.Reader }
	body := &unknownReader{Reader: strings.NewReader("once")}
	req, _ := http.NewRequest("POST", "http://example.test/api/v1/kbs", body)
	req.Header.Set("Authorization", "Bearer old-token")
	// Sanity: GetBody should be nil for this body type.
	if req.GetBody != nil {
		t.Fatalf("test premise failed: GetBody is set for an unknownReader body")
	}
	r, err := rt.RoundTrip(req)
	if err != nil {
		t.Fatalf("roundtrip: %v", err)
	}
	if r.StatusCode != 401 {
		t.Errorf("status=%d, want 401 (passthrough)", r.StatusCode)
	}
}
