package embedding

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
)

type memoryEmbeddingCache struct {
	mu       sync.Mutex
	values   map[string][]byte
	readErr  error
	writeErr error
	setCalls int
}

func newMemoryEmbeddingCache() *memoryEmbeddingCache {
	return &memoryEmbeddingCache{values: make(map[string][]byte)}
}

func (c *memoryEmbeddingCache) GetMany(_ context.Context, keys []string) (map[string][]byte, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.readErr != nil {
		return nil, c.readErr
	}
	result := make(map[string][]byte)
	for _, key := range keys {
		if value, found := c.values[key]; found {
			result[key] = append([]byte(nil), value...)
		}
	}
	return result, nil
}

func (c *memoryEmbeddingCache) SetMany(_ context.Context, values map[string][]byte, _ time.Duration) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.setCalls++
	if c.writeErr != nil {
		return c.writeErr
	}
	for key, value := range values {
		c.values[key] = append([]byte(nil), value...)
	}
	return nil
}

func (c *memoryEmbeddingCache) put(key string, value []byte) {
	c.mu.Lock()
	c.values[key] = append([]byte(nil), value...)
	c.mu.Unlock()
}

func (c *memoryEmbeddingCache) size() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.values)
}

type countingEmbedder struct {
	mu               sync.Mutex
	modelID          string
	dimensions       int
	embedCalls       int
	batchCalls       int
	poolCalls        int
	batchInputs      [][]string
	providerErr      error
	wrongResultCount bool
}

func (e *countingEmbedder) Embed(_ context.Context, text string) ([]float32, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.embedCalls++
	if e.providerErr != nil {
		return nil, e.providerErr
	}
	return testVector(text, e.dimensions), nil
}

func (e *countingEmbedder) BatchEmbed(_ context.Context, texts []string) ([][]float32, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.batchCalls++
	e.batchInputs = append(e.batchInputs, append([]string(nil), texts...))
	if e.providerErr != nil {
		return nil, e.providerErr
	}
	count := len(texts)
	if e.wrongResultCount && count > 0 {
		count--
	}
	result := make([][]float32, count)
	for index := range result {
		result[index] = testVector(texts[index], e.dimensions)
	}
	return result, nil
}

func (e *countingEmbedder) BatchEmbedWithPool(ctx context.Context, _ Embedder, texts []string) ([][]float32, error) {
	e.mu.Lock()
	e.poolCalls++
	e.mu.Unlock()
	return e.BatchEmbed(ctx, texts)
}

func (e *countingEmbedder) GetModelName() string { return "test-model" }
func (e *countingEmbedder) GetDimensions() int   { return e.dimensions }
func (e *countingEmbedder) GetModelID() string   { return e.modelID }

func testVector(text string, dimensions int) []float32 {
	if dimensions <= 0 {
		dimensions = 2
	}
	vector := make([]float32, dimensions)
	for index := range vector {
		vector[index] = float32(len(text) + index)
	}
	return vector
}

func baseCacheConfig() EmbeddingCacheConfig {
	return EmbeddingCacheConfig{Enabled: true, TTL: time.Hour, Prefix: "test:embedding:v1"}
}

func baseModelConfig() Config {
	return Config{
		Source:                    types.ModelSourceRemote,
		ModelID:                   "model-1",
		Provider:                  "openai",
		ModelName:                 "embedding-model",
		BaseURL:                   "https://provider.example/v1",
		Dimensions:                2,
		SupportsDimensionOverride: true,
		TruncatePromptTokens:      8192,
	}
}

func newTestCachingEmbedder(store EmbeddingCache, provider *countingEmbedder, config Config) *cachingEmbedder {
	provider.modelID = config.ModelID
	provider.dimensions = config.Dimensions
	return newCachingEmbedder(provider, store, config, baseCacheConfig(), sharedEmbeddingCacheStats).(*cachingEmbedder)
}

