package service

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/artifactkey"
	"github.com/Tencent/WeKnora/internal/models/vlm"
	"github.com/Tencent/WeKnora/internal/testutil/modelcount"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func newMultimodalArtifactTestService(t *testing.T) (*ImageMultimodalService, *gorm.DB) {
	dsn := fmt.Sprintf("file:%s?_busy_timeout=5000&_journal_mode=WAL", filepath.ToSlash(filepath.Join(t.TempDir(), "artifacts.db")))
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&types.DerivedArtifact{}))
	return &ImageMultimodalService{artifactRepo: repository.NewDerivedArtifactRepository(db)}, db
}

type blockingMultimodalVLM struct {
	entered chan struct{}
	release chan struct{}
	result  string
	err     error
	count   atomic.Int32
	once    sync.Once
}

func newBlockingMultimodalVLM(result string) *blockingMultimodalVLM {
	return &blockingMultimodalVLM{entered: make(chan struct{}), release: make(chan struct{}), result: result}
}
func (m *blockingMultimodalVLM) Predict(ctx context.Context, _ [][]byte, _ string) (string, error) {
	m.count.Add(1)
	m.once.Do(func() { close(m.entered) })
	select {
	case <-m.release:
		return m.result, m.err
	case <-ctx.Done():
		return "", ctx.Err()
	}
}
func (*blockingMultimodalVLM) GetModelName() string { return "blocking" }
func (*blockingMultimodalVLM) GetModelID() string   { return "blocking-vlm" }

var _ vlm.VLM = (*blockingMultimodalVLM)(nil)

type sequenceMultimodalVLM struct {
	mu       sync.Mutex
	calls    int
	firstErr error
	result   string
}

func (m *sequenceMultimodalVLM) Predict(context.Context, [][]byte, string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.calls++
	if m.calls == 1 {
		return "sensitive provider response", m.firstErr
	}
	return m.result, nil
}
func (*sequenceMultimodalVLM) GetModelName() string { return "sequence" }
func (*sequenceMultimodalVLM) GetModelID() string   { return "sequence-vlm" }
func (m *sequenceMultimodalVLM) Count() int         { m.mu.Lock(); defer m.mu.Unlock(); return m.calls }

func multimodalTestSpec() multimodalCacheSpec {
	return multimodalCacheSpec{Kind: "multimodal.ocr", Operation: types.IngestionOperationMultimodalOCR, Prompt: "ocr prompt", Normalize: sanitizeOCRText}
}

func multimodalTestKey(tenant uint64, modelID string, image []byte, spec multimodalCacheSpec) string {
	promptVersion := spec.PromptVersion
	if promptVersion == "" {
		promptVersion = multimodalPromptVersion
	}
	producerVersion := spec.ProducerVersion
	if producerVersion == "" {
		producerVersion = multimodalProducerVersion
	}
	configDigest, _ := artifactkey.DigestConfig(map[string]string{"prompt": spec.Prompt})
	return artifactkey.Generate(artifactkey.KeyInput{Kind: spec.Kind, TenantScope: fmt.Sprintf("tenant:%d", tenant), InputDigest: artifactkey.DigestBytes(image), ModelID: modelID, PromptVersion: promptVersion, ConfigDigest: configDigest, ProducerVersion: producerVersion})
}

func TestMultimodalArtifactCacheFreezesCanonicalResult(t *testing.T) {
	svc, _ := newMultimodalArtifactTestService(t)
	model := modelcount.NewCountingVLM(modelcount.CountingVLMOptions{ModelID: "vlm-a", OCRResponse: "  alpha\r\n beta  "})
	spec := multimodalCacheSpec{Kind: "multimodal.ocr", Operation: types.IngestionOperationMultimodalOCR, Prompt: "ocr", Normalize: sanitizeOCRText}

	first, hit, err := svc.cachedMultimodalPredict(context.Background(), 1, model, "vlm-a", []byte("same-image"), spec)
	require.NoError(t, err)
	require.False(t, hit)
	second, hit, err := svc.cachedMultimodalPredict(context.Background(), 1, model, "vlm-a", []byte("same-image"), spec)
	require.NoError(t, err)
	require.True(t, hit)
	require.Equal(t, first, second)
	require.Equal(t, 1, model.Snapshot().PredictRequestCount)
}

