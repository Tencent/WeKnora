// Package modelcache provides tenant-isolated persistent embedding caching.
package modelcache

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
	"sync"
	"time"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/modelobs"
	"github.com/Tencent/WeKnora/internal/models/embedding"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/google/uuid"
	"golang.org/x/sync/singleflight"
)

const (
	// DefaultTTL is the lifetime of one validated embedding cache entry.
	DefaultTTL = 30 * 24 * time.Hour
	// CleanupInterval is the period between background expiration sweeps.
	CleanupInterval = 24 * time.Hour
	// CleanupBatchSize bounds one expiration delete statement.
	CleanupBatchSize = 500
	// CleanupRoundTimeout bounds one complete expiration sweep.
	CleanupRoundTimeout = 30 * time.Second
	cacheWriteTimeout   = 2 * time.Second
	// Bound the amount of successful work lost when a later provider batch fails.
	cacheProgressWindow = 64
)

// CachePrefix is the non-text portion of the tenant-isolated composite key.
type CachePrefix struct {
	TenantID             uint64
	ModelID              string
	ModelFingerprint     string
	RequestOptionsSHA256 string
}

// Store persists content-addressed vectors.
type Store interface {
	GetEmbeddingCache(context.Context, CachePrefix, []string) (map[string]*types.EmbeddingCacheEntry, error)
	PutEmbeddingCache(context.Context, []*types.EmbeddingCacheEntry) error
	DeleteExpiredEmbeddingCache(context.Context, int) (int64, error)
}

// EventStore persists aggregate hit, miss, and bypass counts without cache keys or source text.
type EventStore interface {
	RecordEmbeddingCacheLookup(context.Context, *types.EmbeddingCacheLookupRecord) error
}

// Coordinator owns the process-wide singleflight group shared by every wrapper.
type Coordinator struct {
	store     Store
	ttl       time.Duration
	requests  singleflight.Group
	startOnce sync.Once
	stopOnce  sync.Once
	started   chan struct{}
	stop      chan struct{}
	done      chan struct{}
}

// NewCoordinator creates a cache coordinator for one persistent store.
func NewCoordinator(store Store) *Coordinator {
	return &Coordinator{
		store: store, ttl: DefaultTTL,
		started: make(chan struct{}), stop: make(chan struct{}), done: make(chan struct{}),
	}
}

// CleanupOnce deletes expired rows in bounded batches for at most one cleanup round.
func (c *Coordinator) CleanupOnce(ctx context.Context) error {
	if c == nil || c.store == nil {
		return nil
	}
	roundCtx, cancel := context.WithTimeout(ctx, CleanupRoundTimeout)
	defer cancel()
	for {
		count, err := c.store.DeleteExpiredEmbeddingCache(roundCtx, CleanupBatchSize)
		if err != nil {
			return err
		}
		if count < CleanupBatchSize {
			return nil
		}
	}
}

// StartCleaner runs one cleanup at startup and then every cleanup interval.
func (c *Coordinator) StartCleaner(ctx context.Context) {
	if c == nil {
		return
	}
	c.startOnce.Do(func() {
		close(c.started)
		go func() {
			defer close(c.done)
			_ = c.CleanupOnce(context.WithoutCancel(ctx))
			ticker := time.NewTicker(CleanupInterval)
			defer ticker.Stop()
			for {
				select {
				case <-ticker.C:
					_ = c.CleanupOnce(context.Background())
				case <-c.stop:
					return
				}
			}
		}()
	})
}

// StopCleaner terminates the process-lifetime cleanup loop.
func (c *Coordinator) StopCleaner() {
	if c == nil {
		return
	}
	select {
	case <-c.started:
	default:
		return
	}
	c.stopOnce.Do(func() { close(c.stop) })
	<-c.done
}

// Wrap installs content-addressed caching around an embedding provider.
func (c *Coordinator) Wrap(model *types.Model, inner embedding.Embedder) embedding.Embedder {
	if c == nil || c.store == nil || model == nil || inner == nil {
		return inner
	}
	return &cachedEmbedder{coordinator: c, model: model, inner: inner}
}

type cachedEmbedder struct {
	coordinator *Coordinator
	model       *types.Model
	inner       embedding.Embedder
}

func (e *cachedEmbedder) Embed(ctx context.Context, text string) ([]float32, error) {
	result, err := e.cachedBatch(ctx, []string{text}, func(
		callCtx context.Context,
		missing []string,
	) ([][]float32, error) {
		vector, err := e.inner.Embed(callCtx, missing[0])
		if err != nil {
			return nil, err
		}
		return [][]float32{vector}, nil
	})
	if err != nil {
		return nil, err
	}
	return result[0], nil
}

