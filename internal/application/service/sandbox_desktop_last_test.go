package service

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

func TestDesktopLastStoreRoundTrip(t *testing.T) {
	store := NewSandboxDesktopLastStore(nil)
	ctx := context.Background()

	got, err := store.Get(ctx, "sess-1")
	require.NoError(t, err)
	require.Empty(t, got)

	require.NoError(t, store.Set(ctx, "sess-1", "sbx-a"))
	got, err = store.Get(ctx, "sess-1")
	require.NoError(t, err)
	require.Equal(t, "sbx-a", got)

	require.NoError(t, store.Set(ctx, "sess-1", "sbx-b"))
	got, err = store.Get(ctx, "sess-1")
	require.NoError(t, err)
	require.Equal(t, "sbx-b", got)
}

func TestDesktopLastStoreRedisRoundTrip(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })

	store := NewSandboxDesktopLastStore(rdb)
	ctx := context.Background()
	require.NoError(t, store.Set(ctx, "sess-1", "sbx-a"))
	got, err := store.Get(ctx, "sess-1")
	require.NoError(t, err)
	require.Equal(t, "sbx-a", got)
}

func TestDesktopLastStoreExpires(t *testing.T) {
	store := newMemoryDesktopLastStore(20 * time.Millisecond)
	ctx := context.Background()
	require.NoError(t, store.Set(ctx, "sess-1", "sbx-a"))
	time.Sleep(40 * time.Millisecond)
	got, err := store.Get(ctx, "sess-1")
	require.NoError(t, err)
	require.Empty(t, got)
}