func TestMultimodalArtifactCacheKeyBoundaries(t *testing.T) {
	svc, _ := newMultimodalArtifactTestService(t)
	model := modelcount.NewCountingVLM(modelcount.CountingVLMOptions{OCRResponse: "text"})
	base := multimodalCacheSpec{Kind: "multimodal.ocr", Operation: types.IngestionOperationMultimodalOCR, Prompt: "p1", Normalize: sanitizeOCRText}
	_, _, err := svc.cachedMultimodalPredict(context.Background(), 1, model, "model-a", []byte("image-a"), base)
	require.NoError(t, err)
	_, _, err = svc.cachedMultimodalPredict(context.Background(), 1, model, "model-a", []byte("image-b"), base)
	require.NoError(t, err)
	_, _, err = svc.cachedMultimodalPredict(context.Background(), 1, model, "model-b", []byte("image-a"), base)
	require.NoError(t, err)
	_, _, err = svc.cachedMultimodalPredict(context.Background(), 2, model, "model-a", []byte("image-a"), base)
	require.NoError(t, err)
	changedPrompt := base
	changedPrompt.Prompt = "p2"
	_, _, err = svc.cachedMultimodalPredict(context.Background(), 1, model, "model-a", []byte("image-a"), changedPrompt)
	require.NoError(t, err)
	changedVersion := base
	changedVersion.PromptVersion = "multimodal-prompt/v2"
	_, _, err = svc.cachedMultimodalPredict(context.Background(), 1, model, "model-a", []byte("image-a"), changedVersion)
	require.NoError(t, err)
	changedProducer := base
	changedProducer.ProducerVersion = "multimodal-producer/v2"
	_, _, err = svc.cachedMultimodalPredict(context.Background(), 1, model, "model-a", []byte("image-a"), changedProducer)
	require.NoError(t, err)
	require.Equal(t, 7, model.Snapshot().PredictRequestCount)
}

func TestMultimodalArtifactKeyIgnoresUnrelatedIngestionSettings(t *testing.T) {
	svc, _ := newMultimodalArtifactTestService(t)
	model := modelcount.NewCountingVLM(modelcount.CountingVLMOptions{OCRResponse: "stable"})
	spec, image := multimodalTestSpec(), []byte("shared-across-knowledge")
	// Knowledge ID, embedding model, chunk size, and Wiki configuration are
	// intentionally absent from cachedMultimodalPredict and KeyInput.
	unrelatedVariants := []struct {
		knowledgeID, embeddingModel string
		chunkSize                   int
		wikiEnabled                 bool
	}{
		{"knowledge-a", "embedding-a", 512, false},
		{"knowledge-b", "embedding-b", 2048, true},
	}
	for i := range unrelatedVariants {
		_, hit, err := svc.cachedMultimodalPredict(context.Background(), 1, model, "model-a", image, spec)
		require.NoError(t, err)
		require.Equal(t, i > 0, hit)
	}
	require.Equal(t, 1, model.Snapshot().PredictRequestCount)

	caption := spec
	caption.Kind = "multimodal.caption"
	caption.Operation = types.IngestionOperationMultimodalCaption
	_, hit, err := svc.cachedMultimodalPredict(context.Background(), 1, model, "model-a", image, caption)
	require.NoError(t, err)
	require.False(t, hit)
	scanned := spec
	scanned.Prompt = "scanned pdf OCR prompt"
	_, hit, err = svc.cachedMultimodalPredict(context.Background(), 1, model, "model-a", image, scanned)
	require.NoError(t, err)
	require.False(t, hit)
	custom := spec
	custom.Prompt = spec.Prompt + "\ncustom instructions"
	_, hit, err = svc.cachedMultimodalPredict(context.Background(), 1, model, "model-a", image, custom)
	require.NoError(t, err)
	require.False(t, hit)
}

