package retriever

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Tencent/WeKnora/internal/logger"
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

// embedRetryAttempts and embedRetryBaseDelay control the exponential backoff
// applied to BatchEmbedWithPool calls.
const (
	embedRetryAttempts  = 5
	embedRetryBaseDelay = 200 * time.Millisecond
)

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
	// cacheService is the best-effort artifact cache for embedding vectors.
	// Nil-safe: all call sites check for nil before using.
	cacheService interfaces.ArtifactCacheService
}

// NewKVHybridRetrieveEngine creates a new instance of the hybrid retrieval engine
// KV stands for KeywordsVector
func NewKVHybridRetrieveEngine(
	indexRepository interfaces.RetrieveEngineRepository,
	engineType types.RetrieverEngineType,
	cacheServices ...interfaces.ArtifactCacheService,
) interfaces.RetrieveEngineService {
	var cacheService interfaces.ArtifactCacheService
	if len(cacheServices) > 0 {
		cacheService = cacheServices[0]
	}
	return &KeywordsVectorHybridRetrieveEngineService{
		indexRepository: indexRepository,
		engineType:      engineType,
		cacheService:    cacheService,
	}
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
		// Resolve cached vectors independently so a single changed chunk only
		// triggers embedding work for that chunk rather than the full batch.
		embeddings, missing := v.loadEmbeddingCache(ctx, embedder, contentList)
		if len(missing) > 0 {
			missingContent := make([]string, len(missing))
			for i, index := range missing {
				missingContent[i] = contentList[index]
			}
			computed, err := batchEmbedWithBackoff(ctx, embedder, missingContent)
			if err != nil {
				return err
			}
			if len(computed) != len(missing) {
				return fmt.Errorf("embedding provider returned %d vectors for %d inputs", len(computed), len(missing))
			}
			for i, index := range missing {
				embeddings[index] = computed[i]
			}
			v.storeEmbeddingCache(ctx, embedder, indexInfoList, contentList, embeddings)
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

// batchEmbedWithBackoff calls BatchEmbedWithPool with exponential backoff on
// transient failures (200 / 400 / 800 / 1600 / 3200 ms). It returns the last
// embedding result on success or the last error if every attempt failed.
func batchEmbedWithBackoff(ctx context.Context, embedder embedding.Embedder, contentList []string) ([][]float32, error) {
	delay := embedRetryBaseDelay
	var (
		embeddings [][]float32
		err        error
	)
	for attempt := 0; attempt < embedRetryAttempts; attempt++ {
		embeddings, err = embedder.BatchEmbedWithPool(ctx, embedder, contentList)
		if err == nil {
			return embeddings, nil
		}
		logger.Errorf(ctx, "BatchEmbedWithPool attempt %d/%d failed: %v", attempt+1, embedRetryAttempts, err)
		if attempt+1 < embedRetryAttempts {
			select {
			case <-time.After(delay):
			case <-ctx.Done():
				return nil, ctx.Err()
			}
			delay *= 2
		}
	}
	return embeddings, err
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

// loadEmbeddingCache returns cached vectors and the indexes that still need
// provider work. A malformed or wrong-dimensional value is treated as a miss.
func (v *KeywordsVectorHybridRetrieveEngineService) loadEmbeddingCache(
	ctx context.Context,
	embedder embedding.Embedder,
	contentList []string,
) ([][]float32, []int) {
	result := make([][]float32, len(contentList))
	missing := make([]int, 0)
	if v.cacheService == nil {
		for i := range contentList {
			missing = append(missing, i)
		}
		return result, missing
	}

	modelID := embedder.GetModelID()
	dims := embedder.GetDimensions()
	configHash := types.HashAll(modelID, fmt.Sprintf("%d", dims))
	for i, content := range contentList {
		textHash := types.HashString(content)
		cacheKey := "embedding:" + modelID + ":" + textHash[:16]
		cached := v.cacheService.Lookup(ctx, 0, cacheKey, types.ArtifactCacheTypeChunkEmbedding, textHash, configHash)
		if cached != nil && cached.OutputSize > 0 && cached.OutputJSON != nil {
			var vector []float32
			if err := json.Unmarshal(cached.OutputJSON, &vector); err == nil && len(vector) == dims {
				result[i] = vector
				continue
			}
		}
		missing = append(missing, i)
	}
	return result, missing
}

// storeEmbeddingCache persists freshly computed embedding vectors in the
// artifact cache for future re-parses. Best-effort: errors are logged.
func (v *KeywordsVectorHybridRetrieveEngineService) storeEmbeddingCache(
	ctx context.Context,
	embedder embedding.Embedder,
	indexInfoList []*types.IndexInfo,
	contentList []string,
	embeddings [][]float32,
) {
	if v.cacheService == nil || len(embeddings) != len(contentList) {
		return
	}
	modelID := embedder.GetModelID()
	dims := embedder.GetDimensions()
	configHash := types.HashAll(modelID, fmt.Sprintf("%d", dims))

	for i, vec := range embeddings {
		if len(vec) == 0 {
			continue
		}
		textHash := types.HashString(contentList[i])
		cacheKey := "embedding:" + modelID + ":" + textHash[:16]
		vecJSON, err := json.Marshal(vec)
		if err != nil {
			continue
		}
		v.cacheService.Store(ctx, &types.ArtifactCache{
			TenantID:   0, // cross-document dedup
			CacheKey:   cacheKey,
			CacheType:  types.ArtifactCacheTypeChunkEmbedding,
			InputHash:  textHash,
			ConfigHash: configHash,
			OutputJSON: vecJSON,
			OutputSize: int64(len(vec) * 4),
		})
	}
}
