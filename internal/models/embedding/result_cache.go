package embedding

import (
	"container/list"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/redis/go-redis/v9"
	"golang.org/x/sync/singleflight"
)

const (
	embeddingCacheKeyVersion   = "v1"
	embeddingCacheValueVersion = byte(1)
	defaultEmbeddingCacheTTL   = 168 * time.Hour
	defaultEmbeddingCacheSize  = 2000
)

// ResultCache stores one embedding vector for a deterministic cache key.
// Implementations must treat a missing key as (nil, false, nil). Cache errors
// are intentionally separated from misses so callers can fail open.
type ResultCache interface {
	Get(ctx context.Context, key string) ([]float32, bool, error)
	Set(ctx context.Context, key string, vector []float32, ttl time.Duration) error
}

// BatchResultCache is an optional optimization for stores that can read and
// write multiple keys in one round trip. Wrappers fall back to ResultCache
// when a backend does not implement it.
type BatchResultCache interface {
	ResultCache
	GetMany(ctx context.Context, keys []string) (map[string][]float32, error)
	SetMany(ctx context.Context, entries map[string][]float32, ttl time.Duration) error
}

type deletableResultCache interface {
	Delete(ctx context.Context, key string) error
}

type noopResultCache struct{}

func (noopResultCache) Get(context.Context, string) ([]float32, bool, error)        { return nil, false, nil }
func (noopResultCache) Set(context.Context, string, []float32, time.Duration) error { return nil }
func (noopResultCache) GetMany(context.Context, []string) (map[string][]float32, error) {
	return map[string][]float32{}, nil
}

func (noopResultCache) SetMany(context.Context, map[string][]float32, time.Duration) error {
	return nil
}
func (noopResultCache) Delete(context.Context, string) error { return nil }

// NewEmbeddingResultCache selects the shared cache used by model instances.
// Redis is preferred when configured; otherwise Lite mode uses a bounded
// process-local LRU cache so a single-node deployment does not need Redis.
func NewEmbeddingResultCache(redisClient *redis.Client) ResultCache {
	if !embeddingCacheEnabled() {
		logger.Infof(context.Background(), "[EmbeddingCache] disabled")
		return noopResultCache{}
	}
	if redisClient != nil {
		logger.Infof(context.Background(), "[EmbeddingCache] enabled backend=redis ttl=%s", resolveEmbeddingCacheTTL())
		return &redisResultCache{client: redisClient, metrics: &cacheMetrics{}}
	}
	logger.Infof(context.Background(), "[EmbeddingCache] enabled backend=memory ttl=%s max_entries=%d",
		resolveEmbeddingCacheTTL(), resolveEmbeddingCacheSize())
	return newMemoryResultCache(resolveEmbeddingCacheSize())
}

func embeddingCacheEnabled() bool {
	value := strings.TrimSpace(os.Getenv("WEKNORA_EMBEDDING_CACHE_ENABLED"))
	if value == "" {
		return false
	}
	enabled, err := strconv.ParseBool(value)
	return err == nil && enabled
}

func resolveEmbeddingCacheTTL() time.Duration {
	value := strings.TrimSpace(os.Getenv("WEKNORA_EMBEDDING_CACHE_TTL"))
	if value == "" {
		return defaultEmbeddingCacheTTL
	}
	if duration, err := time.ParseDuration(value); err == nil && duration > 0 {
		return duration
	}
	return defaultEmbeddingCacheTTL
}

func resolveEmbeddingCacheSize() int {
	value := strings.TrimSpace(os.Getenv("WEKNORA_EMBEDDING_CACHE_MEMORY_MAX_ENTRIES"))
	if value == "" {
		return defaultEmbeddingCacheSize
	}
	size, err := strconv.Atoi(value)
	if err != nil || size < 1 {
		return defaultEmbeddingCacheSize
	}
	return size
}

type redisResultCache struct {
	client  *redis.Client
	metrics *cacheMetrics
}

