package service

import (
	"context"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

func TestWorkbenchTicketsRedisReplayExpiryAndBinding(t *testing.T) {
	s, ctx, _, _, _ := newWorkbenchFixture(t)
	rdb := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: rdb.Addr(), MaxRetries: -1})
	t.Cleanup(func() { _ = client.Close() })
	s.store = &redisWorkbenchStore{client: client, prefix: "test:"}
	ticket, err := s.IssueTicket(ctx, "session", "http://localhost:15173")
	require.NoError(t, err)
	require.Equal(t, "/api/v1/sandbox-terminal", ticket.WebsocketPath)
	keys := rdb.Keys()
	require.Len(t, keys, 1)
	require.NotContains(t, keys[0], ticket.Ticket)
	stored, err := rdb.Get(keys[0])
	require.NoError(t, err)
	require.NotContains(t, stored, ticket.Ticket)
	var winners atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			identity, err := s.ConsumeTicket(context.Background(), ticket.Ticket, "http://localhost:15173")
			if err == nil {
				if identity.UserID == "alice" && identity.TenantID == 7 && identity.SessionID == "session" {
					winners.Add(1)
				}
			}
		}()
	}
	wg.Wait()
	require.EqualValues(t, 1, winners.Load())
	ticket, err = s.IssueTicket(ctx, "session", "http://localhost:15173")
	require.NoError(t, err)
	rdb.FastForward(WorkbenchTicketTTL)
	_, err = s.ConsumeTicket(context.Background(), ticket.Ticket, "http://localhost:15173")
	require.ErrorIs(t, err, ErrWorkbenchTicket)
	ticket, err = s.IssueTicket(ctx, "session", "http://localhost:15173")
	require.NoError(t, err)
	_, err = s.ConsumeTicket(context.Background(), ticket.Ticket, "http://evil.example")
	require.ErrorIs(t, err, ErrWorkbenchTicket)
	_, err = s.ConsumeTicket(context.Background(), ticket.Ticket, "http://localhost:15173")
	require.ErrorIs(t, err, ErrWorkbenchTicket)
	ticket, err = s.IssueTicket(ctx, "session", "http://localhost:15173")
	require.NoError(t, err)
	require.NoError(t, s.deps.DB.Exec("UPDATE tenant_members SET status='suspended' WHERE user_id='alice'").Error)
	_, err = s.ConsumeTicket(context.Background(), ticket.Ticket, "http://localhost:15173")
	require.ErrorIs(t, err, ErrWorkbenchDenied)
}

func TestWorkbenchRedisLeaseCompareRenewAndFailClosed(t *testing.T) {
	rdb := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: rdb.Addr(), MaxRetries: -1, DialTimeout: 100 * time.Millisecond})
	defer client.Close()
	store := &redisWorkbenchStore{client: client, prefix: "test:"}
	ctx := context.Background()
	require.NoError(t, store.acquire(ctx, "session", "user", "first"))
	require.ErrorIs(t, store.acquire(ctx, "session", "user", "second"), ErrWorkbenchBusy)
	require.NoError(t, store.acquire(ctx, "other-tenant-session", "other-user", "other"))
	require.ErrorIs(t, store.renew(ctx, "session", "user", "second"), ErrWorkbenchUnavailable)
	require.NoError(t, store.release(ctx, "session", "user", "second"))
	require.True(t, rdb.Exists("test:{console}:session:session"))
	rdb.FastForward(10 * time.Second)
	require.NoError(t, store.renew(ctx, "session", "user", "first"))
	rdb.FastForward(10 * time.Second)
	require.ErrorIs(t, store.acquire(ctx, "session", "user", "second"), ErrWorkbenchBusy)
	rdb.FastForward(6 * time.Second)
	require.NoError(t, store.acquire(ctx, "session", "user", "second"))
	require.NoError(t, store.release(ctx, "session", "user", "first"))
	require.ErrorIs(t, store.renew(ctx, "session", "user", "first"), ErrWorkbenchUnavailable)
	value, err := rdb.Get("test:{console}:session:session")
	require.NoError(t, err)
	require.Equal(t, "second", value)
	rdb.Close()
	err = store.acquire(ctx, "new", "user", "token")
	require.ErrorIs(t, err, ErrWorkbenchUnavailable)
}

