package milvus

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"

	"github.com/milvus-io/milvus/client/v2/column"
	"github.com/milvus-io/milvus/client/v2/entity"
	"github.com/milvus-io/milvus/client/v2/index"
	client "github.com/milvus-io/milvus/client/v2/milvusclient"

	"github.com/Tencent/WeKnora/internal/logger"
)

const defaultMultilingualMigrationBatchSize = 64

// migrationOutputFields deliberately excludes the generated BM25 sparse
// vector. The migration only needs the source content, dense vector, and
// metadata required to rebuild the target row. Querying "*" can exceed
// Milvus's per-query result-size limit when a batch contains long chunks.
var migrationOutputFields = []string{
	fieldID,
	fieldContent,
	fieldSourceID,
	fieldSourceType,
	fieldChunkID,
	fieldKnowledgeID,
	fieldKnowledgeBaseID,
	fieldTagID,
	fieldIsEnabled,
	fieldEmbedding,
}

// MultilingualMigrationOptions describes a safe, copy-only migration from
// legacy Milvus collections to collections with multilingual BM25 analyzers.
// The source collections are never modified or deleted.
type MultilingualMigrationOptions struct {
	SourceCollectionBaseName string
	TargetCollectionBaseName string
	MetricType               entity.MetricType
	ShardsNum                int
	ReplicaNumber            int
	BatchSize                int
}

// MigrationSummary reports the result of a copy-only migration.
type MigrationSummary struct {
	ExaminedCollections int
	MigratedCollections int
	MigratedRows        int
}

// MigrateLegacyCollections copies all legacy WeKnora Milvus collections to a
// new multilingual collection base. Existing dense vectors are reused; only
// Milvus's BM25 sparse vectors are regenerated from content and language.
//
// The target collection base must differ from the source base. The operation
// is resumable because rows keep their original primary keys and are upserted.
func MigrateLegacyCollections(
	ctx context.Context,
	milvusClient *client.Client,
	options MultilingualMigrationOptions,
) (MigrationSummary, error) {
	var summary MigrationSummary
	if milvusClient == nil {
		return summary, fmt.Errorf("milvus client is nil")
	}

	sourceBase := strings.TrimSpace(options.SourceCollectionBaseName)
	targetBase := strings.TrimSpace(options.TargetCollectionBaseName)
	if sourceBase == "" || targetBase == "" {
		return summary, fmt.Errorf("source and target collection base names are required")
	}
	if sourceBase == targetBase {
		return summary, fmt.Errorf("source and target collection base names must differ")
	}
	if options.BatchSize <= 0 {
		options.BatchSize = defaultMultilingualMigrationBatchSize
	}

	log := logger.GetLogger(ctx)
	collections, err := milvusClient.ListCollections(ctx, client.NewListCollectionOption())
	if err != nil {
		return summary, fmt.Errorf("list Milvus collections: %w", err)
	}
	sort.Strings(collections)

	targetRepo := &milvusRepository{
		client:             milvusClient,
		collectionBaseName: targetBase,
		shardsNum:          options.ShardsNum,
		replicaNumber:      options.ReplicaNumber,
	}

	for _, sourceCollectionName := range collections {
		dimension, ok := collectionDimensionForBase(sourceCollectionName, sourceBase)
		if !ok {
			continue
		}
		summary.ExaminedCollections++

		sourceCollection, err := milvusClient.DescribeCollection(
			ctx,
			client.NewDescribeCollectionOption(sourceCollectionName),
		)
		if err != nil {
			return summary, fmt.Errorf("describe source collection %s: %w", sourceCollectionName, err)
		}
		if analyzerModeFromSchema(sourceCollection.Schema) == collectionAnalyzerMulti {
			log.Infof(
				"[Milvus] Source collection %s already uses multilingual analyzers, skipping",
				sourceCollectionName,
			)
			continue
		}
		schemaDimension, err := dimensionFromSchema(sourceCollection.Schema)
		if err != nil {
			return summary, fmt.Errorf("read dimension from source collection %s: %w", sourceCollectionName, err)
		}
		if schemaDimension != dimension {
			return summary, fmt.Errorf(
				"source collection %s name dimension %d does not match schema dimension %d",
				sourceCollectionName,
				dimension,
				schemaDimension,
			)
		}

		targetCollectionName := fmt.Sprintf("%s_%d", targetBase, dimension)
		sourceMetric, err := embeddingMetricFromCollection(ctx, milvusClient, sourceCollectionName)
		if err != nil {
			return summary, fmt.Errorf("read metric from source collection %s: %w", sourceCollectionName, err)
		}
		metricType, err := resolveMigrationMetric(options.MetricType, sourceMetric)
		if err != nil {
			return summary, fmt.Errorf("collection %s: %w", sourceCollectionName, err)
		}
		hasTarget, err := milvusClient.HasCollection(ctx, client.NewHasCollectionOption(targetCollectionName))
		if err != nil {
			return summary, fmt.Errorf("check target collection %s: %w", targetCollectionName, err)
		}
		if hasTarget {
			targetMetric, err := embeddingMetricFromCollection(ctx, milvusClient, targetCollectionName)
			if err != nil {
				return summary, fmt.Errorf("read metric from target collection %s: %w", targetCollectionName, err)
			}
			if targetMetric != metricType {
				return summary, fmt.Errorf(
					"target collection %s uses metric %s, source collection %s uses %s; choose a new target base",
					targetCollectionName,
					targetMetric,
					sourceCollectionName,
					metricType,
				)
			}
		}
		targetRepo.metricType = metricType
		if err := targetRepo.ensureCollection(ctx, dimension); err != nil {
			return summary, fmt.Errorf("prepare target collection %s: %w", targetCollectionName, err)
		}
		mode, err := targetRepo.collectionAnalyzerMode(ctx, targetCollectionName)
		if err != nil {
			return summary, fmt.Errorf("inspect target collection %s: %w", targetCollectionName, err)
		}
		if mode != collectionAnalyzerMulti {
			return summary, fmt.Errorf(
				"target collection %s is not multilingual; choose a new target base",
				targetCollectionName,
			)
		}

		if err := loadMilvusCollection(ctx, milvusClient, sourceCollectionName); err != nil {
			return summary, fmt.Errorf("load source collection %s: %w", sourceCollectionName, err)
		}
		copied, err := migrateCollectionRows(
			ctx,
			milvusClient,
			sourceCollectionName,
			targetCollectionName,
			dimension,
			options.BatchSize,
		)
		summary.MigratedRows += copied
		if err != nil {
			return summary, fmt.Errorf("migrate collection %s: %w", sourceCollectionName, err)
		}
		if err := flushMilvusCollection(ctx, milvusClient, targetCollectionName); err != nil {
			return summary, fmt.Errorf("flush target collection %s: %w", targetCollectionName, err)
		}

		summary.MigratedCollections++
		log.Infof(
			"[Milvus] Migrated %d rows from %s to %s",
			copied,
			sourceCollectionName,
			targetCollectionName,
		)
	}

	return summary, nil
}