func (r *redisResultCache) Get(ctx context.Context, key string) ([]float32, bool, error) {
	if r == nil || r.client == nil {
		return nil, false, nil
	}
	payload, err := r.client.Get(ctx, key).Bytes()
	if err == redis.Nil {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	vector, err := decodeEmbedding(payload)
	if err != nil {
		if r.metrics != nil {
			r.metrics.invalidEntries.Add(1)
		}
		// A malformed value is treated as a miss. Deletion is best effort so a
		// corrupt entry cannot poison every subsequent request for the key.
		_ = r.client.Del(ctx, key).Err()
		return nil, false, err
	}
	if !validEmbedding(vector, 0) {
		if r.metrics != nil {
			r.metrics.invalidEntries.Add(1)
		}
		_ = r.client.Del(ctx, key).Err()
		return nil, false, embeddingPayloadError()
	}
	return vector, true, nil
}

func (r *redisResultCache) Set(ctx context.Context, key string, vector []float32, ttl time.Duration) error {
	if r == nil || r.client == nil || len(vector) == 0 {
		return nil
	}
	payload, err := encodeEmbedding(vector)
	if err != nil {
		return err
	}
	return r.client.Set(ctx, key, payload, ttl).Err()
}

func (r *redisResultCache) Delete(ctx context.Context, key string) error {
	if r == nil || r.client == nil {
		return nil
	}
	return r.client.Del(ctx, key).Err()
}

func (r *redisResultCache) GetMany(ctx context.Context, keys []string) (map[string][]float32, error) {
	result := make(map[string][]float32)
	if r == nil || r.client == nil || len(keys) == 0 {
		return result, nil
	}
	values, err := r.client.MGet(ctx, keys...).Result()
	if err != nil {
		return nil, err
	}
	for index, value := range values {
		if value == nil {
			continue
		}
		var payload []byte
		switch typed := value.(type) {
		case string:
			payload = []byte(typed)
		case []byte:
			payload = typed
		default:
			continue
		}
		vector, decodeErr := decodeEmbedding(payload)
		if decodeErr != nil {
			if r.metrics != nil {
				r.metrics.invalidEntries.Add(1)
			}
			_ = r.client.Del(ctx, keys[index]).Err()
			continue
		}
		if !validEmbedding(vector, 0) {
			if r.metrics != nil {
				r.metrics.invalidEntries.Add(1)
			}
			_ = r.client.Del(ctx, keys[index]).Err()
			continue
		}
		result[keys[index]] = vector
	}
	return result, nil
}

func (r *redisResultCache) SetMany(ctx context.Context, entries map[string][]float32, ttl time.Duration) error {
	if r == nil || r.client == nil || len(entries) == 0 {
		return nil
	}
	pipe := r.client.Pipeline()
	for key, vector := range entries {
		if len(vector) == 0 {
			continue
		}
		payload, err := encodeEmbedding(vector)
		if err != nil {
			continue
		}
		pipe.Set(ctx, key, payload, ttl)
	}
	_, err := pipe.Exec(ctx)
	return err
}

type memoryResultCache struct {
	mu         sync.Mutex
	maxEntries int
	items      map[string]*list.Element
	order      *list.List
	metrics    *cacheMetrics
}

type memoryCacheEntry struct {
	key       string
	vector    []float32
	expiresAt time.Time
}

func newMemoryResultCache(maxEntries int) ResultCache {
	if maxEntries < 1 {
		maxEntries = defaultEmbeddingCacheSize
	}
	return &memoryResultCache{
		maxEntries: maxEntries,
		items:      make(map[string]*list.Element, maxEntries),
		order:      list.New(),
		metrics:    &cacheMetrics{},
	}
}

func (r *redisResultCache) cacheMetrics() *cacheMetrics {
	if r == nil {
		return nil
	}
	return r.metrics
}

func (m *memoryResultCache) cacheMetrics() *cacheMetrics {
	if m == nil {
		return nil
	}
	return m.metrics
}

func (m *memoryResultCache) Get(_ context.Context, key string) ([]float32, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	element, ok := m.items[key]
	if !ok {
		return nil, false, nil
	}
	entry := element.Value.(*memoryCacheEntry)
	if !entry.expiresAt.IsZero() && time.Now().After(entry.expiresAt) {
		m.removeElement(element)
		return nil, false, nil
	}
	m.order.MoveToFront(element)
	return cloneEmbedding(entry.vector), true, nil
}

func (m *memoryResultCache) Set(_ context.Context, key string, vector []float32, ttl time.Duration) error {
	if len(vector) == 0 {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	expiresAt := time.Time{}
	if ttl > 0 {
		expiresAt = time.Now().Add(ttl)
	}
	if element, ok := m.items[key]; ok {
		entry := element.Value.(*memoryCacheEntry)
		entry.vector = cloneEmbedding(vector)
		entry.expiresAt = expiresAt
		m.order.MoveToFront(element)
		return nil
	}
	element := m.order.PushFront(&memoryCacheEntry{
		key:       key,
		vector:    cloneEmbedding(vector),
		expiresAt: expiresAt,
	})
	m.items[key] = element
	for len(m.items) > m.maxEntries {
		m.removeElement(m.order.Back())
	}
	return nil
}

func (m *memoryResultCache) Delete(_ context.Context, key string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if element, ok := m.items[key]; ok {
		m.removeElement(element)
	}
	return nil
}

func (m *memoryResultCache) GetMany(ctx context.Context, keys []string) (map[string][]float32, error) {
	result := make(map[string][]float32, len(keys))
	for _, key := range keys {
		if vector, ok, err := m.Get(ctx, key); err == nil && ok {
			result[key] = vector
		}
	}
	return result, nil
}

func (m *memoryResultCache) SetMany(ctx context.Context, entries map[string][]float32, ttl time.Duration) error {
	for key, vector := range entries {
		if err := m.Set(ctx, key, vector, ttl); err != nil {
			return err
		}
	}
	return nil
}

func (m *memoryResultCache) removeElement(element *list.Element) {
	if element == nil {
		return
	}
	entry := element.Value.(*memoryCacheEntry)
	delete(m.items, entry.key)
	m.order.Remove(element)
}

// CacheIdentity captures all model settings that can change an embedding
// result. Secrets are deliberately excluded; the resulting fingerprint is
// safe to use in logs and Redis keys.
type CacheIdentity struct {
	Version                   string            `json:"version"`
	ModelID                   string            `json:"model_id"`
	ModelName                 string            `json:"model_name"`
	Source                    string            `json:"source"`
	Provider                  string            `json:"provider"`
	BaseURL                   string            `json:"base_url"`
	ModelVersion              time.Time         `json:"model_version"`
	Dimensions                int               `json:"dimensions"`
	TruncatePromptTokens      int               `json:"truncate_prompt_tokens"`
	SupportsDimensionOverride bool              `json:"supports_dimension_override"`
	ExtraConfig               map[string]string `json:"extra_config,omitempty"`
}

// CacheFingerprint returns a stable model fingerprint without exposing
// API keys or custom request headers in the cache key.
func CacheFingerprint(config Config) string {
	identity := CacheIdentity{
		Version:                   embeddingCacheKeyVersion,
		ModelID:                   config.ModelID,
		ModelName:                 config.ModelName,
		Source:                    string(config.Source),
		Provider:                  config.Provider,
		BaseURL:                   config.BaseURL,
		ModelVersion:              config.ModelVersion,
		Dimensions:                config.Dimensions,
		TruncatePromptTokens:      config.TruncatePromptTokens,
		SupportsDimensionOverride: config.SupportsDimensionOverride,
		ExtraConfig:               config.ExtraConfig,
	}
	payload, _ := json.Marshal(identity)
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:])
}

