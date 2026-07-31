package milvus

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"os"
	"slices"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"github.com/milvus-io/milvus-proto/go-api/v2/schemapb"
	"github.com/milvus-io/milvus/client/v2/column"
	"github.com/milvus-io/milvus/client/v2/entity"
	"github.com/milvus-io/milvus/client/v2/index"
	client "github.com/milvus-io/milvus/client/v2/milvusclient"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

const (
	envMilvusCollection   = "MILVUS_COLLECTION"
	envMilvusMetricType   = "MILVUS_METRIC_TYPE"
	defaultCollectionName = "weknora_embeddings"
	fieldContent          = "content"
	fieldSourceID         = "source_id"
	fieldSourceType       = "source_type"
	fieldChunkID          = "chunk_id"
	fieldKnowledgeID      = "knowledge_id"
	fieldKnowledgeBaseID  = "knowledge_base_id"
	fieldTagID            = "tag_id"
	fieldFolderID         = "folder_id"
	fieldEmbedding        = "embedding"
	fieldIsEnabled        = "is_enabled"
	fieldID               = "id"
	fieldContentSparse    = "content_sparse"
)

var (
	allFields = []string{fieldID, fieldContent, fieldSourceID, fieldSourceType, fieldChunkID,
		fieldKnowledgeID, fieldKnowledgeBaseID, fieldTagID, fieldFolderID, fieldIsEnabled, fieldEmbedding}
)

// NewMilvusRetrieveEngineRepository creates and initializes a new Milvus repository.
// indexCfg is optional — pass nil to use env var / default values (env path).
func NewMilvusRetrieveEngineRepository(client *client.Client, indexCfg *types.IndexConfig) interfaces.RetrieveEngineRepository {
	log := logger.GetLogger(context.Background())
	log.Info("[Milvus] Initializing Milvus retriever engine repository")

	collectionBaseName := types.ResolveCollectionName(indexCfg, envMilvusCollection, defaultCollectionName)

	metricType := entity.IP
	if mt := os.Getenv(envMilvusMetricType); mt != "" {
		switch strings.ToUpper(mt) {
		case "COSINE":
			metricType = entity.COSINE
		case "L2":
			metricType = entity.L2
		case "IP":
			metricType = entity.IP
		default:
			log.Warnf("[Milvus] Unknown MILVUS_METRIC_TYPE '%s', using default IP", mt)
		}
	}
	log.Infof("[Milvus] Using metric type: %s", metricType)

	res := &milvusRepository{
		filter:             filter{},
		client:             client,
		collectionBaseName: collectionBaseName,
		metricType:         metricType,
		shardsNum:          indexCfg.GetShardsNum(0),
		replicaNumber:      indexCfg.GetReplicaNumber(0),
	}

	log.Info("[Milvus] Successfully initialized repository")
	return res
}

// getCollectionName returns the collection name for a specific dimension
func (m *milvusRepository) getCollectionName(dimension int) string {
	return fmt.Sprintf("%s_%d", m.collectionBaseName, dimension)
}

// ensureCollection ensures the collection exists for the given dimension
func (m *milvusRepository) ensureCollection(ctx context.Context, dimension int) error {
	collectionName := m.getCollectionName(dimension)

	// Check cache first
	if _, ok := m.initializedCollections.Load(collectionName); ok {
		return nil
	}

	m.ensureMu.Lock()
	defer m.ensureMu.Unlock()
	if _, ok := m.initializedCollections.Load(collectionName); ok {
		return nil
	}

	log := logger.GetLogger(ctx)

	// Check if collection exists
	hasCollection, err := m.client.HasCollection(ctx, client.NewHasCollectionOption(collectionName))
	if err != nil {
		log.Errorf("[Milvus] Failed to check collection existence: %v", err)
		return fmt.Errorf("failed to check collection existence: %w", err)
	}

	if !hasCollection {
		log.Infof("[Milvus] Creating collection %s with dimension %d", collectionName, dimension)

		// Define schema
		schema := &entity.Schema{
			CollectionName: collectionName,
			Description:    fmt.Sprintf("WeKnora embeddings collection with dimension %d", dimension),
			AutoID:         false,
			Fields: []*entity.Field{
				entity.NewField().
					WithName(fieldID).
					WithDataType(entity.FieldTypeVarChar).
					WithIsPrimaryKey(true).
					WithMaxLength(1024),
				entity.NewField().
					WithName(fieldEmbedding).
					WithDataType(entity.FieldTypeFloatVector).
					WithDim(int64(dimension)),
				entity.NewField().
					WithName(fieldContent).
					WithDataType(entity.FieldTypeVarChar).
					WithMaxLength(65535).
					WithEnableAnalyzer(true).
					WithEnableMatch(true),
				entity.NewField().
					WithName(fieldContentSparse).
					WithDataType(entity.FieldTypeSparseVector),
				entity.NewField().
					WithName(fieldSourceID).
					WithDataType(entity.FieldTypeVarChar).
					WithMaxLength(255),
				entity.NewField().
					WithName(fieldSourceType).
					WithDataType(entity.FieldTypeInt64),
				entity.NewField().
					WithName(fieldChunkID).
					WithDataType(entity.FieldTypeVarChar).
					WithMaxLength(255),
				entity.NewField().
					WithName(fieldKnowledgeID).
					WithDataType(entity.FieldTypeVarChar).
					WithMaxLength(255),
				entity.NewField().
					WithName(fieldKnowledgeBaseID).
					WithDataType(entity.FieldTypeVarChar).
					WithMaxLength(255),
				entity.NewField().
					WithName(fieldTagID).
					WithDataType(entity.FieldTypeVarChar).
					WithMaxLength(255),
				newFolderField(),
				entity.NewField().
					WithName(fieldIsEnabled).
					WithDataType(entity.FieldTypeBool),
			},
		}

		// Add BM25 function for content sparse vector
		// ref: https://milvus.io/docs/zh/full-text-search.md
		schema.WithFunction(entity.NewFunction().
			WithName("text_bm25_emb").
			WithInputFields(fieldContent).
			WithOutputFields(fieldContentSparse).
			WithType(entity.FunctionTypeBM25))

		indexOpts := make([]client.CreateIndexOption, 0)
		// hnsw index for embedding field
		indexOpts = append(indexOpts, client.NewCreateIndexOption(collectionName, fieldEmbedding, index.NewHNSWIndex(m.metricType, 16, 128)))
		indexOpts = append(indexOpts, client.NewCreateIndexOption(collectionName, fieldContentSparse, index.NewAutoIndex(entity.BM25)))
		// Create payload indexes for filtering
		indexFields := []string{
			fieldChunkID,
			fieldKnowledgeID,
			fieldKnowledgeBaseID,
			fieldSourceID,
			fieldFolderID,
			fieldIsEnabled,
		}
		for _, fieldName := range indexFields {
			indexOpts = append(indexOpts, client.NewCreateIndexOption(collectionName, fieldName, index.NewAutoIndex(entity.IP)))
		}

		// Create collection
		createOpt := client.NewCreateCollectionOption(collectionName, schema).WithIndexOptions(indexOpts...)
		if m.shardsNum > 0 {
			createOpt = createOpt.WithShardNum(int32(m.shardsNum))
		}
		err = m.client.CreateCollection(ctx, createOpt)
		if err != nil {
			log.Errorf("[Milvus] Failed to create collection: %v", err)
			return fmt.Errorf("failed to create collection: %w", err)
		}

		log.Infof("[Milvus] Successfully created collection %s", collectionName)
	} else if err := m.ensureExistingCollectionSchema(ctx, collectionName); err != nil {
		return err
	}

	if err := m.loadCollection(ctx, collectionName); err != nil {
		return err
	}

	m.initializedCollections.Store(collectionName, true)
	return nil
}

