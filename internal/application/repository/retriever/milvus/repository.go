package repomilvus

import (
	"context"
	"errors"
	"strconv"

	"github.com/milvus-io/milvus/client/v2/entity"
	"github.com/milvus-io/milvus/client/v2/index"
	"github.com/milvus-io/milvus/client/v2/milvusclient"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

type MilvusCollectionConfigurator interface {
	// SetCollectionName allows changing or specifying a collection name dynamically.
	SetCollectionName(ctx context.Context, dim int) error

	// GetCollectionName returns the current collection name (optional helper).
	GetCollectionName() string
}

type milvusRepository struct {
	client         *milvusclient.Client
	collectionName string
}

func NewMilvusRepository(client *milvusclient.Client) (interfaces.RetrieveEngineRepository, error) {
	ctx := context.TODO()
	collectionName, err := ensureCollectionName(ctx, client, EmbeddingsDefaultDimension)
	if err != nil {
		logger.GetLogger(ctx).Errorf("[Milvus] Failed to ensure collection name: %s", collectionName)
		return nil, err
	}

	task, err := client.LoadCollection(ctx, milvusclient.NewLoadCollectionOption(collectionName))
	if err != nil {
		return nil, err
	}
	err = task.Await(ctx)
	if err != nil {
		return nil, err
	}

	return &milvusRepository{client, EmbeddingsCollectionDefaultName}, nil
}

func ensureCollectionName(ctx context.Context, client *milvusclient.Client, dim int) (string, error) {
	collectionName := EmbeddingsCollectionNamePrefix + strconv.Itoa(dim)
	exists, err := client.HasCollection(ctx, milvusclient.NewHasCollectionOption(collectionName))
	if err != nil {
		return "", err
	}
	if exists {
		return collectionName, nil
	}
	analyzerParams := map[string]any{"type": "chinese"}

	schema := entity.NewSchema().WithDynamicFieldEnabled(true)
	schema.WithField(entity.NewField().WithName("chunk_id").WithIsAutoID(false).WithDataType(entity.FieldTypeVarChar).WithMaxLength(36).WithIsPrimaryKey(true))
	schema.WithField(entity.NewField().WithName("vector").WithDataType(entity.FieldTypeFloatVector).WithDim(int64(dim)))
	// 这里6000是目前前端设置页分块大小范围是100~4000，分块重叠范围是0~1000，所以预设6000预留一些空间
	schema.WithField(entity.NewField().WithName("content").WithDataType(entity.FieldTypeVarChar).WithMaxLength(6000).WithEnableAnalyzer(true).WithAnalyzerParams(analyzerParams))
	schema.WithField(entity.NewField().WithName("source_type").WithDataType(entity.FieldTypeInt32))
	schema.WithField(entity.NewField().WithName("source_id").WithDataType(entity.FieldTypeVarChar).WithMaxLength(36))
	schema.WithField(entity.NewField().WithName("knowledge_base_id").WithDataType(entity.FieldTypeVarChar).WithMaxLength(36))
	schema.WithField(entity.NewField().WithName("knowledge_id").WithDataType(entity.FieldTypeVarChar).WithMaxLength(36))
	schema.WithField(entity.NewField().WithName("sparse").WithDataType(entity.FieldTypeSparseVector))
	function := entity.NewFunction().WithName("text_bm25_emb").WithInputFields("content").WithOutputFields("sparse").WithType(entity.FunctionTypeBM25)
	schema.WithFunction(function)

	indexVectorOption := milvusclient.NewCreateIndexOption(collectionName, "vector", index.NewAutoIndex(index.MetricType(entity.IP)))
	// sparse用于支持全文检索
	indexSparseOption := milvusclient.NewCreateIndexOption(collectionName, "sparse", index.NewSparseInvertedIndex(entity.MetricType(entity.BM25), 0.1))
	indexSparseOption.WithExtraParam("inverted_index_algo", "DAAT_MAXSCORE")
	indexSparseOption.WithExtraParam("bm25_k1", 1.2)
	indexSparseOption.WithExtraParam("bm25_b", 0.75)

	if err := client.CreateCollection(ctx, milvusclient.NewCreateCollectionOption(collectionName, schema).WithIndexOptions(indexVectorOption, indexSparseOption)); err != nil {
		return "", err
	}
	return collectionName, nil
}

func (r *milvusRepository) SetCollectionName(ctx context.Context, dim int) error {
	collectionName, err := ensureCollectionName(ctx, r.client, dim)
	if err != nil {
		return err
	}
	r.collectionName = collectionName
	task, err := r.client.LoadCollection(ctx, milvusclient.NewLoadCollectionOption(r.collectionName))
	if err != nil {
		return err
	}
	err = task.Await(ctx)
	if err != nil {
		return err
	}
	logger.GetLogger(ctx).Infof("[Milvus] SetCollectionName: %v", collectionName)

	return nil
}

func (r *milvusRepository) GetCollectionName() string {
	return r.collectionName
}

func (r *milvusRepository) EngineType() types.RetrieverEngineType {
	return types.MilvusRetrieverEngineType
}

func (r *milvusRepository) Support() []types.RetrieverType {
	return []types.RetrieverType{types.KeywordsRetrieverType, types.VectorRetrieverType}
}

func (r *milvusRepository) Retrieve(ctx context.Context, params types.RetrieveParams) ([]*types.RetrieveResult, error) {
	logger.GetLogger(ctx).Debugf("[Postgres] Processing retrieval request of type: %s", params.RetrieverType)
	switch params.RetrieverType {
	case types.KeywordsRetrieverType:
		return r.KeywordsRetrieve(ctx, params)
	case types.VectorRetrieverType:
		return r.VectorRetrieve(ctx, params)
	}
	err := errors.New("invalid retriever type")
	logger.GetLogger(ctx).Errorf("[Postgres] %v: %s", err, params.RetrieverType)
	return nil, err
}

func (r *milvusRepository) Save(ctx context.Context, indexInfo *types.IndexInfo, params map[string]any) error {
	logger.GetLogger(ctx).Debugf("[Milvus] Saving index for chunk ID: %s", indexInfo.ChunkID)
	mcv := NewMilvusVectorFromIndexInfo(indexInfo, params)
	insertOption := ToInsertOption(r.collectionName, []*MilvusChunkVector{mcv})
	result, err := r.client.Insert(ctx, insertOption)
	if err != nil {
		logger.GetLogger(ctx).Errorf("[Milvus] Failed to save index: %v", err)
		return err
	}
	logger.GetLogger(ctx).Infof("[Milvus] Saved %d vectors with IDs: %v", result.InsertCount, result.IDs)
	return nil
}

func (r *milvusRepository) BatchSave(ctx context.Context, indexInfos []*types.IndexInfo, params map[string]any) error {
	logger.GetLogger(ctx).Debugf("[Milvus] Batch saving %d indexes", len(indexInfos))
	embeddingDBs := make([]*MilvusChunkVector, len(indexInfos))
	for i, indexInfo := range indexInfos {
		embeddingDBs[i] = NewMilvusVectorFromIndexInfo(indexInfo, params)
	}
	insertOption := ToInsertOption(r.collectionName, embeddingDBs)
	result, err := r.client.Insert(ctx, insertOption)

	if err != nil {
		logger.GetLogger(ctx).Errorf("[Milvus] Failed to batch save indexes: %v", err)
		return err
	}
	logger.GetLogger(ctx).Infof("[Milvus] Batch saved %d vectors", result.InsertCount)
	return nil
}

func (r *milvusRepository) DeleteByChunkIDList(ctx context.Context, chunkIDList []string, dimension int) error {
	logger.GetLogger(ctx).Debugf("[Milvus] Deleting %d indexes by chunk ID list", len(chunkIDList))
	deleteOption := ToDeleteOptionByChunkIds(r.collectionName, chunkIDList)
	result, err := r.client.Delete(ctx, deleteOption)
	if err != nil {
		logger.GetLogger(ctx).Errorf("[Milvus] Failed to delete indexes by chunk ID list: %v", err)
		return err
	}
	logger.GetLogger(ctx).Infof("[Milvus] Deleted %d vectors", result.DeleteCount)
	return nil
}

func (r *milvusRepository) DeleteByKnowledgeIDList(ctx context.Context, knowledgeIDList []string, dimension int) error {
	logger.GetLogger(ctx).Debugf("[Milvus] Deleting %d indexes by knowledge ID list", len(knowledgeIDList))
	deleteOption := ToDeleteOptionByKnowledgeIds(r.collectionName, knowledgeIDList)
	result, err := r.client.Delete(ctx, deleteOption)
	if err != nil {
		logger.GetLogger(ctx).Errorf("[Milvus] Failed to delete indexes by knowledge ID list: %v", err)
		return err
	}
	logger.GetLogger(ctx).Infof("[Milvus] Deleted %d vectors", result.DeleteCount)
	return nil
}

func (r *milvusRepository) KeywordsRetrieve(ctx context.Context,
	params types.RetrieveParams,
) ([]*types.RetrieveResult, error) {
	resultSets, err := r.client.Search(ctx, ToSearchOptionByKeywordsRetrieve(r.collectionName, params))
	if err != nil {
		logger.GetLogger(ctx).Errorf("[Milvus] Keywords retrieval failed: %v", err)
		return nil, err
	}
	if len(resultSets) == 0 {
		return nil, nil
	}
	results := ToRetrieveResult(resultSets[0], types.MatchTypeKeywords)
	logger.GetLogger(ctx).Infof("[Milvus] Keywords retrieval found %d results", len(results))
	return []*types.RetrieveResult{
		{
			Results:             results,
			RetrieverEngineType: types.MilvusRetrieverEngineType,
			RetrieverType:       types.KeywordsRetrieverType,
			Error:               nil,
		},
	}, nil
}

func (r *milvusRepository) VectorRetrieve(ctx context.Context,
	params types.RetrieveParams,
) ([]*types.RetrieveResult, error) {
	resultSets, err := r.client.Search(ctx, ToSearchOptionByVectorRetrieve(r.collectionName, params))
	if err != nil {
		logger.GetLogger(ctx).Errorf("[Milvus] Keywords retrieval failed: %v", err)
		return nil, err
	}
	if len(resultSets) == 0 {
		return nil, nil
	}
	results := ToRetrieveResult(resultSets[0], types.MatchTypeKeywords)
	logger.GetLogger(ctx).Infof("[Milvus] Vector retrieval found %d results", len(results))
	return []*types.RetrieveResult{
		{
			Results:             results,
			RetrieverEngineType: types.MilvusRetrieverEngineType,
			RetrieverType:       types.KeywordsRetrieverType,
			Error:               nil,
		},
	}, nil
}

// calculateIndexStorageSize calculates storage size for a single index entry
func (r *milvusRepository) calculateIndexStorageSize(embeddingDB *MilvusChunkVector) int64 {
	// 1. Text content size
	contentSizeBytes := int64(len(embeddingDB.Content))

	// 2. Vector storage size (4 bytes per float32 dimension)
	var vectorSizeBytes int64 = 0
	if len(embeddingDB.Vector) > 0 {
		vectorSizeBytes = int64(len(embeddingDB.Vector) * 4)
	}

	// 3. Metadata size (fixed overhead for IDs, etc.)
	metadataSizeBytes := int64(200)

	// 4. Index overhead (HNSW index is ~2x vector size)
	indexOverheadBytes := vectorSizeBytes * 2

	// Total size in bytes
	totalSizeBytes := contentSizeBytes + vectorSizeBytes + metadataSizeBytes + indexOverheadBytes

	return totalSizeBytes
}

// EstimateStorageSize estimates the storage size
func (r *milvusRepository) EstimateStorageSize(ctx context.Context, indexInfoList []*types.IndexInfo, params map[string]any) int64 {
	var totalStorageSize int64 = 0
	for _, indexInfo := range indexInfoList {
		embeddingDB := NewMilvusVectorFromIndexInfo(indexInfo, params)
		totalStorageSize += r.calculateIndexStorageSize(embeddingDB)
	}
	logger.GetLogger(ctx).Infof(
		"[Milvus] Estimated storage size for %d indices: %d bytes",
		len(indexInfoList), totalStorageSize,
	)
	return totalStorageSize
}

func (r *milvusRepository) CopyIndices(
	ctx context.Context,
	sourceKnowledgeBaseID string,
	sourceToTargetKBIDMap map[string]string,
	sourceToTargetChunkIDMap map[string]string,
	targetKnowledgeBaseID string,
	dimension int,
) error {
	logger.GetLogger(ctx).Infof(
		"[Milvus] Copying indices, source knowledge base: %s, target knowledge base: %s, mapping count: %d",
		sourceKnowledgeBaseID, targetKnowledgeBaseID, len(sourceToTargetChunkIDMap),
	)

	if len(sourceToTargetChunkIDMap) == 0 {
		logger.GetLogger(ctx).Warnf("[Milvus] Mapping is empty, no need to copy")
		return nil
	}

	batchSize := 500
	offset := 0
	var totalCopied int64 = 0

	for {
		qOpt := milvusclient.NewQueryOption(r.collectionName).WithOutputFields(OutputFields...).WithLimit(batchSize).WithOffset(offset).WithFilter(
			FilterWithStringIds("knowledge_base_id", []string{sourceKnowledgeBaseID}),
		)
		rs, err := r.client.Query(ctx, qOpt)
		if err != nil {
			logger.GetLogger(ctx).Errorf("[Milvus] Failed to query source knowledge base: %v", err)
			return err
		}
		batchCount := rs.Len()
		if batchCount == 0 {
			if offset == 0 {
				logger.GetLogger(ctx).Warnf("[Milvus] No source index data found")
			}
			break
		}
		insertOpt := ToInsertOptionFromResultSet(ctx, r.collectionName, rs, targetKnowledgeBaseID, sourceToTargetKBIDMap, sourceToTargetChunkIDMap)
		result, err := r.client.Insert(ctx, insertOpt)
		if err != nil {
			logger.GetLogger(ctx).Errorf("[Milvus] Failed to batch save indexes: %v", err)
			return err
		}
		totalCopied += result.InsertCount
		logger.GetLogger(ctx).Infof("[Milvus] Batch copied %d vectors, total copied: %d", result.InsertCount, totalCopied)

		offset += batchSize
		if batchCount < batchSize {
			break
		}
	}
	logger.GetLogger(ctx).Infof("[Milvus] Index copying completed, total copied: %d", totalCopied)
	return nil
}