// CacheKey builds a tenant-scoped key. Text is hashed so raw user
// content is never persisted in Redis or exposed through operational tools.
func CacheKey(tenantID uint64, modelFingerprint, text string) string {
	tenantHash := sha256.Sum256([]byte("tenant:" + strconv.FormatUint(tenantID, 10)))
	textHash := sha256.Sum256([]byte(text))
	return embeddingCachePrefix() + ":" +
		hex.EncodeToString(tenantHash[:8]) + ":" + modelFingerprint + ":" + hex.EncodeToString(textHash[:])
}

func embeddingCachePrefix() string {
	prefix := strings.TrimSpace(os.Getenv("WEKNORA_EMBEDDING_CACHE_PREFIX"))
	if prefix == "" || !strings.HasPrefix(prefix, "weknora:") ||
		strings.ContainsAny(prefix, " \t\r\n") || strings.Contains(prefix, "*") {
		return "weknora:embedding:" + embeddingCacheKeyVersion
	}
	return strings.TrimSuffix(prefix, ":")
}

type resultCacheEmbedder struct {
	inner            Embedder
	cache            ResultCache
	modelFingerprint string
	tenantID         uint64
	ttl              time.Duration
	group            singleflight.Group
	metrics          *cacheMetrics
	inflightMu       sync.Mutex
	inflight         map[string]int
	lockClient       redis.UniversalClient
	lockLease        time.Duration
	lockWait         time.Duration
	lockRenewal      time.Duration
}