// ensureExistingCollection prepares an existing collection for folder-scoped
// retrieval without creating a missing collection. Ordinary retrieval keeps
// the legacy path and must not build a folder index in its request deadline.
func (m *milvusRepository) ensureExistingCollection(ctx context.Context, collectionName string) error {
	if _, ok := m.initializedCollections.Load(collectionName); ok {
		return nil
	}

	m.ensureMu.Lock()
	defer m.ensureMu.Unlock()
	if _, ok := m.initializedCollections.Load(collectionName); ok {
		return nil
	}

	if err := m.ensureExistingCollectionSchema(ctx, collectionName); err != nil {
		return err
	}
	if err := m.loadCollection(ctx, collectionName); err != nil {
		return err
	}
	m.initializedCollections.Store(collectionName, true)
	return nil
}

func (m *milvusRepository) ensureExistingCollectionSchema(ctx context.Context, collectionName string) error {
	collection, err := m.client.DescribeCollection(
		ctx,
		client.NewDescribeCollectionOption(collectionName),
	)
	if err != nil {
		return fmt.Errorf("describe existing Milvus collection %s: %w", collectionName, err)
	}
	if collection == nil || collection.Schema == nil {
		return fmt.Errorf("describe existing Milvus collection %s returned no schema", collectionName)
	}

	folderField := schemaField(collection.Schema, fieldFolderID)
	if folderField == nil {
		addErr := m.addFolderField(ctx, collectionName)
		// Always re-read the authoritative schema. A concurrent process may
		// have won the add-field race even if our request returned an error.
		collection, err = m.client.DescribeCollection(
			ctx,
			client.NewDescribeCollectionOption(collectionName),
		)
		if err != nil {
			if addErr != nil {
				return errors.Join(addErr, fmt.Errorf(
					"verify %s field on Milvus collection %s: %w",
					fieldFolderID,
					collectionName,
					err,
				))
			}
			return fmt.Errorf(
				"verify %s field on Milvus collection %s: %w",
				fieldFolderID,
				collectionName,
				err,
			)
		}
		if collection == nil || collection.Schema == nil {
			return fmt.Errorf(
				"verify %s field on Milvus collection %s returned no schema",
				fieldFolderID,
				collectionName,
			)
		}
		folderField = schemaField(collection.Schema, fieldFolderID)
		if folderField == nil && addErr != nil {
			return addErr
		}
	}

	if err := validateFolderField(collectionName, folderField); err != nil {
		return err
	}
	if err := m.ensureFolderIndex(ctx, collectionName); err != nil {
		return err
	}
	return nil
}

func (m *milvusRepository) loadCollection(ctx context.Context, collectionName string) error {
	log := logger.GetLogger(ctx)
	loadOpt := client.NewLoadCollectionOption(collectionName)
	if m.replicaNumber > 0 {
		loadOpt = loadOpt.WithReplica(m.replicaNumber)
	}
	loadTask, err := m.client.LoadCollection(ctx, loadOpt)
	if err != nil {
		log.Errorf("[Milvus] Failed to load collection: %v", err)
		return fmt.Errorf("failed to load collection: %w", err)
	}
	if err := loadTask.Await(ctx); err != nil {
		log.Errorf("[Milvus] Failed to await load collection: %v", err)
		return fmt.Errorf("failed to await load collection: %w", err)
	}
	return nil
}

func (m *milvusRepository) addFolderField(ctx context.Context, collectionName string) error {
	if err := m.client.AddCollectionField(
		ctx,
		client.NewAddCollectionFieldOption(collectionName, newFolderField()),
	); err != nil {
		return fmt.Errorf("add %s field to Milvus collection %s: %w", fieldFolderID, collectionName, err)
	}
	return nil
}

// newFolderField keeps newly-created collections writable by workers from the
// first rollout phase. Those workers do not send folder_id, so the field must
// use the same nullable root default as an in-place schema upgrade.
func newFolderField() *entity.Field {
	return entity.NewField().
		WithName(fieldFolderID).
		WithDataType(entity.FieldTypeVarChar).
		WithMaxLength(36).
		WithNullable(true).
		WithDefaultValueString("")
}

func (m *milvusRepository) ensureFolderIndex(ctx context.Context, collectionName string) error {
	indexNames, err := m.client.ListIndexes(
		ctx,
		client.NewListIndexOption(collectionName).WithFieldName(fieldFolderID),
	)
	if err != nil {
		return fmt.Errorf("list %s indexes on Milvus collection %s: %w", fieldFolderID, collectionName, err)
	}
	if len(indexNames) > 0 {
		for _, indexName := range indexNames {
			description, err := m.client.DescribeIndex(
				ctx,
				client.NewDescribeIndexOption(collectionName, indexName),
			)
			if err != nil {
				return fmt.Errorf(
					"describe %s index %s on Milvus collection %s: %w",
					fieldFolderID,
					indexName,
					collectionName,
					err,
				)
			}
			if description.Index == nil {
				return fmt.Errorf(
					"describe %s index %s on Milvus collection %s returned no index definition",
					fieldFolderID,
					indexName,
					collectionName,
				)
			}
			if !isScalarFilterIndex(description.IndexType()) {
				return fmt.Errorf(
					"Milvus collection %s has incompatible %s index type %s; rebuild the index as a scalar filter index",
					collectionName,
					fieldFolderID,
					description.IndexType(),
				)
			}
		}
		return nil
	}
	indexTask, err := m.client.CreateIndex(
		ctx,
		client.NewCreateIndexOption(collectionName, fieldFolderID, index.NewAutoIndex(entity.IP)),
	)
	if err != nil {
		return fmt.Errorf("create %s index on Milvus collection %s: %w", fieldFolderID, collectionName, err)
	}
	if indexTask != nil {
		if err := indexTask.Await(ctx); err != nil {
			return fmt.Errorf("await %s index on Milvus collection %s: %w", fieldFolderID, collectionName, err)
		}
	}
	return nil
}

func isScalarFilterIndex(indexType index.IndexType) bool {
	switch indexType {
	case index.AUTOINDEX, index.Trie, index.Sorted, index.Inverted, index.BITMAP:
		return true
	default:
		return false
	}
}

