package embedding

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"math"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/models/provider"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/redis/go-redis/v9"
)

const (
	embeddingCacheVersion       = "v1"
	defaultEmbeddingCachePrefix = "weknora:embedding:v1"
	defaultEmbeddingCacheTTL    = 30 * 24 * time.Hour
)

// EmbeddingCache is the deliberately small storage contract needed by the
// embedding decorator. Values are opaque versioned binary payloads.
//
//nolint:revive // Public name retained for embedding API compatibility.
type EmbeddingCache interface {
	GetMany(ctx context.Context, keys []string) (map[string][]byte, error)
	SetMany(ctx context.Context, values map[string][]byte, ttl time.Duration) error
}

// EmbeddingCacheConfig controls the process-wide embedding cache. It is loaded
// in one place so environment reads do not leak into business services.
//
//nolint:revive // Public name retained for embedding API compatibility.
type EmbeddingCacheConfig struct {
	Enabled bool
	TTL     time.Duration
	Prefix  string
}

// LoadEmbeddingCacheConfigFromEnv follows the existing startup-env pattern.
// Invalid values fail safe: caching stays disabled or the documented default
// TTL is retained, while the caller receives warnings to log.
func LoadEmbeddingCacheConfigFromEnv() (EmbeddingCacheConfig, []error) {
	config := EmbeddingCacheConfig{
		TTL:    defaultEmbeddingCacheTTL,
		Prefix: defaultEmbeddingCachePrefix,
	}
	var warnings []error

	if raw := strings.TrimSpace(os.Getenv("EMBEDDING_CACHE_ENABLED")); raw != "" {
		enabled, err := strconv.ParseBool(raw)
		if err != nil {
			warnings = append(warnings, fmt.Errorf("invalid EMBEDDING_CACHE_ENABLED %q: %w", raw, err))
		} else {
			config.Enabled = enabled
		}
	}
	if raw := strings.TrimSpace(os.Getenv("EMBEDDING_CACHE_TTL")); raw != "" {
		ttl, err := time.ParseDuration(raw)
		if err != nil || ttl < 0 {
			if err == nil {
				err = fmt.Errorf("duration must not be negative")
			}
			warnings = append(warnings, fmt.Errorf("invalid EMBEDDING_CACHE_TTL %q: %w", raw, err))
		} else {
			config.TTL = ttl
		}
	}
	if prefix := strings.TrimSpace(os.Getenv("EMBEDDING_CACHE_PREFIX")); prefix != "" {
		config.Prefix = strings.TrimSuffix(prefix, ":")
	}
	return config, warnings
}

var embeddingCacheRuntime struct {
	sync.RWMutex
	cache  EmbeddingCache
	config EmbeddingCacheConfig
}

// ConfigureEmbeddingCache installs the cache used by subsequently-created
// embedders. A nil cache disables decoration even when config.Enabled is true.
func ConfigureEmbeddingCache(cache EmbeddingCache, config EmbeddingCacheConfig) {
	if config.Prefix == "" {
		config.Prefix = defaultEmbeddingCachePrefix
	}
	embeddingCacheRuntime.Lock()
	embeddingCacheRuntime.cache = cache
	embeddingCacheRuntime.config = config
	embeddingCacheRuntime.Unlock()
}

func configuredEmbeddingCache() (EmbeddingCache, EmbeddingCacheConfig) {
	embeddingCacheRuntime.RLock()
	defer embeddingCacheRuntime.RUnlock()
	return embeddingCacheRuntime.cache, embeddingCacheRuntime.config
}

// RedisEmbeddingCache adapts the application's shared Redis client.
type RedisEmbeddingCache struct{ client *redis.Client }

// NewRedisEmbeddingCache creates an embedding cache backed by Redis.
func NewRedisEmbeddingCache(client *redis.Client) EmbeddingCache {
	if client == nil {
		return nil
	}
	return &RedisEmbeddingCache{client: client}
}

