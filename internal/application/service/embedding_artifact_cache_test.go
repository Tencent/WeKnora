package service

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/application/repository"
	retrieversvc "github.com/Tencent/WeKnora/internal/application/service/retriever"
	"github.com/Tencent/WeKnora/internal/artifactkey"
	"github.com/Tencent/WeKnora/internal/models/embedding"
	"github.com/Tencent/WeKnora/internal/testutil/modelcount"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func newEmbeddingArtifactTestRepo(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := fmt.Sprintf("file:%s?_busy_timeout=5000&_journal_mode=WAL", filepath.ToSlash(filepath.Join(t.TempDir(), "embedding-artifacts.db")))
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, sqlDB.Close()) })
	require.NoError(t, db.AutoMigrate(&types.DerivedArtifact{}))
	return db
}

type embeddingProductionPathRepository struct {
	interfaces.RetrieveEngineRepository
	mu    sync.Mutex
	saves []map[string][]float32
}

func (r *embeddingProductionPathRepository) BatchSave(_ context.Context, _ []*types.IndexInfo, params map[string]any) error {
	embeddings := params["embedding"].(map[string][]float32)
	copied := make(map[string][]float32, len(embeddings))
	for key, vector := range embeddings {
		copied[key] = cloneEmbeddingVector(vector)
	}
	r.mu.Lock()
	r.saves = append(r.saves, copied)
	r.mu.Unlock()
	return nil
}

type embeddingProductionPathIndexer struct {
	engine interfaces.RetrieveEngineService
}

func (i embeddingProductionPathIndexer) BatchIndex(ctx context.Context, embedder embedding.Embedder, infos []*types.IndexInfo) error {
	return i.engine.BatchIndex(ctx, embedder, infos, []types.RetrieverType{types.VectorRetrieverType})
}

type blockingEmbeddingModel struct {
	entered    chan struct{}
	release    chan struct{}
	once       sync.Once
	count      atomic.Int32
	dimensions int
	value      float32
}

func newBlockingEmbeddingModel(dimensions int, value float32) *blockingEmbeddingModel {
	return &blockingEmbeddingModel{entered: make(chan struct{}), release: make(chan struct{}), dimensions: dimensions, value: value}
}
func (m *blockingEmbeddingModel) Embed(ctx context.Context, text string) ([]float32, error) {
	v, err := m.BatchEmbed(ctx, []string{text})
	if err != nil {
		return nil, err
	}
	return v[0], nil
}
func (m *blockingEmbeddingModel) BatchEmbed(ctx context.Context, texts []string) ([][]float32, error) {
	m.count.Add(1)
	m.once.Do(func() { close(m.entered) })
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-m.release:
	}
	result := make([][]float32, len(texts))
	for i := range result {
		result[i] = make([]float32, m.dimensions)
		for j := range result[i] {
			result[i][j] = m.value + float32(i+j)
		}
	}
	return result, nil
}
func (m *blockingEmbeddingModel) BatchEmbedWithPool(ctx context.Context, model embedding.Embedder, texts []string) ([][]float32, error) {
	return model.BatchEmbed(ctx, texts)
}
func (m *blockingEmbeddingModel) GetModelName() string { return "blocking-model" }
func (m *blockingEmbeddingModel) GetModelID() string   { return "blocking-model-id" }
func (m *blockingEmbeddingModel) GetDimensions() int   { return m.dimensions }

func newCachedTestEmbedder(t *testing.T, repo interfaces.DerivedArtifactRepository, inner embedding.Embedder) *artifactCachedEmbedder {
	t.Helper()
	cached, err := newArtifactCachedEmbedder(inner, repo, embedding.Config{ModelName: inner.GetModelName(), Dimensions: inner.GetDimensions(), Provider: "test"})
	require.NoError(t, err)
	return cached.(*artifactCachedEmbedder)
}

func embeddingTestKey(e *artifactCachedEmbedder, tenantID uint64, text string) string {
	return artifactkey.Generate(artifactkey.KeyInput{Kind: embeddingArtifactKind, TenantScope: fmt.Sprintf("tenant:%d", tenantID), InputDigest: artifactkey.DigestText(text), ModelID: e.modelID, ModelRevision: e.modelRevision, PromptVersion: e.promptVersion, ConfigDigest: e.configDigest, ProducerVersion: embeddingProducerVersion})
}

func embeddingArtifactTestContext(tenantID uint64, operation types.IngestionOperation) context.Context {
	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, tenantID)
	return types.WithIngestionOperation(ctx, operation)
}