func TestWorkbenchRedisAuthenticatedConsoleQuotas(t *testing.T) {
	rdb := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: rdb.Addr(), MaxRetries: -1})
	t.Cleanup(func() { _ = client.Close() })
	store := &redisWorkbenchStore{client: client, prefix: "test:"}
	ctx := context.Background()

	for i := 0; i < WorkbenchMaxAuthenticatedConsolesUser; i++ {
		require.NoError(t, store.acquire(ctx, "same-user-"+strings.Repeat("x", i+1), "user", "user-token-"+strings.Repeat("x", i+1)))
	}
	require.ErrorIs(t, store.acquire(ctx, "same-user-over", "user", "over"), ErrWorkbenchBusy)
	require.NoError(t, store.acquire(ctx, "different-user", "other-user", "other"))

	rdb.FlushAll()
	for i := 0; i < WorkbenchMaxAuthenticatedConsoles; i++ {
		suffix := strings.Repeat("x", i+1)
		require.NoError(t, store.acquire(ctx, "session-"+suffix, "user-"+suffix, "token-"+suffix))
	}
	require.ErrorIs(t, store.acquire(ctx, "global-over", "fresh-user", "over"), ErrWorkbenchBusy)
	require.NoError(t, store.release(ctx, "session-x", "user-x", "token-x"))
	require.NoError(t, store.acquire(ctx, "global-replacement", "fresh-user", "replacement"))
}

func TestWorkbenchMemoryTicketBoundedAndTTL(t *testing.T) {
	store := newMemoryWorkbenchStore()
	now := time.Now()
	store.now = func() time.Time { return now }
	identity := WorkbenchIdentity{ExpiresAt: now.Add(WorkbenchTicketTTL)}
	for i := 0; i < workbenchMemoryLimit; i++ {
		store.tickets[strings.Repeat("x", i+1)] = identity
	}
	require.ErrorIs(t, store.putTicket(context.Background(), "full", identity), ErrWorkbenchBusy)
	now = now.Add(WorkbenchTicketTTL)
	_, err := store.consumeTicket(context.Background(), "x")
	require.ErrorIs(t, err, ErrWorkbenchTicket)
	require.Empty(t, store.tickets)
	identity.ExpiresAt = now.Add(WorkbenchTicketTTL)
	require.NoError(t, store.putTicket(context.Background(), "valid", identity))
	_, err = store.consumeTicket(context.Background(), "valid")
	require.NoError(t, err)
	_, err = store.consumeTicket(context.Background(), "valid")
	require.ErrorIs(t, err, ErrWorkbenchTicket)
}

func TestWorkbenchMemoryAuthenticatedConsoleQuotas(t *testing.T) {
	store := newMemoryWorkbenchStore()
	ctx := context.Background()
	for i := 0; i < WorkbenchMaxAuthenticatedConsolesUser; i++ {
		suffix := strings.Repeat("x", i+1)
		require.NoError(t, store.acquire(ctx, "same-user-"+suffix, "user", "token-"+suffix))
	}
	require.ErrorIs(t, store.acquire(ctx, "same-user-over", "user", "over"), ErrWorkbenchBusy)
	require.NoError(t, store.acquire(ctx, "different-user", "other-user", "other"))

	store = newMemoryWorkbenchStore()
	for i := 0; i < WorkbenchMaxAuthenticatedConsoles; i++ {
		suffix := strings.Repeat("x", i+1)
		require.NoError(t, store.acquire(ctx, "session-"+suffix, "user-"+suffix, "token-"+suffix))
	}
	require.ErrorIs(t, store.acquire(ctx, "global-over", "fresh-user", "over"), ErrWorkbenchBusy)
	require.NoError(t, store.release(ctx, "session-x", "user-x", "token-x"))
	require.NoError(t, store.acquire(ctx, "global-replacement", "fresh-user", "replacement"))
}