// GetMany retrieves cached embedding payloads by key.
func (c *RedisEmbeddingCache) GetMany(ctx context.Context, keys []string) (map[string][]byte, error) {
	if len(keys) == 0 {
		return map[string][]byte{}, nil
	}
	values, err := c.client.MGet(ctx, keys...).Result()
	if err != nil {
		return nil, err
	}
	result := make(map[string][]byte, len(values))
	for index, value := range values {
		if value == nil {
			continue
		}
		switch typed := value.(type) {
		case string:
			result[keys[index]] = []byte(typed)
		case []byte:
			result[keys[index]] = append([]byte(nil), typed...)
		default:
			return nil, fmt.Errorf("embedding cache returned unsupported value type %T", value)
		}
	}
	return result, nil
}

// SetMany stores embedding payloads with a shared TTL.
func (c *RedisEmbeddingCache) SetMany(ctx context.Context, values map[string][]byte, ttl time.Duration) error {
	if len(values) == 0 {
		return nil
	}
	pipe := c.client.Pipeline()
	for key, value := range values {
		pipe.Set(ctx, key, value, ttl)
	}
	_, err := pipe.Exec(ctx)
	return err
}

// EmbeddingCacheStats is a process-wide, race-safe snapshot. Every decorator
// shares the same collector because model instances are recreated frequently.
//
//nolint:revive // Public name retained for embedding API compatibility.
type EmbeddingCacheStats struct {
	EmbeddingRequests uint64 `json:"embedding_requests"`
	EmbeddingInputs   uint64 `json:"embedding_inputs"`
	CacheHits         uint64 `json:"cache_hits"`
	CacheMisses       uint64 `json:"cache_misses"`
	ProviderInputs    uint64 `json:"provider_inputs"`
	CacheReadErrors   uint64 `json:"cache_read_errors"`
	CacheWriteErrors  uint64 `json:"cache_write_errors"`
}

type embeddingCacheStatsCollector struct {
	embeddingRequests atomic.Uint64
	embeddingInputs   atomic.Uint64
	cacheHits         atomic.Uint64
	cacheMisses       atomic.Uint64
	providerInputs    atomic.Uint64
	cacheReadErrors   atomic.Uint64
	cacheWriteErrors  atomic.Uint64
}

var sharedEmbeddingCacheStats = &embeddingCacheStatsCollector{}

func (s *embeddingCacheStatsCollector) add(summary cacheRequestSummary) {
	s.embeddingRequests.Add(1)
	s.embeddingInputs.Add(uint64(summary.inputs))
	s.cacheHits.Add(uint64(summary.hits))
	s.cacheMisses.Add(uint64(summary.misses))
	s.providerInputs.Add(uint64(summary.providerInputs))
	if summary.readError {
		s.cacheReadErrors.Add(1)
	}
	if summary.writeError {
		s.cacheWriteErrors.Add(1)
	}
}

func (s *embeddingCacheStatsCollector) snapshot() EmbeddingCacheStats {
	return EmbeddingCacheStats{
		EmbeddingRequests: s.embeddingRequests.Load(),
		EmbeddingInputs:   s.embeddingInputs.Load(),
		CacheHits:         s.cacheHits.Load(),
		CacheMisses:       s.cacheMisses.Load(),
		ProviderInputs:    s.providerInputs.Load(),
		CacheReadErrors:   s.cacheReadErrors.Load(),
		CacheWriteErrors:  s.cacheWriteErrors.Load(),
	}
}

func (s *embeddingCacheStatsCollector) reset() {
	s.embeddingRequests.Store(0)
	s.embeddingInputs.Store(0)
	s.cacheHits.Store(0)
	s.cacheMisses.Store(0)
	s.providerInputs.Store(0)
	s.cacheReadErrors.Store(0)
	s.cacheWriteErrors.Store(0)
}

// GetEmbeddingCacheStats returns the shared process-lifetime counters.
func GetEmbeddingCacheStats() EmbeddingCacheStats { return sharedEmbeddingCacheStats.snapshot() }

// ResetEmbeddingCacheStats is intended for deterministic tests and benchmark
// runs. Production code normally lets the counters live for the process life.
func ResetEmbeddingCacheStats() { sharedEmbeddingCacheStats.reset() }

type embeddingCacheBypassContextKey struct{}

// WithEmbeddingCacheBypass marks provider connection/debug probes. It bypasses
// cache reads and writes while preserving the existing inner wrapper chain.
func WithEmbeddingCacheBypass(ctx context.Context) context.Context {
	return context.WithValue(ctx, embeddingCacheBypassContextKey{}, true)
}

