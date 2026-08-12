package embedding

import (
	"net/http"
	"strings"
	"testing"
	"time"

	secutils "github.com/Tencent/WeKnora/internal/utils"
)

func TestNewEmbeddingHTTPClient_ReusesTransport(t *testing.T) {
	firstTimeout := 15 * time.Second
	secondTimeout := 45 * time.Second
	first := newEmbeddingHTTPClient(firstTimeout)
	second := newEmbeddingHTTPClient(secondTimeout)

	if first == second {
		t.Fatal("expected distinct HTTP clients")
	}
	firstGuard, ok := first.Transport.(*secutils.SSRFValidatingRoundTripper)
	if !ok {
		t.Fatalf("expected SSRF-validating transport, got %T", first.Transport)
	}
	secondGuard, ok := second.Transport.(*secutils.SSRFValidatingRoundTripper)
	if !ok {
		t.Fatalf("expected SSRF-validating transport, got %T", second.Transport)
	}
	if firstGuard.Base != secondGuard.Base {
		t.Fatal("expected embedding HTTP clients to share a base transport")
	}
	if firstGuard.Base != http.RoundTripper(sharedEmbeddingHTTPTransport) {
		t.Fatal("expected embedding HTTP client to use the shared transport")
	}
	if first.Timeout != firstTimeout {
		t.Fatalf("unexpected first client timeout: got %v, want %v", first.Timeout, firstTimeout)
	}
	if second.Timeout != secondTimeout {
		t.Fatalf("unexpected second client timeout: got %v, want %v", second.Timeout, secondTimeout)
	}
}

func TestValidateEmbeddingBaseURL_RejectsLoopback(t *testing.T) {
	err := validateEmbeddingBaseURL("http://169.254.169.254/latest/meta-data")
	if err == nil {
		t.Fatal("expected SSRF error for link-local metadata URL")
	}
	if !strings.Contains(err.Error(), "SSRF") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestValidateEmbeddingBaseURL_AllowsEmpty(t *testing.T) {
	if err := validateEmbeddingBaseURL(""); err != nil {
		t.Fatalf("empty base URL should be allowed: %v", err)
	}
}

func TestNewOpenAIEmbedder_RejectsPrivateBaseURL(t *testing.T) {
	_, err := NewOpenAIEmbedder(
		"test-key",
		"http://169.254.169.254/latest/meta-data",
		"text-embedding-3-small",
		511,
		256,
		"model-id",
		nil,
	)
	if err == nil {
		t.Fatal("expected SSRF rejection for link-local metadata URL")
	}
}