func validateFolderField(collectionName string, field *entity.Field) error {
	if field == nil {
		return fmt.Errorf(
			"Milvus collection %s is missing required %s field after schema update",
			collectionName,
			fieldFolderID,
		)
	}
	if field.DataType != entity.FieldTypeVarChar {
		return fmt.Errorf(
			"Milvus collection %s has incompatible %s data type %s; expected VarChar",
			collectionName,
			fieldFolderID,
			field.DataType.Name(),
		)
	}
	maxLength, err := strconv.Atoi(field.TypeParams[entity.TypeParamMaxLength])
	if err != nil || maxLength < 36 {
		return fmt.Errorf(
			"Milvus collection %s has incompatible %s max length %q; expected at least 36",
			collectionName,
			fieldFolderID,
			field.TypeParams[entity.TypeParamMaxLength],
		)
	}

	hasEmptyDefault := false
	if field.DefaultValue != nil {
		_, isStringDefault := field.DefaultValue.GetData().(*schemapb.ValueField_StringData)
		hasEmptyDefault = isStringDefault && field.DefaultValue.GetStringData() == ""
	}
	if field.DefaultValue != nil && !hasEmptyDefault {
		return fmt.Errorf(
			"Milvus collection %s has incompatible %s default; expected an empty string",
			collectionName,
			fieldFolderID,
		)
	}
	if !field.Nullable {
		return fmt.Errorf(
			"Milvus collection %s has non-nullable %s; the field must be nullable so older workers can omit it during a rolling upgrade",
			collectionName,
			fieldFolderID,
		)
	}
	if !hasEmptyDefault {
		return fmt.Errorf(
			"Milvus collection %s has nullable %s without an empty-string default; backfill root rows and rebuild the field",
			collectionName,
			fieldFolderID,
		)
	}
	return nil
}

func schemaField(schema *entity.Schema, fieldName string) *entity.Field {
	if schema == nil || fieldName == "" {
		return nil
	}
	for _, field := range schema.Fields {
		if field != nil && field.Name == fieldName {
			return field
		}
	}
	return nil
}

func schemaHasField(schema *entity.Schema, fieldName string) bool {
	return schemaField(schema, fieldName) != nil
}

func (m *milvusRepository) EngineType() types.RetrieverEngineType {
	return types.MilvusRetrieverEngineType
}

func (m *milvusRepository) Support() []types.RetrieverType {
	return []types.RetrieverType{types.KeywordsRetrieverType, types.VectorRetrieverType}
}

// EstimateStorageSize calculates the estimated storage size for a list of indices
func (m *milvusRepository) EstimateStorageSize(ctx context.Context,
	indexInfoList []*types.IndexInfo, params map[string]any,
) int64 {
	var totalStorageSize int64
	for _, embedding := range indexInfoList {
		embeddingDB := toMilvusVectorEmbedding(embedding, params)
		totalStorageSize += m.calculateStorageSize(embeddingDB)
	}
	logger.GetLogger(ctx).Infof(
		"[Milvus] Storage size for %d indices: %d bytes", len(indexInfoList), totalStorageSize,
	)
	return totalStorageSize
}

// Save stores a single point in Milvus
func (m *milvusRepository) Save(ctx context.Context,
	embedding *types.IndexInfo,
	additionalParams map[string]any,
) error {
	log := logger.GetLogger(ctx)
	log.Debugf("[Milvus] Saving index for chunk ID: %s", embedding.ChunkID)

	embeddingDB := toMilvusVectorEmbedding(embedding, additionalParams)
	if len(embeddingDB.Embedding) == 0 {
		err := fmt.Errorf("empty embedding vector for chunk ID: %s", embedding.ChunkID)
		log.Errorf("[Milvus] %v", err)
		return err
	}

	dimension := len(embeddingDB.Embedding)
	if err := m.ensureCollection(ctx, dimension); err != nil {
		return err
	}

	collectionName := m.getCollectionName(dimension)

	embeddingDB.ID = uuid.New().String()
	opts := createUpsert(collectionName, []*MilvusVectorEmbedding{embeddingDB})

	_, err := m.client.Upsert(ctx, opts)
	if err != nil {
		log.Errorf("[Milvus] Failed to save index: %v", err)
		return err
	}

	log.Infof("[Milvus] Successfully saved index for chunk ID: %s", embedding.ChunkID)
	return nil
}

// BatchSave stores multiple points in Milvus using batch insert
func (m *milvusRepository) BatchSave(ctx context.Context,
	embeddingList []*types.IndexInfo, additionalParams map[string]any,
) error {
	log := logger.GetLogger(ctx)
	if len(embeddingList) == 0 {
		log.Warn("[Milvus] Empty list provided to BatchSave, skipping")
		return nil
	}

	log.Infof("[Milvus] Batch saving %d indices", len(embeddingList))

	// Group points by dimension
	embeddingsByDimension := make(map[int][]*types.IndexInfo)

	for _, embedding := range embeddingList {
		embeddingDB := toMilvusVectorEmbedding(embedding, additionalParams)
		if len(embeddingDB.Embedding) == 0 {
			log.Warnf("[Milvus] Skipping empty embedding for chunk ID: %s", embedding.ChunkID)
			continue
		}

		dimension := len(embeddingDB.Embedding)
		embeddingsByDimension[dimension] = append(embeddingsByDimension[dimension], embedding)
		log.Debugf("[Milvus] Added chunk ID %s to batch request (dimension: %d)", embedding.ChunkID, dimension)
	}

	if len(embeddingsByDimension) == 0 {
		log.Warn("[Milvus] No valid points to save after filtering")
		return nil
	}

	// Save points to each dimension-specific collection
	totalSaved := 0
	for dimension, embeddings := range embeddingsByDimension {
		if err := m.ensureCollection(ctx, dimension); err != nil {
			return err
		}

		collectionName := m.getCollectionName(dimension)
		n := len(embeddings)
		embeddingDBList := make([]*MilvusVectorEmbedding, 0, n)

		for _, embedding := range embeddings {
			embeddingDB := toMilvusVectorEmbedding(embedding, additionalParams)
			embeddingDB.ID = uuid.New().String()
			embeddingDBList = append(embeddingDBList, embeddingDB)
		}
		opts := createUpsert(collectionName, embeddingDBList)
		_, err := m.client.Upsert(ctx, opts)
		if err != nil {
			log.Errorf("[Milvus] Failed to execute batch operation for dimension %d: %v", dimension, err)
			return fmt.Errorf("failed to batch save (dimension %d): %w", dimension, err)
		}
		totalSaved += n
		log.Infof("[Milvus] Saved %d points to collection %s", n, collectionName)
	}

	log.Infof("[Milvus] Successfully batch saved %d indices", totalSaved)
	return nil
}

