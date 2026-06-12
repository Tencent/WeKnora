package service

import "testing"

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
