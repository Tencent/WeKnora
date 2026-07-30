package service

import (
	"context"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

func TestGraphExtractCacheStoresEmptyResults(t *testing.T) {
	ctx := context.Background()
	mr, err := miniredis.Run()
	require.NoError(t, err)
	t.Cleanup(mr.Close)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })

	svc := &ChunkExtractService{redisClient: rdb}
	svc.setCachedGraphExtract(ctx, "empty-graph", &types.GraphData{})

	got, ok := svc.getCachedGraphExtract(ctx, "empty-graph")
	require.True(t, ok)
	require.NotNil(t, got)
	require.Empty(t, got.Node)
	require.Empty(t, got.Relation)
}

func TestGraphExtractCacheRejectsMissingTimestamp(t *testing.T) {
	ctx := context.Background()
	mr, err := miniredis.Run()
	require.NoError(t, err)
	t.Cleanup(mr.Close)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })

	require.NoError(t, rdb.Set(ctx, "missing-ts", `{"graph":{}}`, 0).Err())
	svc := &ChunkExtractService{redisClient: rdb}

	_, ok := svc.getCachedGraphExtract(ctx, "missing-ts")
	require.False(t, ok)
}