// DeleteByChunkIDList removes points from the collection based on chunk IDs
func (m *milvusRepository) DeleteByChunkIDList(ctx context.Context, chunkIDList []string, dimension int, knowledgeType string) error {
	log := logger.GetLogger(ctx)
	if len(chunkIDList) == 0 {
		log.Warn("[Milvus] Empty chunk ID list provided for deletion, skipping")
		return nil
	}

	collectionName := m.getCollectionName(dimension)
	log.Infof("[Milvus] Deleting indices by chunk IDs from %s, count: %d", collectionName, len(chunkIDList))

	deleteOpt := client.NewDeleteOption(collectionName)
	deleteOpt.WithStringIDs(fieldChunkID, chunkIDList)
	_, err := m.client.Delete(ctx, deleteOpt)
	if err != nil {
		log.Errorf("[Milvus] Failed to delete by chunk IDs: %v", err)
		return fmt.Errorf("failed to delete by chunk IDs: %w", err)
	}

	log.Infof("[Milvus] Successfully deleted documents by chunk IDs")
	return nil
}

// DeleteByKnowledgeIDList removes points from the collection based on knowledge IDs
func (m *milvusRepository) DeleteByKnowledgeIDList(ctx context.Context,
	knowledgeIDList []string, dimension int, knowledgeType string,
) error {
	log := logger.GetLogger(ctx)
	if len(knowledgeIDList) == 0 {
		log.Warn("[Milvus] Empty knowledge ID list provided for deletion, skipping")
		return nil
	}

	collectionName := m.getCollectionName(dimension)
	log.Infof("[Milvus] Deleting indices by knowledge IDs from %s, count: %d", collectionName, len(knowledgeIDList))

	deleteOpt := client.NewDeleteOption(collectionName)
	deleteOpt.WithStringIDs(fieldKnowledgeID, knowledgeIDList)
	_, err := m.client.Delete(ctx, deleteOpt)
	if err != nil {
		log.Errorf("[Milvus] Failed to delete by knowledge IDs: %v", err)
		return fmt.Errorf("failed to delete by knowledge IDs: %w", err)
	}

	log.Infof("[Milvus] Successfully deleted documents by knowledge IDs")
	return nil
}

// DeleteBySourceIDList removes points from the collection based on source IDs
func (m *milvusRepository) DeleteBySourceIDList(ctx context.Context,
	sourceIDList []string, dimension int, knowledgeType string,
) error {
	log := logger.GetLogger(ctx)
	if len(sourceIDList) == 0 {
		log.Warn("[Milvus] Empty source ID list provided for deletion, skipping")
		return nil
	}

	collectionName := m.getCollectionName(dimension)
	log.Infof("[Milvus] Deleting indices by source IDs from %s, count: %d", collectionName, len(sourceIDList))

	deleteOpt := client.NewDeleteOption(collectionName)
	deleteOpt.WithStringIDs(fieldSourceID, sourceIDList)
	_, err := m.client.Delete(ctx, deleteOpt)
	if err != nil {
		log.Errorf("[Milvus] Failed to delete by source IDs: %v", err)
		return fmt.Errorf("failed to delete by source IDs: %w", err)
	}

	log.Infof("[Milvus] Successfully deleted documents by source IDs")
	return nil
}

// BatchUpdateChunkEnabledStatus updates the enabled status of chunks in batch
func (m *milvusRepository) BatchUpdateChunkEnabledStatus(ctx context.Context, chunkStatusMap map[string]bool) error {
	log := logger.GetLogger(ctx)
	if len(chunkStatusMap) == 0 {
		log.Warn("[Milvus] Empty chunk status map provided, skipping")
		return nil
	}

	log.Infof("[Milvus] Batch updating chunk enabled status, count: %d", len(chunkStatusMap))

	// Get all collections
	collections, err := m.client.ListCollections(ctx, client.NewListCollectionOption())
	if err != nil {
		log.Errorf("[Milvus] Failed to list collections: %v", err)
		return fmt.Errorf("failed to list collections: %w", err)
	}

	// Group chunks by enabled status for batch updates
	enabledChunkIDs := make([]string, 0)
	disabledChunkIDs := make([]string, 0)

	for chunkID, enabled := range chunkStatusMap {
		if enabled {
			enabledChunkIDs = append(enabledChunkIDs, chunkID)
		} else {
			disabledChunkIDs = append(disabledChunkIDs, chunkID)
		}
	}

	// A disabled row in the primary DB must never remain searchable because an
	// index update failed silently.
	if err := updateChunkEnabledStatusInCollections(
		ctx, collections, m.collectionBaseName, enabledChunkIDs, disabledChunkIDs,
		m.updateChunkEnabledStatusInCollection,
	); err != nil {
		log.Warnf("[Milvus] Failed to update chunk enabled status: %v", err)
		return err
	}

	log.Infof("[Milvus] Batch update chunk enabled status completed")
	return nil
}

func updateChunkEnabledStatusInCollections(
	ctx context.Context,
	collections []string,
	collectionBaseName string,
	enabledChunkIDs []string,
	disabledChunkIDs []string,
	update func(context.Context, string, []string, bool) error,
) error {
	var updateErrs []error
	for _, collectionName := range collections {
		if len(collectionName) <= len(collectionBaseName) ||
			collectionName[:len(collectionBaseName)] != collectionBaseName {
			continue
		}
		if err := update(ctx, collectionName, enabledChunkIDs, true); err != nil {
			updateErrs = append(updateErrs, fmt.Errorf("update enabled chunks in %s: %w", collectionName, err))
		}
		if err := update(ctx, collectionName, disabledChunkIDs, false); err != nil {
			updateErrs = append(updateErrs, fmt.Errorf("update disabled chunks in %s: %w", collectionName, err))
		}
	}
	return errors.Join(updateErrs...)
}

func (m *milvusRepository) updateChunkEnabledStatusInCollection(
	ctx context.Context,
	collectionName string,
	chunkIDs []string,
	enabled bool,
) error {
	if len(chunkIDs) == 0 {
		return nil
	}

	embeddings, _, err := m.searchByFilter(ctx, collectionName, &universalFilterCondition{
		Field:    fieldChunkID,
		Operator: operatorIn,
		Value:    chunkIDs,
	}, nil, nil)
	if err != nil {
		return err
	}

	upsertEmbeddings := make([]*MilvusVectorEmbedding, 0, len(embeddings))
	for _, embedding := range embeddings {
		embedding.IsEnabled = enabled
		upsertEmbeddings = append(upsertEmbeddings, &embedding.MilvusVectorEmbedding)
	}
	if len(upsertEmbeddings) == 0 {
		return nil
	}

	req := createUpsert(collectionName, upsertEmbeddings)
	if _, err := m.client.Upsert(ctx, req); err != nil {
		return err
	}
	return nil
}

