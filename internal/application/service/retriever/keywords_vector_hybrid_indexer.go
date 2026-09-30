package retriever

import (
	"context"
	"errors"
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
	if indexInfo.IsImage() && !slices.Contains(retrieverTypes, types.VectorRetrieverType) {
		return nil
	}

	params := make(map[string]any)
	embeddingMap := make(map[string][]float32)
	if slices.Contains(retrieverTypes, types.VectorRetrieverType) {
		var (
			vector []float32
			err    error
		)
		switch {
		case indexInfo.IsImage():
			vector, err = embedImageIndexInfo(ctx, embedder, indexInfo)
		case indexInfo.MultimodalEnvelope:
			vector, err = embedding.EmbedMultimodal(ctx, embedder, embedding.Input{
				Text: sanitizeForEmbedding(ctx, indexInfo.Content),
			})
		default:
			vector, err = embedder.Embed(ctx, sanitizeForEmbedding(ctx, indexInfo.Content))
		}
		if err != nil {
			return err
		}
		embeddingMap[indexInfo.SourceID] = vector
	}
	params["embedding"] = embeddingMap
	return v.indexRepository.Save(ctx, indexInfo, params)
}

// embedImageIndexInfo embeds one image entry with the model's multimodal path.
func embedImageIndexInfo(
	ctx context.Context, embedder embedding.Embedder, indexInfo *types.IndexInfo,
) ([]float32, error) {
	vector, err := embedding.EmbedMultimodal(ctx, embedder, embedding.Input{
		Images: []embedding.ImagePart{{Data: indexInfo.ImageBytes, MIME: indexInfo.ImageMIME}},
	})
	if err != nil {
		return nil, fmt.Errorf("embed image %s: %w", indexInfo.SourceID, err)
	}
	return vector, nil
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
		var embeddings [][]float32
		var err error
		indexInfoList, embeddings, err = embedIndexInfos(ctx, embedder, indexInfoList)
		if err != nil {
			return err
		}
		if len(indexInfoList) == 0 {
			return nil
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

	// For non-vector retrieval, use concurrent batch saving as well.
	// Image entries are dropped first: there is no text to keyword-index.
	indexInfoList = dropImageIndexInfos(ctx, indexInfoList)
	if len(indexInfoList) == 0 {
		return nil
	}
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

// embedIndexInfos produces one embedding per entry, routing image entries to
// the multimodal path and everything else to the text path. Entries carrying
// MultimodalEnvelope always go through the multimodal path, even plain text
// ones: a KB that stores image vectors lives entirely in the chat-template
// space, so its text chunks must be encoded there too.
//
// The returned list may be SHORTER than the input: a failed image embedding is
// dropped, while a text failure aborts the batch, because losing an image
// vector degrades to OCR/caption behavior whereas losing text loses the
// document.
func embedIndexInfos(
	ctx context.Context, embedder embedding.Embedder, list []*types.IndexInfo,
) ([]*types.IndexInfo, [][]float32, error) {
	var textPositions, imagePositions, unifiedTextPositions []int
	var stamped, unstamped int
	for i, info := range list {
		if info.MultimodalEnvelope {
			stamped++
		} else {
			unstamped++
		}
		switch {
		case info.IsImage():
			imagePositions = append(imagePositions, i)
		case info.MultimodalEnvelope:
			unifiedTextPositions = append(unifiedTextPositions, i)
		default:
			textPositions = append(textPositions, i)
		}
	}
	if stamped > 0 && unstamped > 0 {
		logger.Warnf(ctx,
			"mixed embedding envelopes in one batch: %d entry/entries via the "+
				"multimodal envelope, %d via the plain text envelope; "+
				"their vectors will not be comparable",
			stamped, unstamped)
	}

	embeddings := make([][]float32, len(list))
	drop := make(map[int]bool)

	if len(textPositions) > 0 {
		contentList := make([]string, 0, len(textPositions))
		for _, i := range textPositions {
			contentList = append(contentList, sanitizeForEmbedding(ctx, list[i].Content))
		}
		vectors, err := batchEmbedWithBackoff(ctx, embedder, contentList)
		if err != nil {
			return nil, nil, err
		}
		if len(vectors) != len(textPositions) {
			return nil, nil, fmt.Errorf("text embedding count mismatch: got %d, want %d",
				len(vectors), len(textPositions))
		}
		for k, i := range textPositions {
			embeddings[i] = vectors[k]
		}
	}

	if len(unifiedTextPositions) > 0 {
		inputs := make([]embedding.Input, 0, len(unifiedTextPositions))
		for _, i := range unifiedTextPositions {
			inputs = append(inputs, embedding.Input{
				Text: sanitizeForEmbedding(ctx, list[i].Content),
			})
		}
		vectors, err := batchEmbedMultimodalWithBackoff(ctx, embedder, inputs)
		if err != nil {
			return nil, nil, fmt.Errorf("multimodal text embedding failed: %w", err)
		}
		if len(vectors) != len(unifiedTextPositions) {
			return nil, nil, fmt.Errorf("multimodal text embedding count mismatch: got %d, want %d",
				len(vectors), len(unifiedTextPositions))
		}
		for k, i := range unifiedTextPositions {
			embeddings[i] = vectors[k]
		}
	}

	var imageErr error
	if len(imagePositions) > 0 {
		inputs := make([]embedding.Input, 0, len(imagePositions))
		for _, i := range imagePositions {
			// Text is deliberately left empty: the point of an image entry is
			// the pixels. Folding the chunk's markdown placeholder into the
			// request would tilt the vector toward a meaningless token string.
			inputs = append(inputs, embedding.Input{
				Images: []embedding.ImagePart{{Data: list[i].ImageBytes, MIME: list[i].ImageMIME}},
			})
		}
		vectors, err := batchEmbedMultimodalWithBackoff(ctx, embedder, inputs)
		if err != nil {
			imageErr = err
		} else if len(vectors) != len(imagePositions) {
			imageErr = fmt.Errorf("image embedding count mismatch: got %d, want %d",
				len(vectors), len(imagePositions))
		}

		if imageErr != nil {
			if len(imagePositions) < len(list) {
				// Mixed batch: text entries can still be stored, so degrade to
				// text-only rather than throwing the whole document away.
				logger.Errorf(ctx, "image embedding failed for %d input(s); skipping image vectors: %v",
					len(imagePositions), imageErr)
			}
			for _, i := range imagePositions {
				drop[i] = true
			}
		} else {
			for k, i := range imagePositions {
				embeddings[i] = vectors[k]
			}
		}
	}

	if len(drop) == 0 {
		return list, embeddings, nil
	}
	// An all-image batch that failed has nothing left to save. Returning an
	// empty list here would look like success to the caller and leave the chunk
	// marked indexed with no vector behind it.
	if len(drop) == len(list) && imageErr != nil {
		return nil, nil, imageErr
	}
	kept := make([]*types.IndexInfo, 0, len(list)-len(drop))
	keptEmbeddings := make([][]float32, 0, len(list)-len(drop))
	for i, info := range list {
		if drop[i] {
			continue
		}
		kept = append(kept, info)
		keptEmbeddings = append(keptEmbeddings, embeddings[i])
	}
	return kept, keptEmbeddings, nil
}

// batchEmbedMultimodalWithBackoff mirrors batchEmbedWithBackoff for image
// inputs, except that ErrMultimodalUnsupported short-circuits: retrying cannot
// teach a text-only model to accept pixels.
func batchEmbedMultimodalWithBackoff(
	ctx context.Context, embedder embedding.Embedder, inputs []embedding.Input,
) ([][]float32, error) {
	delay := embedRetryBaseDelay
	var (
		embeddings [][]float32
		err        error
	)
	for attempt := 0; attempt < embedRetryAttempts; attempt++ {
		embeddings, err = embedding.BatchEmbedMultimodalWith(ctx, embedder, inputs)
		if err == nil {
			return embeddings, nil
		}
		if errors.Is(err, embedding.ErrMultimodalUnsupported) {
			return nil, err
		}
		logger.Errorf(ctx, "BatchEmbedMultimodal attempt %d/%d failed: %v", attempt+1, embedRetryAttempts, err)
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

// dropImageIndexInfos removes image entries, used when the caller did not ask
// for vector indexing and therefore has nowhere to put an image vector.
func dropImageIndexInfos(ctx context.Context, list []*types.IndexInfo) []*types.IndexInfo {
	kept := make([]*types.IndexInfo, 0, len(list))
	for _, info := range list {
		if info.IsImage() {
			continue
		}
		kept = append(kept, info)
	}
	if len(kept) != len(list) {
		logger.Warnf(ctx, "dropped %d image index entries: no vector retriever configured", len(list)-len(kept))
	}
	return kept
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