func TestNewImageMultimodalServiceRequiresArtifactRepository(t *testing.T) {
	svc, _ := newMultimodalArtifactTestService(t)
	require.Panics(t, func() {
		NewImageMultimodalService(nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	})
	handler := NewImageMultimodalService(nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, svc.artifactRepo, nil)
	require.NotNil(t, handler)
}

func TestMultimodalArtifactSingleflightBusyWaitsForCanonicalResult(t *testing.T) {
	svc, _ := newMultimodalArtifactTestService(t)
	svc.artifactTiming = multimodalArtifactTiming{Lease: 200 * time.Millisecond, Wait: time.Second, Poll: 5 * time.Millisecond}
	model := newBlockingMultimodalVLM("  canonical\r\nresult ")
	spec, image := multimodalTestSpec(), []byte("concurrent-image")
	type result struct {
		value string
		hit   bool
		err   error
	}
	results := make(chan result, 2)
	go func() {
		v, h, e := svc.cachedMultimodalPredict(context.Background(), 1, model, "blocking-vlm", image, spec)
		results <- result{v, h, e}
	}()
	<-model.entered
	go func() {
		v, h, e := svc.cachedMultimodalPredict(context.Background(), 1, model, "blocking-vlm", image, spec)
		results <- result{v, h, e}
	}()
	time.Sleep(25 * time.Millisecond)
	require.Equal(t, int32(1), model.count.Load())
	close(model.release)
	a, b := <-results, <-results
	require.NoError(t, a.err)
	require.NoError(t, b.err)
	require.Equal(t, a.value, b.value)
	require.Equal(t, "canonical\nresult", a.value)
	require.NotEqual(t, a.hit, b.hit)
	require.Equal(t, int32(1), model.count.Load())
}

func TestMultimodalArtifactBusyTimeoutAndCancellationDoNotCallVLM(t *testing.T) {
	svc, _ := newMultimodalArtifactTestService(t)
	svc.artifactTiming = multimodalArtifactTiming{Lease: time.Second, Wait: 35 * time.Millisecond, Poll: 5 * time.Millisecond}
	spec, image := multimodalTestSpec(), []byte("busy-image")
	key := multimodalTestKey(1, "counting-vlm", image, spec)
	_, err := svc.artifactRepo.Claim(context.Background(), interfaces.ArtifactClaim{TenantID: 1, ArtifactKey: key, ArtifactKind: spec.Kind, InputDigest: artifactkey.DigestBytes(image), ModelID: "counting-vlm", PromptVersion: multimodalPromptVersion, ConfigDigest: artifactkey.DigestText("config"), ProducerVersion: multimodalProducerVersion, OwnerToken: "other-owner", LeaseDuration: time.Second})
	require.NoError(t, err)
	model := modelcount.NewCountingVLM(modelcount.CountingVLMOptions{})
	_, _, err = svc.cachedMultimodalPredict(context.Background(), 1, model, "counting-vlm", image, spec)
	require.Error(t, err)
	require.True(t, isMultimodalCacheInfrastructureError(err))
	require.Contains(t, err.Error(), "busy wait timed out")
	require.Zero(t, model.Snapshot().PredictRequestCount)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	start := time.Now()
	_, _, err = svc.cachedMultimodalPredict(ctx, 1, model, "counting-vlm", image, spec)
	require.ErrorIs(t, err, context.Canceled)
	require.Less(t, time.Since(start), 100*time.Millisecond)
	require.Zero(t, model.Snapshot().PredictRequestCount)
}

func TestMultimodalArtifactHeartbeatKeepsLongComputationOwned(t *testing.T) {
	svc, _ := newMultimodalArtifactTestService(t)
	svc.artifactTiming = multimodalArtifactTiming{Lease: 60 * time.Millisecond, Wait: time.Second, Poll: 5 * time.Millisecond}
	model := newBlockingMultimodalVLM("canonical")
	spec, image := multimodalTestSpec(), []byte("heartbeat-image")
	done := make(chan error, 1)
	go func() {
		_, _, err := svc.cachedMultimodalPredict(context.Background(), 1, model, "blocking-vlm", image, spec)
		done <- err
	}()
	<-model.entered
	time.Sleep(95 * time.Millisecond)
	key := multimodalTestKey(1, "blocking-vlm", image, spec)
	claim, err := svc.artifactRepo.Claim(context.Background(), interfaces.ArtifactClaim{TenantID: 1, ArtifactKey: key, ArtifactKind: spec.Kind, InputDigest: artifactkey.DigestBytes(image), ModelID: "blocking-vlm", OwnerToken: "second-owner", LeaseDuration: time.Second})
	require.NoError(t, err)
	require.Equal(t, interfaces.ArtifactClaimBusy, claim.Outcome)
	close(model.release)
	require.NoError(t, <-done)
	require.Equal(t, int32(1), model.count.Load())
}

type lostRenewRepository struct {
	interfaces.DerivedArtifactRepository
	lost chan struct{}
	once sync.Once
}

func (r *lostRenewRepository) RenewLease(context.Context, uint64, string, string, time.Time, time.Duration) error {
	r.once.Do(func() { close(r.lost) })
	return interfaces.ErrArtifactLostOwnership
}

func TestMultimodalArtifactLostOwnerCannotOverwriteTakeover(t *testing.T) {
	svc, _ := newMultimodalArtifactTestService(t)
	wrapped := &lostRenewRepository{DerivedArtifactRepository: svc.artifactRepo, lost: make(chan struct{})}
	svc.artifactRepo = wrapped
	svc.artifactTiming = multimodalArtifactTiming{Lease: 45 * time.Millisecond, Wait: time.Second, Poll: 5 * time.Millisecond}
	oldModel := newBlockingMultimodalVLM("old-result")
	spec, image := multimodalTestSpec(), []byte("takeover-image")
	oldDone := make(chan error, 1)
	go func() {
		_, _, err := svc.cachedMultimodalPredict(context.Background(), 1, oldModel, "blocking-vlm", image, spec)
		oldDone <- err
	}()
	<-oldModel.entered
	<-wrapped.lost
	time.Sleep(50 * time.Millisecond)
	newModel := modelcount.NewCountingVLM(modelcount.CountingVLMOptions{OCRResponse: "new-result"})
	value, hit, err := svc.cachedMultimodalPredict(context.Background(), 1, newModel, "blocking-vlm", image, spec)
	require.NoError(t, err)
	require.False(t, hit)
	require.Equal(t, "new-result", value)
	close(oldModel.release)
	require.Error(t, <-oldDone)
	value, hit, err = svc.cachedMultimodalPredict(context.Background(), 1, newModel, "blocking-vlm", image, spec)
	require.NoError(t, err)
	require.True(t, hit)
	require.Equal(t, "new-result", value)
}

func TestMultimodalArtifactProviderFailureCanRetryAndFreeze(t *testing.T) {
	svc, db := newMultimodalArtifactTestService(t)
	model := &sequenceMultimodalVLM{firstErr: errors.New("secret provider detail"), result: "successful"}
	spec, image := multimodalTestSpec(), []byte("private-image-bytes")
	_, _, err := svc.cachedMultimodalPredict(context.Background(), 1, model, "sequence-vlm", image, spec)
	require.Error(t, err)
	var failed types.DerivedArtifact
	require.NoError(t, db.Where("artifact_kind = ?", spec.Kind).Take(&failed).Error)
	require.Equal(t, types.DerivedArtifactFailed, failed.Status)
	require.NotContains(t, failed.ErrorMessage, "private-image-bytes")
	require.NotContains(t, failed.ErrorMessage, "ocr prompt")
	require.NotContains(t, failed.ErrorMessage, "secret provider detail")
	_, hit, err := svc.cachedMultimodalPredict(context.Background(), 1, model, "sequence-vlm", image, spec)
	require.NoError(t, err)
	require.False(t, hit)
	value, hit, err := svc.cachedMultimodalPredict(context.Background(), 1, model, "sequence-vlm", image, spec)
	require.NoError(t, err)
	require.True(t, hit)
	require.Equal(t, "successful", value)
	require.Equal(t, 2, model.Count())
}

func TestMultimodalArtifactCancellationPersistsFailedWithCleanupContext(t *testing.T) {
	svc, db := newMultimodalArtifactTestService(t)
	svc.artifactTiming = multimodalArtifactTiming{Lease: time.Second, Wait: time.Second, Poll: 5 * time.Millisecond, Cleanup: 200 * time.Millisecond}
	model := newBlockingMultimodalVLM("")
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, _, err := svc.cachedMultimodalPredict(ctx, 1, model, "blocking-vlm", []byte("cancel-image"), multimodalTestSpec())
		done <- err
	}()
	<-model.entered
	cancel()
	require.ErrorIs(t, <-done, context.Canceled)
	var artifact types.DerivedArtifact
	require.NoError(t, db.Where("artifact_kind = ?", "multimodal.ocr").Take(&artifact).Error)
	require.Equal(t, types.DerivedArtifactFailed, artifact.Status)
}