func newEmbeddingArtifactTestModel(t *testing.T, db *gorm.DB, dimensions int, configChanges ...func(*embedding.Config)) (*modelcount.CountingEmbedder, *artifactCachedEmbedder) {
	t.Helper()
	model := modelcount.NewCountingEmbedder(modelcount.CountingEmbedderOptions{ModelID: "db-model-id", ModelName: "actual-model", Dimensions: dimensions})
	config := embedding.Config{ModelID: "db-model-id", ModelName: "actual-model", Dimensions: dimensions, Provider: "openai"}
	for _, change := range configChanges {
		change(&config)
	}
	cached, err := newArtifactCachedEmbedder(model, repository.NewDerivedArtifactRepository(db), config)
	require.NoError(t, err)
	return model, cached.(*artifactCachedEmbedder)
}

func TestEmbeddingArtifactBinaryRoundTripPreservesFloat32Bits(t *testing.T) {
	want := []float32{0, math.Float32frombits(0x80000000), 1.25, math.SmallestNonzeroFloat32, math.MaxFloat32}
	payload, err := encodeEmbeddingArtifact(want)
	require.NoError(t, err)
	require.Equal(t, embeddingArtifactMagic[:], payload[:len(embeddingArtifactMagic)])
	require.Equal(t, uint32(len(want)), binary.LittleEndian.Uint32(payload[len(embeddingArtifactMagic):]))
	got, err := decodeEmbeddingArtifact(payload, embeddingArtifactEncoding, len(want))
	require.NoError(t, err)
	for i := range want {
		require.Equal(t, math.Float32bits(want[i]), math.Float32bits(got[i]))
	}
	got[0] = 99
	again, err := decodeEmbeddingArtifact(payload, embeddingArtifactEncoding, len(want))
	require.NoError(t, err)
	require.Equal(t, float32(0), again[0])
}

func TestEmbeddingArtifactBinaryRejectsCorruption(t *testing.T) {
	valid, err := encodeEmbeddingArtifact([]float32{1, 2})
	require.NoError(t, err)
	badMagic := append([]byte(nil), valid...)
	badMagic[0] ^= 0xff
	badDimension := append([]byte(nil), valid...)
	binary.LittleEndian.PutUint32(badDimension[len(embeddingArtifactMagic):], 3)
	nan := append([]byte(nil), valid...)
	binary.LittleEndian.PutUint32(nan[len(embeddingArtifactMagic)+4:], math.Float32bits(float32(math.NaN())))
	inf := append([]byte(nil), valid...)
	binary.LittleEndian.PutUint32(inf[len(embeddingArtifactMagic)+4:], math.Float32bits(float32(math.Inf(1))))
	cases := []struct {
		name      string
		payload   []byte
		encoding  string
		dimension int
	}{
		{"encoding", valid, "json", 2}, {"expected-dimension", valid, embeddingArtifactEncoding, 3},
		{"truncated-header", valid[:5], embeddingArtifactEncoding, 2}, {"truncated-vector", valid[:len(valid)-1], embeddingArtifactEncoding, 2},
		{"extra-byte", append(append([]byte(nil), valid...), 0), embeddingArtifactEncoding, 2}, {"magic", badMagic, embeddingArtifactEncoding, 2},
		{"dimension", badDimension, embeddingArtifactEncoding, 2}, {"nan", nan, embeddingArtifactEncoding, 2}, {"inf", inf, embeddingArtifactEncoding, 2},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := decodeEmbeddingArtifact(tc.payload, tc.encoding, tc.dimension)
			require.ErrorIs(t, err, interfaces.ErrArtifactCorrupt)
		})
	}
	_, err = encodeEmbeddingArtifact(nil)
	require.ErrorIs(t, err, interfaces.ErrArtifactCorrupt)
	_, err = encodeEmbeddingArtifact([]float32{float32(math.NaN())})
	require.ErrorIs(t, err, interfaces.ErrArtifactCorrupt)
	_, err = encodeEmbeddingArtifact([]float32{float32(math.Inf(-1))})
	require.ErrorIs(t, err, interfaces.ErrArtifactCorrupt)
}

func TestEmbeddingArtifactCachePartialHitPreservesOrder(t *testing.T) {
	db := newEmbeddingArtifactTestRepo(t)
	ctx := embeddingArtifactTestContext(7, types.IngestionOperationEmbeddingChunk)
	provider, cached := newEmbeddingArtifactTestModel(t, db, 3)

	first, err := cached.BatchEmbedWithPool(ctx, cached, []string{"hit-a", "hit-b"})
	require.NoError(t, err)
	require.Len(t, first, 2)

	second, err := cached.BatchEmbedWithPool(ctx, cached, []string{"hit-b", "miss-c", "hit-a", "miss-c"})
	require.NoError(t, err)
	require.Len(t, second, 4)
	require.Equal(t, first[1], second[0])
	require.Equal(t, first[0], second[2])
	require.Equal(t, second[1], second[3])

	snapshot := provider.Snapshot()
	require.Equal(t, 2, snapshot.RequestCount)
	require.Equal(t, []int{2, 1}, snapshot.BatchSizes)
	require.Equal(t, 3, snapshot.TotalInputItems)
}