// WrapResultCache adds best-effort result reuse around an existing Embedder.
// Cache failures fail open and never change the Provider result or error.
func WrapResultCache(inner Embedder, cache ResultCache, config Config, tenantID uint64) Embedder {
	if inner == nil || cache == nil || tenantID == 0 {
		return inner
	}
	if _, disabled := cache.(noopResultCache); disabled {
		return inner
	}
	lockLease, lockWait, lockRenewal := embeddingCacheLockSettings()
	var lockClient redis.UniversalClient
	if embeddingDistributedLockEnabled() {
		lockClient = cacheLockClient(cache)
	}
	return &resultCacheEmbedder{
		inner:            inner,
		cache:            cache,
		modelFingerprint: CacheFingerprint(config),
		tenantID:         tenantID,
		ttl:              resolveEmbeddingCacheTTL(),
		metrics:          ensureCacheMetrics(cacheMetricsFor(cache)),
		inflight:         make(map[string]int),
		lockClient:       lockClient,
		lockLease:        lockLease,
		lockWait:         lockWait,
		lockRenewal:      lockRenewal,
	}
}

func ensureCacheMetrics(metrics *cacheMetrics) *cacheMetrics {
	if metrics == nil {
		return &cacheMetrics{}
	}
	return metrics
}

func cacheMetricsFor(cache ResultCache) *cacheMetrics {
	if provider, ok := cache.(interface{ cacheMetrics() *cacheMetrics }); ok {
		return provider.cacheMetrics()
	}
	return nil
}

func (w *resultCacheEmbedder) Embed(ctx context.Context, text string) ([]float32, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	w.metrics.requests.Add(1)
	key := CacheKey(w.tenantID, w.modelFingerprint, text)
	if vector, ok := w.get(ctx, key); ok {
		w.metrics.hitItems.Add(1)
		return vector, nil
	}
	w.metrics.missItems.Add(1)
	release := w.trackSingleflight(key)
	defer release()
	value, err := w.coalesce(ctx, key, func() (interface{}, error) {
		return w.runMiss(ctx, key, func(callCtx context.Context) (interface{}, error) {
			if vector, ok := w.get(callCtx, key); ok {
				return vector, nil
			}
			w.metrics.providerRequests.Add(1)
			w.metrics.providerItems.Add(1)
			// Losing a cache lock can prevent a cache write, but must not
			// cancel the caller's provider request.
			vector, err := w.inner.Embed(ctx, text)
			if err != nil {
				return nil, err
			}
			w.store(callCtx, key, vector)
			return vector, nil
		})
	})
	if err != nil {
		return nil, err
	}
	return cloneEmbedding(value.([]float32)), nil
}

