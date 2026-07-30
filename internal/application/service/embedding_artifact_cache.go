package service

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"sync"
	"time"

	"github.com/Tencent/WeKnora/internal/artifactkey"
	"github.com/Tencent/WeKnora/internal/models/embedding"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

const (
	embeddingArtifactKind          = "embedding.vector"
	embeddingArtifactSchemaVersion = "embedding-artifact/v1"
	embeddingProducerVersion       = "embedding-producer/v1"
	embeddingArtifactEncoding      = "embedding-f32-le-v1"
	embeddingArtifactLease         = 2 * time.Minute
	embeddingArtifactWait          = 3 * time.Minute
	embeddingArtifactPoll          = 100 * time.Millisecond
	embeddingArtifactCleanup       = 2 * time.Second
)

var embeddingArtifactMagic = [8]byte{'W', 'K', 'N', 'E', 'M', 'B', 1, 0}

type embeddingArtifactTiming struct {
	Lease, Wait, Poll, Cleanup time.Duration
}

type artifactCachedEmbedder struct {
	inner         embedding.Embedder
	repo          interfaces.DerivedArtifactRepository
	modelID       string
	modelRevision string
	promptVersion string
	configDigest  string
	timing        embeddingArtifactTiming
}

type embeddingCacheRecorderContextKey struct{}

type embeddingCacheRecorder struct {
	mu                                     sync.Mutex
	totalItems, computedItems, reusedItems int
	requestCount, batchCount, inputChars   int
	status                                 types.IngestionCacheStatus
}

func withEmbeddingCacheRecorder(ctx context.Context, recorder *embeddingCacheRecorder) context.Context {
	return context.WithValue(ctx, embeddingCacheRecorderContextKey{}, recorder)
}

func embeddingCacheRecorderFromContext(ctx context.Context) *embeddingCacheRecorder {
	if ctx == nil {
		return nil
	}
	recorder, _ := ctx.Value(embeddingCacheRecorderContextKey{}).(*embeddingCacheRecorder)
	return recorder
}

func (r *embeddingCacheRecorder) recordProvider(texts []string) {
	if r == nil {
		return
	}
	chars := 0
	for _, text := range texts {
		chars += len(text)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.requestCount++
	r.batchCount++
	r.inputChars += chars
}

func (r *embeddingCacheRecorder) providerRequests() int {
	if r == nil {
		return 0
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.requestCount
}

func (r *embeddingCacheRecorder) recordResult(total, computed int, status types.IngestionCacheStatus) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.totalItems = total
	r.computedItems = computed
	r.reusedItems = total - computed
	r.status = status
}

func (r *embeddingCacheRecorder) recordError(total int) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.totalItems = total
	r.status = types.IngestionCacheStatusError
}