func resetCacheTestState(t *testing.T) {
	t.Helper()
	ResetEmbeddingCacheStats()
	ConfigureEmbeddingCache(nil, EmbeddingCacheConfig{})
	t.Cleanup(func() {
		ResetEmbeddingCacheStats()
		ConfigureEmbeddingCache(nil, EmbeddingCacheConfig{})
	})
}

func TestCachingEmbedderEmbedMissThenHit(t *testing.T) {
	resetCacheTestState(t)
	store := newMemoryEmbeddingCache()
	provider := &countingEmbedder{}
	cache := newTestCachingEmbedder(store, provider, baseModelConfig())

	first, err := cache.Embed(context.Background(), "hello")
	if err != nil {
		t.Fatal(err)
	}
	second, err := cache.Embed(context.Background(), "hello")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("cached vector = %v, first = %v", second, first)
	}
	if provider.embedCalls != 1 {
		t.Fatalf("provider Embed calls = %d, want 1", provider.embedCalls)
	}
	wantStats := EmbeddingCacheStats{
		EmbeddingRequests: 2, EmbeddingInputs: 2,
		CacheHits: 1, CacheMisses: 1, ProviderInputs: 1,
	}
	if got := GetEmbeddingCacheStats(); got != wantStats {
		t.Fatalf("stats = %+v, want %+v", got, wantStats)
	}
}

func TestCachingEmbedderBatchAllMissPartialHitAllHitAndOrder(t *testing.T) {
	resetCacheTestState(t)
	store := newMemoryEmbeddingCache()
	provider := &countingEmbedder{}
	cache := newTestCachingEmbedder(store, provider, baseModelConfig())

	allMiss, err := cache.BatchEmbed(context.Background(), []string{"A", "C"})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(allMiss, [][]float32{testVector("A", 2), testVector("C", 2)}) {
		t.Fatalf("all-miss vectors = %v", allMiss)
	}
	provider.batchInputs = nil

	inputs := []string{"A", "B", "A", "C", "D"}
	got, err := cache.BatchEmbed(context.Background(), inputs)
	if err != nil {
		t.Fatal(err)
	}
	want := [][]float32{testVector("A", 2), testVector("B", 2), testVector("A", 2), testVector("C", 2), testVector("D", 2)}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("partial-hit vectors = %v, want %v", got, want)
	}
	if !reflect.DeepEqual(provider.batchInputs, [][]string{{"B", "D"}}) {
		t.Fatalf("provider inputs = %v, want [[B D]]", provider.batchInputs)
	}

	if _, err := cache.BatchEmbed(context.Background(), inputs); err != nil {
		t.Fatal(err)
	}
	if len(provider.batchInputs) != 1 {
		t.Fatalf("all-hit batch reached provider: %v", provider.batchInputs)
	}
}

func TestCachingEmbedderBatchDeduplicatesAllMisses(t *testing.T) {
	resetCacheTestState(t)
	provider := &countingEmbedder{}
	cache := newTestCachingEmbedder(newMemoryEmbeddingCache(), provider, baseModelConfig())
	got, err := cache.BatchEmbed(context.Background(), []string{"A", "A", "B"})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(provider.batchInputs, [][]string{{"A", "B"}}) {
		t.Fatalf("provider inputs = %v", provider.batchInputs)
	}
	if !reflect.DeepEqual(got, [][]float32{testVector("A", 2), testVector("A", 2), testVector("B", 2)}) {
		t.Fatalf("vectors = %v", got)
	}
}

func TestCachingEmbedderBatchWithPoolDoesNotReenterCache(t *testing.T) {
	resetCacheTestState(t)
	provider := &countingEmbedder{}
	cache := newTestCachingEmbedder(newMemoryEmbeddingCache(), provider, baseModelConfig())
	inputs := []string{"one", "two", "one"}
	if _, err := cache.BatchEmbedWithPool(context.Background(), cache, inputs); err != nil {
		t.Fatal(err)
	}
	if _, err := cache.BatchEmbedWithPool(context.Background(), cache, inputs); err != nil {
		t.Fatal(err)
	}
	if provider.poolCalls != 1 || !reflect.DeepEqual(provider.batchInputs, [][]string{{"one", "two"}}) {
		t.Fatalf("pool calls=%d, provider inputs=%v", provider.poolCalls, provider.batchInputs)
	}
}

