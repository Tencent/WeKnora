package embedding

import (
	"container/list"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
)

const (
	defaultEmbeddingCacheTTL        = 24 * time.Hour
	defaultEmbeddingCacheMaxEntries = 10_000

	embeddingCacheEnabledKey    = "embedding_cache_enabled"
	embeddingCacheTTLSecondsKey = "embedding_cache_ttl_seconds"
	embeddingCacheMaxEntriesKey = "embedding_cache_max_entries"
)

type embeddingCacheOptions struct {
	enabled    bool
	ttl        time.Duration
	maxEntries int
}

type embeddingCacheEntry struct {
	key       string
	vector    []float32
	expiresAt time.Time
}

// PersistentCache stores only tenant-scoped hash keys and vectors; raw input
// text is never exposed to the backend.
type PersistentCacheEntry struct {
	Vector    []float32
	ExpiresAt time.Time
}

type PersistentCache interface {
	Get(ctx context.Context, keys []string, now time.Time) (map[string]PersistentCacheEntry, error)
	Put(ctx context.Context, modelID string, vectors map[string][]float32, expiresAt time.Time) error
}

var persistentEmbeddingCache struct {
	sync.RWMutex
	backend PersistentCache
}

// SetPersistentCache installs the process-wide durable cache backend. Passing
// nil disables persistence while retaining the bounded in-memory LRU.
func SetPersistentCache(backend PersistentCache) {
	persistentEmbeddingCache.Lock()
	persistentEmbeddingCache.backend = backend
	persistentEmbeddingCache.Unlock()
}

func getPersistentCache() PersistentCache {
	persistentEmbeddingCache.RLock()
	defer persistentEmbeddingCache.RUnlock()
	return persistentEmbeddingCache.backend
}

// embeddingCacheStore is shared across model instances because modelService
// rebuilds an Embedder for each lookup. Each model/config namespace owns its
// bounded LRU so one model's smaller limit cannot evict another model's cache.
type embeddingCacheStore struct {
	mu       sync.Mutex
	segments map[string]*embeddingCacheSegment
}

type embeddingCacheSegment struct {
	entries map[string]*list.Element
	lru     *list.List
}

var sharedEmbeddingCache = newEmbeddingCacheStore()

func newEmbeddingCacheStore() *embeddingCacheStore {
	return &embeddingCacheStore{
		segments: make(map[string]*embeddingCacheSegment),
	}
}

func (s *embeddingCacheStore) get(namespace, key string, now time.Time) ([]float32, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	segment := s.segments[namespace]
	if segment == nil {
		return nil, false
	}
	element, ok := segment.entries[key]
	if !ok {
		return nil, false
	}
	entry := element.Value.(*embeddingCacheEntry)
	if !now.Before(entry.expiresAt) {
		s.remove(segment, element)
		return nil, false
	}
	segment.lru.MoveToFront(element)
	return cloneVector(entry.vector), true
}