func (r *embeddingCacheRecorder) snapshot() embeddingRequestSnapshot {
	if r == nil {
		return embeddingRequestSnapshot{}
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return embeddingRequestSnapshot{RequestCount: r.requestCount, BatchCount: r.batchCount, TotalItems: r.totalItems,
		ComputedItems: r.computedItems, ReusedItems: r.reusedItems, InputChars: r.inputChars,
		CacheStatus: r.status, CacheSupported: true}
}

type embeddingArtifactCacheMarker interface{ isEmbeddingArtifactCache() }

func (*artifactCachedEmbedder) isEmbeddingArtifactCache() {}

var _ embedding.Embedder = (*artifactCachedEmbedder)(nil)

// NewArtifactCachedEmbedder adds the durable cache to the model boundary.
// It remains inert unless the context carries one of the explicitly supported
// ingestion embedding operations and a tenant ID, so query-time embeddings are
// never persisted by this wrapper.
func newArtifactCachedEmbedder(inner embedding.Embedder, repo interfaces.DerivedArtifactRepository, config embedding.Config) (embedding.Embedder, error) {
	if inner == nil || repo == nil {
		return inner, nil
	}
	identityConfig := embeddingIdentityConfig(config, inner)
	digest, err := artifactkey.DigestConfig(identityConfig)
	if err != nil {
		return nil, fmt.Errorf("digest embedding artifact config: %w", err)
	}
	modelID := config.ModelName
	if remoteModelName := config.ExtraConfig["remote_model_name"]; remoteModelName != "" {
		modelID = remoteModelName
	}
	if modelID == "" {
		modelID = inner.GetModelName()
	}
	modelRevision := allowedEmbeddingModelRevision(config.ExtraConfig)
	return &artifactCachedEmbedder{
		inner: inner, repo: repo, modelID: modelID,
		modelRevision: modelRevision,
		promptVersion: config.ExtraConfig["embedding_prompt_version"],
		configDigest:  digest,
	}, nil
}

func embeddingIdentityConfig(config embedding.Config, inner embedding.Embedder) map[string]any {
	// Deliberate allowlist. BaseURL, API keys, arbitrary ExtraConfig, and custom
	// headers are transport/credential concerns and never participate in cache
	// identity. New output-affecting fields must be reviewed and added here.
	return map[string]any{
		"source":                      config.Source,
		"provider":                    config.Provider,
		"model_name":                  config.ModelName,
		"remote_model_name":           config.ExtraConfig["remote_model_name"],
		"dimensions":                  inner.GetDimensions(),
		"truncate_prompt_tokens":      config.TruncatePromptTokens,
		"supports_dimension_override": config.SupportsDimensionOverride,
		"document_mode":               config.ExtraConfig["document_mode"],
		"embedding_instruction":       config.ExtraConfig["embedding_instruction"],
		"embedding_prefix":            config.ExtraConfig["embedding_prefix"],
		"embedding_prompt_version":    config.ExtraConfig["embedding_prompt_version"],
		"model_revision":              allowedEmbeddingModelRevision(config.ExtraConfig),
	}
}

func allowedEmbeddingModelRevision(extra map[string]string) string {
	if value := extra["model_revision"]; value != "" {
		return value
	}
	return extra["revision"]
}

func (e *artifactCachedEmbedder) artifactTiming() embeddingArtifactTiming {
	t := e.timing
	if t.Lease <= 0 {
		t.Lease = embeddingArtifactLease
	}
	if t.Wait <= 0 {
		t.Wait = embeddingArtifactWait
	}
	if t.Poll <= 0 {
		t.Poll = embeddingArtifactPoll
	}
	if t.Cleanup <= 0 {
		t.Cleanup = embeddingArtifactCleanup
	}
	return t
}

func (e *artifactCachedEmbedder) Embed(ctx context.Context, text string) ([]float32, error) {
	vectors, err := e.cachedBatch(ctx, []string{text}, func(computeCtx context.Context, misses []string) ([][]float32, error) {
		vectors := make([][]float32, len(misses))
		for i, input := range misses {
			vector, err := e.inner.Embed(computeCtx, input)
			if err != nil {
				return nil, err
			}
			vectors[i] = vector
		}
		return vectors, nil
	})
	if err != nil {
		return nil, err
	}
	if len(vectors) != 1 {
		return nil, fmt.Errorf("embedding cache returned %d vectors for one input", len(vectors))
	}
	return vectors[0], nil
}

func (e *artifactCachedEmbedder) BatchEmbed(ctx context.Context, texts []string) ([][]float32, error) {
	return e.cachedBatch(ctx, texts, func(computeCtx context.Context, misses []string) ([][]float32, error) {
		return e.inner.BatchEmbed(computeCtx, misses)
	})
}

func (e *artifactCachedEmbedder) BatchEmbedWithPool(ctx context.Context, _ embedding.Embedder, texts []string) ([][]float32, error) {
	return e.cachedBatch(ctx, texts, func(computeCtx context.Context, misses []string) ([][]float32, error) {
		return e.inner.BatchEmbedWithPool(computeCtx, e.inner, misses)
	})
}

func (e *artifactCachedEmbedder) GetModelName() string { return e.inner.GetModelName() }
func (e *artifactCachedEmbedder) GetDimensions() int   { return e.inner.GetDimensions() }
func (e *artifactCachedEmbedder) GetModelID() string   { return e.inner.GetModelID() }

type embeddingClaim struct{ key, digest, owner, text string }

func (e *artifactCachedEmbedder) cachedBatch(ctx context.Context, texts []string, compute func(context.Context, []string) ([][]float32, error)) ([][]float32, error) {
	if !cacheableEmbeddingOperation(types.IngestionOperationFromContext(ctx)) {
		return compute(ctx, texts)
	}
	tenantID, ok := types.TenantIDFromContext(ctx)
	if !ok || tenantID == 0 {
		return compute(ctx, texts)
	}
	if len(texts) == 0 {
		if recorder := embeddingCacheRecorderFromContext(ctx); recorder != nil {
			recorder.recordResult(0, 0, types.IngestionCacheStatusHit)
		}
		return [][]float32{}, nil
	}

	results := make([][]float32, len(texts))
	positions := make(map[string][]int, len(texts))
	unique := make([]string, 0, len(texts))
	for i, text := range texts {
		d := artifactkey.DigestText(text)
		if _, exists := positions[d]; !exists {
			unique = append(unique, text)
		}
		positions[d] = append(positions[d], i)
	}
	claims := make([]embeddingClaim, 0, len(unique))
	for _, text := range unique {
		digest := artifactkey.DigestText(text)
		key := artifactkey.Generate(artifactkey.KeyInput{
			Kind: embeddingArtifactKind, TenantScope: fmt.Sprintf("tenant:%d", tenantID), InputDigest: digest,
			ModelID: e.modelID, ModelRevision: e.modelRevision, PromptVersion: e.promptVersion,
			ConfigDigest: e.configDigest, ProducerVersion: embeddingProducerVersion,
		})
		vector, claim, err := e.lookupOrClaim(ctx, tenantID, key, digest, text)
		if err != nil {
			if recorder := embeddingCacheRecorderFromContext(ctx); recorder != nil {
				recorder.recordError(len(texts))
			}
			e.failEmbeddingClaims(ctx, tenantID, claims, "embedding_cache_failure")
			return nil, err
		}
		if claim == nil {
			for _, pos := range positions[digest] {
				results[pos] = cloneEmbeddingVector(vector)
			}
		} else {
			claims = append(claims, *claim)
		}
	}
	if len(claims) == 0 {
		if recorder := embeddingCacheRecorderFromContext(ctx); recorder != nil {
			recorder.recordResult(len(texts), 0, types.IngestionCacheStatusHit)
		}
		return results, nil
	}
	misses := make([]string, len(claims))
	for i := range claims {
		misses[i] = claims[i].text
	}
	stopHeartbeat := e.startEmbeddingHeartbeat(ctx, tenantID, claims)
	recorder := embeddingCacheRecorderFromContext(ctx)
	if recorder != nil {
		recorder.recordResult(len(texts), len(claims), types.IngestionCacheStatusMiss)
	}
	beforeRequests := recorder.providerRequests()
	computeCtx := embedding.WithProviderCallObserver(ctx, func(providerTexts []string) { recorder.recordProvider(providerTexts) })
	vectors, err := compute(computeCtx, misses)
	if recorder != nil && recorder.providerRequests() == beforeRequests {
		recorder.recordProvider(misses)
	}
	ownershipErr := stopHeartbeat()
	if ownershipErr != nil {
		if recorder != nil {
			recorder.recordError(len(texts))
		}
		return nil, fmt.Errorf("embedding artifact lease: %w", ownershipErr)
	}
	if err != nil {
		if recorder != nil {
			recorder.recordError(len(texts))
		}
		e.failEmbeddingClaims(ctx, tenantID, claims, "embedding_provider_failure")
		return nil, err
	}
	if len(vectors) != len(claims) {
		if recorder != nil {
			recorder.recordError(len(texts))
		}
		e.failEmbeddingClaims(ctx, tenantID, claims, "embedding_invalid_response")
		return nil, fmt.Errorf("embedding provider returned %d vectors for %d inputs", len(vectors), len(claims))
	}
	for i, c := range claims {
		if len(vectors[i]) != e.GetDimensions() {
			if recorder != nil {
				recorder.recordError(len(texts))
			}
			e.failEmbeddingClaims(ctx, tenantID, claims[i:], "embedding_invalid_dimension")
			return nil, fmt.Errorf("embedding vector dimension %d does not match configured dimension %d", len(vectors[i]), e.GetDimensions())
		}
		payload, encodeErr := encodeEmbeddingArtifact(vectors[i])
		if encodeErr != nil {
			if recorder != nil {
				recorder.recordError(len(texts))
			}
			e.failEmbeddingClaims(ctx, tenantID, claims[i:], "embedding_payload_failure")
			return nil, encodeErr
		}
		if completeErr := e.repo.Complete(ctx, interfaces.ArtifactCompletion{TenantID: tenantID, ArtifactKey: c.key, OwnerToken: c.owner, Payload: payload, PayloadEncoding: embeddingArtifactEncoding, PayloadDigest: artifactkey.DigestBytes(payload)}); completeErr != nil {
			if recorder != nil {
				recorder.recordError(len(texts))
			}
			e.failEmbeddingClaims(ctx, tenantID, claims[i:], "embedding_cache_failure")
			return nil, fmt.Errorf("complete embedding artifact: %w", completeErr)
		}
		for _, pos := range positions[c.digest] {
			results[pos] = cloneEmbeddingVector(vectors[i])
		}
	}
	return results, nil
}

func (e *artifactCachedEmbedder) startEmbeddingHeartbeat(ctx context.Context, tenantID uint64, claims []embeddingClaim) func() error {
	timing := e.artifactTiming()
	heartbeatCtx, cancel := context.WithCancel(ctx)
	stop := make(chan struct{})
	var stopOnce sync.Once
	done := make(chan struct{})
	var mu sync.Mutex
	var ownershipErr error
	go func() {
		defer close(done)
		interval := timing.Lease / 3
		if interval <= 0 {
			interval = time.Millisecond
		}
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-stop:
				return
			case <-heartbeatCtx.Done():
				return
			case now := <-ticker.C:
				for _, claim := range claims {
					if err := e.repo.RenewLease(heartbeatCtx, tenantID, claim.key, claim.owner, now, timing.Lease); err != nil {
						if heartbeatCtx.Err() != nil {
							return
						}
						mu.Lock()
						ownershipErr = err
						mu.Unlock()
						return
					}
				}
			}
		}
	}()
	return func() error {
		stopOnce.Do(func() { close(stop) })
		<-done
		cancel()
		mu.Lock()
		defer mu.Unlock()
		return ownershipErr
	}
}