func (w *resultCacheEmbedder) BatchEmbed(ctx context.Context, texts []string) ([][]float32, error) {
	return w.batchEmbed(ctx, texts, w.inner.BatchEmbed)
}

func (w *resultCacheEmbedder) batchEmbed(
	ctx context.Context, texts []string,
	embed func(context.Context, []string) ([][]float32, error),
) ([][]float32, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	w.metrics.requests.Add(1)
	if len(texts) == 0 {
		return embed(ctx, texts)
	}
	result := make([][]float32, len(texts))
	misses := make([]string, 0, len(texts))
	missIndexes := make(map[string][]int, len(texts))
	keyByText := make(map[string]string, len(texts))
	keys := make([]string, 0, len(texts))
	for _, text := range texts {
		if _, seen := keyByText[text]; seen {
			continue
		}
		key := CacheKey(w.tenantID, w.modelFingerprint, text)
		keyByText[text] = key
		keys = append(keys, key)
	}
	cachedByKey := w.getMany(ctx, keys)
	for index, text := range texts {
		if vector, ok := cachedByKey[keyByText[text]]; ok {
			w.metrics.hitItems.Add(1)
			result[index] = cloneEmbedding(vector)
			continue
		}
		w.metrics.missItems.Add(1)
		if _, seen := missIndexes[text]; !seen {
			misses = append(misses, text)
		}
		missIndexes[text] = append(missIndexes[text], index)
	}
	if len(misses) == 0 {
		return result, nil
	}
	batchKey := batchCacheKey(w.tenantID, w.modelFingerprint, misses)
	release := w.trackSingleflight(batchKey)
	defer release()
	value, err := w.coalesce(ctx, batchKey, func() (interface{}, error) {
		return w.runMiss(ctx, batchKey, func(callCtx context.Context) (interface{}, error) {
			keys := make([]string, 0, len(misses))
			for _, text := range misses {
				keys = append(keys, CacheKey(w.tenantID, w.modelFingerprint, text))
			}
			cached := w.getMany(callCtx, keys)
			vectors := make([][]float32, len(misses))
			remaining := make([]string, 0, len(misses))
			remainingIndexes := make([]int, 0, len(misses))
			for index, key := range keys {
				if vector, found := cached[key]; found {
					vectors[index] = vector
				} else {
					remaining = append(remaining, misses[index])
					remainingIndexes = append(remainingIndexes, index)
				}
			}
			if len(remaining) == 0 {
				return vectors, nil
			}
			w.metrics.providerRequests.Add(1)
			w.metrics.providerItems.Add(int64(len(remaining)))
			fresh, err := embed(ctx, remaining)
			if err != nil {
				return nil, err
			}
			if len(fresh) != len(remaining) {
				return nil, fmt.Errorf("embedding cache: provider returned %d embeddings for %d inputs",
					len(fresh), len(remaining))
			}
			entries := make(map[string][]float32, len(remaining))
			for i, index := range remainingIndexes {
				vectors[index] = fresh[i]
				entries[keys[index]] = fresh[i]
			}
			// Publish before releasing the Redis lock so waiters can reuse the result.
			w.storeMany(callCtx, entries)
			return vectors, nil
		})
	})
	if err != nil {
		return nil, err
	}
	vectors, ok := value.([][]float32)
	if !ok {
		return nil, fmt.Errorf("embedding cache: invalid provider batch result")
	}
	if len(vectors) != len(misses) {
		return nil, fmt.Errorf("embedding cache: provider returned %d embeddings for %d inputs",
			len(vectors), len(misses))
	}
	for missIndex, text := range misses {
		vector := vectors[missIndex]
		for _, originalIndex := range missIndexes[text] {
			result[originalIndex] = cloneEmbedding(vector)
		}
	}
	return result, nil
}