func TestEmbeddingArtifactCacheEmptyBatchDoesNotCallProvider(t *testing.T) {
	db := newEmbeddingArtifactTestRepo(t)
	provider, cached := newEmbeddingArtifactTestModel(t, db, 3)
	ctx := embeddingArtifactTestContext(1, types.IngestionOperationEmbeddingChunk)
	for _, call := range []func() ([][]float32, error){func() ([][]float32, error) { return cached.BatchEmbed(ctx, nil) }, func() ([][]float32, error) { return cached.BatchEmbedWithPool(ctx, cached, []string{}) }} {
		vectors, err := call()
		require.NoError(t, err)
		require.Empty(t, vectors)
	}
	require.Zero(t, provider.Snapshot().RequestCount)
}

func TestEmbeddingArtifactCacheReusesAcrossCallersButNotTenants(t *testing.T) {
	db := newEmbeddingArtifactTestRepo(t)
	provider, cached := newEmbeddingArtifactTestModel(t, db, 3)
	ctx := embeddingArtifactTestContext(11, types.IngestionOperationEmbeddingSummary)

	first, err := cached.Embed(ctx, "same final text")
	require.NoError(t, err)
	second, err := cached.Embed(ctx, "same final text")
	require.NoError(t, err)
	require.Equal(t, first, second)
	require.Equal(t, 1, provider.Snapshot().TotalInputItems)

	_, err = cached.Embed(embeddingArtifactTestContext(12, types.IngestionOperationEmbeddingSummary), "same final text")
	require.NoError(t, err)
	require.Equal(t, 2, provider.Snapshot().TotalInputItems)
}

func TestEmbeddingArtifactCacheIdentityIncludesModelConfigAndDimension(t *testing.T) {
	db := newEmbeddingArtifactTestRepo(t)
	ctx := embeddingArtifactTestContext(3, types.IngestionOperationEmbeddingQuestion)
	providerA, cachedA := newEmbeddingArtifactTestModel(t, db, 3)
	_, err := cachedA.Embed(ctx, "identity")
	require.NoError(t, err)
	require.Equal(t, 1, providerA.Snapshot().TotalInputItems)

	providerB, cachedB := newEmbeddingArtifactTestModel(t, db, 3, func(config *embedding.Config) { config.ModelName = "other-model" })
	_, err = cachedB.Embed(ctx, "identity")
	require.NoError(t, err)
	require.Equal(t, 1, providerB.Snapshot().TotalInputItems)

	providerC, cachedC := newEmbeddingArtifactTestModel(t, db, 4)
	_, err = cachedC.Embed(ctx, "identity")
	require.NoError(t, err)
	require.Equal(t, 1, providerC.Snapshot().TotalInputItems)
}

func TestEmbeddingArtifactCacheIdentityAllowlistIgnoresCredentialsAndRouting(t *testing.T) {
	db := newEmbeddingArtifactTestRepo(t)
	ctx := embeddingArtifactTestContext(4, types.IngestionOperationEmbeddingChunk)
	providerA, cachedA := newEmbeddingArtifactTestModel(t, db, 3, func(config *embedding.Config) {
		config.APIKey = "old-key"
		config.BaseURL = "https://old.example/v1"
		config.CustomHeaders = map[string]string{"Authorization": "Bearer old"}
		config.ExtraConfig = map[string]string{"token": "old-token", "password": "old-password"}
	})
	_, err := cachedA.Embed(ctx, "credential rotation")
	require.NoError(t, err)
	require.Equal(t, 1, providerA.Snapshot().TotalInputItems)

	providerB, cachedB := newEmbeddingArtifactTestModel(t, db, 3, func(config *embedding.Config) {
		config.APIKey = "new-key"
		config.BaseURL = "https://new.example/v1"
		config.CustomHeaders = map[string]string{"Authorization": "Bearer new", "X-API-Key": "new"}
		config.ExtraConfig = map[string]string{"token": "new-token", "secret": "new-secret", "unrelated_wiki_config": "changed"}
	})
	vector, err := cachedB.Embed(ctx, "credential rotation")
	require.NoError(t, err)
	require.Len(t, vector, 3)
	require.Zero(t, providerB.Snapshot().TotalInputItems)
}

