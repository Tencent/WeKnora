package embedding

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	secutils "github.com/Tencent/WeKnora/internal/utils"
)

// RETRY-1 (SIGSEGV regression, see openai.go:110 comment): all connection
// attempts fail → doRequestWithRetry must return (nil, err), never (nil, nil).
func TestDoRequestWithRetry_AllConnFail_ReturnsError(t *testing.T) {
	// A server that is already closed: connection refused on every attempt.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	url := srv.URL
	srv.Close()

	e := &OpenAIEmbedder{
		baseURL:    url,
		apiKey:     "k",
		maxRetries: 2,
		httpClient: &http.Client{Timeout: 2 * time.Second},
	}
	resp, err := e.doRequestWithRetry(context.Background(), []byte(`{}`))
	if err == nil {
		if resp != nil {
			resp.Body.Close()
		}
		t.Fatal("all-fail retry must return non-nil error (SIGSEGV regression: was (nil,nil))")
	}
	if resp != nil {
		t.Fatal("resp must be nil alongside error")
	}
}

// RETRY-2: end-to-end BatchEmbed against dead upstream — error, never panic.
func TestBatchEmbed_ConnError_ReturnsErrNoPanic(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	url := srv.URL
	srv.Close()

	e := &OpenAIEmbedder{
		baseURL:    url,
		apiKey:     "k",
		modelName:  "m",
		maxRetries: 1,
		httpClient: &http.Client{Timeout: 2 * time.Second},
	}
	out, err := e.BatchEmbed(context.Background(), []string{"hello"})
	if err == nil {
		t.Fatalf("expected error, got %v", out)
	}
	if out != nil {
		t.Fatalf("expected nil embeddings alongside error, got %v", out)
	}
	// reaching here without panic is the contract (historical SIGSEGV)
}

// RETRY-3: fails twice then succeeds → success with exactly 3 attempts.
func TestDoRequestWithRetry_FailsThenSucceeds(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&calls, 1) <= 2 {
			// hard connection-level failure: close without response
			hj, ok := w.(http.Hijacker)
			if ok {
				conn, _, _ := hj.Hijack()
				conn.Close()
				return
			}
			http.Error(w, "boom", http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{}`)
	}))
	defer srv.Close()

	e := &OpenAIEmbedder{
		baseURL:    srv.URL,
		apiKey:     "k",
		maxRetries: 3,
		httpClient: srv.Client(),
	}
	resp, err := e.doRequestWithRetry(context.Background(), []byte(`{}`))
	if err != nil {
		t.Fatalf("expected success on third attempt: %v", err)
	}
	resp.Body.Close()
	if got := atomic.LoadInt32(&calls); got != 3 {
		t.Fatalf("attempts=%d, want 3", got)
	}
}

// RETRY-4: 429 + Retry-After: 1 → honored (second request >=1s later).
func TestDoRequestWithRetry_Honors429RetryAfter(t *testing.T) {
	var times []time.Time
	var mu = make(chan struct{}, 1)
	mu <- struct{}{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-mu
		times = append(times, time.Now())
		mu <- struct{}{}
		if len(times) == 1 {
			w.Header().Set("Retry-After", "1")
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{}`)
	}))
	defer srv.Close()

	e := &OpenAIEmbedder{
		baseURL:    srv.URL,
		apiKey:     "k",
		maxRetries: 3,
		httpClient: srv.Client(),
	}
	start := time.Now()
	resp, err := e.doRequestWithRetry(context.Background(), []byte(`{}`))
	if err != nil {
		t.Fatalf("expected success after 429 retry: %v", err)
	}
	resp.Body.Close()
	if elapsed := time.Since(start); elapsed < 900*time.Millisecond {
		t.Fatalf("Retry-After: 1 not honored, elapsed %v", elapsed)
	}
}

// RETRY-5: context cancelled during backoff stops retries.
func TestDoRequestWithRetry_ContextCancelStopsRetries(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		hj, ok := w.(http.Hijacker)
		if ok {
			conn, _, _ := hj.Hijack()
			conn.Close()
			return
		}
	}))
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(1200 * time.Millisecond) // inside the first backoff window (1s..)
		cancel()
	}()
	e := &OpenAIEmbedder{
		baseURL:    srv.URL,
		apiKey:     "k",
		maxRetries: 5,
		httpClient: srv.Client(),
	}
	_, err := e.doRequestWithRetry(ctx, []byte(`{}`))
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
	if got := atomic.LoadInt32(&calls); got > 2 {
		t.Fatalf("retries continued past cancellation, calls=%d", got)
	}
}

// RETRY-6: client timeout configured at construction.
func TestNewEmbeddingHTTPClient_TimeoutConfigured(t *testing.T) {
	c := newEmbeddingHTTPClient(60 * time.Second)
	if c.Timeout != 60*time.Second {
		t.Fatalf("client timeout=%v, want 60s", c.Timeout)
	}
}

// RETRY-7 (SSRF): validateEmbeddingBaseURL rejects private/loopback hosts.
// NOTE: in this dev environment a TUN fake-ip proxy maps ALL public hostnames
// into 198.18.0.0/15, so even api.siliconflow.cn is rejected at resolve time.
// That rejection is the SSRF guard working as designed for a restricted range;
// the test asserts host-class behavior without depending on live DNS.
// resetSSRFWhitelistForDeterministicTest pins the SSRF whitelist to a
// deterministic empty baseline for the duration of one test, then restores it.
// Needed because the whitelist is cached process-wide behind a sync.Once and
// sibling tests in this package legitimately install their own entries
// (e.g. gemini tests whitelist 127.0.0.1), which would otherwise leak into
// these assertions depending on test order. Same convention as
// whitelistAzureTestEndpoint above and internal/models/asr tests.
func resetSSRFWhitelistForDeterministicTest(t *testing.T) {
	t.Helper()
	t.Setenv("SSRF_WHITELIST", "")
	secutils.ResetSSRFWhitelistForTest()
	t.Cleanup(secutils.ResetSSRFWhitelistForTest)
}

func TestValidateEmbeddingBaseURL_RejectsPrivateHosts(t *testing.T) {
	resetSSRFWhitelistForDeterministicTest(t)
	for _, bad := range []string{
		"http://127.0.0.1:3130",
		"http://10.0.0.5/v1",
		"http://192.168.1.4",
		"http://169.254.169.254/latest/meta-data",
		"http://[::1]:8080",
		"http://localhost:9090",
	} {
		if err := validateEmbeddingBaseURL(bad); err == nil {
			t.Fatalf("validateEmbeddingBaseURL(%q) must reject private/loopback host", bad)
		}
	}
	// NOTE: direct-IP access is also rejected by policy ("use domain name or
	// add to SSRF_WHITELIST") — pinned here as documented behavior; environments
	// relying on IP endpoints must whitelist them explicitly.
	if err := validateEmbeddingBaseURL("https://1.1.1.1/v1"); err == nil {
		t.Fatal("direct IP access must be rejected without whitelist")
	}
	// Empty base URL is allowed (callers apply provider defaults).
	if err := validateEmbeddingBaseURL(""); err != nil {
		t.Fatalf("empty base URL must pass: %v", err)
	}
}

var _ net.Error = (*net.DNSError)(nil) // keep net import if refactor drops uses