func TestMultimodalArtifactCorruptSucceededFailsClosed(t *testing.T) {
	cases := []struct {
		name, encoding string
		payload        []byte
		badDigest      bool
	}{
		{"digest", "json", []byte(`{"schema_version":"multimodal-artifact/v1","result":"x","present":true}`), true},
		{"malformed", "json", []byte(`{`), false}, {"schema", "json", []byte(`{"schema_version":"future","result":"x","present":true}`), false},
		{"missing", "json", []byte(`{"schema_version":"multimodal-artifact/v1","result":"x","present":false}`), false},
		{"encoding", "text", []byte(`x`), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			svc, db := newMultimodalArtifactTestService(t)
			spec, image := multimodalTestSpec(), []byte("corrupt-image")
			model := modelcount.NewCountingVLM(modelcount.CountingVLMOptions{OCRResponse: "initial"})
			_, _, err := svc.cachedMultimodalPredict(context.Background(), 1, model, "counting-vlm", image, spec)
			require.NoError(t, err)
			digest := artifactkey.DigestBytes(tc.payload)
			if tc.badDigest {
				digest = artifactkey.DigestText("wrong")
			}
			require.NoError(t, db.Model(&types.DerivedArtifact{}).Where("artifact_kind = ?", spec.Kind).Updates(map[string]any{"payload": tc.payload, "payload_encoding": tc.encoding, "payload_digest": digest}).Error)
			_, _, err = svc.cachedMultimodalPredict(context.Background(), 1, model, "counting-vlm", image, spec)
			require.Error(t, err)
			require.True(t, isMultimodalCacheInfrastructureError(err))
			require.Zero(t, model.Snapshot().PredictRequestCount-1)
			var row types.DerivedArtifact
			require.NoError(t, db.Where("artifact_kind = ?", spec.Kind).Take(&row).Error)
			require.Equal(t, types.DerivedArtifactSucceeded, row.Status)
		})
	}
}

func TestMultimodalArtifactPayloadAcceptsExplicitEmptyResult(t *testing.T) {
	payload := []byte(`{"schema_version":"multimodal-artifact/v1","result":"","present":true}`)
	value, err := decodeMultimodalArtifact(&types.DerivedArtifact{Payload: payload, PayloadEncoding: "json"})
	require.NoError(t, err)
	require.Empty(t, value)

	_, err = decodeMultimodalArtifact(&types.DerivedArtifact{Payload: []byte(`{"schema_version":"future","result":"","present":true}`), PayloadEncoding: "json"})
	require.Error(t, err)
}