func TestEmbeddingArtifactCacheIdentityInvalidatesAllowedOutputConfig(t *testing.T) {
	cases := []struct {
		name   string
		change func(*embedding.Config)
	}{
		{"revision", func(c *embedding.Config) { c.ExtraConfig = map[string]string{"model_revision": "r2"} }},
		{"instruction", func(c *embedding.Config) {
			c.ExtraConfig = map[string]string{"embedding_instruction": "represent a document"}
		}},
		{"prefix", func(c *embedding.Config) { c.ExtraConfig = map[string]string{"embedding_prefix": "passage: "} }},
		{"prompt-version", func(c *embedding.Config) { c.ExtraConfig = map[string]string{"embedding_prompt_version": "doc-v2"} }},
		{"document-mode", func(c *embedding.Config) { c.ExtraConfig = map[string]string{"document_mode": "document"} }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			db := newEmbeddingArtifactTestRepo(t)
			ctx := embeddingArtifactTestContext(1, types.IngestionOperationEmbeddingChunk)
			_, baseline := newEmbeddingArtifactTestModel(t, db, 3)
			_, err := baseline.Embed(ctx, "config identity")
			require.NoError(t, err)
			provider, changed := newEmbeddingArtifactTestModel(t, db, 3, tc.change)
			_, err = changed.Embed(ctx, "config identity")
			require.NoError(t, err)
			require.Equal(t, 1, provider.Snapshot().TotalInputItems)
		})
	}
}

func TestEmbeddingArtifactCacheReturnsIndependentVectorSlices(t *testing.T) {
	db := newEmbeddingArtifactTestRepo(t)
	ctx := embeddingArtifactTestContext(1, types.IngestionOperationEmbeddingChunk)
	_, cached := newEmbeddingArtifactTestModel(t, db, 3)
	first, err := cached.BatchEmbed(ctx, []string{"duplicate", "duplicate"})
	require.NoError(t, err)
	require.Equal(t, first[0], first[1])
	first[0][0] = 999
	require.NotEqual(t, first[0], first[1])
	hit, err := cached.BatchEmbed(ctx, []string{"duplicate", "duplicate"})
	require.NoError(t, err)
	require.NotEqual(t, float32(999), hit[0][0])
	hit[0][0] = 777
	require.NotEqual(t, hit[0], hit[1])
}

func TestEmbeddingArtifactCorruptSucceededFailsClosed(t *testing.T) {
	db := newEmbeddingArtifactTestRepo(t)
	provider, cached := newEmbeddingArtifactTestModel(t, db, 3)
	ctx := embeddingArtifactTestContext(1, types.IngestionOperationEmbeddingChunk)
	_, err := cached.Embed(ctx, "corrupt")
	require.NoError(t, err)
	require.Equal(t, 1, provider.Snapshot().RequestCount)
	bad := []byte("bad-payload")
	require.NoError(t, db.Model(&types.DerivedArtifact{}).Where("artifact_kind = ?", embeddingArtifactKind).Updates(map[string]any{"payload": bad, "payload_encoding": embeddingArtifactEncoding, "payload_digest": artifactkey.DigestBytes(bad)}).Error)
	_, err = cached.Embed(ctx, "corrupt")
	require.ErrorIs(t, err, interfaces.ErrArtifactCorrupt)
	require.Equal(t, 1, provider.Snapshot().RequestCount)
}

func TestEmbeddingArtifactCacheBypassesRealtimeAndUnsupportedOperations(t *testing.T) {
	db := newEmbeddingArtifactTestRepo(t)
	provider, cached := newEmbeddingArtifactTestModel(t, db, 3)
	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(9))

	_, err := cached.Embed(ctx, "query text")
	require.NoError(t, err)
	_, err = cached.Embed(ctx, "query text")
	require.NoError(t, err)
	_, err = cached.Embed(types.WithIngestionOperation(ctx, types.IngestionOperationEmbeddingBatch), "query text")
	require.NoError(t, err)
	require.Equal(t, 3, provider.Snapshot().TotalInputItems)

	var count int64
	require.NoError(t, db.Model(&types.DerivedArtifact{}).Count(&count).Error)
	require.Zero(t, count)
}

func TestEmbeddingArtifactCacheSupportsEveryScopedOperation(t *testing.T) {
	operations := []types.IngestionOperation{
		types.IngestionOperationEmbeddingChunk, types.IngestionOperationEmbeddingSummary,
		types.IngestionOperationEmbeddingQuestion, types.IngestionOperationEmbeddingFAQ,
		types.IngestionOperationEmbeddingGraphEntity, types.IngestionOperationEmbeddingGraphRelation,
		types.IngestionOperationEmbeddingWikiPage,
	}
	for _, operation := range operations {
		t.Run(string(operation), func(t *testing.T) {
			db := newEmbeddingArtifactTestRepo(t)
			provider, cached := newEmbeddingArtifactTestModel(t, db, 3)
			ctx := embeddingArtifactTestContext(1, operation)
			_, err := cached.Embed(ctx, "shared")
			require.NoError(t, err)
			_, err = cached.Embed(ctx, "shared")
			require.NoError(t, err)
			require.Equal(t, 1, provider.Snapshot().TotalInputItems)
		})
	}
}

