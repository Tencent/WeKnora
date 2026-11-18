package utils

import (
	"net/http"
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

func TestApplyCustomHeaders_KeepsSignedHeaders(t *testing.T) {
	// OCI request signatures cover these; a custom value would break them.
	req, _ := http.NewRequest("POST", "https://example.com", nil)
	req.Header.Set("Date", "signed-date")
	req.Header.Set("X-Content-Sha256", "signed-digest")
	ApplyCustomHeaders(req, map[string]string{"date": "other", "x-content-sha256": "other"})
	if req.Header.Get("Date") != "signed-date" || req.Header.Get("X-Content-Sha256") != "signed-digest" {
		t.Fatalf("signed headers overwritten: %v", req.Header)
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
