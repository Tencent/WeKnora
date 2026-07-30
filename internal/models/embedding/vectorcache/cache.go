package vectorcache

import (
	"container/list"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/redis/go-redis/v9"
)

const (
	defaultTTL        = 30 * 24 * time.Hour
	defaultMaxEntries = 10000
)

// Cache stores content-addressed embedding vectors. Keys already include
// tenant and model configuration scope and must be treated as opaque.
type Cache interface {
	GetMany(ctx context.Context, keys []string) (map[string][]float32, error)
	SetMany(ctx context.Context, values map[string][]float32) error
}

// EmbedFunc computes vectors for cache misses only.
type EmbedFunc func(context.Context, []string) ([][]float32, error)

// Stats describes one Resolve call.
type Stats struct {
	Inputs         int
	Unique         int
	Hits           int
	Misses         int
	ProviderInputs int
	Coalesced      int
	ReadError      error
	WriteError     error
	MissSamples    []MissSample
}

// MissSample identifies a cache miss without exposing source text.
type MissSample struct {
	Index int
	Hash  string
	Runes int
}

type memoryEntry struct {
	vector    []float32
	expiresAt time.Time
	element   *list.Element
}

// hybridCache uses Redis when available and always keeps a bounded in-process
// cache as a fast path and Lite-mode fallback.
type hybridCache struct {
	redis      *redis.Client
	ttl        time.Duration
	maxEntries int

	mu     sync.Mutex
	values map[string]*memoryEntry
	order  *list.List
	now    func() time.Time
}

type flightEntry struct {
	done   chan struct{}
	vector []float32
	err    error
}

var vectorFlights = struct {
	sync.Mutex
	entries map[string]*flightEntry
}{entries: make(map[string]*flightEntry)}

type disabledCache struct{}

func (disabledCache) GetMany(_ context.Context, _ []string) (map[string][]float32, error) {
	return map[string][]float32{}, nil
}

func (disabledCache) SetMany(_ context.Context, _ map[string][]float32) error { return nil }

// New constructs a shared cache. WEKNORA_EMBEDDING_CACHE_TTL accepts a Go
// duration (for example "720h" or "0" to disable Redis expiry).
// WEKNORA_EMBEDDING_CACHE_MAX_ENTRIES controls the bounded in-process cache.
func New(redisClient *redis.Client) Cache {
	if raw := strings.TrimSpace(strings.ToLower(os.Getenv("WEKNORA_EMBEDDING_CACHE_ENABLED"))); raw == "false" || raw == "0" || raw == "off" {
		return disabledCache{}
	}
	ttl := defaultTTL
	if raw := os.Getenv("WEKNORA_EMBEDDING_CACHE_TTL"); raw != "" {
		if parsed, err := time.ParseDuration(raw); err == nil && parsed >= 0 {
			ttl = parsed
		}
	}
	maxEntries := defaultMaxEntries
	if raw := os.Getenv("WEKNORA_EMBEDDING_CACHE_MAX_ENTRIES"); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil && parsed > 0 {
			maxEntries = parsed
		}
	}
	return &hybridCache{
		redis:      redisClient,
		ttl:        ttl,
		maxEntries: maxEntries,
		values:     make(map[string]*memoryEntry),
		order:      list.New(),
		now:        time.Now,
	}
}

func (c *hybridCache) GetMany(ctx context.Context, keys []string) (map[string][]float32, error) {
	result := make(map[string][]float32, len(keys))
	missing := make([]string, 0, len(keys))

	c.mu.Lock()
	for _, key := range keys {
		if entry, ok := c.values[key]; ok {
			if !entry.expiresAt.IsZero() && !c.now().Before(entry.expiresAt) {
				c.removeMemoryEntry(entry)
				missing = append(missing, key)
				continue
			}
			c.order.MoveToBack(entry.element)
			result[key] = clone(entry.vector)
		} else {
			missing = append(missing, key)
		}
	}
	c.mu.Unlock()

	if c.redis == nil || len(missing) == 0 {
		return result, nil
	}

	rawValues, err := c.redis.MGet(ctx, missing...).Result()
	if err != nil {
		return result, err
	}
	redisHits := make(map[string][]float32)
	for i, raw := range rawValues {
		if raw == nil {
			continue
		}
		var encoded string
		switch value := raw.(type) {
		case string:
			encoded = value
		case []byte:
			encoded = string(value)
		default:
			continue
		}
		var vector []float32
		if err := json.Unmarshal([]byte(encoded), &vector); err != nil || len(vector) == 0 {
			continue
		}
		key := missing[i]
		redisHits[key] = vector
		result[key] = clone(vector)
	}
	c.storeMemory(redisHits)
	return result, nil
}

func (c *hybridCache) SetMany(ctx context.Context, values map[string][]float32) error {
	if len(values) == 0 {
		return nil
	}
	c.storeMemory(values)
	if c.redis == nil {
		return nil
	}

	pipe := c.redis.Pipeline()
	for key, vector := range values {
		encoded, err := json.Marshal(vector)
		if err != nil {
			return err
		}
		pipe.Set(ctx, key, encoded, c.ttl)
	}
	_, err := pipe.Exec(ctx)
	return err
}