func (m *milvusRepository) searchByFilter(ctx context.Context, collectionName string, filter *universalFilterCondition, limit, offset *int) ([]*MilvusVectorEmbeddingWithScore, int, error) {
	params, err := m.filter.Convert(filter)
	if err != nil {
		return nil, 0, err
	}
	queryOpt := client.NewQueryOption(collectionName)
	if params.exprStr != "" {
		queryOpt.WithFilter(params.exprStr)
		for k, v := range params.params {
			queryOpt.WithTemplateParam(k, v)
		}
	}
	queryOpt.WithOutputFields("*")
	if limit != nil {
		queryOpt.WithLimit(*limit)
	}
	if offset != nil {
		queryOpt.WithOffset(*offset)
	}
	resultSet, err := m.client.Query(ctx, queryOpt)
	if err != nil {
		return nil, 0, err
	}
	embeddings, _, err := convertResultSet([]client.ResultSet{resultSet})
	if err != nil {
		return nil, 0, err
	}
	return embeddings, resultSet.ResultCount, nil
}

// BatchUpdateChunkTagID updates the tag ID of chunks in batch
func (m *milvusRepository) BatchUpdateChunkTagID(ctx context.Context, chunkTagMap map[string]string) error {
	log := logger.GetLogger(ctx)
	if len(chunkTagMap) == 0 {
		log.Warn("[Milvus] Empty chunk tag map provided, skipping")
		return nil
	}

	log.Infof("[Milvus] Batch updating chunk tag ID, count: %d", len(chunkTagMap))

	// Get all collections
	collections, err := m.client.ListCollections(ctx, client.NewListCollectionOption())
	if err != nil {
		log.Errorf("[Milvus] Failed to list collections: %v", err)
		return fmt.Errorf("failed to list collections: %w", err)
	}

	// Group chunks by tag ID for batch updates
	tagGroups := make(map[string][]string)
	for chunkID, tagID := range chunkTagMap {
		tagGroups[tagID] = append(tagGroups[tagID], chunkID)
	}

	// Update in all matching collections
	for _, collectionName := range collections {
		// Only process collections that start with our base name
		if len(collectionName) <= len(m.collectionBaseName) ||
			collectionName[:len(m.collectionBaseName)] != m.collectionBaseName {
			continue
		}
		// Update chunks for each tag ID
		for tagID, chunkIDs := range tagGroups {
			embeddings, _, err := m.searchByFilter(ctx, collectionName, &universalFilterCondition{
				Field:    fieldChunkID,
				Operator: operatorIn,
				Value:    chunkIDs,
			}, nil, nil)
			if err != nil {
				log.Warnf("[Milvus] Failed to search chunks in %s: %v", collectionName, err)
				continue
			}
			upsertEmbeddings := make([]*MilvusVectorEmbedding, 0, len(embeddings))
			for _, embedding := range embeddings {
				embedding.TagID = tagID
				upsertEmbeddings = append(upsertEmbeddings, &embedding.MilvusVectorEmbedding)
			}
			if len(upsertEmbeddings) > 0 {
				req := createUpsert(collectionName, upsertEmbeddings)
				_, err := m.client.Upsert(ctx, req)
				if err != nil {
					log.Warnf("[Milvus] Failed to update chunks in %s: %v", collectionName, err)
					continue
				}
			}
		}

	}

	log.Infof("[Milvus] Batch update chunk tag ID completed")
	return nil
}

// BatchUpdateChunkFolderID updates the folder ID of chunks in batch.
func (m *milvusRepository) BatchUpdateChunkFolderID(ctx context.Context, chunkFolderMap map[string]string) error {
	if len(chunkFolderMap) == 0 {
		return nil
	}
	collections, err := m.client.ListCollections(ctx, client.NewListCollectionOption())
	if err != nil {
		return fmt.Errorf("list Milvus collections: %w", err)
	}
	folderGroups := make(map[string][]string)
	for chunkID, folderID := range chunkFolderMap {
		folderGroups[folderID] = append(folderGroups[folderID], chunkID)
	}
	for _, collectionName := range collections {
		if len(collectionName) <= len(m.collectionBaseName) ||
			collectionName[:len(m.collectionBaseName)] != m.collectionBaseName {
			continue
		}
		for folderID, chunkIDs := range folderGroups {
			embeddings, _, err := m.searchByFilter(ctx, collectionName, &universalFilterCondition{
				Field: fieldChunkID, Operator: operatorIn, Value: chunkIDs,
			}, nil, nil)
			if err != nil {
				return fmt.Errorf("find Milvus chunks in %s: %w", collectionName, err)
			}
			upserts := make([]*MilvusVectorEmbedding, 0, len(embeddings))
			for _, embedding := range embeddings {
				embedding.FolderID = folderID
				upserts = append(upserts, &embedding.MilvusVectorEmbedding)
			}
			if len(upserts) == 0 {
				continue
			}
			if _, err := m.client.Upsert(ctx, createUpsert(collectionName, upserts)); err != nil {
				return fmt.Errorf("update Milvus chunks in %s: %w", collectionName, err)
			}
		}
	}
	return nil
}

func (m *milvusRepository) getBaseFilterForQuery(params types.RetrieveParams) (string, map[string]any, error) {
	filters := make([]*universalFilterCondition, 0)
	if len(params.KnowledgeBaseIDs) > 0 {
		filters = append(filters, &universalFilterCondition{
			Field:    fieldKnowledgeBaseID,
			Operator: operatorIn,
			Value:    params.KnowledgeBaseIDs,
		})
	}
	if len(params.KnowledgeIDs) > 0 {
		filters = append(filters, &universalFilterCondition{
			Field:    fieldKnowledgeID,
			Operator: operatorIn,
			Value:    params.KnowledgeIDs,
		})
	}
	if len(params.TagIDs) > 0 {
		filters = append(filters, &universalFilterCondition{
			Field:    fieldTagID,
			Operator: operatorIn,
			Value:    params.TagIDs,
		})
	}
	if len(params.FolderIDs) > 0 {
		filters = append(filters, &universalFilterCondition{
			Field:    fieldFolderID,
			Operator: operatorIn,
			Value:    params.FolderIDs,
		})
	}
	if len(params.ExcludeKnowledgeIDs) > 0 {
		filters = append(filters, &universalFilterCondition{
			Field:    fieldKnowledgeID,
			Operator: operatorNotIn,
			Value:    params.ExcludeKnowledgeIDs,
		})
	}
	if len(params.ExcludeChunkIDs) > 0 {
		filters = append(filters, &universalFilterCondition{
			Field:    fieldChunkID,
			Operator: operatorNotIn,
			Value:    params.ExcludeChunkIDs,
		})
	}
	filters = append(filters, &universalFilterCondition{
		Field:    fieldIsEnabled,
		Operator: operatorEqual,
		Value:    true,
	})
	if len(filters) == 0 {
		return "", nil, nil
	}
	f, err := m.filter.Convert(&universalFilterCondition{
		Operator: operatorAnd,
		Value:    filters,
	})
	if err != nil {
		return "", nil, err
	}
	return f.exprStr, f.params, nil
}