func TestEmbeddingArtifactConcurrentBusyWaitSharesCanonicalVector(t *testing.T) {
	db := newEmbeddingArtifactTestRepo(t)
	repo := repository.NewDerivedArtifactRepository(db)
	model := newBlockingEmbeddingModel(3, 10)
	cached := newCachedTestEmbedder(t, repo, model)
	cached.timing = embeddingArtifactTiming{Lease: 200 * time.Millisecond, Wait: time.Second, Poll: 5 * time.Millisecond, Cleanup: 100 * time.Millisecond}
	ctx := embeddingArtifactTestContext(1, types.IngestionOperationEmbeddingChunk)
	type result struct {
		vector []float32
		err    error
	}
	results := make(chan result, 2)
	go func() { v, err := cached.Embed(ctx, "concurrent"); results <- result{v, err} }()
	<-model.entered
	go func() { v, err := cached.Embed(ctx, "concurrent"); results <- result{v, err} }()
	time.Sleep(25 * time.Millisecond)
	require.Equal(t, int32(1), model.count.Load())
	close(model.release)
	a, b := <-results, <-results
	require.NoError(t, a.err)
	require.NoError(t, b.err)
	require.Equal(t, a.vector, b.vector)
	require.Equal(t, int32(1), model.count.Load())
}

func TestEmbeddingArtifactBusyTimeoutAndCancellationDoNotCallProvider(t *testing.T) {
	db := newEmbeddingArtifactTestRepo(t)
	repo := repository.NewDerivedArtifactRepository(db)
	model := modelcount.NewCountingEmbedder(modelcount.CountingEmbedderOptions{Dimensions: 3})
	cached := newCachedTestEmbedder(t, repo, model)
	cached.timing = embeddingArtifactTiming{Lease: time.Second, Wait: 35 * time.Millisecond, Poll: 5 * time.Millisecond, Cleanup: 100 * time.Millisecond}
	ctx := embeddingArtifactTestContext(1, types.IngestionOperationEmbeddingChunk)
	text := "busy"
	_, err := repo.Claim(ctx, interfaces.ArtifactClaim{TenantID: 1, ArtifactKey: embeddingTestKey(cached, 1, text), ArtifactKind: embeddingArtifactKind, InputDigest: artifactkey.DigestText(text), ModelID: cached.modelID, ConfigDigest: cached.configDigest, ProducerVersion: embeddingProducerVersion, OwnerToken: "other", LeaseDuration: time.Second})
	require.NoError(t, err)
	_, err = cached.Embed(ctx, text)
	require.ErrorContains(t, err, "busy wait timed out")
	require.Zero(t, model.Snapshot().RequestCount)
	cancelCtx, cancel := context.WithCancel(ctx)
	cancel()
	start := time.Now()
	_, err = cached.Embed(cancelCtx, text)
	require.ErrorIs(t, err, context.Canceled)
	require.Less(t, time.Since(start), 100*time.Millisecond)
	require.Zero(t, model.Snapshot().RequestCount)
}

func TestEmbeddingArtifactHeartbeatKeepsEveryClaimOwned(t *testing.T) {
	db := newEmbeddingArtifactTestRepo(t)
	repo := repository.NewDerivedArtifactRepository(db)
	model := newBlockingEmbeddingModel(3, 20)
	cached := newCachedTestEmbedder(t, repo, model)
	cached.timing = embeddingArtifactTiming{Lease: 180 * time.Millisecond, Wait: time.Second, Poll: 5 * time.Millisecond, Cleanup: 100 * time.Millisecond}
	ctx := embeddingArtifactTestContext(1, types.IngestionOperationEmbeddingChunk)
	done := make(chan error, 1)
	go func() { _, err := cached.BatchEmbed(ctx, []string{"one", "two"}); done <- err }()
	<-model.entered
	time.Sleep(260 * time.Millisecond)
	for _, text := range []string{"one", "two"} {
		claim, err := repo.Claim(ctx, interfaces.ArtifactClaim{TenantID: 1, ArtifactKey: embeddingTestKey(cached, 1, text), ArtifactKind: embeddingArtifactKind, InputDigest: artifactkey.DigestText(text), ModelID: cached.modelID, ConfigDigest: cached.configDigest, ProducerVersion: embeddingProducerVersion, OwnerToken: "takeover", LeaseDuration: time.Second})
		require.NoError(t, err)
		require.Equal(t, interfaces.ArtifactClaimBusy, claim.Outcome)
	}
	close(model.release)
	require.NoError(t, <-done)
}

