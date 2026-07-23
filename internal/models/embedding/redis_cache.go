package embedding

import (
	"context"
	"encoding/binary"
	"math"
	"os"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"
)

const (
	// redisCacheKeyPrefix versions the on-wire encoding; bump v1 -> v2 if the
	// value format ever changes so stale entries are naturally orphaned.
	redisCacheKeyPrefix = "weknora:cache:embedding:v1:"
	// defaultEmbeddingCacheTTL keeps entries long enough to cover typical
	// edit/reparse cycles while still letting unused entries age out.
	defaultEmbeddingCacheTTL = 30 * 24 * time.Hour
)

// redisCacheStore is the Redis-backed CacheStore. Vectors are stored as
// little-endian float32 arrays (4 bytes/dim — ~3x smaller than JSON).
// All operations are best-effort: Redis errors degrade to cache misses.
type redisCacheStore struct {
	client *redis.Client
	ttl    time.Duration
}

// NewRedisCacheStore builds a Redis-backed embedding cache. TTL is
// overridable via EMBEDDING_CACHE_TTL_DAYS (<=0 keeps the default).
// Returns nil when client is nil (Lite mode) so callers can install
// unconditionally.
func NewRedisCacheStore(client *redis.Client) CacheStore {
	if client == nil {
		return nil
	}
	ttl := defaultEmbeddingCacheTTL
	if v := os.Getenv("EMBEDDING_CACHE_TTL_DAYS"); v != "" {
		if days, err := strconv.Atoi(v); err == nil && days > 0 {
			ttl = time.Duration(days) * 24 * time.Hour
		}
	}
	return &redisCacheStore{client: client, ttl: ttl}
}

func (s *redisCacheStore) MGet(ctx context.Context, keys []string) [][]float32 {
	results := make([][]float32, len(keys))
	if len(keys) == 0 {
		return results
	}
	prefixed := make([]string, len(keys))
	for i, k := range keys {
		prefixed[i] = redisCacheKeyPrefix + k
	}
	values, err := s.client.MGet(ctx, prefixed...).Result()
	if err != nil {
		return results // degrade to full miss
	}
	for i, v := range values {
		raw, ok := v.(string)
		if !ok {
			continue
		}
		results[i] = decodeVector([]byte(raw))
	}
	return results
}

func (s *redisCacheStore) MSet(ctx context.Context, keys []string, vectors [][]float32) {
	if len(keys) == 0 || len(keys) != len(vectors) {
		return
	}
	pipe := s.client.Pipeline()
	for i, k := range keys {
		if len(vectors[i]) == 0 {
			continue
		}
		pipe.Set(ctx, redisCacheKeyPrefix+k, encodeVector(vectors[i]), s.ttl)
	}
	_, _ = pipe.Exec(ctx) // best-effort
}

func encodeVector(vec []float32) []byte {
	buf := make([]byte, 4*len(vec))
	for i, f := range vec {
		binary.LittleEndian.PutUint32(buf[i*4:], math.Float32bits(f))
	}
	return buf
}

func decodeVector(b []byte) []float32 {
	if len(b) == 0 || len(b)%4 != 0 {
		return nil
	}
	vec := make([]float32, len(b)/4)
	for i := range vec {
		vec[i] = math.Float32frombits(binary.LittleEndian.Uint32(b[i*4:]))
	}
	return vec
}