func (c *hybridCache) storeMemory(values map[string][]float32) {
	if len(values) == 0 {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	for key, value := range values {
		if len(value) == 0 {
			continue
		}
		expiresAt := time.Time{}
		if c.ttl > 0 {
			expiresAt = c.now().Add(c.ttl)
		}
		if entry, exists := c.values[key]; exists {
			entry.vector = clone(value)
			entry.expiresAt = expiresAt
			c.order.MoveToBack(entry.element)
			continue
		}
		element := c.order.PushBack(key)
		c.values[key] = &memoryEntry{vector: clone(value), expiresAt: expiresAt, element: element}
	}
	for len(c.values) > c.maxEntries && c.order.Len() > 0 {
		oldest := c.order.Front()
		if entry := c.values[oldest.Value.(string)]; entry != nil {
			c.removeMemoryEntry(entry)
		} else {
			c.order.Remove(oldest)
		}
	}
}

func (c *hybridCache) removeMemoryEntry(entry *memoryEntry) {
	if entry == nil || entry.element == nil {
		return
	}
	key, _ := entry.element.Value.(string)
	delete(c.values, key)
	c.order.Remove(entry.element)
}

// Resolve returns vectors in the original input order, reusing cached values
// and embedding each unique miss once. Cache read/write failures fail open;
// provider failures and malformed provider output are returned.
func Resolve(
	ctx context.Context,
	cache Cache,
	keyPrefix string,
	dimensions int,
	texts []string,
	embed EmbedFunc,
) ([][]float32, Stats, error) {
	stats := Stats{Inputs: len(texts)}
	if len(texts) == 0 {
		return [][]float32{}, stats, nil
	}

	keys := make([]string, len(texts))
	uniqueKeys := make([]string, 0, len(texts))
	textByKey := make(map[string]string, len(texts))
	firstIndexByKey := make(map[string]int, len(texts))
	for i, text := range texts {
		hash := sha256.Sum256([]byte(text))
		key := keyPrefix + ":" + hex.EncodeToString(hash[:])
		keys[i] = key
		if _, exists := textByKey[key]; !exists {
			textByKey[key] = text
			firstIndexByKey[key] = i
			uniqueKeys = append(uniqueKeys, key)
		}
	}
	stats.Unique = len(uniqueKeys)

	cached, cacheErr := cache.GetMany(ctx, uniqueKeys)
	stats.ReadError = cacheErr
	if cached == nil {
		cached = make(map[string][]float32, len(uniqueKeys))
	}

	missKeys := make([]string, 0, len(uniqueKeys))
	missTexts := make([]string, 0, len(uniqueKeys))
	for _, key := range uniqueKeys {
		vector, ok := cached[key]
		if ok && (dimensions <= 0 || len(vector) == dimensions) {
			continue
		}
		delete(cached, key)
		missKeys = append(missKeys, key)
		missTexts = append(missTexts, textByKey[key])
		if len(stats.MissSamples) < 8 {
			contentHash := sha256.Sum256([]byte(textByKey[key]))
			stats.MissSamples = append(stats.MissSamples, MissSample{
				Index: firstIndexByKey[key],
				Hash:  hex.EncodeToString(contentHash[:4]),
				Runes: utf8.RuneCountInString(textByKey[key]),
			})
		}
	}
	stats.Misses = len(missKeys)
	stats.Hits = stats.Unique - stats.Misses

	if len(missTexts) > 0 {
		ownedKeys := make([]string, 0, len(missKeys))
		ownedTexts := make([]string, 0, len(missKeys))
		entries := make([]*flightEntry, len(missKeys))

		vectorFlights.Lock()
		for i, key := range missKeys {
			if existing := vectorFlights.entries[key]; existing != nil {
				entries[i] = existing
				stats.Coalesced++
				continue
			}
			entry := &flightEntry{done: make(chan struct{})}
			vectorFlights.entries[key] = entry
			entries[i] = entry
			ownedKeys = append(ownedKeys, key)
			ownedTexts = append(ownedTexts, textByKey[key])
		}
		vectorFlights.Unlock()

		if len(ownedTexts) > 0 {
			stats.ProviderInputs = len(ownedTexts)
			vectors, providerErr := embed(ctx, ownedTexts)
			if providerErr == nil && len(vectors) != len(ownedTexts) {
				providerErr = fmt.Errorf("embedding provider returned %d vectors for %d texts", len(vectors), len(ownedTexts))
			}
			if providerErr == nil {
				for i := range vectors {
					if dimensions > 0 && len(vectors[i]) != dimensions {
						providerErr = fmt.Errorf("embedding provider returned dimension %d, expected %d", len(vectors[i]), dimensions)
						break
					}
				}
			}

			fresh := make(map[string][]float32, len(ownedKeys))
			if providerErr == nil {
				for i, key := range ownedKeys {
					fresh[key] = vectors[i]
				}
				stats.WriteError = cache.SetMany(ctx, fresh)
			}

			vectorFlights.Lock()
			for i, key := range ownedKeys {
				entry := vectorFlights.entries[key]
				if providerErr == nil {
					entry.vector = clone(vectors[i])
				} else {
					entry.err = providerErr
				}
				delete(vectorFlights.entries, key)
				close(entry.done)
			}
			vectorFlights.Unlock()
		}

		for i, entry := range entries {
			select {
			case <-ctx.Done():
				return nil, stats, ctx.Err()
			case <-entry.done:
				if entry.err != nil {
					return nil, stats, entry.err
				}
				cached[missKeys[i]] = clone(entry.vector)
			}
		}
	}

	results := make([][]float32, len(texts))
	for i, key := range keys {
		vector, ok := cached[key]
		if !ok {
			if cacheErr != nil {
				return nil, stats, fmt.Errorf("embedding cache read failed and result %d is missing: %w", i, cacheErr)
			}
			return nil, stats, fmt.Errorf("embedding cache assembly missing result for input %d", i)
		}
		results[i] = clone(vector)
	}
	return results, stats, nil
}

func clone(vector []float32) []float32 {
	return append([]float32(nil), vector...)
}
