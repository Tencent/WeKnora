package service

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func desktopTicketClaims() SandboxDesktopTicketClaims {
	return SandboxDesktopTicketClaims{
		UserID: "u-1", TenantID: 7, SessionID: "sess-1", TokenID: "tok-1",
	}
}

func TestDesktopTicketRoundTrip(t *testing.T) {
	store := NewSandboxDesktopTicketStore(nil) // memory fallback
	ctx := context.Background()

	ticket, err := store.Issue(ctx, desktopTicketClaims())
	require.NoError(t, err)
	require.NotEmpty(t, ticket)

	got, err := store.Consume(ctx, ticket)
	require.NoError(t, err)
	require.Equal(t, desktopTicketClaims(), *got)
}

func TestDesktopTicketIsSingleUse(t *testing.T) {
	// The ticket travels in a query string, so it lands in gateway access
	// logs and browser history. One-shot consumption is what bounds that.
	store := NewSandboxDesktopTicketStore(nil)
	ctx := context.Background()

	ticket, err := store.Issue(ctx, desktopTicketClaims())
	require.NoError(t, err)
	_, err = store.Consume(ctx, ticket)
	require.NoError(t, err)

	_, err = store.Consume(ctx, ticket)
	require.ErrorIs(t, err, ErrDesktopTicketInvalid)
}

func TestDesktopTicketRejectsUnknown(t *testing.T) {
	store := NewSandboxDesktopTicketStore(nil)
	_, err := store.Consume(context.Background(), "not-a-ticket")
	require.ErrorIs(t, err, ErrDesktopTicketInvalid)
}

func TestDesktopTicketRejectsEmpty(t *testing.T) {
	store := NewSandboxDesktopTicketStore(nil)
	_, err := store.Consume(context.Background(), "")
	require.ErrorIs(t, err, ErrDesktopTicketInvalid)
}

func TestDesktopTicketRequiresCompleteClaims(t *testing.T) {
	store := NewSandboxDesktopTicketStore(nil)
	_, err := store.Issue(context.Background(), SandboxDesktopTicketClaims{UserID: "u-1"})
	require.Error(t, err)
}

func TestDesktopTicketsAreDistinct(t *testing.T) {
	store := NewSandboxDesktopTicketStore(nil)
	ctx := context.Background()
	a, err := store.Issue(ctx, desktopTicketClaims())
	require.NoError(t, err)
	b, err := store.Issue(ctx, desktopTicketClaims())
	require.NoError(t, err)
	require.NotEqual(t, a, b)
}
