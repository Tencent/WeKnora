package embedding

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
)

// Set WEKNORA_TEST_REDIS_ADDR to run against a dedicated Redis instance.
// Cleanup only touches the unique prefix allocated by this test.
func TestResultCacheRedisIntegration(t *testing.T) {
	address := os.Getenv("WEKNORA_TEST_REDIS_ADDR")
	if address == "" {
		t.Skip("WEKNORA_TEST_REDIS_ADDR is not configured")
	}
	t.Setenv("WEKNORA_EMBEDDING_CACHE_ENABLED", "true")
	t.Setenv("WEKNORA_EMBEDDING_CACHE_PREFIX", fmt.Sprintf("weknora:test:embedding:%d", time.Now().UnixNano()))
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	clients := []*redis.Client{
		redis.NewClient(&redis.Options{Addr: address}),
		redis.NewClient(&redis.Options{Addr: address}),
	}
	for _, client := range clients {
		t.Cleanup(func() { _ = client.Close() })
		if err := client.Ping(ctx).Err(); err != nil {
			t.Fatal(err)
		}
	}
	key := CacheKey(42, CacheFingerprint(testCacheConfig()), "redis integration")
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), time.Second)
		defer cleanupCancel()
		_ = clients[0].Del(cleanupCtx, key, key+":lock").Err()
	})
	provider := &cacheTestEmbedder{}
	disabled := WrapResultCache(provider, noopResultCache{}, testCacheConfig(), 42)
	for range 2 {
		if _, err := disabled.Embed(ctx, "redis integration"); err != nil {
			t.Fatal(err)
		}
	}
	for _, client := range clients {
		model := WrapResultCache(provider, NewEmbeddingResultCache(client), testCacheConfig(), 42)
		if _, err := model.Embed(ctx, "redis integration"); err != nil {
			t.Fatal(err)
		}
	}
	if provider.embedCalls != 3 {
		t.Fatalf("disabled/cold/warm provider calls = %d, want 2 + 1 + 0", provider.embedCalls)
	}
	if ttl := clients[0].PTTL(ctx, key).Val(); ttl <= 0 || ttl > defaultEmbeddingCacheTTL {
		t.Fatalf("unexpected Redis TTL: %s", ttl)
	}
	if err := clients[0].Set(ctx, key, "invalid vector", time.Minute).Err(); err != nil {
		t.Fatal(err)
	}
	model := WrapResultCache(provider, NewEmbeddingResultCache(clients[1]), testCacheConfig(), 42)
	if _, err := model.Embed(ctx, "redis integration"); err != nil {
		t.Fatal(err)
	}
	if provider.embedCalls != 4 {
		t.Fatal("corrupt Redis entry was not recomputed")
	}
	t.Log("Redis disabled/cold/warm: provider calls 2/1/0; TTL and corruption recovery verified")
}
