package inferencecache

import (
	"container/list"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
	"golang.org/x/sync/singleflight"
)

const (
	cacheKeyVersion   = "v1"
	defaultTTL        = 30 * 24 * time.Hour
	defaultMaxEntries = 10000
)

// Loader performs one expensive inference call. Successful empty responses
// are cacheable; errors are never cached.
type Loader func(context.Context) ([]byte, error)

// Stats describes one cache resolution without exposing cached content.
type Stats struct {
	Hit        bool
	Coalesced  bool
	ReadError  error
	WriteError error
}

// Cache stores validated model outputs by opaque content-addressed keys.
type Cache interface {
	Resolve(ctx context.Context, key string, loader Loader) ([]byte, Stats, error)
	Invalidate(ctx context.Context, key string) error
}

type memoryEntry struct {
	value     []byte
	expiresAt time.Time
	element   *list.Element
}

type hybridCache struct {
	redis      *redis.Client
	ttl        time.Duration
	maxEntries int
	now        func() time.Time

	mu     sync.Mutex
	values map[string]*memoryEntry
	order  *list.List
	group  singleflight.Group
}

type disabledCache struct{}

type flightResult struct {
	value      []byte
	hit        bool
	readError  error
	writeError error
}

// New builds the shared inference cache used by deterministic enrichment
// stages (VLM OCR/caption, GraphRAG extraction, and Wiki map calls).
func New(redisClient *redis.Client) Cache {
	if raw := strings.TrimSpace(strings.ToLower(os.Getenv("WEKNORA_INFERENCE_CACHE_ENABLED"))); raw == "false" || raw == "0" || raw == "off" {
		return disabledCache{}
	}
	ttl := defaultTTL
	if raw := os.Getenv("WEKNORA_INFERENCE_CACHE_TTL"); raw != "" {
		if parsed, err := time.ParseDuration(raw); err == nil && parsed >= 0 {
			ttl = parsed
		}
	}
	maxEntries := defaultMaxEntries
	if raw := os.Getenv("WEKNORA_INFERENCE_CACHE_MAX_ENTRIES"); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil && parsed > 0 {
			maxEntries = parsed
		}
	}
	return &hybridCache{
		redis:      redisClient,
		ttl:        ttl,
		maxEntries: maxEntries,
		now:        time.Now,
		values:     make(map[string]*memoryEntry),
		order:      list.New(),
	}
}

func (disabledCache) Resolve(ctx context.Context, _ string, loader Loader) ([]byte, Stats, error) {
	value, err := loader(ctx)
	return clone(value), Stats{}, err
}

func (disabledCache) Invalidate(context.Context, string) error { return nil }

func (c *hybridCache) Resolve(ctx context.Context, key string, loader Loader) ([]byte, Stats, error) {
	if value, ok, err := c.get(ctx, key); ok {
		return value, Stats{Hit: true}, nil
	} else if err != nil {
		// Fail open. The error is carried into the singleflight result so the
		// caller can log it while still receiving the provider output.
	}

	resultCh := c.group.DoChan(key, func() (any, error) {
		result := flightResult{}
		if value, ok, err := c.get(ctx, key); ok {
			result.value = value
			result.hit = true
			return result, nil
		} else if err != nil {
			result.readError = err
		}

		value, err := loader(ctx)
		if err != nil {
			return nil, err
		}
		result.value = clone(value)
		result.writeError = c.set(ctx, key, value)
		return result, nil
	})

	select {
	case <-ctx.Done():
		return nil, Stats{}, ctx.Err()
	case shared := <-resultCh:
		if shared.Err != nil {
			return nil, Stats{Coalesced: shared.Shared}, shared.Err
		}
		result := shared.Val.(flightResult)
		return clone(result.value), Stats{
			Hit:        result.hit,
			Coalesced:  shared.Shared,
			ReadError:  result.readError,
			WriteError: result.writeError,
		}, nil
	}
}

func (c *hybridCache) Invalidate(ctx context.Context, key string) error {
	c.mu.Lock()
	if entry := c.values[key]; entry != nil {
		c.removeEntry(entry)
	}
	c.mu.Unlock()
	if c.redis == nil {
		return nil
	}
	return c.redis.Del(ctx, key).Err()
}