// Retrieve dispatches the retrieval operation to the appropriate method based on retriever type
func (m *milvusRepository) Retrieve(ctx context.Context,
	params types.RetrieveParams,
) ([]*types.RetrieveResult, error) {
	log := logger.GetLogger(ctx)
	log.Debugf("[Milvus] Processing retrieval request of type: %s", params.RetrieverType)

	switch params.RetrieverType {
	case types.VectorRetrieverType:
		return m.VectorRetrieve(ctx, params)
	case types.KeywordsRetrieverType:
		return m.KeywordsRetrieve(ctx, params)
	}

	err := fmt.Errorf("invalid retriever type: %v", params.RetrieverType)
	log.Errorf("[Milvus] %v", err)
	return nil, err
}

// VectorRetrieve performs vector similarity search
func (m *milvusRepository) VectorRetrieve(ctx context.Context,
	params types.RetrieveParams,
) ([]*types.RetrieveResult, error) {
	log := logger.GetLogger(ctx)
	dimension := len(params.Embedding)
	log.Infof("[Milvus] Vector retrieval: dim=%d, topK=%d, threshold=%.4f",
		dimension, params.TopK, params.Threshold)

	// Get collection name based on embedding dimension
	collectionName := m.getCollectionName(dimension)

	// Check if collection exists
	hasCollection, err := m.client.HasCollection(ctx, client.NewHasCollectionOption(collectionName))
	if err != nil {
		log.Errorf("[Milvus] Failed to check collection existence: %v", err)
		return nil, fmt.Errorf("failed to check collection: %w", err)
	}
	if !hasCollection {
		log.Warnf("[Milvus] Collection %s does not exist, returning empty results", collectionName)
		return buildRetrieveResult(nil, types.VectorRetrieverType), nil
	}
	if len(params.FolderIDs) > 0 {
		if err := m.ensureExistingCollection(ctx, collectionName); err != nil {
			return nil, fmt.Errorf("prepare Milvus collection %s for vector retrieval: %w", collectionName, err)
		}
	}

	expr, paramsMap, err := m.getBaseFilterForQuery(params)
	if err != nil {
		log.Errorf("[Milvus] Failed to build base filter: %v", err)
		return nil, fmt.Errorf("failed to build filter: %w", err)
	}
	var sp *index.CustomAnnParam
	if params.Threshold > 0 {
		ann := index.NewCustomAnnParam()
		ann.WithRadius(params.Threshold)
		sp = &ann
	}
	searchOption := client.NewSearchOption(collectionName, params.TopK, []entity.Vector{entity.FloatVector(params.Embedding)})
	searchOption.WithANNSField(fieldEmbedding)
	if sp != nil {
		searchOption.WithAnnParam(sp)
	}
	if expr != "" {
		searchOption.WithFilter(expr)
		for k, v := range paramsMap {
			searchOption.WithTemplateParam(k, v)
		}
	}
	searchOption.WithOutputFields("*")
	resultSet, err := m.client.Search(ctx, searchOption)
	if err != nil {
		log.Errorf("[Milvus] Vector search failed: %v", err)
		return nil, fmt.Errorf("failed to search: %w", err)
	}
	sets, scores, err := convertResultSet(resultSet)
	if err != nil {
		log.Errorf("[Milvus] Failed to convert result set: %v", err)
		return nil, fmt.Errorf("failed to convert result set: %w", err)
	}
	var results []*types.IndexWithScore
	for i, set := range sets {
		set.Score = scores[i]
		results = append(results, fromMilvusVectorEmbedding(set.ID, set, types.MatchTypeEmbedding))
	}
	if len(results) == 0 {
		log.Warnf("[Milvus] No vector matches found that meet threshold %.4f", params.Threshold)
	} else {
		log.Infof("[Milvus] Vector retrieval found %d results", len(results))
		log.Debugf("[Milvus] Top result score: %.4f", results[0].Score)
	}
	return buildRetrieveResult(results, types.VectorRetrieverType), nil
}

// KeywordsRetrieve performs keyword-based search in document content
func (m *milvusRepository) KeywordsRetrieve(ctx context.Context,
	params types.RetrieveParams,
) ([]*types.RetrieveResult, error) {
	log := logger.GetLogger(ctx)
	log.Infof("[Milvus] Performing keywords retrieval with query: %s, topK: %d", params.Query, params.TopK)

	// Get all collections
	collections, err := m.client.ListCollections(ctx, client.NewListCollectionOption())
	if err != nil {
		log.Errorf("[Milvus] Failed to list collections: %v", err)
		return nil, fmt.Errorf("failed to list collections: %w", err)
	}

	var allResults []*types.IndexWithScore

	// Search in all matching collections
	for _, collectionName := range collections {
		// Only process collections that start with our base name
		if len(collectionName) <= len(m.collectionBaseName) ||
			collectionName[:len(m.collectionBaseName)] != m.collectionBaseName {
			continue
		}
		if len(params.FolderIDs) > 0 {
			if err := m.ensureExistingCollection(ctx, collectionName); err != nil {
				return nil, fmt.Errorf(
					"prepare Milvus collection %s for keyword retrieval: %w",
					collectionName,
					err,
				)
			}
		}

		expr, paramsMap, err := m.getBaseFilterForQuery(params)
		if err != nil {
			log.Errorf("[Milvus] Failed to build base filter: %v", err)
			continue
		}
		searchOpt := client.NewSearchOption(collectionName, params.TopK, []entity.Vector{entity.Text(params.Query)})
		searchOpt.WithANNSField(fieldContentSparse)
		if expr != "" {
			searchOpt.WithFilter(expr)
			for k, v := range paramsMap {
				searchOpt.WithTemplateParam(k, v)
			}
		}
		searchOpt.WithOutputFields("*")
		resultSet, err := m.client.Search(ctx, searchOpt)
		if err != nil {
			log.Errorf("[Milvus] Keywords search failed: %v", err)
			continue
		}
		sets, _, err := convertResultSet(resultSet)
		if err != nil {
			log.Errorf("[Milvus] Failed to convert result set: %v", err)
			continue
		}
		for _, set := range sets {
			set.Score = 1.0
			allResults = append(allResults, fromMilvusVectorEmbedding(set.ID, set, types.MatchTypeKeywords))
		}
	}

	// Limit results to topK
	if len(allResults) > params.TopK {
		allResults = allResults[:params.TopK]
	}

	if len(allResults) == 0 {
		log.Warnf("[Milvus] No keyword matches found for query: %s", params.Query)
	} else {
		log.Infof("[Milvus] Keywords retrieval found %d results", len(allResults))
	}

	return buildRetrieveResult(allResults, types.KeywordsRetrieverType), nil
}