func (w *resultCacheEmbedder) runMiss(
	ctx context.Context,
	key string,
	provider func(context.Context) (interface{}, error),
) (interface{}, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if w.lockClient == nil {
		return provider(ctx)
	}
	value, err := withEmbeddingRedisLock(ctx, w.lockClient, key, w.lockLease, w.lockWait, w.lockRenewal, provider)
	if err == nil {
		return value, nil
	}
	if errors.Is(err, errEmbeddingLockWaitTimeout) || errors.Is(err, errEmbeddingLockUnavailable) {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		w.metrics.lockWaits.Add(1)
		// Cache coordination is best effort. A Redis outage or a bounded wait
		// must never turn an Embedding request into a service outage.
		return provider(ctx)
	}
	return value, err
}

func (w *resultCacheEmbedder) trackSingleflight(key string) func() {
	w.inflightMu.Lock()
	if w.inflight[key] > 0 {
		w.metrics.singleflightWaits.Add(1)
	}
	w.inflight[key]++
	w.inflightMu.Unlock()
	return func() {
		w.inflightMu.Lock()
		defer w.inflightMu.Unlock()
		w.inflight[key]--
		if w.inflight[key] == 0 {
			delete(w.inflight, key)
		}
	}
}

func (w *resultCacheEmbedder) coalesce(
	ctx context.Context, key string, fn func() (interface{}, error),
) (interface{}, error) {
	result := w.group.DoChan(key, fn)
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case value := <-result:
		return value.Val, value.Err
	}
}

func batchCacheKey(tenantID uint64, modelFingerprint string, texts []string) string {
	hash := sha256.New()
	_, _ = hash.Write([]byte("batch:"))
	_, _ = hash.Write([]byte(strconv.FormatUint(tenantID, 10)))
	_, _ = hash.Write([]byte{0})
	_, _ = hash.Write([]byte(modelFingerprint))
	for _, text := range texts {
		_, _ = hash.Write([]byte{0})
		var length [8]byte
		binary.BigEndian.PutUint64(length[:], uint64(len(text)))
		_, _ = hash.Write(length[:])
		_, _ = hash.Write([]byte(text))
	}
	return embeddingCachePrefix() + ":batch:" + hex.EncodeToString(hash.Sum(nil))
}

func (w *resultCacheEmbedder) BatchEmbedWithPool(ctx context.Context, _ Embedder, texts []string) ([][]float32, error) {
	// Filter and deduplicate the whole batch before the existing pool splits it.
	// The original provider wrappers still govern every actual provider call.
	return w.batchEmbed(ctx, texts, func(ctx context.Context, misses []string) ([][]float32, error) {
		return w.inner.BatchEmbedWithPool(ctx, w.inner, misses)
	})
}

func (w *resultCacheEmbedder) GetModelName() string { return w.inner.GetModelName() }
func (w *resultCacheEmbedder) GetDimensions() int   { return w.inner.GetDimensions() }
func (w *resultCacheEmbedder) GetModelID() string   { return w.inner.GetModelID() }

func (w *resultCacheEmbedder) get(ctx context.Context, key string) ([]float32, bool) {
	vector, ok, err := w.cache.Get(ctx, key)
	if err != nil {
		w.metrics.getErrors.Add(1)
		return nil, false
	}
	if !ok {
		return nil, false
	}
	if !validEmbedding(vector, w.inner.GetDimensions()) {
		w.metrics.invalidEntries.Add(1)
		if deletable, ok := w.cache.(deletableResultCache); ok {
			_ = deletable.Delete(ctx, key)
		}
		return nil, false
	}
	return cloneEmbedding(vector), true
}