func (e *cachedEmbedder) BatchEmbed(ctx context.Context, texts []string) ([][]float32, error) {
	return e.cachedBatch(ctx, texts, e.inner.BatchEmbed)
}

func (e *cachedEmbedder) BatchEmbedWithPool(
	ctx context.Context,
	_ embedding.Embedder,
	texts []string,
) ([][]float32, error) {
	result := make([][]float32, 0, len(texts))
	for start := 0; start < len(texts); start += cacheProgressWindow {
		if err := ctx.Err(); err != nil {
			return nil, context.Cause(ctx)
		}
		end := min(start+cacheProgressWindow, len(texts))
		vectors, err := e.cachedBatch(ctx, texts[start:end], func(
			callCtx context.Context, missing []string,
		) ([][]float32, error) {
			return e.inner.BatchEmbedWithPool(callCtx, e.inner, missing)
		})
		if err != nil {
			return nil, err
		}
		result = append(result, vectors...)
	}
	return result, nil
}

func (e *cachedEmbedder) GetModelName() string { return e.inner.GetModelName() }
func (e *cachedEmbedder) GetDimensions() int   { return e.inner.GetDimensions() }
func (e *cachedEmbedder) GetModelID() string   { return e.inner.GetModelID() }

func (e *cachedEmbedder) cachedBatch(
	ctx context.Context,
	texts []string,
	provider func(context.Context, []string) ([][]float32, error),
) ([][]float32, error) {
	if len(texts) == 0 {
		return [][]float32{}, nil
	}
	tenantID, ok := types.TenantIDFromContext(ctx)
	if !ok || tenantID == 0 {
		tenantID = e.model.TenantID
	}
	prefix := CachePrefix{
		TenantID: tenantID, ModelID: e.model.ID,
		ModelFingerprint:     EmbeddingModelFingerprint(e.model),
		RequestOptionsSHA256: requestOptionsSHA256(e.inner.GetDimensions()),
	}
	uniqueTexts := make([]string, 0, len(texts))
	uniqueHashes := make([]string, 0, len(texts))
	hashToIndex := make(map[string]int, len(texts))
	originalHashes := make([]string, len(texts))
	for i, text := range texts {
		hash := sha256Hex([]byte(text))
		originalHashes[i] = hash
		if _, exists := hashToIndex[hash]; exists {
			continue
		}
		hashToIndex[hash] = len(uniqueTexts)
		uniqueTexts = append(uniqueTexts, text)
		uniqueHashes = append(uniqueHashes, hash)
	}

	lookupStarted := time.Now()
	cached, lookupErr := e.coordinator.store.GetEmbeddingCache(ctx, prefix, uniqueHashes)
	lookupDuration := time.Since(lookupStarted)
	lookupStatus := types.ApplicationCacheStatusMiss
	if lookupErr != nil {
		cached = map[string]*types.EmbeddingCacheEntry{}
		lookupStatus = types.ApplicationCacheStatusBypass
	}
	uniqueVectors := make(map[string][]float32, len(uniqueTexts))
	missingTexts := make([]string, 0)
	missingHashes := make([]string, 0)
	for i, hash := range uniqueHashes {
		if entry := cached[hash]; entry != nil {
			if vector, err := decodeCacheVector(entry, e.inner.GetDimensions()); err == nil {
				uniqueVectors[hash] = vector
				continue
			}
		}
		missingTexts = append(missingTexts, uniqueTexts[i])
		missingHashes = append(missingHashes, hash)
	}
	hitItems := int64(len(uniqueHashes) - len(missingHashes))
	missItems := int64(len(missingHashes))
	bypassItems := int64(0)
	if lookupErr != nil {
		bypassItems = int64(len(uniqueHashes))
		missItems = 0
	}
	e.coordinator.recordLookup(
		ctx, prefix, int64(len(texts)), int64(len(uniqueTexts)),
		hitItems, missItems, bypassItems, lookupDuration,
	)
	if len(missingTexts) > 0 {
		batchKey := modelobs.SharingScope(ctx) + "\x00" + singleflightBatchKey(prefix, missingHashes)
		resultCh := e.coordinator.requests.DoChan(batchKey, func() (any, error) {
			// A caller can observe a miss before another flight writes its result,
			// then become the leader after that flight has left the group. Recheck
			// inside the flight, including partial fills from overlapping batches.
			fresh, refreshErr := e.coordinator.store.GetEmbeddingCache(ctx, prefix, missingHashes)
			vectors := make([][]float32, len(missingHashes))
			pendingTexts := make([]string, 0, len(missingTexts))
			pendingHashes := make([]string, 0, len(missingHashes))
			pendingIndices := make([]int, 0, len(missingHashes))
			for index, hash := range missingHashes {
				if refreshErr == nil && fresh[hash] != nil {
					if vector, err := decodeCacheVector(fresh[hash], e.inner.GetDimensions()); err == nil {
						vectors[index] = vector
						continue
					}
				}
				pendingTexts = append(pendingTexts, missingTexts[index])
				pendingHashes = append(pendingHashes, hash)
				pendingIndices = append(pendingIndices, index)
			}
			if len(pendingTexts) == 0 {
				return vectors, nil
			}
			callCtx := modelobs.WithApplicationCacheStatus(ctx, lookupStatus)
			generated, err := provider(callCtx, pendingTexts)
			if err != nil {
				return nil, err
			}
			entries, err := buildCacheEntries(
				prefix, pendingHashes, generated, e.inner.GetDimensions(), e.coordinator.ttl,
			)
			if err != nil {
				return nil, err
			}
			writeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), cacheWriteTimeout)
			defer cancel()
			// Best-effort persistence: vectors are still returned on write
			// failure, but the degraded cache layer must be observable.
			// Storage errors can include payloads or connection details. Emit
			// only counts and a fixed error category, never the error text.
			if err := e.coordinator.store.PutEmbeddingCache(writeCtx, entries); err != nil {
				logger.Warnf(ctx,
					"Embedding cache persist failed (vectors still returned): "+
						"entries %d, error_kind %s",
					len(entries), cacheWriteErrorKind(err))
			}
			for index, position := range pendingIndices {
				vectors[position] = generated[index]
			}
			return vectors, nil
		})
		var shared singleflight.Result
		select {
		case shared = <-resultCh:
		case <-ctx.Done():
			return nil, context.Cause(ctx)
		}
		if shared.Err != nil {
			return nil, shared.Err
		}
		vectors := shared.Val.([][]float32)
		if len(vectors) != len(missingHashes) {
			return nil, errors.New("embedding cache: provider result count mismatch")
		}
		for i, hash := range missingHashes {
			uniqueVectors[hash] = append([]float32(nil), vectors[i]...)
		}
	}
	result := make([][]float32, len(texts))
	for i, hash := range originalHashes {
		vector, exists := uniqueVectors[hash]
		if !exists {
			return nil, errors.New("embedding cache: missing restored vector")
		}
		result[i] = append([]float32(nil), vector...)
	}
	return result, nil
}

