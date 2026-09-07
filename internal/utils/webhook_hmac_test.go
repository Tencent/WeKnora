package utils

import (
	"crypto/hmac"
	"testing"
	"time"
)

func TestSignWebhookTimestampBodyDiffersFromBodyOnly(t *testing.T) {
	raw := []byte(`{"type":"webhook.test"}`)
	sig := SignWebhookTimestampBody("whsec_test_secret_16", 1756624860, raw)
	if sig == "" || len(sig) < 10 {
		t.Fatalf("empty signature")
	}
	if !hmac.Equal([]byte(sig[:7]), []byte("sha256=")) {
		t.Fatalf("prefix = %q", sig[:7])
	}
	other := SignWebhookTimestampBody("whsec_test_secret_16", 1756624861, raw)
	if hmac.Equal([]byte(sig), []byte(other)) {
		t.Fatal("different timestamps must produce different signatures")
	}
}

func TestVerifyWebhookTimestampBodyWindow(t *testing.T) {
	raw := []byte(`{"ok":true}`)
	now := time.Unix(1_700_000_000, 0)
	sig := SignWebhookTimestampBody("secret-16-bytes!!", now.Unix(), raw)
	if err := VerifyWebhookTimestampBody("secret-16-bytes!!", sig, "1700000000", raw, now, 300*time.Second); err != nil {
		t.Fatalf("verify: %v", err)
	}
	if err := VerifyWebhookTimestampBody("secret-16-bytes!!", sig, "1699999600", raw, now, 300*time.Second); err == nil {
		t.Fatal("stale timestamp must fail")
	}
	if err := VerifyWebhookTimestampBody("wrong-secret-16b!", sig, "1700000000", raw, now, 300*time.Second); err == nil {
		t.Fatal("wrong secret must fail")
	}
}