func embeddingCacheBypassed(ctx context.Context) bool {
	bypassed, _ := ctx.Value(embeddingCacheBypassContextKey{}).(bool)
	return bypassed
}

type cachingEmbedder struct {
	inner       Embedder
	cache       EmbeddingCache
	config      EmbeddingCacheConfig
	modelConfig Config
	stats       *embeddingCacheStatsCollector
}

func wrapEmbeddingCache(inner Embedder, modelConfig Config) Embedder {
	cache, cacheConfig := configuredEmbeddingCache()
	if inner == nil || cache == nil || !cacheConfig.Enabled {
		return inner
	}
	return newCachingEmbedder(inner, cache, modelConfig, cacheConfig, sharedEmbeddingCacheStats)
}

func newCachingEmbedder(
	inner Embedder,
	cache EmbeddingCache,
	modelConfig Config,
	config EmbeddingCacheConfig,
	stats *embeddingCacheStatsCollector,
) Embedder {
	return &cachingEmbedder{inner: inner, cache: cache, modelConfig: modelConfig, config: config, stats: stats}
}

func (c *cachingEmbedder) Embed(ctx context.Context, text string) ([]float32, error) {
	if embeddingCacheBypassed(ctx) {
		return c.inner.Embed(ctx, text)
	}
	return c.embed(ctx, text)
}

func (c *cachingEmbedder) BatchEmbed(ctx context.Context, texts []string) ([][]float32, error) {
	if embeddingCacheBypassed(ctx) {
		return c.inner.BatchEmbed(ctx, texts)
	}
	return c.batch(ctx, "batch", texts, func(misses []string) ([][]float32, error) {
		return c.inner.BatchEmbed(ctx, misses)
	})
}

func (c *cachingEmbedder) BatchEmbedWithPool(ctx context.Context, _ Embedder, texts []string) ([][]float32, error) {
	if embeddingCacheBypassed(ctx) {
		return c.inner.BatchEmbedWithPool(ctx, c.inner, texts)
	}
	// Resolve partial hits before entering the pool. Passing c.inner prevents
	// pool sub-batches from recursively re-entering this cache decorator.
	return c.batch(ctx, "pool", texts, func(misses []string) ([][]float32, error) {
		return c.inner.BatchEmbedWithPool(ctx, c.inner, misses)
	})
}

func (c *cachingEmbedder) GetModelName() string { return c.inner.GetModelName() }
func (c *cachingEmbedder) GetDimensions() int   { return c.inner.GetDimensions() }
func (c *cachingEmbedder) GetModelID() string   { return c.inner.GetModelID() }

type cacheRequestSummary struct {
	requestType    string
	inputs         int
	hits           int
	misses         int
	providerInputs int
	readError      bool
	writeError     bool
	duration       time.Duration
}

func (c *cachingEmbedder) embed(ctx context.Context, text string) ([]float32, error) {
	started := time.Now()
	summary := cacheRequestSummary{requestType: "embed", inputs: 1}
	defer func() {
		summary.duration = time.Since(started)
		c.record(ctx, summary)
	}()

	key := c.key(ctx, text)
	cached, err := c.cache.GetMany(ctx, []string{key})
	if err != nil {
		summary.readError = true
		logger.Warnf(ctx, "[EmbeddingCache] read failed; falling back to provider: %v", err)
	} else if payload, found := cached[key]; found {
		vector, decodeErr := decodeEmbedding(payload, c.expectedDimensions())
		if decodeErr == nil {
			summary.hits = 1
			return vector, nil
		}
		logger.Warnf(ctx, "[EmbeddingCache] invalid cached vector; falling back to provider: %v", decodeErr)
	}

	summary.misses = 1
	summary.providerInputs = 1
	vector, err := c.inner.Embed(ctx, text)
	if err != nil {
		return nil, err
	}
	if err := c.cache.SetMany(ctx, map[string][]byte{key: encodeEmbedding(vector)}, c.config.TTL); err != nil {
		summary.writeError = true
		logger.Warnf(ctx, "[EmbeddingCache] write failed; provider result preserved: %v", err)
	}
	return vector, nil
}