func (c *Coordinator) recordLookup(
	ctx context.Context,
	prefix CachePrefix,
	requestedItems, uniqueItems, hitItems, missItems, bypassItems int64,
	duration time.Duration,
) {
	store, ok := c.store.(EventStore)
	if !ok {
		return
	}
	writeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), cacheWriteTimeout)
	defer cancel()
	record := &types.EmbeddingCacheLookupRecord{
		ID: uuid.NewString(), TenantID: prefix.TenantID, ModelID: prefix.ModelID,
		RequestedItems: requestedItems, UniqueItems: uniqueItems,
		HitItems: hitItems, MissItems: missItems, BypassItems: bypassItems,
		Status: cacheLookupStatus(hitItems, missItems, bypassItems), DurationMs: duration.Milliseconds(),
		OccurredAt: time.Now().UTC(),
	}
	// Lookup statistics are observability data: a persist failure must not fail
	// the embedding call, but it must be visible in logs. The status and fixed
	// error category exclude raw storage errors and tenant/model identifiers.
	if err := store.RecordEmbeddingCacheLookup(writeCtx, record); err != nil {
		logger.Warnf(ctx,
			"Embedding cache lookup record persist failed: status %s, error_kind %s",
			record.Status, cacheWriteErrorKind(err))
	}
}

// cacheWriteErrorKind deliberately uses a bounded vocabulary: arbitrary store
// errors are not safe to print because drivers can embed data or credentials.
func cacheWriteErrorKind(err error) string {
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return "timeout"
	case errors.Is(err, context.Canceled):
		return "canceled"
	default:
		return "storage"
	}
}

