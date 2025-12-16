package retriever

import (
	"context"
	"slices"
	"time"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/models/embedding"
	"github.com/Tencent/WeKnora/internal/models/utils"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
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
// in batches for efficiency
func (v *KeywordsVectorHybridRetrieveEngineService) BatchIndex(ctx context.Context,
	embedder embedding.Embedder, indexInfoList []*types.IndexInfo, retrieverTypes []types.RetrieverType,
) error {
	if len(indexInfoList) == 0 {
		return nil
	}

	if slices.Contains(retrieverTypes, types.VectorRetrieverType) {
		var contentList []string
		for _, indexInfo := range indexInfoList {
			contentList = append(contentList, indexInfo.Content)
		}
		var embeddings [][]float32
		var err error
		for range 5 {
			embeddings, err = embedder.BatchEmbedWithPool(ctx, embedder, contentList)
			if err == nil {
				break
			} else {
				logger.Errorf(ctx, "BatchEmbedWithPool failed: %v", err)
				time.Sleep(100 * time.Millisecond)
			}
		}
		if err != nil {
			return err
		}
		batchSize := 40
		for i, indexChunk := range utils.ChunkSlice(indexInfoList, batchSize) {
			params := make(map[string]any)
			embeddingMap := make(map[string][]float32)
			for j, indexInfo := range indexChunk {
				embeddingMap[indexInfo.SourceID] = embeddings[i*batchSize+j]
			}
			params["embedding"] = embeddingMap
			err = v.indexRepository.BatchSave(ctx, indexChunk, params)
			if err != nil {
				return err
			}
		}
		return nil
	}
	var err error
	for _, indexChunk := range utils.ChunkSlice(indexInfoList, 10) {
		params := make(map[string]any)
		err = v.indexRepository.BatchSave(ctx, indexChunk, params)
		if err != nil {
			return err
		}
	}
	return nil
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
