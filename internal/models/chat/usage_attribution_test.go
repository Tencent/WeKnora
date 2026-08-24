package chat

import (
	"context"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
)

func TestUsageAttributionEmptyOutsideSessions(t *testing.T) {
	// Calls without session or principal context (document parsing, title
	// generation, background jobs) must keep the usage line byte-identical.
	if got := usageAttribution(context.Background()); got != "" {
		t.Fatalf("expected empty suffix, got %q", got)
	}
}

func TestUsageAttributionCarriesSessionAndPrincipal(t *testing.T) {
	ctx := types.WithSessionID(context.Background(), "sess-123")
	ctx = types.WithPrincipal(ctx, types.Principal{Type: types.PrincipalAPIExternalUser, ID: "10000:42"})

	got := usageAttribution(ctx)
	want := ", session_id=sess-123, principal=api_external_user:10000:42"
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestUsageAttributionSessionOnly(t *testing.T) {
	ctx := types.WithSessionID(context.Background(), "sess-9")
	if got := usageAttribution(ctx); got != ", session_id=sess-9" {
		t.Fatalf("got %q", got)
	}
}