type lostEmbeddingRenewRepository struct {
	interfaces.DerivedArtifactRepository
	lost chan struct{}
	once sync.Once
}

func (r *lostEmbeddingRenewRepository) RenewLease(context.Context, uint64, string, string, time.Time, time.Duration) error {
	r.once.Do(func() { close(r.lost) })
	return interfaces.ErrArtifactLostOwnership
}

func TestEmbeddingArtifactLostOwnerCannotOverwriteTakeover(t *testing.T) {
	db := newEmbeddingArtifactTestRepo(t)
	baseRepo := repository.NewDerivedArtifactRepository(db)
	lostRepo := &lostEmbeddingRenewRepository{DerivedArtifactRepository: baseRepo, lost: make(chan struct{})}
	oldModel := newBlockingEmbeddingModel(3, 30)
	oldCached := newCachedTestEmbedder(t, lostRepo, oldModel)
	oldCached.timing = embeddingArtifactTiming{Lease: 45 * time.Millisecond, Wait: time.Second, Poll: 5 * time.Millisecond, Cleanup: 100 * time.Millisecond}
	ctx := embeddingArtifactTestContext(1, types.IngestionOperationEmbeddingChunk)
	oldDone := make(chan error, 1)
	go func() { _, err := oldCached.Embed(ctx, "takeover"); oldDone <- err }()
	<-oldModel.entered
	<-lostRepo.lost
	time.Sleep(55 * time.Millisecond)
	newProvider := modelcount.NewCountingEmbedder(modelcount.CountingEmbedderOptions{ModelName: "blocking-model", Dimensions: 3})
	newCached := newCachedTestEmbedder(t, baseRepo, newProvider)
	newCached.timing = oldCached.timing
	canonical, err := newCached.Embed(ctx, "takeover")
	require.NoError(t, err)
	require.Equal(t, 1, newProvider.Snapshot().RequestCount)
	close(oldModel.release)
	require.Error(t, <-oldDone)
	hit, err := newCached.Embed(ctx, "takeover")
	require.NoError(t, err)
	require.Equal(t, canonical, hit)
	require.Equal(t, 1, newProvider.Snapshot().RequestCount)
}

func TestEmbeddingArtifactCancellationPersistsFailedForRetry(t *testing.T) {
	db := newEmbeddingArtifactTestRepo(t)
	repo := repository.NewDerivedArtifactRepository(db)
	model := newBlockingEmbeddingModel(3, 1)
	cached := newCachedTestEmbedder(t, repo, model)
	cached.timing = embeddingArtifactTiming{Lease: time.Second, Wait: time.Second, Poll: 5 * time.Millisecond, Cleanup: 200 * time.Millisecond}
	ctx, cancel := context.WithCancel(embeddingArtifactTestContext(1, types.IngestionOperationEmbeddingChunk))
	done := make(chan error, 1)
	go func() { _, err := cached.Embed(ctx, "cancel"); done <- err }()
	<-model.entered
	cancel()
	require.ErrorIs(t, <-done, context.Canceled)
	var row types.DerivedArtifact
	require.NoError(t, db.Where("artifact_kind = ?", embeddingArtifactKind).Take(&row).Error)
	require.Equal(t, types.DerivedArtifactFailed, row.Status)
	retryProvider := modelcount.NewCountingEmbedder(modelcount.CountingEmbedderOptions{ModelName: "blocking-model", Dimensions: 3})
	retryCached := newCachedTestEmbedder(t, repo, retryProvider)
	_, err := retryCached.Embed(embeddingArtifactTestContext(1, types.IngestionOperationEmbeddingChunk), "cancel")
	require.NoError(t, err)
	require.Equal(t, 1, retryProvider.Snapshot().RequestCount)
}

type failNthCompleteRepository struct {
	interfaces.DerivedArtifactRepository
	failAt int
	calls  atomic.Int32
}

func (r *failNthCompleteRepository) Complete(ctx context.Context, completion interfaces.ArtifactCompletion) error {
	if int(r.calls.Add(1)) == r.failAt {
		return errors.New("injected complete failure")
	}
	return r.DerivedArtifactRepository.Complete(ctx, completion)
}

