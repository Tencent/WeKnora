package core

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// retryTestServer builds a server that always answers the auth-token call and
// routes the given target path to h, so tests can drive DoRequest's retry loop.
func retryTestServer(target string, h http.HandlerFunc) (*httptest.Server, *Config) {
	mux := http.NewServeMux()
	mux.HandleFunc("/open-apis/auth/v3/tenant_access_token/internal", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, TokenResponse{
			ApiResponse:       ApiResponse{Code: 0},
			TenantAccessToken: "fake-token",
			Expire:            7200,
		})
	})
	mux.HandleFunc(target, h)
	ts := httptest.NewServer(mux)
	return ts, &Config{AppID: "a", AppSecret: "b", BaseURL: ts.URL}
}

func TestDoRequest_RetriesOn429ThenSucceeds(t *testing.T) {
	var attempts int
	ts, cfg := retryTestServer("/target", func(w http.ResponseWriter, r *http.Request) {
		attempts++
		if attempts == 1 {
			// "0" is coerced to a short delay inside the client so the test stays fast.
			w.Header().Set("Retry-After", "0")
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = io.WriteString(w, `{"code":99991400,"msg":"rate limited"}`)
			return
		}
		writeJSON(w, ApiResponse{Code: 0})
	})
	defer ts.Close()

	c := NewClient(cfg)
	var resp ApiResponse
	if err := c.DoRequest(context.Background(), http.MethodGet, "/target", nil, &resp); err != nil {
		t.Fatalf("expected success after retry, got %v", err)
	}
	if attempts < 2 {
		t.Errorf("attempts = %d, want >= 2 (should retry after 429)", attempts)
	}
}

func TestDoRequest_429ExhaustsRetries(t *testing.T) {
	var attempts int
	ts, cfg := retryTestServer("/target", func(w http.ResponseWriter, r *http.Request) {
		attempts++
		w.Header().Set("Retry-After", "0")
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = io.WriteString(w, `{"code":99991400,"msg":"rate limited"}`)
	})
	defer ts.Close()

	c := NewClient(cfg)
	err := c.DoRequest(context.Background(), http.MethodGet, "/target", nil, nil)
	if err == nil {
		t.Fatal("expected error when 429s exceed the retry budget")
	}
	if attempts != 4 { // initial + 3 retries
		t.Errorf("attempts = %d, want 4 (1 + 3 retries)", attempts)
	}
}

func TestDoRequest_5xxRetriesOnce(t *testing.T) {
	var attempts int
	ts, cfg := retryTestServer("/target", func(w http.ResponseWriter, r *http.Request) {
		attempts++
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = io.WriteString(w, `{"code":1,"msg":"internal error"}`)
	})
	defer ts.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	c := NewClient(cfg)
	if err := c.DoRequest(ctx, http.MethodGet, "/target", nil, nil); err == nil {
		t.Fatal("expected error after 5xx exhaustion")
	}
	if attempts != 2 { // initial + 1 retry
		t.Errorf("attempts = %d, want 2 (5xx retries exactly once)", attempts)
	}
}

func TestDoRequest_4xxNotRetried(t *testing.T) {
	var attempts int
	ts, cfg := retryTestServer("/target", func(w http.ResponseWriter, r *http.Request) {
		attempts++
		w.WriteHeader(http.StatusBadRequest)
		_, _ = io.WriteString(w, `{"code":1,"msg":"bad request"}`)
	})
	defer ts.Close()

	c := NewClient(cfg)
	if err := c.DoRequest(context.Background(), http.MethodGet, "/target", nil, nil); err == nil {
		t.Fatal("expected error on 400")
	}
	if attempts != 1 {
		t.Errorf("attempts = %d, want 1 (non-429/5xx 4xx must not retry)", attempts)
	}
}