// CopyIndices copies index data from source knowledge base to target knowledge base
func (m *milvusRepository) CopyIndices(ctx context.Context,
	sourceKnowledgeBaseID string,
	sourceToTargetKBIDMap map[string]string,
	sourceToTargetChunkIDMap map[string]string,
	targetKnowledgeBaseID string,
	dimension int,
	knowledgeType string,
) error {
	log := logger.GetLogger(ctx)
	log.Infof(
		"[Milvus] Copying indices from source knowledge base %s to target knowledge base %s, count: %d, dimension: %d",
		sourceKnowledgeBaseID, targetKnowledgeBaseID, len(sourceToTargetChunkIDMap), dimension,
	)

	if len(sourceToTargetChunkIDMap) == 0 {
		log.Warn("[Milvus] Empty mapping, skipping copy")
		return nil
	}

	collectionName := m.getCollectionName(dimension)
	// Ensure target collection exists
	if err := m.ensureCollection(ctx, dimension); err != nil {
		return err
	}

	batchSize := 64
	totalCopied := 0
	var offset *int
	for {
		sourceEmbeddings, count, err := m.searchByFilter(ctx, collectionName, &universalFilterCondition{
			Field:    fieldKnowledgeBaseID,
			Operator: operatorEqual,
			Value:    sourceKnowledgeBaseID,
		}, &batchSize, offset)
		if err != nil {
			log.Errorf("[Milvus] Failed to query source points: %v", err)
			return err
		}
		if len(sourceEmbeddings) == 0 {
			break
		}
		targetEmbeddings := make([]*MilvusVectorEmbedding, 0, len(sourceEmbeddings))
		for _, sourceEmbedding := range sourceEmbeddings {
			sourceChunkID := sourceEmbedding.ChunkID
			sourceKnowledgeID := sourceEmbedding.KnowledgeID
			originalSourceID := sourceEmbedding.SourceID

			targetChunkID, ok := sourceToTargetChunkIDMap[sourceChunkID]
			if !ok {
				log.Warnf("[Milvus] Source chunk %s not found in target mapping, skipping", sourceChunkID)
				continue
			}
			targetKnowledgeID, ok := sourceToTargetKBIDMap[sourceKnowledgeID]
			if !ok {
				log.Warnf("[Milvus] Source knowledge %s not found in target mapping, skipping", sourceKnowledgeID)
				continue
			}
			var targetSourceID string
			if originalSourceID == sourceChunkID {
				targetSourceID = targetChunkID
			} else if strings.HasPrefix(originalSourceID, sourceChunkID+"-") {
				questionID := strings.TrimPrefix(originalSourceID, sourceChunkID+"-")
				targetSourceID = fmt.Sprintf("%s-%s", targetChunkID, questionID)
			} else {
				targetSourceID = uuid.New().String()
			}
			targetEmbedding := &MilvusVectorEmbedding{
				ID:              uuid.New().String(),
				Content:         sourceEmbedding.Content,
				SourceID:        targetSourceID,
				SourceType:      sourceEmbedding.SourceType,
				ChunkID:         targetChunkID,
				KnowledgeID:     targetKnowledgeID,
				KnowledgeBaseID: targetKnowledgeBaseID,
				TagID:           sourceEmbedding.TagID,
				// Folder IDs are KB-scoped, so copied documents land at target root.
				FolderID:  "",
				Embedding: sourceEmbedding.Embedding,
				IsEnabled: sourceEmbedding.IsEnabled,
			}
			targetEmbeddings = append(targetEmbeddings, targetEmbedding)
		}
		if len(targetEmbeddings) > 0 {
			opts := createUpsert(collectionName, targetEmbeddings)
			_, err := m.client.Upsert(ctx, opts)
			if err != nil {
				log.Errorf("[Milvus] Failed to batch upsert target points: %v", err)
				return err
			}
			totalCopied += len(targetEmbeddings)
			log.Infof("[Milvus] Successfully copied batch, batch size: %d, total copied: %d",
				len(targetEmbeddings), totalCopied)
		}

		if count < batchSize {
			break
		}
		if offset == nil {
			offset = new(int)
		}
		*offset += count
	}

	log.Infof("[Milvus] Index copy completed, total copied: %d", totalCopied)
	return nil
}

func buildRetrieveResult(results []*types.IndexWithScore, retrieverType types.RetrieverType) []*types.RetrieveResult {
	return []*types.RetrieveResult{
		{
			Results:             results,
			RetrieverEngineType: types.MilvusRetrieverEngineType,
			RetrieverType:       retrieverType,
			Error:               nil,
		},
	}
}

func (m *milvusRepository) calculateStorageSize(embedding *MilvusVectorEmbedding) int64 {
	// Payload fields
	payloadSizeBytes := int64(0)
	payloadSizeBytes += int64(len(embedding.Content))         // content string
	payloadSizeBytes += int64(len(embedding.SourceID))        // source_id string
	payloadSizeBytes += int64(len(embedding.ChunkID))         // chunk_id string
	payloadSizeBytes += int64(len(embedding.KnowledgeID))     // knowledge_id string
	payloadSizeBytes += int64(len(embedding.KnowledgeBaseID)) // knowledge_base_id string
	payloadSizeBytes += 8                                     // source_type int64

	// Vector storage and index
	var vectorSizeBytes int64 = 0
	var indexBytes int64 = 0
	if embedding.Embedding != nil {
		dimensions := int64(len(embedding.Embedding))
		vectorSizeBytes = dimensions * 4

		// IVF_FLAT index: vectors are duplicated in inverted lists (grouped by cluster),
		// plus a small per-vector overhead for cluster assignment and list management.
		// The centroid table (nlist × dim × 4) is a shared structure and should NOT
		// be counted per-vector.
		indexBytes = vectorSizeBytes + 16
	}

	// ID tracker and metadata: ~32 bytes per vector
	const metadataBytes int64 = 32

	totalSizeBytes := payloadSizeBytes + vectorSizeBytes + indexBytes + metadataBytes
	return totalSizeBytes
}

// toMilvusVectorEmbedding converts IndexInfo to Milvus format
func toMilvusVectorEmbedding(embedding *types.IndexInfo, additionalParams map[string]interface{}) *MilvusVectorEmbedding {
	vector := &MilvusVectorEmbedding{
		Content:         embedding.Content,
		SourceID:        embedding.SourceID,
		SourceType:      int(embedding.SourceType),
		ChunkID:         embedding.ChunkID,
		KnowledgeID:     embedding.KnowledgeID,
		KnowledgeBaseID: embedding.KnowledgeBaseID,
		TagID:           embedding.TagID,
		FolderID:        embedding.FolderID,
		IsEnabled:       embedding.IsEnabled,
	}
	if additionalParams != nil && slices.Contains(slices.Collect(maps.Keys(additionalParams)), fieldEmbedding) {
		if embeddingMap, ok := additionalParams[fieldEmbedding].(map[string][]float32); ok {
			vector.Embedding = embeddingMap[embedding.SourceID]
		}
	}
	return vector
}