func TestCachingEmbedderProviderCountMismatch(t *testing.T) {
	resetCacheTestState(t)
	provider := &countingEmbedder{wrongResultCount: true}
	cache := newTestCachingEmbedder(newMemoryEmbeddingCache(), provider, baseModelConfig())
	_, err := cache.BatchEmbed(context.Background(), []string{"A", "B"})
	if err == nil || err.Error() != "embedding model returned 1 embeddings for 2 inputs" {
		t.Fatalf("error = %v", err)
	}
}

func TestCachingEmbedderProviderErrorDoesNotWrite(t *testing.T) {
	resetCacheTestState(t)
	store := newMemoryEmbeddingCache()
	provider := &countingEmbedder{providerErr: errors.New("provider down")}
	cache := newTestCachingEmbedder(store, provider, baseModelConfig())
	if _, err := cache.BatchEmbed(context.Background(), []string{"A"}); !errors.Is(err, provider.providerErr) {
		t.Fatalf("error = %v", err)
	}
	if store.setCalls != 0 || store.size() != 0 {
		t.Fatalf("provider error wrote cache: setCalls=%d size=%d", store.setCalls, store.size())
	}
}

func TestCachingEmbedderCacheErrorsFailOpen(t *testing.T) {
	t.Run("read", func(t *testing.T) {
		resetCacheTestState(t)
		store := newMemoryEmbeddingCache()
		store.readErr = errors.New("redis unavailable")
		provider := &countingEmbedder{}
		cache := newTestCachingEmbedder(store, provider, baseModelConfig())
		if _, err := cache.BatchEmbed(context.Background(), []string{"A", "A", "B"}); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(provider.batchInputs, [][]string{{"A", "B"}}) {
			t.Fatalf("provider inputs = %v", provider.batchInputs)
		}
		if GetEmbeddingCacheStats().CacheReadErrors != 1 {
			t.Fatalf("stats = %+v", GetEmbeddingCacheStats())
		}
	})

	t.Run("write", func(t *testing.T) {
		resetCacheTestState(t)
		store := newMemoryEmbeddingCache()
		store.writeErr = errors.New("redis read-only")
		provider := &countingEmbedder{}
		cache := newTestCachingEmbedder(store, provider, baseModelConfig())
		got, err := cache.Embed(context.Background(), "A")
		if err != nil || !reflect.DeepEqual(got, testVector("A", 2)) {
			t.Fatalf("vector=%v error=%v", got, err)
		}
		if GetEmbeddingCacheStats().CacheWriteErrors != 1 {
			t.Fatalf("stats = %+v", GetEmbeddingCacheStats())
		}
	})
}

func TestCachingEmbedderCorruptValueBecomesMissAndIsOverwritten(t *testing.T) {
	resetCacheTestState(t)
	store := newMemoryEmbeddingCache()
	provider := &countingEmbedder{}
	cache := newTestCachingEmbedder(store, provider, baseModelConfig())
	key := cache.key(context.Background(), "A")
	store.put(key, []byte{2, 0, 0, 0, 1})
	got, err := cache.Embed(context.Background(), "A")
	if err != nil || !reflect.DeepEqual(got, testVector("A", 2)) {
		t.Fatalf("vector=%v error=%v", got, err)
	}
	payloads, _ := store.GetMany(context.Background(), []string{key})
	decoded, err := decodeEmbedding(payloads[key], 2)
	if err != nil || !reflect.DeepEqual(decoded, testVector("A", 2)) {
		t.Fatalf("overwritten value=%v error=%v", decoded, err)
	}
}