func (s *embeddingCacheStore) put(
	namespace, key string, vector []float32, expiresAt time.Time, maxEntries int,
) {
	if maxEntries <= 0 {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	segment := s.segments[namespace]
	if segment == nil {
		segment = &embeddingCacheSegment{entries: make(map[string]*list.Element), lru: list.New()}
		s.segments[namespace] = segment
	}
	if element, ok := segment.entries[key]; ok {
		entry := element.Value.(*embeddingCacheEntry)
		entry.vector = cloneVector(vector)
		entry.expiresAt = expiresAt
		segment.lru.MoveToFront(element)
	} else {
		entry := &embeddingCacheEntry{key: key, vector: cloneVector(vector), expiresAt: expiresAt}
		element := segment.lru.PushFront(entry)
		segment.entries[key] = element
	}
	for segment.lru.Len() > maxEntries {
		s.remove(segment, segment.lru.Back())
	}
}

func (s *embeddingCacheStore) remove(segment *embeddingCacheSegment, element *list.Element) {
	if element == nil {
		return
	}
	entry := element.Value.(*embeddingCacheEntry)
	delete(segment.entries, entry.key)
	segment.lru.Remove(element)
}

type cachedEmbedder struct {
	inner     Embedder
	namespace string
	options   embeddingCacheOptions
	store     *embeddingCacheStore
	now       func() time.Time
}

func (c *cachedEmbedder) Embed(ctx context.Context, text string) ([]float32, error) {
	key := c.key(ctx, text)
	if vector, ok := c.store.get(c.namespace, key, c.now()); ok {
		c.observeCache(ctx, 1, 1, 0, 0)
		return vector, nil
	}
	if backend := c.persistentBackend(ctx); backend != nil {
		vectors, err := backend.Get(ctx, []string{key}, c.now())
		if err == nil {
			if entry, ok := vectors[key]; ok {
				c.store.put(c.namespace, key, entry.Vector, entry.ExpiresAt, c.options.maxEntries)
				c.observeCache(ctx, 1, 1, 0, 0)
				return cloneVector(entry.Vector), nil
			}
		}
	}
	vector, err := c.inner.Embed(ctx, text)
	c.observeCache(ctx, 1, 0, 1, 0)
	if err != nil {
		return nil, err
	}
	expiresAt := c.now().Add(c.options.ttl)
	c.store.put(c.namespace, key, vector, expiresAt, c.options.maxEntries)
	c.persist(ctx, map[string][]float32{key: vector}, expiresAt)
	return cloneVector(vector), nil
}

func (c *cachedEmbedder) BatchEmbed(ctx context.Context, texts []string) ([][]float32, error) {
	return c.batchEmbed(ctx, texts, func(missing []string) ([][]float32, error) {
		return c.inner.BatchEmbed(ctx, missing)
	})
}

func (c *cachedEmbedder) BatchEmbedWithPool(
	ctx context.Context, _ Embedder, texts []string,
) ([][]float32, error) {
	return c.batchEmbed(ctx, texts, func(missing []string) ([][]float32, error) {
		return c.inner.BatchEmbedWithPool(ctx, c.inner, missing)
	})
}

func (c *cachedEmbedder) batchEmbed(
	ctx context.Context,
	texts []string,
	fetch func([]string) ([][]float32, error),
) ([][]float32, error) {
	results := make([][]float32, len(texts))
	type miss struct {
		key     string
		text    string
		indices []int
	}
	misses := make([]miss, 0, len(texts))
	missByKey := make(map[string]int, len(texts))
	now := c.now()
	hitCount := 0
	deduplicatedCount := 0
	missCount := 0
	defer func() {
		c.observeCache(ctx, len(texts), hitCount, missCount, deduplicatedCount)
	}()

	for index, text := range texts {
		key := c.key(ctx, text)
		if vector, ok := c.store.get(c.namespace, key, now); ok {
			results[index] = vector
			hitCount++
			continue
		}
		if missIndex, ok := missByKey[key]; ok {
			misses[missIndex].indices = append(misses[missIndex].indices, index)
			deduplicatedCount++
			continue
		}
		missByKey[key] = len(misses)
		misses = append(misses, miss{key: key, text: text, indices: []int{index}})
	}
	if backend := c.persistentBackend(ctx); backend != nil && len(misses) > 0 {
		keys := make([]string, len(misses))
		for index := range misses {
			keys[index] = misses[index].key
		}
		if persisted, err := backend.Get(ctx, keys, now); err == nil && len(persisted) > 0 {
			remaining := misses[:0]
			for _, item := range misses {
				entry, ok := persisted[item.key]
				if !ok {
					remaining = append(remaining, item)
					continue
				}
				c.store.put(c.namespace, item.key, entry.Vector, entry.ExpiresAt, c.options.maxEntries)
				// Duplicate positions were already classified as in-batch
				// deduplication above. Count the persisted unique key once so
				// avoided computations never exceeds total lookups.
				hitCount++
				for _, resultIndex := range item.indices {
					results[resultIndex] = cloneVector(entry.Vector)
				}
			}
			misses = remaining
		}
	}
	missCount = len(misses)
	if len(misses) == 0 {
		return results, nil
	}

	missingTexts := make([]string, len(misses))
	for index := range misses {
		missingTexts[index] = misses[index].text
	}
	vectors, err := fetch(missingTexts)
	if err != nil {
		return nil, err
	}
	if len(vectors) != len(misses) {
		return nil, fmt.Errorf("embedding provider returned %d vectors for %d inputs", len(vectors), len(misses))
	}

	expiresAt := c.now().Add(c.options.ttl)
	persistentVectors := make(map[string][]float32, len(misses))
	for missIndex, item := range misses {
		vector := vectors[missIndex]
		c.store.put(c.namespace, item.key, vector, expiresAt, c.options.maxEntries)
		persistentVectors[item.key] = vector
		for _, resultIndex := range item.indices {
			results[resultIndex] = cloneVector(vector)
		}
	}
	c.persist(ctx, persistentVectors, expiresAt)
	return results, nil
}

func (c *cachedEmbedder) observeCache(
	ctx context.Context, lookups, hits, misses, deduplicated int,
) {
	if lookups <= 0 {
		return
	}
	types.DispatchEmbeddingCacheObservation(ctx, types.EmbeddingCacheObservation{
		ModelID: c.inner.GetModelID(), ModelName: c.inner.GetModelName(),
		LookupCount: lookups, HitCount: hits, MissCount: misses,
		DeduplicatedCount: deduplicated, AvoidedComputations: hits + deduplicated,
	})
}

func (c *cachedEmbedder) key(ctx context.Context, text string) string {
	tenantID, _ := types.TenantIDFromContext(ctx)
	mode := "passage"
	if isQuery, _ := ctx.Value(types.EmbedQueryContextKey).(bool); isQuery {
		mode = "query"
	}
	digest := sha256.Sum256([]byte(
		strconv.FormatUint(tenantID, 10) + "\x00" + c.namespace + "\x00" + mode + "\x00" + text,
	))
	return hex.EncodeToString(digest[:])
}

func (c *cachedEmbedder) persistentBackend(ctx context.Context) PersistentCache {
	if tenantID, ok := types.TenantIDFromContext(ctx); !ok || tenantID == 0 {
		return nil
	}
	return getPersistentCache()
}

func (c *cachedEmbedder) persist(
	ctx context.Context, vectors map[string][]float32, expiresAt time.Time,
) {
	if len(vectors) == 0 {
		return
	}
	if backend := c.persistentBackend(ctx); backend != nil {
		// Persistence is best-effort: a cache database failure must never fail
		// an embedding request that the provider already completed.
		_ = backend.Put(context.WithoutCancel(ctx), c.inner.GetModelID(), vectors, expiresAt)
	}
}

func (c *cachedEmbedder) GetModelName() string { return c.inner.GetModelName() }
func (c *cachedEmbedder) GetDimensions() int   { return c.inner.GetDimensions() }
func (c *cachedEmbedder) GetModelID() string   { return c.inner.GetModelID() }

func wrapEmbeddingCache(embedder Embedder, config Config) Embedder {
	options := embeddingCacheOptionsFromConfig(config)
	if embedder == nil || !options.enabled {
		return embedder
	}
	return newCachedEmbedder(embedder, embeddingCacheNamespace(config), options, sharedEmbeddingCache)
}

func newCachedEmbedder(
	embedder Embedder,
	namespace string,
	options embeddingCacheOptions,
	store *embeddingCacheStore,
) Embedder {
	return &cachedEmbedder{
		inner:     embedder,
		namespace: namespace,
		options:   options,
		store:     store,
		now:       time.Now,
	}
}

func embeddingCacheOptionsFromConfig(config Config) embeddingCacheOptions {
	options := embeddingCacheOptions{
		enabled:    true,
		ttl:        defaultEmbeddingCacheTTL,
		maxEntries: defaultEmbeddingCacheMaxEntries,
	}
	if value := strings.TrimSpace(config.ExtraConfig[embeddingCacheEnabledKey]); value != "" {
		if parsed, err := strconv.ParseBool(value); err == nil {
			options.enabled = parsed
		}
	}
	if value := strings.TrimSpace(config.ExtraConfig[embeddingCacheTTLSecondsKey]); value != "" {
		if seconds, err := strconv.ParseInt(value, 10, 64); err == nil && seconds > 0 {
			options.ttl = time.Duration(seconds) * time.Second
		}
	}
	if value := strings.TrimSpace(config.ExtraConfig[embeddingCacheMaxEntriesKey]); value != "" {
		if entries, err := strconv.Atoi(value); err == nil && entries > 0 {
			options.maxEntries = entries
		}
	}
	return options
}

func embeddingCacheNamespace(config Config) string {
	identityParts := []string{
		config.ModelID,
		config.ModelName,
		string(config.Source),
		config.Provider,
		config.BaseURL,
		strconv.Itoa(config.Dimensions),
		strconv.Itoa(config.TruncatePromptTokens),
		strconv.FormatBool(config.SupportsDimensionOverride),
		strconv.Itoa(config.MaxConcurrency),
	}
	identityParts = appendCacheIdentityMap(identityParts, "extra", config.ExtraConfig)
	identityParts = appendCacheIdentityMap(identityParts, "header", config.CustomHeaders)
	identity := strings.Join(identityParts, "\x00")
	digest := sha256.Sum256([]byte(identity))
	return hex.EncodeToString(digest[:])
}

func appendCacheIdentityMap(parts []string, prefix string, values map[string]string) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		if !isSecretCacheIdentityKey(key) {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	for _, key := range keys {
		parts = append(parts, prefix+":"+key, values[key])
	}
	return parts
}

func isSecretCacheIdentityKey(key string) bool {
	normalized := strings.ToLower(strings.TrimSpace(key))
	compact := strings.NewReplacer("_", "", "-", "", ".", "").Replace(normalized)
	return strings.Contains(normalized, "secret") || strings.Contains(normalized, "password") ||
		strings.Contains(normalized, "token") || strings.Contains(normalized, "credential") ||
		strings.Contains(normalized, "authorization") || strings.Contains(normalized, "cookie") ||
		strings.Contains(normalized, "signature") || strings.Contains(compact, "apikey")
}

func cloneVector(vector []float32) []float32 {
	if vector == nil {
		return nil
	}
	return append([]float32(nil), vector...)
}