func cacheLookupStatus(hitItems, missItems, bypassItems int64) string {
	if bypassItems > 0 {
		return types.EmbeddingCacheLookupStatusBypass
	}
	if hitItems > 0 && missItems > 0 {
		return types.EmbeddingCacheLookupStatusPartial
	}
	if hitItems > 0 {
		return types.EmbeddingCacheLookupStatusHit
	}
	return types.EmbeddingCacheLookupStatusMiss
}

// EmbeddingModelFingerprint hashes behavior-affecting model configuration without secrets.
func EmbeddingModelFingerprint(model *types.Model) string {
	if model == nil {
		return sha256Hex(nil)
	}
	behavior := types.EvaluationModelBehaviorConfigFrom(model)
	encoded, _ := json.Marshal(struct {
		ID        string                    `json:"id"`
		Name      string                    `json:"name"`
		Source    types.ModelSource         `json:"source"`
		Provider  string                    `json:"provider"`
		BaseURL   string                    `json:"base_url"`
		Embedding types.EmbeddingParameters `json:"embedding"`
		Extra     map[string]string         `json:"extra"`
	}{
		ID: model.ID, Name: model.Name, Source: model.Source, Provider: behavior.Provider,
		BaseURL: behavior.BaseURL, Embedding: behavior.EmbeddingParameters,
		Extra: behavior.ExtraConfig,
	})
	return sha256Hex(encoded)
}

func requestOptionsSHA256(dimensions int) string {
	return sha256Hex([]byte(fmt.Sprintf("schema=1;encoding=float32-le;dimensions=%d", dimensions)))
}

func singleflightBatchKey(prefix CachePrefix, hashes []string) string {
	return fmt.Sprintf("%d\x00%s\x00%s\x00%s\x00%s", prefix.TenantID, prefix.ModelID,
		prefix.ModelFingerprint, prefix.RequestOptionsSHA256, strings.Join(hashes, ","))
}

func buildCacheEntries(
	prefix CachePrefix,
	hashes []string,
	vectors [][]float32,
	expectedDimension int,
	ttl time.Duration,
) ([]*types.EmbeddingCacheEntry, error) {
	if len(vectors) != len(hashes) {
		return nil, errors.New("embedding cache: provider result count mismatch")
	}
	now := time.Now().UTC()
	entries := make([]*types.EmbeddingCacheEntry, len(vectors))
	for i, vector := range vectors {
		if err := validateVector(vector, expectedDimension); err != nil {
			return nil, err
		}
		encoded := encodeVector(vector)
		entries[i] = &types.EmbeddingCacheEntry{
			TenantID: prefix.TenantID, ModelID: prefix.ModelID, ModelFingerprint: prefix.ModelFingerprint,
			RequestOptionsSHA256: prefix.RequestOptionsSHA256, TextSHA256: hashes[i], Embedding: encoded,
			Dimension: len(vector), ExpiresAt: now.Add(ttl),
			AccessedAt: now, CreatedAt: now, UpdatedAt: now,
		}
	}
	return entries, nil
}

func validateVector(vector []float32, expectedDimension int) error {
	if len(vector) == 0 {
		return errors.New("embedding cache: empty provider vector")
	}
	if expectedDimension > 0 && len(vector) != expectedDimension {
		return errors.New("embedding cache: provider dimension mismatch")
	}
	for _, value := range vector {
		if math.IsNaN(float64(value)) || math.IsInf(float64(value), 0) {
			return errors.New("embedding cache: non-finite provider vector")
		}
	}
	return nil
}

func encodeVector(vector []float32) []byte {
	encoded := make([]byte, len(vector)*4)
	for i, value := range vector {
		binary.LittleEndian.PutUint32(encoded[i*4:], math.Float32bits(value))
	}
	return encoded
}

func decodeCacheVector(entry *types.EmbeddingCacheEntry, expectedDimension int) ([]float32, error) {
	if entry == nil || entry.Dimension <= 0 || len(entry.Embedding) != entry.Dimension*4 ||
		(expectedDimension > 0 && entry.Dimension != expectedDimension) {
		return nil, errors.New("embedding cache: invalid cached vector")
	}
	vector := make([]float32, entry.Dimension)
	for i := range vector {
		vector[i] = math.Float32frombits(binary.LittleEndian.Uint32(entry.Embedding[i*4:]))
	}
	if err := validateVector(vector, expectedDimension); err != nil {
		return nil, err
	}
	return vector, nil
}

func sha256Hex(value []byte) string { sum := sha256.Sum256(value); return hex.EncodeToString(sum[:]) }