func TestEmbeddingArtifactPartialCompleteRetryOnlyComputesUnfinished(t *testing.T) {
	db := newEmbeddingArtifactTestRepo(t)
	baseRepo := repository.NewDerivedArtifactRepository(db)
	failing := &failNthCompleteRepository{DerivedArtifactRepository: baseRepo, failAt: 2}
	providerA := modelcount.NewCountingEmbedder(modelcount.CountingEmbedderOptions{ModelName: "partial-model", Dimensions: 3})
	cachedA := newCachedTestEmbedder(t, failing, providerA)
	ctx := embeddingArtifactTestContext(1, types.IngestionOperationEmbeddingChunk)
	_, err := cachedA.BatchEmbed(ctx, []string{"completed", "unfinished"})
	require.ErrorContains(t, err, "complete embedding artifact")
	var succeeded, failed int64
	require.NoError(t, db.Model(&types.DerivedArtifact{}).Where("status = ?", types.DerivedArtifactSucceeded).Count(&succeeded).Error)
	require.NoError(t, db.Model(&types.DerivedArtifact{}).Where("status = ?", types.DerivedArtifactFailed).Count(&failed).Error)
	require.Equal(t, int64(1), succeeded)
	require.Equal(t, int64(1), failed)
	providerB := modelcount.NewCountingEmbedder(modelcount.CountingEmbedderOptions{ModelName: "partial-model", Dimensions: 3})
	cachedB := newCachedTestEmbedder(t, baseRepo, providerB)
	vectors, err := cachedB.BatchEmbed(ctx, []string{"completed", "unfinished"})
	require.NoError(t, err)
	require.Len(t, vectors, 2)
	require.Equal(t, 1, providerB.Snapshot().TotalInputItems)
}

type wrongCountEmbedder struct{ *modelcount.CountingEmbedder }

func (e *wrongCountEmbedder) BatchEmbed(ctx context.Context, texts []string) ([][]float32, error) {
	vectors, err := e.CountingEmbedder.BatchEmbed(ctx, texts)
	if err != nil || len(vectors) == 0 {
		return vectors, err
	}
	return vectors[:len(vectors)-1], nil
}

func TestEmbeddingArtifactProviderCountMismatchFailsAllClaims(t *testing.T) {
	db := newEmbeddingArtifactTestRepo(t)
	repo := repository.NewDerivedArtifactRepository(db)
	provider := &wrongCountEmbedder{modelcount.NewCountingEmbedder(modelcount.CountingEmbedderOptions{ModelName: "wrong-count", Dimensions: 3})}
	cached := newCachedTestEmbedder(t, repo, provider)
	_, err := cached.BatchEmbed(embeddingArtifactTestContext(1, types.IngestionOperationEmbeddingChunk), []string{"a", "b"})
	require.ErrorContains(t, err, "returned 1 vectors for 2 inputs")
	var failed int64
	require.NoError(t, db.Model(&types.DerivedArtifact{}).Where("status = ?", types.DerivedArtifactFailed).Count(&failed).Error)
	require.Equal(t, int64(2), failed)
}

func TestEmbeddingArtifactObservationReportsMissHitAndBatchReuse(t *testing.T) {
	db := newEmbeddingArtifactTestRepo(t)
	provider, cached := newEmbeddingArtifactTestModel(t, db, 3)
	ctx := embeddingArtifactTestContext(1, types.IngestionOperationEmbeddingChunk)
	missObserved := newIngestionObservedEmbedder(cached, types.IngestionOperationEmbeddingChunk)
	_, err := missObserved.BatchEmbed(ctx, []string{"alpha", "beta", "alpha"})
	require.NoError(t, err)
	miss := missObserved.Snapshot()
	require.Equal(t, 3, miss.TotalItems)
	require.Equal(t, 2, miss.ComputedItems)
	require.Equal(t, 1, miss.ReusedItems)
	require.Equal(t, 1, miss.RequestCount)
	require.Equal(t, 1, miss.BatchCount)
	require.Equal(t, types.IngestionCacheStatusMiss, miss.CacheStatus)

	hitObserved := newIngestionObservedEmbedder(cached, types.IngestionOperationEmbeddingChunk)
	_, err = hitObserved.BatchEmbed(ctx, []string{"beta", "alpha", "beta"})
	require.NoError(t, err)
	hit := hitObserved.Snapshot()
	require.Equal(t, 3, hit.TotalItems)
	require.Zero(t, hit.ComputedItems)
	require.Equal(t, 3, hit.ReusedItems)
	require.Zero(t, hit.RequestCount)
	require.Zero(t, hit.BatchCount)
	require.Equal(t, types.IngestionCacheStatusHit, hit.CacheStatus)
	require.Equal(t, 2, provider.Snapshot().TotalInputItems)

	output := embeddingOperationOutput(types.IngestionOperationEmbeddingChunk, types.StageEmbedding, cached, hit, 3, 0, true)
	require.Equal(t, "hit", output["cache_status"])
	require.Equal(t, 0, output["computed_items"])
	require.Equal(t, 3, output["reused_items"])
	require.Equal(t, 0, output["request_count"])
}

