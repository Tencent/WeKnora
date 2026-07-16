package redislock_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"

	"github.com/Tencent/WeKnora/internal/common/redislock"
)

func newTokenLockTestClient(t *testing.T) (*redis.Client, *miniredis.Miniredis) {
	t.Helper()

	mini := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: mini.Addr()})
	t.Cleanup(func() { require.NoError(t, client.Close()) })
	return client, mini
}

func TestNewTokenReturnsUniqueOpaqueOwners(t *testing.T) {
	t.Parallel()

	first, err := redislock.NewToken()
	require.NoError(t, err)
	second, err := redislock.NewToken()
	require.NoError(t, err)

	require.Len(t, first, 32)
	require.Len(t, second, 32)
	require.NotEqual(t, first, second)
}

func TestTokenLockAcquireAndReleaseRequiresOwnership(t *testing.T) {
	client, _ := newTokenLockTestClient(t)
	ctx := context.Background()
	key := "lock:ownership"

	acquired, err := redislock.TryAcquire(ctx, client, key, "owner-a", time.Minute)
	require.NoError(t, err)
	require.True(t, acquired)

	acquired, err = redislock.TryAcquire(ctx, client, key, "owner-b", time.Minute)
	require.NoError(t, err)
	require.False(t, acquired)

	released, err := redislock.Release(ctx, client, key, "owner-b")
	require.NoError(t, err)
	require.False(t, released)
	require.Equal(t, "owner-a", client.Get(ctx, key).Val())

	released, err = redislock.Release(ctx, client, key, "owner-a")
	require.NoError(t, err)
	require.True(t, released)
	require.ErrorIs(t, client.Get(ctx, key).Err(), redis.Nil)
}

func TestTokenLockRenewRequiresOwnership(t *testing.T) {
	client, mini := newTokenLockTestClient(t)
	ctx := context.Background()
	key := "lock:renew"

	require.NoError(t, client.Set(ctx, key, "owner-a", time.Second).Err())
	renewed, err := redislock.Renew(ctx, client, key, "owner-b", time.Minute)
	require.NoError(t, err)
	require.False(t, renewed)

	renewed, err = redislock.Renew(ctx, client, key, "owner-a", time.Minute)
	require.NoError(t, err)
	require.True(t, renewed)
	mini.FastForward(2 * time.Second)
	require.Equal(t, "owner-a", client.Get(ctx, key).Val())
}

func TestTokenLockRejectsUnsafeLeaseConfiguration(t *testing.T) {
	client, _ := newTokenLockTestClient(t)
	ctx := context.Background()

	_, err := redislock.TryAcquire(ctx, client, "lock:key", "owner", 0)
	require.Error(t, err)
	_, err = redislock.Renew(ctx, client, "lock:key", "owner", -time.Second)
	require.Error(t, err)
	err = redislock.WithRenewableLock(
		ctx,
		client,
		"lock:key",
		time.Second,
		time.Second,
		func(context.Context) error { return nil },
	)
	require.Error(t, err)
}

func TestWithRenewableLockReturnsCallbackErrorAndReleases(t *testing.T) {
	client, _ := newTokenLockTestClient(t)
	ctx := context.Background()
	key := "lock:callback"
	want := errors.New("callback failed")

	err := redislock.WithRenewableLock(
		ctx,
		client,
		key,
		time.Minute,
		20*time.Second,
		func(context.Context) error { return want },
	)

	require.ErrorIs(t, err, want)
	require.ErrorIs(t, client.Get(ctx, key).Err(), redis.Nil)
}

func TestWithRenewableLockHonorsContextWhileWaiting(t *testing.T) {
	client, _ := newTokenLockTestClient(t)
	ctx := context.Background()
	key := "lock:busy"
	require.NoError(t, client.Set(ctx, key, "owner-a", time.Minute).Err())

	waitCtx, cancel := context.WithTimeout(ctx, 25*time.Millisecond)
	defer cancel()
	called := false
	err := redislock.WithRenewableLock(
		waitCtx,
		client,
		key,
		time.Minute,
		20*time.Second,
		func(context.Context) error {
			called = true
			return nil
		},
	)

	require.ErrorIs(t, err, context.DeadlineExceeded)
	require.False(t, called)
}

func TestWithRenewableLockCancelsCallbackAfterOwnershipLoss(t *testing.T) {
	client, _ := newTokenLockTestClient(t)
	key := "lock:lost"

	err := redislock.WithRenewableLock(
		context.Background(),
		client,
		key,
		100*time.Millisecond,
		10*time.Millisecond,
		func(lockCtx context.Context) error {
			require.NoError(t, client.Set(context.Background(), key, "replacement", time.Minute).Err())
			select {
			case <-lockCtx.Done():
				return nil
			case <-time.After(time.Second):
				return errors.New("callback was not cancelled")
			}
		},
	)

	require.Error(t, err)
	require.Contains(t, err.Error(), "ownership lost")
	require.Equal(t, "replacement", client.Get(context.Background(), key).Val())
}

func TestWithRenewableLockReportsOwnershipLossAtRelease(t *testing.T) {
	client, _ := newTokenLockTestClient(t)
	key := "lock:lost-before-renewal"

	err := redislock.WithRenewableLock(
		context.Background(),
		client,
		key,
		time.Minute,
		20*time.Second,
		func(context.Context) error {
			return client.Set(context.Background(), key, "replacement", time.Minute).Err()
		},
	)

	require.Error(t, err)
	require.Contains(t, err.Error(), "ownership lost")
	require.Equal(t, "replacement", client.Get(context.Background(), key).Val())
}
