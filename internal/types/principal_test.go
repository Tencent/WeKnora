package types

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
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

func TestSessionOwnerIDFromContextIsolatesTenantAPIKeys(t *testing.T) {
	ctx := WithPrincipal(context.Background(), Principal{Type: PrincipalAPITenant, ID: "7"})
	ctx = context.WithValue(ctx, TenantIDContextKey, uint64(7))
	ctx = context.WithValue(ctx, UserIDContextKey, "system-7")
	ctx = WithTenantAPIKeyScope(ctx, TenantAPIKeyScope{KeyID: 99})

	if got := SessionOwnerIDFromContext(ctx); got != "api_tenant_key:7:99" {
		t.Fatalf("SessionOwnerIDFromContext() = %q, want per-key isolation", got)
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

func TestPrincipalAuthSourceRoundTrip(t *testing.T) {
	ctx := WithPrincipalAuthSource(context.Background(), AuthSourceWeb)
	require.Equal(t, AuthSourceWeb, PrincipalAuthSourceFromContext(ctx))
	require.Equal(t, "", PrincipalAuthSourceFromContext(context.Background()))
}

func TestStampSessionOwnerSource(t *testing.T) {
	web := WithPrincipalAuthSource(
		WithPrincipal(context.WithValue(context.Background(), UserIDContextKey, "u-1"),
			Principal{Type: PrincipalWebUser, ID: "u-1"}),
		AuthSourceWeb)

	s := &Session{UserID: "u-1", OwnerAuthSource: "forged"}
	StampSessionOwnerSource(web, s)
	require.Equal(t, AuthSourceWeb, s.OwnerAuthSource)

	// A row created for someone else (internal service, admin tooling) is not
	// vouched for by the caller's credentials.
	other := &Session{UserID: "u-2"}
	StampSessionOwnerSource(web, other)
	require.Equal(t, AuthSourceInternal, other.OwnerAuthSource)

	// No middleware source: background or internal context.
	bare := &Session{UserID: "u-1"}
	StampSessionOwnerSource(context.WithValue(context.Background(), UserIDContextKey, "u-1"), bare)
	require.Equal(t, AuthSourceInternal, bare.OwnerAuthSource)

	empty := &Session{}
	StampSessionOwnerSource(web, empty)
	require.Equal(t, AuthSourceUnknown, empty.OwnerAuthSource)
}

func TestAuthSourceForBoundOwnerID(t *testing.T) {
	require.Equal(t, AuthSourceMCPEndpoint, AuthSourceForBoundOwnerID(MCPEndpointPrincipal(7, "e").StorageID()))
	require.Equal(t, AuthSourceEmbed, AuthSourceForBoundOwnerID(EmbedSessionPrincipal(7, "c", "s").StorageID()))
	require.Equal(t, AuthSourceInternal, AuthSourceForBoundOwnerID("u-1"),
		"binding never elevates an owner to a verified person")
}
