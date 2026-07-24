package service

import (
	"context"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

func TestWikiMapCacheRejectsCorruptOrEmptyValues(t *testing.T) {
	ctx := context.Background()
	mr, err := miniredis.Run()
	require.NoError(t, err)
	t.Cleanup(mr.Close)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	svc := &wikiIngestService{redisClient: rdb}

	require.NoError(t, rdb.Set(ctx, "missing-ts", `{"text":"{}"}`, 0).Err())
	_, ok := svc.getCachedWikiMapText(ctx, "missing-ts")
	require.False(t, ok)

	require.NoError(t, rdb.Set(ctx, "empty", `{"text":"","cached_at":1}`, 0).Err())
	_, ok = svc.getCachedWikiMapText(ctx, "empty")
	require.False(t, ok)

	require.NoError(t, rdb.Set(ctx, "valid", `{"text":"{}","cached_at":1}`, 0).Err())
	got, ok := svc.getCachedWikiMapText(ctx, "valid")
	require.True(t, ok)
	require.Equal(t, "{}", got)
}