func (w *resultCacheEmbedder) getMany(ctx context.Context, keys []string) map[string][]float32 {
	if batchCache, ok := w.cache.(BatchResultCache); ok {
		values, err := batchCache.GetMany(ctx, keys)
		if err == nil {
			result := make(map[string][]float32, len(values))
			for key, vector := range values {
				if validEmbedding(vector, w.inner.GetDimensions()) {
					result[key] = cloneEmbedding(vector)
				} else if deletable, deletableOK := w.cache.(deletableResultCache); deletableOK {
					w.metrics.invalidEntries.Add(1)
					_ = deletable.Delete(ctx, key)
				}
			}
			return result
		}
		w.metrics.getErrors.Add(1)
	}
	result := make(map[string][]float32, len(keys))
	for _, key := range keys {
		if vector, ok := w.get(ctx, key); ok {
			result[key] = vector
		}
	}
	return result
}

func (w *resultCacheEmbedder) store(ctx context.Context, key string, vector []float32) {
	if ctx.Err() != nil {
		return
	}
	if !validEmbedding(vector, w.inner.GetDimensions()) {
		w.metrics.invalidEntries.Add(1)
		return
	}
	if err := w.cache.Set(ctx, key, vector, w.ttl); err != nil {
		w.metrics.setErrors.Add(1)
	}
}

func (w *resultCacheEmbedder) storeMany(ctx context.Context, entries map[string][]float32) {
	if ctx.Err() != nil {
		return
	}
	valid := make(map[string][]float32, len(entries))
	for key, vector := range entries {
		if validEmbedding(vector, w.inner.GetDimensions()) {
			valid[key] = vector
		} else {
			w.metrics.invalidEntries.Add(1)
		}
	}
	if len(valid) == 0 {
		return
	}
	if batchCache, ok := w.cache.(BatchResultCache); ok {
		if err := batchCache.SetMany(ctx, valid, w.ttl); err == nil {
			return
		}
		w.metrics.setErrors.Add(1)
	}
	for key, vector := range valid {
		if err := w.cache.Set(ctx, key, vector, w.ttl); err != nil {
			w.metrics.setErrors.Add(1)
		}
	}
}

func validEmbedding(vector []float32, dimensions int) bool {
	if len(vector) == 0 || (dimensions > 0 && len(vector) != dimensions) {
		return false
	}
	for _, value := range vector {
		if math.IsNaN(float64(value)) || math.IsInf(float64(value), 0) {
			return false
		}
	}
	return true
}

func cloneEmbedding(vector []float32) []float32 {
	if vector == nil {
		return nil
	}
	clone := make([]float32, len(vector))
	copy(clone, vector)
	return clone
}

func encodeEmbedding(vector []float32) ([]byte, error) {
	payload := make([]byte, 5+len(vector)*4)
	payload[0] = embeddingCacheValueVersion
	binary.BigEndian.PutUint32(payload[1:5], uint32(len(vector)))
	for index, value := range vector {
		binary.BigEndian.PutUint32(payload[5+index*4:], float32Bits(value))
	}
	return payload, nil
}

func decodeEmbedding(payload []byte) ([]float32, error) {
	if len(payload) < 5 || payload[0] != embeddingCacheValueVersion || (len(payload)-5)%4 != 0 {
		return nil, embeddingPayloadError()
	}
	dimensions := int(binary.BigEndian.Uint32(payload[1:5]))
	if dimensions <= 0 || len(payload) != 5+dimensions*4 {
		return nil, embeddingPayloadError()
	}
	vector := make([]float32, dimensions)
	for index := range vector {
		vector[index] = float32FromBits(binary.BigEndian.Uint32(payload[5+index*4:]))
	}
	return vector, nil
}

// These small helpers keep the cache format independent from architecture
// endianness while avoiding unsafe pointer conversions.
func float32Bits(value float32) uint32     { return math.Float32bits(value) }
func float32FromBits(value uint32) float32 { return math.Float32frombits(value) }

func embeddingPayloadError() error {
	return fmt.Errorf("embedding cache: invalid vector payload")
}

var _ Embedder = (*resultCacheEmbedder)(nil)
