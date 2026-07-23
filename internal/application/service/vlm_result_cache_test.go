package service

import (
	"context"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

func newTestVLMCache(t *testing.T) (*vlmResultCache, *miniredis.Miniredis) {
	t.Helper()
	mr := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	return newVLMResultCache(client), mr
}

func TestVLMResultCacheHitAfterSet(t *testing.T) {
	cache, _ := newTestVLMCache(t)
	ctx := context.Background()

	key := vlmCacheKey("img-hash", "model-1", "ocr prompt")
	if _, hit := cache.Get(ctx, key); hit {
		t.Fatal("cold cache must miss")
	}

	cache.Set(ctx, key, "extracted text")
	got, hit := cache.Get(ctx, key)
	if !hit || got != "extracted text" {
		t.Fatalf("expected hit with frozen text, got hit=%v text=%q", hit, got)
	}
}

func TestVLMResultCacheFreezesEmptyResult(t *testing.T) {
	// A legitimately empty OCR result ("no text in image") must be a HIT so
	// the VLM is never re-consulted for a known-empty image.
	cache, _ := newTestVLMCache(t)
	ctx := context.Background()

	key := vlmCacheKey("img-hash", "model-1", "ocr prompt")
	cache.Set(ctx, key, "")
	got, hit := cache.Get(ctx, key)
	if !hit || got != "" {
		t.Fatalf("cached empty result must hit with empty text, got hit=%v text=%q", hit, got)
	}
}

func TestVLMResultCacheEntriesExpire(t *testing.T) {
	cache, mr := newTestVLMCache(t)
	ctx := context.Background()

	key := vlmCacheKey("img-hash", "model-1", "prompt")
	cache.Set(ctx, key, "text")
	mr.FastForward(31 * 24 * time.Hour)
	if _, hit := cache.Get(ctx, key); hit {
		t.Fatal("entry must expire after TTL")
	}
}

func TestVLMResultCacheNilSafe(t *testing.T) {
	// Lite mode: no Redis. Every operation must silently no-op.
	var cache *vlmResultCache
	ctx := context.Background()
	if _, hit := cache.Get(ctx, "any"); hit {
		t.Fatal("nil cache must always miss")
	}
	cache.Set(ctx, "any", "text") // must not panic

	if c := newVLMResultCache(nil); c != nil {
		t.Fatal("nil redis client must produce a nil cache")
	}
}

func TestVLMCacheKeyLayeredInvalidation(t *testing.T) {
	base := vlmCacheKey("img-hash", "model-1", "prompt v1")

	if got := vlmCacheKey("other-image", "model-1", "prompt v1"); got == base {
		t.Fatal("different image bytes must derive a different key")
	}
	if got := vlmCacheKey("img-hash", "model-2", "prompt v1"); got == base {
		t.Fatal("different VLM model must derive a different key")
	}
	if got := vlmCacheKey("img-hash", "model-1", "prompt v2"); got == base {
		t.Fatal("different prompt (version/language/instructions) must derive a different key")
	}
	if got := vlmCacheKey("img-hash", "model-1", "prompt v1"); got != base {
		t.Fatal("identical inputs must derive the same key")
	}
}

func TestVLMModelCacheKey(t *testing.T) {
	// New-style config uses the DB model ID directly.
	if got := vlmModelCacheKey(types.VLMConfig{ModelID: "model-42"}); got != "model-42" {
		t.Fatalf("expected model ID passthrough, got %q", got)
	}

	// Legacy inline config derives a stable fingerprint from routing fields.
	legacy := types.VLMConfig{ModelName: "qwen-vl", BaseURL: "http://host:11434", InterfaceType: "ollama"}
	a := vlmModelCacheKey(legacy)
	b := vlmModelCacheKey(legacy)
	if a != b {
		t.Fatalf("legacy fingerprint must be stable: %s vs %s", a, b)
	}

	// Rotating the API key must NOT invalidate the cache (output unchanged).
	withKey := legacy
	withKey.APIKey = "rotated-secret"
	if got := vlmModelCacheKey(withKey); got != a {
		t.Fatal("API key rotation must not change the cache identity")
	}

	// Pointing at a different model/endpoint MUST invalidate.
	other := legacy
	other.ModelName = "llava"
	if got := vlmModelCacheKey(other); got == a {
		t.Fatal("different legacy model must derive a different cache identity")
	}
}

func TestHashImageBytes(t *testing.T) {
	a := hashImageBytes([]byte{1, 2, 3})
	b := hashImageBytes([]byte{1, 2, 3})
	c := hashImageBytes([]byte{1, 2, 4})
	if a != b {
		t.Fatal("identical bytes must hash identically")
	}
	if a == c {
		t.Fatal("different bytes must hash differently")
	}
	if len(a) != 64 {
		t.Fatalf("expected hex sha256 (64 chars), got %d", len(a))
	}
}
