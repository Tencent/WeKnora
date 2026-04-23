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

	ApplyCustomHeaders(req, map[string]string{
		"Authorization": "Bearer injected",
		"Content-Type":  "text/plain",
		"X-Trace-Id":    "trace-123",
		"X-Route":       "edge",
		"":              "empty-key-should-be-skipped",
	})

	if got := req.Header.Get("Authorization"); got != "Bearer original" {
		t.Fatalf("authorization overwritten: %q", got)
	}
	if got := req.Header.Get("Content-Type"); got != "application/json" {
		t.Fatalf("content-type overwritten: %q", got)
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
