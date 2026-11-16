package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/require"
)

type fakeKeepAliveTarget struct {
	next    time.Duration
	err     error
	calls   []string
	tenants []uint64
}

func (f *fakeKeepAliveTarget) KeepAliveSession(ctx context.Context, sessionID string) (time.Duration, error) {
	f.calls = append(f.calls, sessionID)
	tenant, _ := types.SandboxTenantIDFromContext(ctx)
	f.tenants = append(f.tenants, tenant)
	return f.next, f.err
}

func TestBackgroundKeepAliverThrottlesPerSession(t *testing.T) {
	now := time.Date(2026, 10, 15, 10, 0, 0, 0, time.UTC)
	target := &fakeKeepAliveTarget{next: 10 * time.Minute}
	k := newBackgroundKeepAliver(target, func() time.Time { return now })
	ctx := context.Background()
	s1 := interfaces.SessionRef{TenantID: 7, SessionID: "s1"}
	s2 := interfaces.SessionRef{TenantID: 7, SessionID: "s2"}

	require.NoError(t, k.KeepAlive(ctx, s1))
	require.NoError(t, k.KeepAlive(ctx, s1))
	require.NoError(t, k.KeepAlive(ctx, s2))
	require.Equal(t, []string{"s1", "s2"}, target.calls, "a refresh within the interval is skipped")

	now = now.Add(10 * time.Minute)
	require.NoError(t, k.KeepAlive(ctx, s1))
	require.Equal(t, []string{"s1", "s2", "s1"}, target.calls)
	require.Equal(t, []uint64{7, 7, 7}, target.tenants, "the session owner's tenant keys the binding")
}

func TestBackgroundKeepAliverRetriesAfterFailure(t *testing.T) {
	now := time.Date(2026, 10, 15, 10, 0, 0, 0, time.UTC)
	target := &fakeKeepAliveTarget{next: 10 * time.Minute, err: errors.New("provider down")}
	k := newBackgroundKeepAliver(target, func() time.Time { return now })
	s := interfaces.SessionRef{TenantID: 7, SessionID: "s1"}

	require.Error(t, k.KeepAlive(context.Background(), s))
	require.Error(t, k.KeepAlive(context.Background(), s))
	require.Len(t, target.calls, 2, "a failed refresh must not start the quiet interval")
}

func TestBackgroundKeepAliverWithoutIntervalDoesNotThrottle(t *testing.T) {
	now := time.Date(2026, 10, 15, 10, 0, 0, 0, time.UTC)
	target := &fakeKeepAliveTarget{}
	k := newBackgroundKeepAliver(target, func() time.Time { return now })
	s := interfaces.SessionRef{TenantID: 7, SessionID: "s1"}

	require.NoError(t, k.KeepAlive(context.Background(), s))
	require.NoError(t, k.KeepAlive(context.Background(), s))
	require.Len(t, target.calls, 2, "nothing was bound yet, so the next call must look again")
}

func TestBackgroundKeepAliverIgnoresIncompleteRef(t *testing.T) {
	target := &fakeKeepAliveTarget{next: time.Minute}
	k := newBackgroundKeepAliver(target, time.Now)

	require.NoError(t, k.KeepAlive(context.Background(), interfaces.SessionRef{SessionID: "s1"}))
	require.NoError(t, k.KeepAlive(context.Background(), interfaces.SessionRef{TenantID: 7}))
	require.Empty(t, target.calls)
}