func TestDownloadRawBytes_RetriesOn429ThenSucceeds(t *testing.T) {
	var attempts int
	ts, cfg := retryTestServer("/dl", func(w http.ResponseWriter, r *http.Request) {
		attempts++
		if attempts == 1 {
			w.Header().Set("Retry-After", "0")
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		w.Header().Set("Content-Type", "application/octet-stream")
		_, _ = w.Write([]byte("payload-bytes"))
	})
	defer ts.Close()

	c := NewClient(cfg)
	data, err := c.downloadRawBytes(context.Background(), "/dl")
	if err != nil {
		t.Fatalf("expected success after retry, got %v", err)
	}
	if string(data) != "payload-bytes" {
		t.Errorf("data = %q, want %q", string(data), "payload-bytes")
	}
	if attempts < 2 {
		t.Errorf("attempts = %d, want >= 2 (should retry download after 429)", attempts)
	}
}

func TestDownloadRawBytes_4xxNotRetried(t *testing.T) {
	var attempts int
	ts, cfg := retryTestServer("/dl", func(w http.ResponseWriter, r *http.Request) {
		attempts++
		w.WriteHeader(http.StatusForbidden)
	})
	defer ts.Close()

	c := NewClient(cfg)
	if _, err := c.downloadRawBytes(context.Background(), "/dl"); err == nil {
		t.Fatal("expected error on 403")
	}
	if attempts != 1 {
		t.Errorf("attempts = %d, want 1 (403 must not retry)", attempts)
	}
}

func TestParseRetryAfter(t *testing.T) {
	fallback := 5 * time.Second
	tests := []struct {
		header string
		want   time.Duration
	}{
		{"", fallback},
		{"0", 100 * time.Millisecond},
		{"-1", 100 * time.Millisecond}, // negative coerced to a short delay
		{"3", 3 * time.Second},
		{"abc", fallback}, // unparseable
	}
	for _, tt := range tests {
		if got := parseRetryAfter(tt.header, fallback); got != tt.want {
			t.Errorf("parseRetryAfter(%q) = %v, want %v", tt.header, got, tt.want)
		}
	}
}

func TestDoRequest_RejectsOversizedResponse(t *testing.T) {
	var hits atomic.Int32
	ts, cfg := retryTestServer("/open-apis/docx/v1/documents", func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		_, _ = w.Write(bytes.Repeat([]byte("a"), 1<<20+1))
	})
	defer ts.Close()

	c := NewClient(cfg)
	c.jsonLimit = 1 << 20
	err := c.DoRequest(context.Background(), http.MethodGet, "/open-apis/docx/v1/documents", nil, &map[string]any{})
	if err == nil {
		t.Fatal("DoRequest accepted a response larger than the cap, want error")
	}
	if !strings.Contains(err.Error(), "exceeds maximum size") {
		t.Fatalf("error = %v, want an explicit over-limit error", err)
	}
	// The same document is just as large on every attempt; retrying only burns
	// the rate-limit budget and re-reads the full cap each time.
	if got := hits.Load(); got != 1 {
		t.Fatalf("server hit %d times, want 1 (oversized responses must not be retried)", got)
	}
}

// The token response is decoded under tokenMu on every refresh and Ping, so it
// must honour the same cap as every other API response.
func TestGetTenantAccessToken_RejectsOversizedResponse(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/open-apis/auth/v3/tenant_access_token/internal", func(w http.ResponseWriter, _ *http.Request) {
		// Valid JSON, so only the size cap can reject it.
		_, _ = io.WriteString(w, `{"code":0,"tenant_access_token":"fake-token","expire":7200,"pad":"`)
		_, _ = w.Write(bytes.Repeat([]byte("a"), 1<<20))
		_, _ = io.WriteString(w, `"}`)
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	c := NewClient(&Config{AppID: "a", AppSecret: "b", BaseURL: ts.URL})
	c.jsonLimit = 1 << 20
	_, err := c.GetTenantAccessToken(context.Background())
	if err == nil {
		t.Fatal("GetTenantAccessToken accepted a response larger than the cap, want error")
	}
	if !strings.Contains(err.Error(), "exceeds maximum size") {
		t.Fatalf("error = %v, want an explicit over-limit error", err)
	}
}

// countingBody records how much of a response body the client consumed.
type countingBody struct {
	io.ReadCloser
	n *atomic.Int64
}

func (b countingBody) Read(p []byte) (int, error) {
	n, err := b.ReadCloser.Read(p)
	b.n.Add(int64(n))
	return n, err
}

type countingTransport struct {
	base http.RoundTripper
	n    *atomic.Int64
}

func (t countingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	resp, err := t.base.RoundTrip(req)
	if err == nil && strings.Contains(req.URL.Path, "/download") {
		resp.Body = countingBody{ReadCloser: resp.Body, n: t.n}
	}
	return resp, err
}

// Download error bodies are only a diagnostic preview: they must be read with a
// small bound and never copied whole into the returned error.
func TestDownloadRawBytes_BoundsErrorBodies(t *testing.T) {
	const bodySize = 1 << 20
	for _, status := range []int{http.StatusTooManyRequests, http.StatusInternalServerError, http.StatusForbidden} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			handler := func(w http.ResponseWriter, _ *http.Request) {
				if status == http.StatusTooManyRequests {
					w.Header().Set("Retry-After", "0")
				}
				w.WriteHeader(status)
				_, _ = w.Write(bytes.Repeat([]byte("a"), bodySize))
			}
			ts, cfg := retryTestServer("/open-apis/drive/v1/files/f/download", handler)
			defer ts.Close()

			c := NewClient(cfg)
			var read atomic.Int64
			hc := *c.httpClient
			base := hc.Transport
			if base == nil {
				base = http.DefaultTransport
			}
			hc.Transport = countingTransport{base: base, n: &read}
			c.httpClient = &hc

			_, err := c.DownloadDriveFile(context.Background(), "f")
			if err == nil {
				t.Fatalf("status %d: want error", status)
			}
			if len(err.Error()) > 1024 {
				t.Fatalf("error carries %d bytes of response body, want a short preview", len(err.Error()))
			}
			if got := read.Load(); got >= bodySize {
				t.Fatalf("read %d bytes of error bodies, want a bounded preview per response", got)
			}
		})
	}
}
