package feishu

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// retryTestServer builds a server that always answers the auth-token call and
// routes the given target path to h, so tests can drive doRequest's retry loop.
func retryTestServer(target string, h http.HandlerFunc) (*httptest.Server, *Config) {
	mux := http.NewServeMux()
	mux.HandleFunc("/open-apis/auth/v3/tenant_access_token/internal", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, tokenResponse{
			apiResponse:       apiResponse{Code: 0},
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
		writeJSON(w, apiResponse{Code: 0})
	})
	defer ts.Close()

	c := NewClient(cfg)
	var resp apiResponse
	if err := c.doRequest(context.Background(), http.MethodGet, "/target", nil, &resp); err != nil {
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
	err := c.doRequest(context.Background(), http.MethodGet, "/target", nil, nil)
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
	if err := c.doRequest(ctx, http.MethodGet, "/target", nil, nil); err == nil {
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
	if err := c.doRequest(context.Background(), http.MethodGet, "/target", nil, nil); err == nil {
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