func TestEmbeddingArtifactObservationPartialHitCountsOnlyUniqueMisses(t *testing.T) {
	db := newEmbeddingArtifactTestRepo(t)
	_, cached := newEmbeddingArtifactTestModel(t, db, 3)
	ctx := embeddingArtifactTestContext(1, types.IngestionOperationEmbeddingSummary)
	_, err := cached.BatchEmbed(ctx, []string{"alpha", "beta"})
	require.NoError(t, err)
	observed := newIngestionObservedEmbedder(cached, types.IngestionOperationEmbeddingSummary)
	_, err = observed.BatchEmbed(ctx, []string{"alpha", "gamma", "gamma"})
	require.NoError(t, err)
	snapshot := observed.Snapshot()
	require.Equal(t, 3, snapshot.TotalItems)
	require.Equal(t, 1, snapshot.ComputedItems)
	require.Equal(t, 2, snapshot.ReusedItems)
	require.Equal(t, 1, snapshot.RequestCount)
	require.Equal(t, types.IngestionCacheStatusMiss, snapshot.CacheStatus)
}

func TestEmbeddingArtifactProductionBatchIndexReusesAndPartiallyRecomputes(t *testing.T) {
	db := newEmbeddingArtifactTestRepo(t)
	provider, cached := newEmbeddingArtifactTestModel(t, db, 3)
	storage := &embeddingProductionPathRepository{}
	engine := retrieversvc.NewKVHybridRetrieveEngine(storage, types.RetrieverEngineType("test"))
	indexer := embeddingProductionPathIndexer{engine: engine}
	ctx := embeddingArtifactTestContext(1, types.IngestionOperationEmbeddingChunk)
	makeInfos := func(second, suffix string) []*types.IndexInfo {
		return []*types.IndexInfo{{Content: "alpha", SourceID: "source-alpha" + suffix, ChunkID: "chunk-a" + suffix, KnowledgeID: "knowledge-a" + suffix, KnowledgeBaseID: "kb-a" + suffix}, {Content: second, SourceID: "source-second" + suffix, ChunkID: "chunk-b" + suffix, KnowledgeID: "knowledge-b" + suffix, KnowledgeBaseID: "kb-b" + suffix}}
	}

	firstObservation, err := executeObservedEmbeddingBatch(ctx, types.IngestionOperationEmbeddingChunk, cached, indexer, makeInfos("beta", ""))
	require.NoError(t, err)
	require.Equal(t, 2, firstObservation.ComputedItems)
	require.Zero(t, firstObservation.ReusedItems)
	require.Equal(t, types.IngestionCacheStatusMiss, firstObservation.CacheStatus)
	require.Equal(t, 2, provider.Snapshot().TotalInputItems)
	firstSaved := storage.saves[len(storage.saves)-1]

	secondObservation, err := executeObservedEmbeddingBatch(ctx, types.IngestionOperationEmbeddingChunk, cached, indexer, makeInfos("beta", "-rebuilt"))
	require.NoError(t, err)
	require.Zero(t, secondObservation.ComputedItems)
	require.Equal(t, 2, secondObservation.ReusedItems)
	require.Zero(t, secondObservation.RequestCount)
	require.Equal(t, types.IngestionCacheStatusHit, secondObservation.CacheStatus)
	require.Equal(t, 2, provider.Snapshot().TotalInputItems)
	secondSaved := storage.saves[len(storage.saves)-1]
	require.Equal(t, firstSaved["source-alpha"], secondSaved["source-alpha-rebuilt"])
	require.Equal(t, firstSaved["source-second"], secondSaved["source-second-rebuilt"])

	partialObservation, err := executeObservedEmbeddingBatch(ctx, types.IngestionOperationEmbeddingChunk, cached, indexer, makeInfos("gamma", "-partial"))
	require.NoError(t, err)
	require.Equal(t, 1, partialObservation.ComputedItems)
	require.Equal(t, 1, partialObservation.ReusedItems)
	require.Equal(t, types.IngestionCacheStatusMiss, partialObservation.CacheStatus)
	require.Equal(t, 3, provider.Snapshot().TotalInputItems)
	partialSaved := storage.saves[len(storage.saves)-1]
	require.Equal(t, firstSaved["source-alpha"], partialSaved["source-alpha-partial"])
	require.Contains(t, partialSaved, "source-second-partial")
}