func collectionDimensionForBase(collectionName, baseName string) (int, bool) {
	prefix := strings.TrimSpace(baseName) + "_"
	if !strings.HasPrefix(collectionName, prefix) {
		return 0, false
	}
	dimension, err := strconv.Atoi(strings.TrimPrefix(collectionName, prefix))
	if err != nil || dimension <= 0 {
		return 0, false
	}
	return dimension, true
}

func matchesDimensionCollection(collectionName, baseName string) bool {
	_, ok := collectionDimensionForBase(collectionName, baseName)
	return ok
}

// ParseMetricType accepts IP, COSINE, or L2. An empty value means the caller
// wants the source collection's metric.
func ParseMetricType(value string) (entity.MetricType, error) {
	switch strings.ToUpper(strings.TrimSpace(value)) {
	case "":
		return "", nil
	case "IP":
		return entity.IP, nil
	case "COSINE":
		return entity.COSINE, nil
	case "L2":
		return entity.L2, nil
	default:
		return "", fmt.Errorf("unsupported metric-type %q, must be IP, COSINE, or L2", value)
	}
}

func resolveMigrationMetric(requested, source entity.MetricType) (entity.MetricType, error) {
	if source == "" {
		return "", fmt.Errorf("source collection metric is unknown")
	}
	if requested == "" || requested == source {
		return source, nil
	}
	return "", fmt.Errorf(
		"requested metric %s does not match source collection metric %s",
		requested,
		source,
	)
}

func metricTypeFromIndexParams(params map[string]string) (entity.MetricType, error) {
	raw := strings.TrimSpace(params[index.MetricTypeKey])
	if raw == "" {
		if nested := strings.TrimSpace(params[index.ParamsKey]); nested != "" {
			var extra map[string]any
			if err := json.Unmarshal([]byte(nested), &extra); err == nil {
				if value, ok := extra[index.MetricTypeKey]; ok {
					raw = fmt.Sprint(value)
				}
			}
		}
	}
	if raw == "" {
		return "", fmt.Errorf("dense vector index is missing metric_type")
	}
	return ParseMetricType(raw)
}

func embeddingMetricFromCollection(
	ctx context.Context,
	milvusClient *client.Client,
	collectionName string,
) (entity.MetricType, error) {
	indexNames, err := milvusClient.ListIndexes(
		ctx,
		client.NewListIndexOption(collectionName).WithFieldName(fieldEmbedding),
	)
	if err != nil {
		return "", fmt.Errorf("list indexes: %w", err)
	}
	if len(indexNames) == 0 {
		return "", fmt.Errorf("no dense vector index found")
	}
	description, err := milvusClient.DescribeIndex(
		ctx,
		client.NewDescribeIndexOption(collectionName, indexNames[0]),
	)
	if err != nil {
		return "", fmt.Errorf("describe index %s: %w", indexNames[0], err)
	}
	return metricTypeFromIndexParams(description.Params())
}