func (c *cachingEmbedder) batch(
	ctx context.Context,
	requestType string,
	texts []string,
	providerCall func([]string) ([][]float32, error),
) ([][]float32, error) {
	started := time.Now()
	summary := cacheRequestSummary{requestType: requestType, inputs: len(texts)}
	defer func() {
		summary.duration = time.Since(started)
		c.record(ctx, summary)
	}()
	if len(texts) == 0 {
		return [][]float32{}, nil
	}

	type uniqueInput struct {
		key       string
		text      string
		positions []int
	}
	unique := make([]*uniqueInput, 0, len(texts))
	byKey := make(map[string]*uniqueInput, len(texts))
	keys := make([]string, 0, len(texts))
	for position, text := range texts {
		key := c.key(ctx, text)
		item, exists := byKey[key]
		if !exists {
			item = &uniqueInput{key: key, text: text}
			byKey[key] = item
			unique = append(unique, item)
			keys = append(keys, key)
		}
		item.positions = append(item.positions, position)
	}

	cached, err := c.cache.GetMany(ctx, keys)
	if err != nil {
		summary.readError = true
		logger.Warnf(ctx, "[EmbeddingCache] batch read failed; falling back to provider: %v", err)
		cached = nil
	}

	results := make([][]float32, len(texts))
	misses := make([]*uniqueInput, 0, len(unique))
	for _, item := range unique {
		if payload, found := cached[item.key]; found {
			vector, decodeErr := decodeEmbedding(payload, c.expectedDimensions())
			if decodeErr == nil {
				for _, position := range item.positions {
					results[position] = vector
					summary.hits++
				}
				continue
			}
			logger.Warnf(ctx, "[EmbeddingCache] invalid cached vector; falling back to provider: %v", decodeErr)
		}
		misses = append(misses, item)
		summary.misses += len(item.positions)
	}

	providerInputs := make([]string, len(misses))
	for index, item := range misses {
		providerInputs[index] = item.text
	}
	summary.providerInputs = len(providerInputs)
	if len(providerInputs) == 0 {
		return results, nil
	}

	vectors, err := providerCall(providerInputs)
	if err != nil {
		return nil, err
	}
	if len(vectors) != len(providerInputs) {
		return nil, fmt.Errorf(
			"embedding model returned %d embeddings for %d inputs", len(vectors), len(providerInputs),
		)
	}

	writes := make(map[string][]byte, len(misses))
	for index, item := range misses {
		vector := vectors[index]
		writes[item.key] = encodeEmbedding(vector)
		for _, position := range item.positions {
			results[position] = vector
		}
	}
	if err := c.cache.SetMany(ctx, writes, c.config.TTL); err != nil {
		summary.writeError = true
		logger.Warnf(ctx, "[EmbeddingCache] batch write failed; provider result preserved: %v", err)
	}
	return results, nil
}

func (c *cachingEmbedder) expectedDimensions() int { return c.inner.GetDimensions() }

func (c *cachingEmbedder) key(ctx context.Context, text string) string {
	modelHash := embeddingModelFingerprint(c.modelConfig, embeddingRequestMode(ctx))
	textHash := sha256.Sum256([]byte(text))
	return strings.TrimSuffix(c.config.Prefix, ":") + ":" + modelHash + ":" + hex.EncodeToString(textHash[:])
}

func (c *cachingEmbedder) record(ctx context.Context, summary cacheRequestSummary) {
	c.stats.add(summary)
	// Publish the cache accounting to the outermost usage wrapper so it can
	// record cache hits/misses/provider inputs on the logical usage row.
	if span := spanFromContext(ctx); span != nil {
		s := summary
		span.cacheSummary = &s
	}
	hitRate := float64(0)
	if summary.inputs > 0 {
		hitRate = float64(summary.hits) / float64(summary.inputs)
	}
	logger.Infof(ctx, "[EmbeddingCache] request_type=%s model_id=%s embedding_inputs=%d cache_hits=%d "+
		"cache_misses=%d provider_inputs=%d cache_hit_rate=%.4f cache_read_error=%t "+
		"cache_write_error=%t duration_ms=%d",
		summary.requestType, c.inner.GetModelID(), summary.inputs, summary.hits, summary.misses,
		summary.providerInputs, hitRate, summary.readError, summary.writeError, summary.duration.Milliseconds())
}

func embeddingRequestMode(ctx context.Context) string {
	if query, _ := ctx.Value(types.EmbedQueryContextKey).(bool); query {
		return "query"
	}
	return "document"
}