func (c *hybridCache) get(ctx context.Context, key string) ([]byte, bool, error) {
	c.mu.Lock()
	if entry := c.values[key]; entry != nil {
		if !entry.expiresAt.IsZero() && !c.now().Before(entry.expiresAt) {
			c.removeEntry(entry)
		} else {
			c.order.MoveToBack(entry.element)
			value := clone(entry.value)
			c.mu.Unlock()
			return value, true, nil
		}
	}
	c.mu.Unlock()

	if c.redis == nil {
		return nil, false, nil
	}
	value, err := c.redis.Get(ctx, key).Bytes()
	if err == redis.Nil {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	c.storeMemory(key, value)
	return clone(value), true, nil
}

func (c *hybridCache) set(ctx context.Context, key string, value []byte) error {
	c.storeMemory(key, value)
	if c.redis == nil {
		return nil
	}
	return c.redis.Set(ctx, key, value, c.ttl).Err()
}

func (c *hybridCache) storeMemory(key string, value []byte) {
	c.mu.Lock()
	defer c.mu.Unlock()
	expiresAt := time.Time{}
	if c.ttl > 0 {
		expiresAt = c.now().Add(c.ttl)
	}
	if entry := c.values[key]; entry != nil {
		entry.value = clone(value)
		entry.expiresAt = expiresAt
		c.order.MoveToBack(entry.element)
	} else {
		element := c.order.PushBack(key)
		c.values[key] = &memoryEntry{value: clone(value), expiresAt: expiresAt, element: element}
	}
	for len(c.values) > c.maxEntries {
		oldest := c.order.Front()
		if oldest == nil {
			break
		}
		c.removeEntry(c.values[oldest.Value.(string)])
	}
}

func (c *hybridCache) removeEntry(entry *memoryEntry) {
	if entry == nil || entry.element == nil {
		return
	}
	key, _ := entry.element.Value.(string)
	delete(c.values, key)
	c.order.Remove(entry.element)
}

// Key scopes an inference result to a stage, tenant, model configuration and
// an unambiguous sequence of request parts. Parts are length-prefixed before
// hashing so ["ab", "c"] cannot collide with ["a", "bc"].
func Key(namespace string, tenantID uint64, modelFingerprint string, parts ...[]byte) string {
	hash := sha256.New()
	var length [8]byte
	for _, part := range parts {
		binary.BigEndian.PutUint64(length[:], uint64(len(part)))
		_, _ = hash.Write(length[:])
		_, _ = hash.Write(part)
	}
	namespace = strings.NewReplacer(":", "_", " ", "_").Replace(strings.TrimSpace(namespace))
	return fmt.Sprintf("weknora:inference:%s:%s:%d:%s:%s",
		cacheKeyVersion, namespace, tenantID, modelFingerprint, hex.EncodeToString(hash.Sum(nil)))
}

// Fingerprint hashes deterministic, non-secret model configuration.
func Fingerprint(value any) string {
	encoded, _ := json.Marshal(value)
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:])
}

// ResolveJSON caches only values that the loader has already validated and
// marshalled successfully. Corrupt entries are evicted and recomputed once.
func ResolveJSON[T any](ctx context.Context, cache Cache, key string, loader func(context.Context) (T, error)) (T, Stats, error) {
	var zero T
	if cache == nil {
		value, err := loader(ctx)
		return value, Stats{}, err
	}
	raw, stats, err := cache.Resolve(ctx, key, func(loadCtx context.Context) ([]byte, error) {
		value, loadErr := loader(loadCtx)
		if loadErr != nil {
			return nil, loadErr
		}
		return json.Marshal(value)
	})
	if err != nil {
		return zero, stats, err
	}
	var value T
	if decodeErr := json.Unmarshal(raw, &value); decodeErr != nil {
		// A truncated/corrupt cache entry must not pin the enrichment stage in
		// a permanent failure loop. Remove it and recompute once.
		_ = cache.Invalidate(ctx, key)
		raw, retryStats, retryErr := cache.Resolve(ctx, key, func(loadCtx context.Context) ([]byte, error) {
			loaded, loadErr := loader(loadCtx)
			if loadErr != nil {
				return nil, loadErr
			}
			return json.Marshal(loaded)
		})
		stats.WriteError = retryStats.WriteError
		stats.ReadError = fmt.Errorf("decode inference cache entry: %w", decodeErr)
		stats.Hit = false
		stats.Coalesced = retryStats.Coalesced
		if retryErr != nil {
			return zero, stats, retryErr
		}
		if err := json.Unmarshal(raw, &value); err != nil {
			return zero, stats, fmt.Errorf("decode refreshed inference cache entry: %w", err)
		}
	}
	return value, stats, nil
}

func clone(value []byte) []byte {
	return append([]byte(nil), value...)
}