func primaryKeyForMigratedRow(
	document *MilvusVectorEmbeddingWithScore,
	ids column.Column,
	rowIndex int,
) (string, error) {
	if document != nil && strings.TrimSpace(document.ID) != "" {
		return document.ID, nil
	}
	if ids != nil {
		id, err := ids.GetAsString(rowIndex)
		if err != nil {
			return "", fmt.Errorf("read primary key at row %d: %w", rowIndex, err)
		}
		if strings.TrimSpace(id) != "" {
			return id, nil
		}
	}
	return "", fmt.Errorf("missing primary key at row %d", rowIndex)
}

func dimensionFromSchema(schema *entity.Schema) (int, error) {
	if schema == nil {
		return 0, fmt.Errorf("collection schema is nil")
	}
	for _, field := range schema.Fields {
		if field == nil || field.Name != fieldEmbedding {
			continue
		}
		rawDimension, ok := field.TypeParams[entity.TypeParamDim]
		if !ok {
			return 0, fmt.Errorf("embedding field has no dimension")
		}
		dimension, err := strconv.Atoi(rawDimension)
		if err != nil || dimension <= 0 {
			return 0, fmt.Errorf("invalid embedding dimension %q", rawDimension)
		}
		return dimension, nil
	}
	return 0, fmt.Errorf("embedding field %q was not found", fieldEmbedding)
}

func loadMilvusCollection(ctx context.Context, milvusClient *client.Client, collectionName string) error {
	loadTask, err := milvusClient.LoadCollection(ctx, client.NewLoadCollectionOption(collectionName))
	if err != nil {
		return err
	}
	return loadTask.Await(ctx)
}

func flushMilvusCollection(ctx context.Context, milvusClient *client.Client, collectionName string) error {
	flushTask, err := milvusClient.Flush(ctx, client.NewFlushOption(collectionName))
	if err != nil {
		return err
	}
	return flushTask.Await(ctx)
}

func migrateCollectionRows(
	ctx context.Context,
	milvusClient *client.Client,
	sourceCollectionName string,
	targetCollectionName string,
	dimension int,
	batchSize int,
) (int, error) {
	log := logger.GetLogger(ctx)
	totalCopied := 0
	batchNumber := 0
	queryIterator, err := milvusClient.QueryIterator(
		ctx,
		client.NewQueryIteratorOption(sourceCollectionName).
			WithOutputFields(migrationOutputFields...).
			WithBatchSize(batchSize),
	)
	if err != nil {
		return totalCopied, fmt.Errorf(
			"create query iterator for source collection %s: %w",
			sourceCollectionName,
			err,
		)
	}

	for {
		resultSet, err := queryIterator.Next(ctx)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return totalCopied, fmt.Errorf(
				"query source collection %s with iterator: %w",
				sourceCollectionName,
				err,
			)
		}
		if resultSet.ResultCount == 0 {
			break
		}
		batchNumber++

		documents, _, err := convertResultSet([]client.ResultSet{resultSet})
		if err != nil {
			return totalCopied, fmt.Errorf(
				"decode source collection %s at iterator batch %d: %w",
				sourceCollectionName,
				batchNumber,
				err,
			)
		}
		if len(documents) == 0 {
			return totalCopied, fmt.Errorf(
				"source collection %s returned rows without decodable fields",
				sourceCollectionName,
			)
		}

		embeddings := make([]*MilvusVectorEmbedding, 0, len(documents))
		for rowIndex, document := range documents {
			if len(document.Embedding) != dimension {
				return totalCopied, fmt.Errorf(
					"source collection %s row %d has vector dimension %d, expected %d",
					sourceCollectionName,
					rowIndex,
					len(document.Embedding),
					dimension,
				)
			}
			document.ID, err = primaryKeyForMigratedRow(document, resultSet.IDs, rowIndex)
			if err != nil {
				return totalCopied, fmt.Errorf(
					"source collection %s at iterator batch %d: %w",
					sourceCollectionName,
					batchNumber,
					err,
				)
			}
			document.Language = detectAnalyzerName(document.Content)
			embeddings = append(embeddings, &document.MilvusVectorEmbedding)
		}

		if _, err := milvusClient.Upsert(ctx, createUpsert(targetCollectionName, embeddings, true)); err != nil {
			return totalCopied, fmt.Errorf(
				"upsert target collection %s at iterator batch %d: %w",
				targetCollectionName,
				batchNumber,
				err,
			)
		}
		totalCopied += len(embeddings)
		log.Infof("[Milvus] Copied %d rows from %s", totalCopied, sourceCollectionName)
	}

	return totalCopied, nil
}