func embeddingModelFingerprint(config Config, requestMode string) string {
	h := sha256.New()
	writeFingerprintField := func(name, value string) {
		_, _ = fmt.Fprintf(h, "%d:%s=%d:%s\n", len(name), name, len(value), value)
	}

	resolvedProvider := provider.ProviderName(config.Provider)
	if resolvedProvider == "" {
		resolvedProvider = provider.DetectProvider(config.BaseURL)
	}
	writeFingerprintField("cache_version", embeddingCacheVersion)
	writeFingerprintField("model_id", config.ModelID)
	writeFingerprintField("source", string(config.Source))
	writeFingerprintField("provider", string(resolvedProvider))
	writeFingerprintField("model_name", config.ModelName)
	writeFingerprintField("base_url", strings.TrimRight(config.BaseURL, "/"))
	writeFingerprintField("dimensions", strconv.Itoa(config.Dimensions))
	writeFingerprintField("supports_dimension_override", strconv.FormatBool(config.SupportsDimensionOverride))
	writeFingerprintField("truncate_prompt_tokens", strconv.Itoa(config.TruncatePromptTokens))
	// Only include ExtraConfig fields that the embedding providers actually
	// consume. ExtraConfig is a generic model bag and may contain credentials
	// for other model types; hashing every entry would make credential rotation
	// invalidate this cache identity even though the embedding request is
	// unchanged.
	for _, key := range []string{"api_version", "remote_model_name"} {
		if value, ok := config.ExtraConfig[key]; ok {
			writeFingerprintField("extra_config."+key, value)
		}
	}
	if len(config.CustomHeaders) > 0 {
		headerHash := canonicalMapHash(config.CustomHeaders, true)
		writeFingerprintField("custom_headers_hash", headerHash)
	}
	writeFingerprintField("request_mode", requestMode)
	return hex.EncodeToString(h.Sum(nil))
}

func writeCanonicalMap(write func(string, string), prefix string, values map[string]string, lowerKeys bool) {
	type pair struct{ key, value string }
	pairs := make([]pair, 0, len(values))
	for key, value := range values {
		if lowerKeys {
			key = strings.ToLower(key)
		}
		pairs = append(pairs, pair{key: key, value: value})
	}
	sort.Slice(pairs, func(i, j int) bool {
		if pairs[i].key == pairs[j].key {
			return pairs[i].value < pairs[j].value
		}
		return pairs[i].key < pairs[j].key
	})
	for _, item := range pairs {
		write(prefix+"."+item.key, item.value)
	}
}

func canonicalMapHash(values map[string]string, lowerKeys bool) string {
	h := sha256.New()
	writeCanonicalMap(func(name, value string) {
		_, _ = fmt.Fprintf(h, "%d:%s=%d:%s\n", len(name), name, len(value), value)
	}, "header", values, lowerKeys)
	return hex.EncodeToString(h.Sum(nil))
}

func encodeEmbedding(vector []float32) []byte {
	payload := make([]byte, 4+len(vector)*4)
	binary.LittleEndian.PutUint32(payload[:4], uint32(len(vector)))
	for index, value := range vector {
		binary.LittleEndian.PutUint32(payload[4+index*4:], math.Float32bits(value))
	}
	return payload
}

func decodeEmbedding(payload []byte, expectedDimensions int) ([]float32, error) {
	if len(payload) < 4 {
		return nil, fmt.Errorf("embedding cache payload too short: %d", len(payload))
	}
	dimensions := int(binary.LittleEndian.Uint32(payload[:4]))
	expectedLength := 4 + dimensions*4
	if expectedLength < 4 || len(payload) != expectedLength {
		return nil, fmt.Errorf(
			"embedding cache payload length %d does not match dimension %d", len(payload), dimensions,
		)
	}
	if expectedDimensions > 0 && dimensions != expectedDimensions {
		return nil, fmt.Errorf(
			"embedding cache dimension %d does not match model dimension %d",
			dimensions, expectedDimensions,
		)
	}
	vector := make([]float32, dimensions)
	for index := range vector {
		vector[index] = math.Float32frombits(binary.LittleEndian.Uint32(payload[4+index*4:]))
	}
	return vector, nil
}
