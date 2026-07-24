package embedding

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"testing"

	"github.com/redis/go-redis/v9"
)

func redisKeyFor(text, modelID string, dims int) string {
	sum := sha256.Sum256([]byte(text))
	return redisCacheKeyPrefix + fmt.Sprintf("%s:%d:%s", modelID, dims, hex.EncodeToString(sum[:]))
}

// TestRedisCacheVerify exercises the real Redis backend (redis_cache.go) end to
// end. It is gated on REDIS_ADDR; when unset it is a no-op skip so the normal
// suite / CI without Redis is unaffected.
func TestRedisCacheVerify(t *testing.T) {
	addr := os.Getenv("REDIS_ADDR")
	if addr == "" {
		t.Skip("REDIS_ADDR not set; skipping real-Redis verification")
	}
	client := redis.NewClient(&redis.Options{Addr: addr})
	defer client.Close()
	ctx := context.Background()
	if err := client.Ping(ctx).Err(); err != nil {
		t.Fatalf("redis ping failed: %v", err)
	}
	if err := client.FlushDB(ctx).Err(); err != nil {
		t.Fatalf("flushdb failed: %v", err)
	}

	store := NewRedisCacheStore(client)
	if store == nil {
		t.Fatal("NewRedisCacheStore returned nil for non-nil client")
	}
	inner := &countingEmbedder{id: "model-a", dims: 2}
	w := &cacheEmbedder{inner: inner, store: store}

	texts := []string{"alpha", "beta", "gamma"}

	// Cold batch: all miss, provider computes all 3.
	first, err := w.BatchEmbed(ctx, texts)
	if err != nil {
		t.Fatalf("first BatchEmbed: %v", err)
	}
	if got := inner.providerTexts(); len(got) != 3 {
		t.Fatalf("cold batch should hit provider 3x, got %v", got)
	}

	// Verify the vectors actually landed in Redis with a positive TTL.
	size, err := client.DBSize(ctx).Result()
	if err != nil {
		t.Fatalf("dbsize: %v", err)
	}
	if size != 3 {
		t.Fatalf("expected 3 keys in Redis after cold batch, got %d", size)
	}
	for i, txt := range texts {
		key := redisKeyFor(txt, inner.id, inner.dims)
		raw, err := client.Get(ctx, key).Result()
		if err != nil {
			t.Fatalf("redis get %q: %v", key, err)
		}
		got := decodeVector([]byte(raw))
		want := inner.vectorFor(txt)
		if len(got) != len(want) {
			t.Fatalf("redis value for %q wrong dims: %v", txt, got)
		}
		for j := range want {
			if got[j] != want[j] {
				t.Fatalf("redis value for %q mismatch: %v vs %v", txt, got, want)
			}
		}
		ttl, err := client.TTL(ctx, key).Result()
		if err != nil {
			t.Fatalf("ttl: %v", err)
		}
		if ttl <= 0 {
			t.Fatalf("expected positive TTL for %q, got %v", key, ttl)
		}
		_ = first[i]
		t.Logf("redis key %q present, ttl=%v", key, ttl)
	}

	// Warm batch: identical texts => full hit, zero extra provider calls.
	if _, err := w.BatchEmbed(ctx, texts); err != nil {
		t.Fatalf("warm BatchEmbed: %v", err)
	}
	if got := inner.providerTexts(); len(got) != 3 {
		t.Fatalf("warm batch must be full hit, provider saw %v", got)
	}

	// Partial hit: one new text => provider called exactly once more.
	if _, err := w.BatchEmbed(ctx, []string{"alpha", "NEW", "beta"}); err != nil {
		t.Fatalf("partial BatchEmbed: %v", err)
	}
	if got := inner.providerTexts(); len(got) != 4 || got[3] != "NEW" {
		t.Fatalf("partial hit should add exactly NEW, provider saw %v", got)
	}

	// Model isolation: different model ID must not reuse model-a's entries.
	innerB := &countingEmbedder{id: "model-b", dims: 2}
	wB := &cacheEmbedder{inner: innerB, store: store}
	if _, err := wB.BatchEmbed(ctx, texts); err != nil {
		t.Fatalf("model-b BatchEmbed: %v", err)
	}
	if got := innerB.providerTexts(); len(got) != 3 {
		t.Fatalf("model-b must recompute, provider saw %v", got)
	}

	t.Logf("real-Redis verification OK: store=%T, keys verified in Redis", store)
}