// fromMilvusVectorEmbedding converts Milvus result to IndexWithScore domain model
func fromMilvusVectorEmbedding(id string,
	embedding *MilvusVectorEmbeddingWithScore,
	matchType types.MatchType,
) *types.IndexWithScore {
	return &types.IndexWithScore{
		ID:              id,
		SourceID:        embedding.SourceID,
		SourceType:      types.SourceType(embedding.SourceType),
		ChunkID:         embedding.ChunkID,
		KnowledgeID:     embedding.KnowledgeID,
		KnowledgeBaseID: embedding.KnowledgeBaseID,
		TagID:           embedding.TagID,
		FolderID:        embedding.FolderID,
		Content:         embedding.Content,
		Score:           embedding.Score,
		MatchType:       matchType,
	}
}

func createUpsert(collectionName string, embeddings []*MilvusVectorEmbedding) client.UpsertOption {
	ids := make([]string, 0, len(embeddings))
	embeddingsData := make([][]float32, 0, len(embeddings))
	contents := make([]string, 0, len(embeddings))
	sourceIDs := make([]string, 0, len(embeddings))
	sourceTypes := make([]int64, 0, len(embeddings))
	chunkIDs := make([]string, 0, len(embeddings))
	knowledgeIDs := make([]string, 0, len(embeddings))
	knowledgeBaseIDs := make([]string, 0, len(embeddings))
	tagIDs := make([]string, 0, len(embeddings))
	folderIDs := make([]string, 0, len(embeddings))
	isEnableds := make([]bool, 0, len(embeddings))
	var dimension int
	for _, embedding := range embeddings {
		ids = append(ids, embedding.ID)
		embeddingsData = append(embeddingsData, embedding.Embedding)
		contents = append(contents, embedding.Content)
		sourceIDs = append(sourceIDs, embedding.SourceID)
		sourceTypes = append(sourceTypes, int64(embedding.SourceType))
		chunkIDs = append(chunkIDs, embedding.ChunkID)
		knowledgeIDs = append(knowledgeIDs, embedding.KnowledgeID)
		knowledgeBaseIDs = append(knowledgeBaseIDs, embedding.KnowledgeBaseID)
		tagIDs = append(tagIDs, embedding.TagID)
		folderIDs = append(folderIDs, embedding.FolderID)
		isEnableds = append(isEnableds, embedding.IsEnabled)
		dimension = len(embedding.Embedding)
	}
	opt := client.NewColumnBasedInsertOption(collectionName).
		WithVarcharColumn(fieldID, ids).
		WithFloatVectorColumn(fieldEmbedding, dimension, embeddingsData).
		WithVarcharColumn(fieldContent, contents).
		WithVarcharColumn(fieldSourceID, sourceIDs).
		WithInt64Column(fieldSourceType, sourceTypes).
		WithVarcharColumn(fieldChunkID, chunkIDs).
		WithVarcharColumn(fieldKnowledgeID, knowledgeIDs).
		WithVarcharColumn(fieldKnowledgeBaseID, knowledgeBaseIDs).
		WithVarcharColumn(fieldTagID, tagIDs).
		WithVarcharColumn(fieldFolderID, folderIDs).
		WithBoolColumn(fieldIsEnabled, isEnableds)
	return opt
}

func convertResultSet(resultSet []client.ResultSet) ([]*MilvusVectorEmbeddingWithScore, []float64, error) {
	var results []*MilvusVectorEmbeddingWithScore
	var scores []float64
	if len(resultSet) == 0 {
		return results, scores, nil
	}
	set := resultSet[0]
	resultLen := set.Len()
	if resultLen == 0 {
		return results, scores, nil
	}

	for _, score := range set.Scores {
		scores = append(scores, float64(score))
	}
	docs := make([]*MilvusVectorEmbeddingWithScore, 0, resultLen)
	for i := 0; i < resultLen; i++ {
		docs = append(docs, &MilvusVectorEmbeddingWithScore{})
	}
	for _, field := range allFields {
		columns := set.GetColumn(field)
		if columns == nil || columns.Len() == 0 {
			continue
		}
		if field == fieldID {
			for i := 0; i < columns.Len(); i++ {
				val, err := columns.GetAsString(i)
				if err != nil {
					return nil, nil, err
				}
				docs[i].ID = val
			}
		}
		if field == fieldContent {
			for i := 0; i < columns.Len(); i++ {
				val, err := columns.GetAsString(i)
				if err != nil {
					return nil, nil, err
				}
				docs[i].Content = val
			}
		}
		if field == fieldSourceID {
			for i := 0; i < columns.Len(); i++ {
				val, err := columns.GetAsString(i)
				if err != nil {
					return nil, nil, err
				}
				docs[i].SourceID = val
			}
		}
		if field == fieldSourceType {
			for i := 0; i < columns.Len(); i++ {
				val, err := columns.GetAsInt64(i)
				if err != nil {
					return nil, nil, err
				}
				docs[i].SourceType = int(val)
			}
		}
		if field == fieldChunkID {
			for i := 0; i < columns.Len(); i++ {
				val, err := columns.GetAsString(i)
				if err != nil {
					return nil, nil, err
				}
				docs[i].ChunkID = val
			}
		}
		if field == fieldKnowledgeID {
			for i := 0; i < columns.Len(); i++ {
				val, err := columns.GetAsString(i)
				if err != nil {
					return nil, nil, err
				}
				docs[i].KnowledgeID = val
			}
		}
		if field == fieldKnowledgeBaseID {
			for i := 0; i < columns.Len(); i++ {
				val, err := columns.GetAsString(i)
				if err != nil {
					return nil, nil, err
				}
				docs[i].KnowledgeBaseID = val
			}
		}
		if field == fieldTagID {
			for i := 0; i < columns.Len(); i++ {
				val, err := columns.GetAsString(i)
				if err != nil {
					return nil, nil, err
				}
				docs[i].TagID = val
			}
		}
		if field == fieldFolderID {
			for i := 0; i < columns.Len(); i++ {
				isNull, err := columns.IsNull(i)
				if err != nil {
					return nil, nil, err
				}
				if isNull {
					docs[i].FolderID = ""
					continue
				}
				val, err := columns.GetAsString(i)
				if err != nil {
					return nil, nil, err
				}
				docs[i].FolderID = val
			}
		}
		if field == fieldIsEnabled {
			for i := 0; i < columns.Len(); i++ {
				val, err := columns.GetAsBool(i)
				if err != nil {
					return nil, nil, err
				}
				docs[i].IsEnabled = val
			}
		}
		if field == fieldEmbedding {
			vectorColumn, ok := columns.(*column.ColumnDoubleArray)
			if !ok {
				continue
			}
			for i := 0; i < vectorColumn.Len(); i++ {
				val, err := vectorColumn.Value(i)
				if err != nil {
					return nil, nil, fmt.Errorf("get vector failed: %w", err)
				}
				embedding := make([]float32, len(val))
				for j, v := range val {
					embedding[j] = float32(v)
				}
				docs[i].Embedding = embedding
			}
		}
	}
	return docs, scores, nil
}
