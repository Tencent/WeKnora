package retriever

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"net"
	"net/http"
	"os"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/models/api"
	"github.com/Tencent/WeKnora/internal/models/embedding"
	"github.com/Tencent/WeKnora/internal/models/utils"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"golang.org/x/sync/errgroup"
)

// safetyMaxChars is an absolute upper bound for any single embedding input.
// Beyond this we truncate (with a warning) instead of blindly forwarding to
// the embedding API, which would either error out or silently truncate in a
// model-specific way. Set well above any current chunkSize budget so it only
// kicks in for genuinely pathological inputs.
const safetyMaxChars = 20000

// Retry only a failed sub-batch, allowing provider rate-limit windows to reset.
const (
	embedRetryAttempts  = 6
	embedRetryBaseDelay = 5 * time.Second
	embedRetryMaxDelay  = time.Minute
)

// envInt reads an integer from the environment, returning defaultVal when the
// variable is unset, empty, or not a valid positive integer.
func envInt(key string, defaultVal int) int {
	v := os.Getenv(key)
	if v == "" {
		return defaultVal
	}
	n, err := strconv.Atoi(v)
	if err != nil || n <= 0 {
		return defaultVal
	}
	return n
}

func embeddingDelay(key string, fallback time.Duration) time.Duration {
	ms := envInt(key, int(fallback/time.Millisecond))
	if int64(ms) > int64((1<<63-1)/time.Millisecond) {
		return fallback
	}
	return time.Duration(ms) * time.Millisecond
}

var embeddingImagePayloadPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?is)<img\b[^>]*\bsrc=["']\s*data:image/[a-z0-9.+-]+;base64,[^"']+["'][^>]*>`),
	regexp.MustCompile(`(?is)!\[[^\]]*\]\(\s*data:image/[a-z0-9.+-]+;base64,[^)]+\)`),
	regexp.MustCompile(`(?i)data:image/[a-z0-9.+-]+;base64,[a-z0-9+/=]{200,}`),
	regexp.MustCompile(`(?i)data:[a-z0-9.+/-]+;base64,[a-z0-9+/=]{200,}`),
}

// KeywordsVectorHybridRetrieveEngineService implements a hybrid retrieval engine
// that supports both keyword-based and vector-based retrieval
type KeywordsVectorHybridRetrieveEngineService struct {
	indexRepository interfaces.RetrieveEngineRepository
	engineType      types.RetrieverEngineType
}

// NewKVHybridRetrieveEngine creates a new instance of the hybrid retrieval engine
// KV stands for KeywordsVector
func NewKVHybridRetrieveEngine(indexRepository interfaces.RetrieveEngineRepository,
	engineType types.RetrieverEngineType,
) interfaces.RetrieveEngineService {
	return &KeywordsVectorHybridRetrieveEngineService{indexRepository: indexRepository, engineType: engineType}
}

// EngineType returns the type of the retrieval engine
func (v *KeywordsVectorHybridRetrieveEngineService) EngineType() types.RetrieverEngineType {
	return v.engineType
}

// Retrieve performs retrieval based on the provided parameters
func (v *KeywordsVectorHybridRetrieveEngineService) Retrieve(ctx context.Context,
	params types.RetrieveParams,
) ([]*types.RetrieveResult, error) {
	return v.indexRepository.Retrieve(ctx, params)
}

// Index creates embeddings for the content and saves it to the repository
// if vector retrieval is enabled in the retriever types
func (v *KeywordsVectorHybridRetrieveEngineService) Index(ctx context.Context,
	embedder embedding.Embedder, indexInfo *types.IndexInfo, retrieverTypes []types.RetrieverType,
) error {
	params := make(map[string]any)
	embeddingMap := make(map[string][]float32)
	if slices.Contains(retrieverTypes, types.VectorRetrieverType) {
		embedding, err := embedder.Embed(ctx, sanitizeForEmbedding(ctx, indexInfo.Content))
		if err != nil {
			return err
		}
		embeddingMap[indexInfo.SourceID] = embedding
	}
	params["embedding"] = embeddingMap
	return v.indexRepository.Save(ctx, indexInfo, params)
}

// BatchIndex creates embeddings for multiple content items and saves them to the repository
// in batches for efficiency. Uses concurrent batch saving to improve performance.
func (v *KeywordsVectorHybridRetrieveEngineService) BatchIndex(ctx context.Context,
	embedder embedding.Embedder, indexInfoList []*types.IndexInfo, retrieverTypes []types.RetrieverType,
) error {
	if len(indexInfoList) == 0 {
		return nil
	}

	if slices.Contains(retrieverTypes, types.VectorRetrieverType) {
		var contentList []string
		for _, indexInfo := range indexInfoList {
			contentList = append(contentList, sanitizeForEmbedding(ctx, indexInfo.Content))
		}
		embeddings, err := batchEmbedSequentially(ctx, embedder, contentList)
		if err != nil {
			return err
		}

		batchSize := 40
		chunks := utils.ChunkSlice(indexInfoList, batchSize)

		// Use concurrent batch saving for better performance
		// Limit concurrency to avoid overwhelming the backend
		const maxConcurrency = 5
		if len(chunks) <= maxConcurrency {
			// For small number of batches, use simple concurrency
			return v.concurrentBatchSave(ctx, chunks, embeddings, batchSize)
		}

		// For large number of batches, use bounded concurrency
		return v.boundedConcurrentBatchSave(ctx, chunks, embeddings, batchSize, maxConcurrency)
	}

	// For non-vector retrieval, use concurrent batch saving as well
	chunks := utils.ChunkSlice(indexInfoList, 10)
	const maxConcurrency = 5
	if len(chunks) <= maxConcurrency {
		return v.concurrentBatchSaveNoEmbedding(ctx, chunks)
	}
	return v.boundedConcurrentBatchSaveNoEmbedding(ctx, chunks, maxConcurrency)
}

// batchEmbedWithBackoff retries transient failures for this sub-batch only.
// Permanent provider rejections are returned immediately. Retry-After is a
// lower bound on the wait; otherwise use capped exponential backoff with jitter.
func batchEmbedWithBackoff(
	ctx context.Context, embedder embedding.Embedder, contentList []string,
) ([][]float32, error) {
	attempts := envInt("EMBED_RETRY_ATTEMPTS", embedRetryAttempts)
	delay := embeddingDelay("EMBED_RETRY_BASE_DELAY_MS", embedRetryBaseDelay)
	maxDelay := embeddingDelay("EMBED_RETRY_MAX_DELAY_MS", embedRetryMaxDelay)
	delay = min(delay, maxDelay)
	var lastErr error
	for attempt := 0; attempt < attempts; attempt++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		embeddings, err := embedder.BatchEmbedWithPool(ctx, embedder, contentList)
		if err == nil {
			if len(embeddings) != len(contentList) {
				return nil, fmt.Errorf("embedding model returned %d embeddings for %d inputs",
					len(embeddings), len(contentList))
			}
			return embeddings, nil
		}
		lastErr = err
		if !retryableEmbeddingError(err) || attempt+1 == attempts {
			return nil, err
		}
		jitter := time.Duration(rand.Int64N(int64(delay/4) + 1))
		wait := delay + min(jitter, maxDelay-delay)
		var httpErr *api.HTTPError
		if errors.As(err, &httpErr) {
			wait = max(wait, httpErr.RetryAfter())
		}
		logger.Warnf(ctx, "Embedding batch attempt %d/%d failed; retrying in %v: %v", attempt+1, attempts, wait, err)
		if err := waitForEmbedding(ctx, wait); err != nil {
			return nil, err
		}
		// Avoid overflow when doubling a configured delay.
		delay += min(delay, maxDelay-delay)
	}
	return nil, lastErr
}

func retryableEmbeddingError(err error) bool {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	var httpErr *api.HTTPError
	if errors.As(err, &httpErr) {
		return httpErr.StatusCode == http.StatusTooManyRequests ||
			(httpErr.StatusCode >= 500 && httpErr.StatusCode < 600)
	}
	var transportErr *api.TransportError
	var netErr net.Error
	return errors.As(err, &transportErr) || errors.As(err, &netErr)
}

func waitForEmbedding(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return ctx.Err()
	}
}

// Embed sequential sub-batches, retaining successful results during retries.
// Batch size defaults to the pooler's default of 5; provider-specific ceilings
// remain enforced by the protocol client. Pacing is per BatchIndex invocation.
func batchEmbedSequentially(
	ctx context.Context, embedder embedding.Embedder, contentList []string,
) ([][]float32, error) {
	// Precomputed image vectors belong to the full row list and never call a
	// provider. Splitting their positional adapter would lose that alignment.
	if precomputed, ok := embedder.(*precomputedEmbedder); ok {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		return precomputed.take(len(contentList))
	}
	batchSize := envInt("BATCH_EMBED_SIZE", 5)
	interval := embeddingDelay("EMBED_BATCH_INTERVAL_MS", 0)

	results := make([][]float32, len(contentList))
	batches := utils.ChunkSlice(contentList, batchSize)
	for i, batch := range batches {
		if i > 0 && interval > 0 {
			if err := waitForEmbedding(ctx, interval); err != nil {
				return nil, err
			}
		}
		vecs, err := batchEmbedWithBackoff(ctx, embedder, batch)
		if err != nil {
			return nil, fmt.Errorf("embedding batch %d/%d: %w", i+1, len(batches), err)
		}
		offset := i * batchSize
		copy(results[offset:], vecs)
	}
	return results, nil
}

// sanitizeForEmbedding caps content length at safetyMaxChars characters so
// pathologically large inputs cannot blow up the embedding API call. The
// truncation point is char-based, not token-based, so it sits well above any
// realistic token limit. We log a warning whenever truncation kicks in.
func sanitizeForEmbedding(ctx context.Context, content string) string {
	sanitized := content
	// Scrubbing only matters when an inline base64 payload is present; skip the
	// regex passes otherwise so the common (no-image) path stays cheap.
	if strings.Contains(content, "base64,") {
		for _, pattern := range embeddingImagePayloadPatterns {
			sanitized = pattern.ReplaceAllString(sanitized, "[image]")
		}
	}

	if utf8.RuneCountInString(sanitized) <= safetyMaxChars {
		return sanitized
	}
	runes := []rune(sanitized)
	logger.Warnf(ctx, "embedding input truncated: %d runes -> %d", len(runes), safetyMaxChars)
	return string(runes[:safetyMaxChars])
}

// concurrentBatchSave saves all batches concurrently without concurrency limit
func (v *KeywordsVectorHybridRetrieveEngineService) concurrentBatchSave(
	ctx context.Context,
	chunks [][]*types.IndexInfo,
	embeddings [][]float32,
	batchSize int,
) error {
	g, ctx := errgroup.WithContext(ctx)
	for i, indexChunk := range chunks {
		g.Go(func() error {
			params := make(map[string]any)
			embeddingMap := make(map[string][]float32)
			for j, indexInfo := range indexChunk {
				embeddingMap[indexInfo.SourceID] = embeddings[i*batchSize+j]
			}
			params["embedding"] = embeddingMap
			return v.indexRepository.BatchSave(ctx, indexChunk, params)
		})
	}
	return g.Wait()
}

// boundedConcurrentBatchSave saves batches with bounded concurrency using semaphore pattern
func (v *KeywordsVectorHybridRetrieveEngineService) boundedConcurrentBatchSave(
	ctx context.Context,
	chunks [][]*types.IndexInfo,
	embeddings [][]float32,
	batchSize int,
	maxConcurrency int,
) error {
	g, ctx := errgroup.WithContext(ctx)
	sem := make(chan struct{}, maxConcurrency)

	for i, indexChunk := range chunks {
		g.Go(func() error {
			select {
			case sem <- struct{}{}:
				defer func() { <-sem }()
			case <-ctx.Done():
				return ctx.Err()
			}

			params := make(map[string]any)
			embeddingMap := make(map[string][]float32)
			for j, indexInfo := range indexChunk {
				embeddingMap[indexInfo.SourceID] = embeddings[i*batchSize+j]
			}
			params["embedding"] = embeddingMap
			return v.indexRepository.BatchSave(ctx, indexChunk, params)
		})
	}
	return g.Wait()
}

// concurrentBatchSaveNoEmbedding saves all batches concurrently without embeddings
func (v *KeywordsVectorHybridRetrieveEngineService) concurrentBatchSaveNoEmbedding(
	ctx context.Context,
	chunks [][]*types.IndexInfo,
) error {
	g, ctx := errgroup.WithContext(ctx)
	for _, indexChunk := range chunks {
		g.Go(func() error {
			params := make(map[string]any)
			return v.indexRepository.BatchSave(ctx, indexChunk, params)
		})
	}
	return g.Wait()
}

// boundedConcurrentBatchSaveNoEmbedding saves batches with bounded concurrency without embeddings
func (v *KeywordsVectorHybridRetrieveEngineService) boundedConcurrentBatchSaveNoEmbedding(
	ctx context.Context,
	chunks [][]*types.IndexInfo,
	maxConcurrency int,
) error {
	g, ctx := errgroup.WithContext(ctx)
	sem := make(chan struct{}, maxConcurrency)

	for _, indexChunk := range chunks {
		g.Go(func() error {
			select {
			case sem <- struct{}{}:
				defer func() { <-sem }()
			case <-ctx.Done():
				return ctx.Err()
			}

			params := make(map[string]any)
			return v.indexRepository.BatchSave(ctx, indexChunk, params)
		})
	}
	return g.Wait()
}

// DeleteByChunkIDList deletes vectors by their chunk IDs
func (v *KeywordsVectorHybridRetrieveEngineService) DeleteByChunkIDList(ctx context.Context,
	indexIDList []string, dimension int, knowledgeType string,
) error {
	return v.indexRepository.DeleteByChunkIDList(ctx, indexIDList, dimension, knowledgeType)
}

// DeleteBySourceIDList deletes vectors by their source IDs
func (v *KeywordsVectorHybridRetrieveEngineService) DeleteBySourceIDList(ctx context.Context,
	sourceIDList []string, dimension int, knowledgeType string,
) error {
	return v.indexRepository.DeleteBySourceIDList(ctx, sourceIDList, dimension, knowledgeType)
}

// DeleteByKnowledgeIDList deletes vectors by their knowledge IDs
func (v *KeywordsVectorHybridRetrieveEngineService) DeleteByKnowledgeIDList(ctx context.Context,
	knowledgeIDList []string, dimension int, knowledgeType string,
) error {
	return v.indexRepository.DeleteByKnowledgeIDList(ctx, knowledgeIDList, dimension, knowledgeType)
}

// Support returns the retriever types supported by this engine
func (v *KeywordsVectorHybridRetrieveEngineService) Support() []types.RetrieverType {
	return v.indexRepository.Support()
}

// EstimateStorageSize estimates the storage space needed for the provided index information
func (v *KeywordsVectorHybridRetrieveEngineService) EstimateStorageSize(
	ctx context.Context,
	embedder embedding.Embedder,
	indexInfoList []*types.IndexInfo,
	retrieverTypes []types.RetrieverType,
) int64 {
	params := make(map[string]any)
	if slices.Contains(retrieverTypes, types.VectorRetrieverType) {
		embeddingMap := make(map[string][]float32)
		// just for estimate storage size
		for _, indexInfo := range indexInfoList {
			embeddingMap[indexInfo.ChunkID] = make([]float32, embedder.GetDimensions())
		}
		params["embedding"] = embeddingMap
	}
	return v.indexRepository.EstimateStorageSize(ctx, indexInfoList, params)
}

// CopyIndices copies indices from a source knowledge base to a target knowledge base
func (v *KeywordsVectorHybridRetrieveEngineService) CopyIndices(
	ctx context.Context,
	sourceKnowledgeBaseID string,
	sourceToTargetKBIDMap map[string]string,
	sourceToTargetChunkIDMap map[string]string,
	targetKnowledgeBaseID string,
	dimension int,
	knowledgeType string,
) error {
	logger.Infof(ctx, "Copy indices from knowledge base %s to %s, mapping relation count: %d",
		sourceKnowledgeBaseID, targetKnowledgeBaseID, len(sourceToTargetChunkIDMap),
	)
	return v.indexRepository.CopyIndices(
		ctx, sourceKnowledgeBaseID, sourceToTargetKBIDMap, sourceToTargetChunkIDMap, targetKnowledgeBaseID, dimension, knowledgeType,
	)
}

// BatchUpdateChunkEnabledStatus updates the enabled status of chunks in batch
func (v *KeywordsVectorHybridRetrieveEngineService) BatchUpdateChunkEnabledStatus(
	ctx context.Context,
	chunkStatusMap map[string]bool,
) error {
	return v.indexRepository.BatchUpdateChunkEnabledStatus(ctx, chunkStatusMap)
}

// BatchUpdateChunkTagID updates the tag ID of chunks in batch
func (v *KeywordsVectorHybridRetrieveEngineService) BatchUpdateChunkTagID(
	ctx context.Context,
	chunkTagMap map[string]string,
) error {
	return v.indexRepository.BatchUpdateChunkTagID(ctx, chunkTagMap)
}

// ValidateKnowledgeIndexMove checks backend support before any mutation.
func (v *KeywordsVectorHybridRetrieveEngineService) ValidateKnowledgeIndexMove(ctx context.Context) error {
	if _, ok := v.indexRepository.(interfaces.KnowledgeIndexMover); !ok {
		return fmt.Errorf("retriever %s does not support moving indices", v.EngineType())
	}
	if validator, ok := v.indexRepository.(interface{ ValidateKnowledgeIndexMove(context.Context) error }); ok {
		return validator.ValidateKnowledgeIndexMove(ctx)
	}

	return nil
}

// MoveKnowledgeIndices delegates metadata relocation to the supported backend.
func (v *KeywordsVectorHybridRetrieveEngineService) MoveKnowledgeIndices(
	ctx context.Context,
	sourceKB, targetKB, knowledgeID string,
	chunkIDs []string,
	dimension int,
	knowledgeType string,
) error {
	if err := v.ValidateKnowledgeIndexMove(ctx); err != nil {
		return err
	}
	return v.indexRepository.(interfaces.KnowledgeIndexMover).MoveKnowledgeIndices(
		ctx,
		sourceKB,
		targetKB,
		knowledgeID,
		chunkIDs,
		dimension,
		knowledgeType,
	)
}