func TestCachingEmbedderIdentityDifferencesMiss(t *testing.T) {
	tests := []struct {
		name   string
		change func(*Config)
	}{
		{"model ID", func(config *Config) { config.ModelID = "model-2" }},
		{"model config", func(config *Config) { config.ModelName = "embedding-model-v2" }},
		{"dimension", func(config *Config) { config.Dimensions = 3 }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resetCacheTestState(t)
			store := newMemoryEmbeddingCache()
			firstProvider := &countingEmbedder{}
			first := newTestCachingEmbedder(store, firstProvider, baseModelConfig())
			if _, err := first.Embed(context.Background(), "same"); err != nil {
				t.Fatal(err)
			}
			changed := baseModelConfig()
			tt.change(&changed)
			secondProvider := &countingEmbedder{}
			second := newTestCachingEmbedder(store, secondProvider, changed)
			if _, err := second.Embed(context.Background(), "same"); err != nil {
				t.Fatal(err)
			}
			if secondProvider.embedCalls != 1 {
				t.Fatal("identity change reused old cache entry")
			}
		})
	}
}

func TestCachingEmbedderRequestModeDifferenceMisses(t *testing.T) {
	resetCacheTestState(t)
	provider := &countingEmbedder{}
	cache := newTestCachingEmbedder(newMemoryEmbeddingCache(), provider, baseModelConfig())
	if _, err := cache.Embed(context.Background(), "same"); err != nil {
		t.Fatal(err)
	}
	queryCtx := context.WithValue(context.Background(), types.EmbedQueryContextKey, true)
	if _, err := cache.Embed(queryCtx, "same"); err != nil {
		t.Fatal(err)
	}
	if provider.embedCalls != 2 {
		t.Fatalf("provider calls = %d, want 2", provider.embedCalls)
	}
}

func TestEmbeddingModelFingerprintExcludesCredentials(t *testing.T) {
	config := baseModelConfig()
	first := embeddingModelFingerprint(config, "document")
	config.APIKey = "rotated-api-key"
	config.AppID = "rotated-app-id"
	config.AppSecret = "rotated-app-secret"
	config.ExtraConfig = map[string]string{
		"api_key":    "legacy-api-key",
		"app_secret": "legacy-app-secret",
		"secret_key": "legacy-secret-key",
	}
	second := embeddingModelFingerprint(config, "document")
	if first != second {
		t.Fatal("credential rotation changed embedding model fingerprint")
	}
}

func TestEmbeddingModelFingerprintIncludesSemanticExtraConfig(t *testing.T) {
	base := baseModelConfig()
	first := embeddingModelFingerprint(base, "document")

	for _, key := range []string{"api_version", "remote_model_name"} {
		changed := base
		changed.ExtraConfig = map[string]string{key: "changed"}
		if embeddingModelFingerprint(changed, "document") == first {
			t.Fatalf("semantic ExtraConfig %q did not change embedding model fingerprint", key)
		}
	}
}

func TestEmbeddingModelFingerprintCanonicalizesCustomHeaders(t *testing.T) {
	first := baseModelConfig()
	first.CustomHeaders = map[string]string{"X-Route": "blue", "X-Tenant": "one"}
	second := baseModelConfig()
	second.CustomHeaders = map[string]string{"x-tenant": "one", "x-route": "blue"}
	if embeddingModelFingerprint(first, "document") != embeddingModelFingerprint(second, "document") {
		t.Fatal("header order or case changed embedding model fingerprint")
	}
	second.CustomHeaders["x-route"] = "green"
	if embeddingModelFingerprint(first, "document") == embeddingModelFingerprint(second, "document") {
		t.Fatal("routing header change did not change embedding model fingerprint")
	}
}

func TestCachingEmbedderBypassAlwaysCallsProvider(t *testing.T) {
	resetCacheTestState(t)
	provider := &countingEmbedder{}
	cache := newTestCachingEmbedder(newMemoryEmbeddingCache(), provider, baseModelConfig())
	if _, err := cache.Embed(context.Background(), "probe"); err != nil {
		t.Fatal(err)
	}
	if _, err := cache.Embed(WithEmbeddingCacheBypass(context.Background()), "probe"); err != nil {
		t.Fatal(err)
	}
	if provider.embedCalls != 2 {
		t.Fatalf("provider calls = %d, want 2", provider.embedCalls)
	}
}

