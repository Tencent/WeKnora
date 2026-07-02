package utils

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestApplyCustomHeaders_SkipReserved(t *testing.T) {
	req, _ := http.NewRequest("GET", "https://example.com", nil)
	req.Header.Set("Authorization", "Bearer original")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-goog-api-key", "google-original")

	ApplyCustomHeaders(req, map[string]string{
		"Authorization":  "Bearer injected",
		"Content-Type":   "text/plain",
		"x-goog-api-key": "google-injected",
		"X-Trace-Id":     "trace-123",
		"X-Route":        "edge",
		"":               "empty-key-should-be-skipped",
	})

	if got := req.Header.Get("Authorization"); got != "Bearer original" {
		t.Fatalf("authorization overwritten: %q", got)
	}
	if got := req.Header.Get("Content-Type"); got != "application/json" {
		t.Fatalf("content-type overwritten: %q", got)
	}
	if got := req.Header.Get("x-goog-api-key"); got != "google-original" {
		t.Fatalf("x-goog-api-key overwritten: %q", got)
	}
	if got := req.Header.Get("X-Trace-Id"); got != "trace-123" {
		t.Fatalf("X-Trace-Id not injected: %q", got)
	}
	if got := req.Header.Get("X-Route"); got != "edge" {
		t.Fatalf("X-Route not injected: %q", got)
	}
}

func TestApplyCustomHeaders_NilSafe(t *testing.T) {
	ApplyCustomHeaders(nil, map[string]string{"x": "y"})
	req, _ := http.NewRequest("GET", "https://example.com", nil)
	ApplyCustomHeaders(req, nil)
	if len(req.Header) != 0 {
		t.Fatalf("unexpected headers added: %+v", req.Header)
	}
}

func TestWrapHTTPClientWithHeaders(t *testing.T) {
	gotTrace := ""
	gotAuth := ""
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotTrace = r.Header.Get("X-Trace-Id")
		gotAuth = r.Header.Get("Authorization")
		io.Copy(io.Discard, r.Body)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	client := WrapHTTPClientWithHeaders(nil, map[string]string{
		"X-Trace-Id":    "rt-1",
		"Authorization": "Bearer should-not-override",
	})

	req, _ := http.NewRequest("POST", srv.URL, strings.NewReader("{}"))
	req.Header.Set("Authorization", "Bearer kept")

	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	resp.Body.Close()

	if gotTrace != "rt-1" {
		t.Fatalf("expected custom header injected, got %q", gotTrace)
	}
	if gotAuth != "Bearer kept" {
		t.Fatalf("reserved header must not be overridden, got %q", gotAuth)
	}
}

func TestWrapHTTPClientWithHeaders_EmptyReturnsOriginal(t *testing.T) {
	orig := &http.Client{}
	wrapped := WrapHTTPClientWithHeaders(orig, nil)
	if wrapped != orig {
		t.Fatalf("expected original client returned when headers empty")
	}
}

func TestMergeHeaders(t *testing.T) {
	// nil extra returns base unchanged
	got := MergeHeaders(map[string]string{"X-WeKnora-User-Id": "1"}, nil)
	if len(got) != 1 || got["X-WeKnora-User-Id"] != "1" {
		t.Fatalf("expected base unchanged with nil extra, got %+v", got)
	}

	// empty extra returns base unchanged
	got = MergeHeaders(map[string]string{"X-WeKnora-User-Id": "1"}, map[string]string{})
	if len(got) != 1 || got["X-WeKnora-User-Id"] != "1" {
		t.Fatalf("expected base unchanged with empty extra, got %+v", got)
	}

	// reserved headers in extra are skipped
	got = MergeHeaders(map[string]string{}, map[string]string{
		"Authorization":       "Bearer injected",
		"Content-Type":        "text/plain",
		"X-WeKnora-Tenant-Id": "tenant-456",
	})
	if got["Authorization"] != "" {
		t.Fatalf("reserved Authorization should be skipped, got %q", got["Authorization"])
	}
	if got["Content-Type"] != "" {
		t.Fatalf("reserved Content-Type should be skipped, got %q", got["Content-Type"])
	}
	if got["X-WeKnora-Tenant-Id"] != "tenant-456" {
		t.Fatalf("non-reserved X-WeKnora-Tenant-Id should be present, got %q", got["X-WeKnora-Tenant-Id"])
	}

	// base takes precedence over extra for same key
	got = MergeHeaders(
		map[string]string{"X-WeKnora-Trace-Id": "base-value"},
		map[string]string{"X-WeKnora-Trace-Id": "extra-value"},
	)
	if got["X-WeKnora-Trace-Id"] != "base-value" {
		t.Fatalf("base should take precedence, got %q", got["X-WeKnora-Trace-Id"])
	}

	// both maps contribute
	got = MergeHeaders(
		map[string]string{"X-WeKnora-User-Id": "10001"},
		map[string]string{"X-WeKnora-Tenant-Id": "20001"},
	)
	if got["X-WeKnora-User-Id"] != "10001" {
		t.Fatalf("base key missing, got %+v", got)
	}
	if got["X-WeKnora-Tenant-Id"] != "20001" {
		t.Fatalf("extra key missing, got %+v", got)
	}
}