func (e *artifactCachedEmbedder) failEmbeddingClaims(ctx context.Context, tenantID uint64, claims []embeddingClaim, code string) {
	failCtx := ctx
	cancel := func() {}
	if ctx.Err() != nil {
		failCtx, cancel = context.WithTimeout(context.WithoutCancel(ctx), e.artifactTiming().Cleanup)
	}
	defer cancel()
	for _, claim := range claims {
		_ = e.repo.Fail(failCtx, interfaces.ArtifactFailure{
			TenantID: tenantID, ArtifactKey: claim.key, OwnerToken: claim.owner,
			ErrorCode: code, ErrorMessage: "embedding artifact computation failed",
		})
	}
}

func (e *artifactCachedEmbedder) lookupOrClaim(ctx context.Context, tenantID uint64, key, digest, text string) ([]float32, *embeddingClaim, error) {
	timing := e.artifactTiming()
	deadline := time.Now().Add(timing.Wait)
	for {
		owner, err := newEmbeddingOwnerToken()
		if err != nil {
			return nil, nil, err
		}
		claim, err := e.repo.Claim(ctx, interfaces.ArtifactClaim{
			TenantID: tenantID, ArtifactKey: key, ArtifactKind: embeddingArtifactKind, InputDigest: digest,
			ModelID: e.modelID, ModelRevision: e.modelRevision, PromptVersion: e.promptVersion,
			ConfigDigest: e.configDigest, ProducerVersion: embeddingProducerVersion,
			OwnerToken: owner, LeaseDuration: timing.Lease,
		})
		if err != nil {
			return nil, nil, fmt.Errorf("claim embedding artifact: %w", err)
		}
		switch claim.Outcome {
		case interfaces.ArtifactClaimHit:
			vector, err := decodeEmbeddingArtifact(claim.Artifact.Payload, claim.Artifact.PayloadEncoding, e.GetDimensions())
			return vector, nil, err
		case interfaces.ArtifactClaimClaimed:
			return nil, &embeddingClaim{key: key, digest: digest, owner: owner, text: text}, nil
		case interfaces.ArtifactClaimBusy:
			if time.Now().After(deadline) {
				return nil, nil, errors.New("embedding artifact busy wait timed out")
			}
			select {
			case <-ctx.Done():
				return nil, nil, ctx.Err()
			case <-time.After(timing.Poll):
			}
		default:
			return nil, nil, fmt.Errorf("unknown embedding artifact claim outcome %q", claim.Outcome)
		}
	}
}