func TestEmbeddingCacheDisabledAndNilRedisPreserveProvider(t *testing.T) {
	tests := []struct {
		name  string
		store EmbeddingCache
		cfg   EmbeddingCacheConfig
	}{
		{"disabled", newMemoryEmbeddingCache(), EmbeddingCacheConfig{Enabled: false}},
		{"nil redis", NewRedisEmbeddingCache(nil), EmbeddingCacheConfig{Enabled: true}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resetCacheTestState(t)
			ConfigureEmbeddingCache(tt.store, tt.cfg)
			provider := &countingEmbedder{modelID: "model-1", dimensions: 2}
			wrapped := wrapEmbeddingCache(provider, baseModelConfig())
			if _, ok := wrapped.(*cachingEmbedder); ok {
				t.Fatal("cache decorator installed")
			}
			if _, err := wrapped.Embed(context.Background(), "A"); err != nil {
				t.Fatal(err)
			}
			if provider.embedCalls != 1 {
				t.Fatalf("provider calls = %d", provider.embedCalls)
			}
		})
	}
}

func TestEmbeddingBinaryEncoding(t *testing.T) {
	want := []float32{-1.25, 0, 3.5}
	payload := encodeEmbedding(want)
	if len(payload) != 4+len(want)*4 {
		t.Fatalf("payload length = %d", len(payload))
	}
	got, err := decodeEmbedding(payload, len(want))
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("vector=%v error=%v", got, err)
	}
	for _, test := range []struct {
		name     string
		payload  []byte
		expected int
	}{
		{"short", []byte{1, 2, 3}, 0},
		{"length", []byte{2, 0, 0, 0, 1, 2, 3, 4}, 0},
		{"dimension", encodeEmbedding(want), 2},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := decodeEmbedding(test.payload, test.expected); err == nil {
				t.Fatal("invalid payload accepted")
			}
		})
	}
}

func TestEmbeddingCacheStatsSharedAcrossDecorators(t *testing.T) {
	resetCacheTestState(t)
	store := newMemoryEmbeddingCache()
	first := newTestCachingEmbedder(store, &countingEmbedder{}, baseModelConfig())
	second := newTestCachingEmbedder(store, &countingEmbedder{}, baseModelConfig())
	_, _ = first.Embed(context.Background(), "A")
	_, _ = second.Embed(context.Background(), "A")
	stats := GetEmbeddingCacheStats()
	if stats.EmbeddingRequests != 2 || stats.CacheHits != 1 || stats.ProviderInputs != 1 {
		t.Fatalf("shared stats = %+v", stats)
	}
}

func TestCachingEmbedderConcurrentAccess(t *testing.T) {
	resetCacheTestState(t)
	provider := &countingEmbedder{}
	cache := newTestCachingEmbedder(newMemoryEmbeddingCache(), provider, baseModelConfig())
	const workers = 20
	var wg sync.WaitGroup
	errs := make(chan error, workers)
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			got, err := cache.Embed(context.Background(), "concurrent")
			if err != nil {
				errs <- err
				return
			}
			if !reflect.DeepEqual(got, testVector("concurrent", 2)) {
				errs <- errors.New("unexpected vector")
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
	if stats := GetEmbeddingCacheStats(); stats.EmbeddingRequests != workers || stats.EmbeddingInputs != workers {
		t.Fatalf("stats = %+v", stats)
	}
}

func TestLoadEmbeddingCacheConfigFromEnv(t *testing.T) {
	t.Setenv("EMBEDDING_CACHE_ENABLED", "true")
	t.Setenv("EMBEDDING_CACHE_TTL", "12h")
	t.Setenv("EMBEDDING_CACHE_PREFIX", "custom:embedding:")
	config, warnings := LoadEmbeddingCacheConfigFromEnv()
	if len(warnings) != 0 {
		t.Fatalf("warnings = %v", warnings)
	}
	if !config.Enabled || config.TTL != 12*time.Hour || config.Prefix != "custom:embedding" {
		t.Fatalf("config = %+v", config)
	}
}
