package service

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
)

func TestEmbedSessionHandleSignVerify(t *testing.T) {
	t.Setenv("SYSTEM_SIGNING_KEY", "")
	t.Setenv("SYSTEM_AES_KEY", "test-embed-signing-key-32-bytes!!!")
	ch := &types.EmbedChannel{ID: "ch-1", PublishToken: "em_secret_token"}
	const sessionID = "11111111-2222-3333-4444-555555555555"

	sig := SignEmbedSessionHandle(ch, sessionID)
	if sig == "" {
		t.Fatal("expected a non-empty signature")
	}
	if !VerifyEmbedSessionHandle(ch, sessionID, sig) {
		t.Fatal("valid handle should verify")
	}

	// Tampered session id must not verify with the same signature.
	if VerifyEmbedSessionHandle(ch, "00000000-0000-0000-0000-000000000000", sig) {
		t.Fatal("signature must be bound to the session id")
	}

	// A different channel token (e.g. after rotation) invalidates the handle.
	rotated := &types.EmbedChannel{ID: "ch-1", PublishToken: "em_rotated_token"}
	if VerifyEmbedSessionHandle(rotated, sessionID, sig) {
		t.Fatal("rotating the channel token must invalidate old handles")
	}

	// A different channel id must not verify.
	other := &types.EmbedChannel{ID: "ch-2", PublishToken: "em_secret_token"}
	if VerifyEmbedSessionHandle(other, sessionID, sig) {
		t.Fatal("signature must be bound to the channel id")
	}

	// Empty / missing signature is rejected.
	if VerifyEmbedSessionHandle(ch, sessionID, "") {
		t.Fatal("empty signature must be rejected")
	}
	if SignEmbedSessionHandle(nil, sessionID) != "" {
		t.Fatal("nil channel must yield empty signature")
	}
	if SignEmbedSessionHandle(ch, "") != "" {
		t.Fatal("empty session id must yield empty signature")
	}
}

func TestIsEmbedSessionToken(t *testing.T) {
	t.Setenv("SYSTEM_SIGNING_KEY", "")
	t.Setenv("SYSTEM_AES_KEY", "test-embed-signing-key-32-bytes!!!")
	if !IsEmbedSessionToken("ems_abc123") {
		t.Fatal("expected ems_ prefix to be session token")
	}
	if IsEmbedSessionToken("em_abc123") {
		t.Fatal("publish token must not be treated as session token")
	}
	if IsEmbedSessionToken("") {
		t.Fatal("empty token must not match")
	}
}

func TestIssueSessionTokenWithoutRedis(t *testing.T) {
	t.Setenv("SYSTEM_SIGNING_KEY", "")
	t.Setenv("SYSTEM_AES_KEY", "test-embed-signing-key-32-bytes!!!")
	svc := &embedChannelService{redis: nil}
	_, _, err := svc.IssueSessionToken(context.Background(), "channel-1")
	if err != ErrEmbedSessionUnavailable {
		t.Fatalf("expected ErrEmbedSessionUnavailable, got %v", err)
	}
}

func TestResolveSessionTokenWithoutRedis(t *testing.T) {
	t.Setenv("SYSTEM_SIGNING_KEY", "")
	t.Setenv("SYSTEM_AES_KEY", "test-embed-signing-key-32-bytes!!!")
	svc := &embedChannelService{redis: nil}
	_, err := svc.ResolveSessionToken(context.Background(), "ems_test")
	if err != ErrEmbedSessionUnavailable {
		t.Fatalf("expected ErrEmbedSessionUnavailable, got %v", err)
	}
}

func TestResolveSessionTokenRejectsPublishToken(t *testing.T) {
	t.Setenv("SYSTEM_SIGNING_KEY", "")
	t.Setenv("SYSTEM_AES_KEY", "test-embed-signing-key-32-bytes!!!")
	svc := &embedChannelService{redis: nil}
	_, err := svc.ResolveSessionToken(context.Background(), "em_publish_only")
	if err != ErrEmbedTokenInvalid {
		t.Fatalf("expected ErrEmbedTokenInvalid for publish token, got %v", err)
	}
}

func TestEmbedSessionRejectsPublicTokenForgery(t *testing.T) {
	t.Setenv("SYSTEM_SIGNING_KEY", "")
	t.Setenv("SYSTEM_AES_KEY", "test-embed-signing-key-32-bytes!!!")
	ch := &types.EmbedChannel{ID: "channel", PublishToken: "public-token"}
	mac := hmac.New(sha256.New, []byte(ch.PublishToken))
	mac.Write([]byte(ch.ID + "|victim-session"))
	forged := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	if VerifyEmbedSessionHandle(ch, "victim-session", forged) {
		t.Fatal("accepted public-token forgery")
	}
	t.Setenv("SYSTEM_SIGNING_KEY", "")
	t.Setenv("SYSTEM_AES_KEY", "")
	if SignEmbedSessionHandle(ch, "session") != "" {
		t.Fatal("signed without a server key")
	}
}
