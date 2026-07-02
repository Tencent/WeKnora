package types

import (
	"context"
	"testing"
)

func TestPrincipalFromContextFallsBackToWebUser(t *testing.T) {
	ctx := context.WithValue(context.Background(), UserIDContextKey, "u1")

	p, ok := PrincipalFromContext(ctx)
	if !ok {
		t.Fatal("expected principal")
	}
	if p.Type != PrincipalWebUser || p.ID != "u1" {
		t.Fatalf("principal = %#v", p)
	}
}

func TestWithPrincipalRejectsBlankValues(t *testing.T) {
	ctx := WithPrincipal(context.Background(), Principal{Type: " ", ID: "x"})

	if _, ok := PrincipalFromContext(ctx); ok {
		t.Fatal("blank principal type should not be stored")
	}
}

func TestPrincipalStorageID(t *testing.T) {
	p := Principal{Type: PrincipalIMUser, ID: "wecom:ch1:u1"}

	if got := p.StorageID(); got != "im_user:wecom:ch1:u1" {
		t.Fatalf("StorageID() = %q", got)
	}
}

func TestSessionOwnerIDFromContextUsesAPIExternalPrincipal(t *testing.T) {
	ctx := WithPrincipal(context.Background(), Principal{
		Type: PrincipalAPIExternalUser,
		ID:   "7:alice",
	})
	ctx = context.WithValue(ctx, UserIDContextKey, "system-7")

	if got := SessionOwnerIDFromContext(ctx); got != "api_external_user:7:alice" {
		t.Fatalf("SessionOwnerIDFromContext() = %q", got)
	}
}

func TestSessionOwnerIDFromContextFallsBackToUserID(t *testing.T) {
	ctx := context.WithValue(context.Background(), UserIDContextKey, "system-7")

	if got := SessionOwnerIDFromContext(ctx); got != "system-7" {
		t.Fatalf("SessionOwnerIDFromContext() = %q", got)
	}
}

func TestSessionOwnerIDFromContextUsesEmbedSessionPrincipal(t *testing.T) {
	ctx := WithPrincipal(context.Background(), EmbedSessionPrincipal(10000, "ch1", "sess1"))
	ctx = context.WithValue(ctx, UserIDContextKey, "embed-ch1")

	if got := SessionOwnerIDFromContext(ctx); got != "embed_session:10000:ch1:sess1" {
		t.Fatalf("SessionOwnerIDFromContext() = %q", got)
	}
}

func TestMCPOAuthPrincipalMapsEmbedSessionToVisitor(t *testing.T) {
	sess := EmbedSessionPrincipal(10000, "ch1", "sess1")
	ctx := WithEmbedVisitorID(context.Background(), "visitor-abc")
	got := MCPOAuthPrincipalFromContext(WithPrincipal(ctx, sess))
	want := "embed_visitor:10000:ch1:visitor-abc"
	if got.StorageID() != want {
		t.Fatalf("MCPOAuthPrincipalFromContext() = %q, want %q", got.StorageID(), want)
	}
}
