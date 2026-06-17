package handler

import (
	"strings"
	"testing"
	"time"
)

const testAESKey = "0123456789abcdef0123456789abcdef"

func TestOAuthState_SignVerify_RoundTrip(t *testing.T) {
	t.Setenv("SYSTEM_AES_KEY", testAESKey)

	payload := &oauthStatePayload{
		DataSourceID: "ds-123",
		RedirectURI:  "https://wk.example.com/api/v1/datasource/oauth/callback",
		Nonce:        randomNonce(),
		Exp:          time.Now().Add(5 * time.Minute).Unix(),
	}
	state, err := signOAuthState(payload)
	if err != nil {
		t.Fatalf("signOAuthState: %v", err)
	}

	got, err := verifyOAuthState(state)
	if err != nil {
		t.Fatalf("verifyOAuthState: %v", err)
	}
	if got.DataSourceID != payload.DataSourceID {
		t.Errorf("ds_id = %q, want %q", got.DataSourceID, payload.DataSourceID)
	}
	if got.RedirectURI != payload.RedirectURI {
		t.Errorf("redirect_uri = %q, want %q", got.RedirectURI, payload.RedirectURI)
	}
}

func TestOAuthState_Tampered(t *testing.T) {
	t.Setenv("SYSTEM_AES_KEY", testAESKey)

	state, err := signOAuthState(&oauthStatePayload{
		DataSourceID: "ds-123",
		RedirectURI:  "https://wk/cb",
		Exp:          time.Now().Add(time.Minute).Unix(),
	})
	if err != nil {
		t.Fatalf("signOAuthState: %v", err)
	}

	// Flip the payload (before the dot) while keeping the original signature.
	b64, sig, _ := strings.Cut(state, ".")
	tampered := b64[:len(b64)-2] + "AA" + "." + sig
	if _, err := verifyOAuthState(tampered); err == nil {
		t.Fatal("expected signature mismatch on tampered state")
	}

	// Garbage signature.
	if _, err := verifyOAuthState(b64 + ".deadbeef"); err == nil {
		t.Fatal("expected signature mismatch on bad signature")
	}
}

func TestOAuthState_Expired(t *testing.T) {
	t.Setenv("SYSTEM_AES_KEY", testAESKey)

	state, err := signOAuthState(&oauthStatePayload{
		DataSourceID: "ds-123",
		RedirectURI:  "https://wk/cb",
		Exp:          time.Now().Add(-time.Minute).Unix(), // already expired
	})
	if err != nil {
		t.Fatalf("signOAuthState: %v", err)
	}
	if _, err := verifyOAuthState(state); err == nil {
		t.Fatal("expected expiry error")
	}
}

func TestOAuthState_CrossKeyRejected(t *testing.T) {
	t.Setenv("SYSTEM_AES_KEY", testAESKey)
	state, err := signOAuthState(&oauthStatePayload{
		DataSourceID: "ds-123",
		RedirectURI:  "https://wk/cb",
		Exp:          time.Now().Add(time.Minute).Unix(),
	})
	if err != nil {
		t.Fatalf("signOAuthState: %v", err)
	}

	// A different signing key must not verify the state.
	t.Setenv("SYSTEM_AES_KEY", "ffffffffffffffffffffffffffffffff")
	if _, err := verifyOAuthState(state); err == nil {
		t.Fatal("expected verification failure under a different key")
	}
}

func TestOAuthStateKey_RequiresConfiguredKey(t *testing.T) {
	t.Setenv("SYSTEM_AES_KEY", "")
	if _, err := oauthStateKey(); err == nil {
		t.Fatal("expected error when SYSTEM_AES_KEY is unset")
	}
	t.Setenv("SYSTEM_AES_KEY", "tooshort")
	if _, err := oauthStateKey(); err == nil {
		t.Fatal("expected error when SYSTEM_AES_KEY is too short")
	}
}
