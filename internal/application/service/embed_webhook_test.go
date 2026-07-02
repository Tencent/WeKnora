package service

import (
	stderrors "errors"
	"net/http"
	"net/http/httptest"
	"testing"

	secutils "github.com/Tencent/WeKnora/internal/utils"
)

func TestValidateEmbedWebhookURL(t *testing.T) {
	withSSRFWhitelist(t, "*.example.com,127.0.0.1")

	if err := ValidateEmbedWebhookURL(""); err != nil {
		t.Fatalf("empty URL should be allowed: %v", err)
	}
	if err := ValidateEmbedWebhookURL("https://hooks.example.com/weknora/events"); err != nil {
		t.Fatalf("whitelisted public https URL should pass: %v", err)
	}
	if err := ValidateEmbedWebhookURL("ftp://hooks.example.com/x"); err == nil {
		t.Fatal("expected non-http(s) scheme to fail")
	}
	if err := ValidateEmbedWebhookURL("http://127.0.0.1/webhook"); err != nil {
		t.Fatalf("whitelisted loopback should pass: %v", err)
	}
	if err := ValidateEmbedWebhookURL("http://169.254.169.254/latest/meta-data/"); err == nil {
		t.Fatal("expected link-local metadata URL to be blocked")
	}
}

func TestSignEmbedWebhookBody(t *testing.T) {
	raw := []byte(`{"type":"message_sent","query":"hi"}`)
	sig := SignEmbedWebhookBody("test-secret", raw)
	if sig == "" || len(sig) != 64 {
		t.Fatalf("unexpected signature: %q", sig)
	}
	sig2 := SignEmbedWebhookBody("test-secret", raw)
	if sig != sig2 {
		t.Fatal("signature not deterministic")
	}
}

func TestEmbedWebhookHTTPClientBlocksRedirectToInternalURL(t *testing.T) {
	withSSRFWhitelist(t, "127.0.0.1")

	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://169.254.169.254/latest/meta-data/", http.StatusFound)
	}))
	defer origin.Close()

	req, err := http.NewRequest(http.MethodPost, origin.URL, nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	resp, err := newEmbedWebhookHTTPClient().Do(req)
	if resp != nil && resp.Body != nil {
		_ = resp.Body.Close()
	}
	if err == nil {
		t.Fatal("expected redirect to link-local metadata endpoint to be blocked")
	}
	if !stderrors.Is(err, secutils.ErrSSRFRedirectBlocked) {
		t.Fatalf("error = %v, want ErrSSRFRedirectBlocked", err)
	}
}
