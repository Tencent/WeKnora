package retriever

import (
	"context"
	"slices"
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
		embedding, err := embedder.Embed(ctx, indexInfo.Content)
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
		embeddings, err := batchEmbedWithBackoff(ctx, embedder, contentList)
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
	if utf8.RuneCountInString(content) <= safetyMaxChars {
		return content
	}
	runes := []rune(content)
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