func encodeEmbeddingArtifact(vector []float32) ([]byte, error) {
	if len(vector) == 0 || uint64(len(vector)) > uint64(^uint32(0)) {
		return nil, fmt.Errorf("encode embedding artifact dimension: %w", interfaces.ErrArtifactCorrupt)
	}
	payload := make([]byte, len(embeddingArtifactMagic)+4+len(vector)*4)
	copy(payload, embeddingArtifactMagic[:])
	binary.LittleEndian.PutUint32(payload[len(embeddingArtifactMagic):], uint32(len(vector)))
	offset := len(embeddingArtifactMagic) + 4
	for i, value := range vector {
		if math.IsNaN(float64(value)) || math.IsInf(float64(value), 0) {
			return nil, fmt.Errorf("encode embedding artifact vector[%d]: %w", i, interfaces.ErrArtifactCorrupt)
		}
		binary.LittleEndian.PutUint32(payload[offset+i*4:], math.Float32bits(value))
	}
	return payload, nil
}

func decodeEmbeddingArtifact(payload []byte, encoding string, expectedDimension int) ([]float32, error) {
	headerSize := len(embeddingArtifactMagic) + 4
	if encoding != embeddingArtifactEncoding || expectedDimension <= 0 || len(payload) < headerSize || !equalEmbeddingMagic(payload[:len(embeddingArtifactMagic)]) {
		return nil, interfaces.ErrArtifactCorrupt
	}
	dimension := int(binary.LittleEndian.Uint32(payload[len(embeddingArtifactMagic):headerSize]))
	if dimension <= 0 || dimension != expectedDimension || len(payload) != headerSize+dimension*4 {
		return nil, interfaces.ErrArtifactCorrupt
	}
	vector := make([]float32, dimension)
	for i := range vector {
		value := math.Float32frombits(binary.LittleEndian.Uint32(payload[headerSize+i*4:]))
		if math.IsNaN(float64(value)) || math.IsInf(float64(value), 0) {
			return nil, interfaces.ErrArtifactCorrupt
		}
		vector[i] = value
	}
	return vector, nil
}

func equalEmbeddingMagic(value []byte) bool {
	if len(value) != len(embeddingArtifactMagic) {
		return false
	}
	for i := range value {
		if value[i] != embeddingArtifactMagic[i] {
			return false
		}
	}
	return true
}

func cloneEmbeddingVector(vector []float32) []float32 {
	if vector == nil {
		return nil
	}
	return append([]float32(nil), vector...)
}

func cacheableEmbeddingOperation(operation types.IngestionOperation) bool {
	switch operation {
	case types.IngestionOperationEmbeddingChunk, types.IngestionOperationEmbeddingSummary,
		types.IngestionOperationEmbeddingQuestion, types.IngestionOperationEmbeddingFAQ,
		types.IngestionOperationEmbeddingGraphEntity, types.IngestionOperationEmbeddingGraphRelation,
		types.IngestionOperationEmbeddingWikiPage:
		return true
	default:
		return false
	}
}

func newEmbeddingOwnerToken() (string, error) {
	var value [24]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(value[:]), nil
}
