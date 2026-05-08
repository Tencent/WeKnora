package tencentvectordb

import (
	"context"
	"fmt"
	"maps"
	"os"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/tencent/vectordatabase-sdk-go/tcvectordb"
)

// NewTencentVectorDBRetrieveEngineRepository creates a Tencent VectorDB-backed retrieve repository.
func NewTencentVectorDBRetrieveEngineRepository(
	client *tcvectordb.RpcClient,
	databaseName string,
	indexCfg *types.IndexConfig,
) interfaces.RetrieveEngineRepository {
	if databaseName == "" {
		databaseName = os.Getenv(envTencentVectorDBDatabase)
	}
	if databaseName == "" {
		databaseName = defaultDatabaseName
	}

	collectionBaseName := types.ResolveCollectionName(indexCfg, envTencentVectorDBCollection, defaultCollectionName)
	return &repository{
		client:             client,
		databaseName:       databaseName,
		collectionBaseName: collectionBaseName,
		shardsNum:          defaultIfZero(indexCfg.GetShardsNum(1), 1),
		replicasNum:        defaultIfZero(indexCfg.GetReplicaNumber(1), 1),
	}
}

func (r *repository) EngineType() types.RetrieverEngineType {
	return types.TencentVectorDBRetrieverEngineType
}

func (r *repository) Support() []types.RetrieverType {
	return []types.RetrieverType{types.VectorRetrieverType}
}

func (r *repository) Save(ctx context.Context, indexInfo *types.IndexInfo, params map[string]any) error {
	return r.BatchSave(ctx, []*types.IndexInfo{indexInfo}, params)
}

func (r *repository) BatchSave(ctx context.Context, indexInfoList []*types.IndexInfo, params map[string]any) error {
	log := logger.GetLogger(ctx)
	if len(indexInfoList) == 0 {
		return nil
	}

	docsByDimension := make(map[int][]tcvectordb.Document)
	for _, indexInfo := range indexInfoList {
		embedding := toVectorEmbedding(indexInfo, params)
		if len(embedding.Embedding) == 0 {
			log.Warnf("[TencentVectorDB] skip empty embedding for chunk_id=%s", indexInfo.ChunkID)
			continue
		}
		dim := len(embedding.Embedding)
		docsByDimension[dim] = append(docsByDimension[dim], toDocument(embedding))
	}
	if len(docsByDimension) == 0 {
		return nil
	}

	buildIndex := true
	for dim, docs := range docsByDimension {
		if err := r.ensureCollection(ctx, dim); err != nil {
			return err
		}
		collectionName := r.collectionName(dim)
		_, err := r.client.Database(r.databaseName).Collection(collectionName).Upsert(
			ctx,
			docs,
			&tcvectordb.UpsertDocumentParams{BuildIndex: &buildIndex},
		)
		if err != nil {
			return fmt.Errorf("tencent vectordb batch save %s: %w", collectionName, err)
		}
	}
	return nil
}

func (r *repository) EstimateStorageSize(ctx context.Context, indexInfoList []*types.IndexInfo, params map[string]any) int64 {
	var total int64
	for _, indexInfo := range indexInfoList {
		embedding := toVectorEmbedding(indexInfo, params)
		total += int64(len(embedding.Content))
		total += int64(len(embedding.Embedding) * 4)
		total += int64(len(embedding.SourceID) + len(embedding.ChunkID) + len(embedding.KnowledgeID) + len(embedding.KnowledgeBaseID) + 256)
	}
	logger.GetLogger(ctx).Infof("[TencentVectorDB] estimated storage size for %d indices: %d bytes", len(indexInfoList), total)
	return total
}

func (r *repository) DeleteByChunkIDList(ctx context.Context, chunkIDList []string, dimension int, knowledgeType string) error {
	return r.deleteByFilter(ctx, dimension, tcvectordb.In(fieldChunkID, chunkIDList))
}

func (r *repository) DeleteBySourceIDList(ctx context.Context, sourceIDList []string, dimension int, knowledgeType string) error {
	return r.deleteByFilter(ctx, dimension, tcvectordb.In(fieldSourceID, sourceIDList))
}

func (r *repository) DeleteByKnowledgeIDList(ctx context.Context, knowledgeIDList []string, dimension int, knowledgeType string) error {
	return r.deleteByFilter(ctx, dimension, tcvectordb.In(fieldKnowledgeID, knowledgeIDList))
}

func (r *repository) CopyIndices(
	ctx context.Context,
	sourceKnowledgeBaseID string,
	sourceToTargetKBIDMap map[string]string,
	sourceToTargetChunkIDMap map[string]string,
	targetKnowledgeBaseID string,
	dimension int,
	knowledgeType string,
) error {
	if len(sourceToTargetChunkIDMap) == 0 {
		return nil
	}
	collectionName := r.collectionName(dimension)
	ids := slices.Collect(maps.Keys(sourceToTargetChunkIDMap))
	query, err := r.client.Database(r.databaseName).Collection(collectionName).Query(
		ctx,
		nil,
		&tcvectordb.QueryDocumentParams{
			Filter:         tcvectordb.NewFilter(tcvectordb.In(fieldChunkID, ids)),
			RetrieveVector: true,
			OutputFields:   outputFields(),
			Limit:          int64(len(ids)),
		},
	)
	if err != nil {
		return fmt.Errorf("tencent vectordb query source indices: %w", err)
	}

	docs := make([]tcvectordb.Document, 0, len(query.Documents))
	for _, doc := range query.Documents {
		embedding := fromDocument(doc)
		targetChunkID := sourceToTargetChunkIDMap[embedding.ChunkID]
		if targetChunkID == "" {
			continue
		}
		embedding.ID = targetChunkID
		embedding.ChunkID = targetChunkID
		embedding.KnowledgeBaseID = targetKnowledgeBaseID
		if targetKBID := sourceToTargetKBIDMap[embedding.KnowledgeID]; targetKBID != "" {
			embedding.KnowledgeID = targetKBID
		}
		docs = append(docs, toDocument(embedding))
	}
	if len(docs) == 0 {
		return nil
	}

	buildIndex := true
	_, err = r.client.Database(r.databaseName).Collection(collectionName).Upsert(
		ctx,
		docs,
		&tcvectordb.UpsertDocumentParams{BuildIndex: &buildIndex},
	)
	if err != nil {
		return fmt.Errorf("tencent vectordb copy indices: %w", err)
	}
	return nil
}

func (r *repository) BatchUpdateChunkEnabledStatus(ctx context.Context, chunkStatusMap map[string]bool) error {
	if len(chunkStatusMap) == 0 {
		return nil
	}
	grouped := make(map[bool][]string)
	for chunkID, enabled := range chunkStatusMap {
		grouped[enabled] = append(grouped[enabled], chunkID)
	}
	for enabled, chunkIDs := range grouped {
		if err := r.updateChunkFields(ctx, chunkIDs, map[string]tcvectordb.Field{fieldIsEnabled: {Val: boolToUint64(enabled)}}); err != nil {
			return err
		}
	}
	return nil
}

func (r *repository) BatchUpdateChunkTagID(ctx context.Context, chunkTagMap map[string]string) error {
	if len(chunkTagMap) == 0 {
		return nil
	}
	grouped := make(map[string][]string)
	for chunkID, tagID := range chunkTagMap {
		grouped[tagID] = append(grouped[tagID], chunkID)
	}
	for tagID, chunkIDs := range grouped {
		if err := r.updateChunkFields(ctx, chunkIDs, map[string]tcvectordb.Field{fieldTagID: {Val: tagID}}); err != nil {
			return err
		}
	}
	return nil
}

func (r *repository) Retrieve(ctx context.Context, params types.RetrieveParams) ([]*types.RetrieveResult, error) {
	switch params.RetrieverType {
	case types.VectorRetrieverType:
		return r.VectorRetrieve(ctx, params)
	case types.KeywordsRetrieverType:
		return nil, fmt.Errorf("tencent vectordb keyword retrieval is not supported")
	default:
		return nil, fmt.Errorf("invalid retriever type: %s", params.RetrieverType)
	}
}

func (r *repository) VectorRetrieve(ctx context.Context, params types.RetrieveParams) ([]*types.RetrieveResult, error) {
	dimension := len(params.Embedding)
	if dimension == 0 {
		return r.retrieveResult(nil, types.VectorRetrieverType), nil
	}

	collectionName := r.collectionName(dimension)
	exists, err := r.client.Database(r.databaseName).ExistsCollection(ctx, collectionName)
	if err != nil {
		return nil, fmt.Errorf("tencent vectordb check collection %s: %w", collectionName, err)
	}
	if !exists {
		return r.retrieveResult(nil, types.VectorRetrieverType), nil
	}

	limit := int64(params.TopK)
	if limit <= 0 {
		limit = 10
	}
	searchParams := &tcvectordb.SearchDocumentParams{
		Filter:         r.baseFilter(params),
		Params:         &tcvectordb.SearchDocParams{Ef: 100},
		RetrieveVector: false,
		OutputFields:   outputFields(),
		Limit:          limit,
	}
	if params.Threshold > 0 {
		radius := float32(params.Threshold)
		searchParams.Radius = &radius
	}

	search, err := r.client.Database(r.databaseName).Collection(collectionName).Search(ctx, [][]float32{params.Embedding}, searchParams)
	if err != nil {
		return nil, fmt.Errorf("tencent vectordb vector search %s: %w", collectionName, err)
	}
	if len(search.Documents) == 0 {
		return r.retrieveResult(nil, types.VectorRetrieverType), nil
	}

	results := make([]*types.IndexWithScore, 0, len(search.Documents[0]))
	for _, doc := range search.Documents[0] {
		embedding := fromDocument(doc)
		results = append(results, toIndexWithScore(embedding, types.MatchTypeEmbedding))
	}
	return r.retrieveResult(results, types.VectorRetrieverType), nil
}

func (r *repository) ensureCollection(ctx context.Context, dimension int) error {
	if _, ok := r.initialized.Load(dimension); ok {
		return nil
	}

	if _, err := r.client.CreateDatabaseIfNotExists(ctx, r.databaseName); err != nil {
		return fmt.Errorf("tencent vectordb ensure database %s: %w", r.databaseName, err)
	}

	collectionName := r.collectionName(dimension)
	exists, err := r.client.Database(r.databaseName).ExistsCollection(ctx, collectionName)
	if err != nil {
		return fmt.Errorf("tencent vectordb check collection %s: %w", collectionName, err)
	}
	if exists {
		r.initialized.Store(dimension, true)
		return nil
	}

	indexes := tcvectordb.Indexes{
		VectorIndex: []tcvectordb.VectorIndex{
			{
				FilterIndex: tcvectordb.FilterIndex{
					FieldName: fieldVector,
					FieldType: tcvectordb.Vector,
					IndexType: tcvectordb.HNSW,
				},
				Dimension:  uint32(dimension),
				MetricType: tcvectordb.COSINE,
				Params: &tcvectordb.HNSWParam{
					M:              16,
					EfConstruction: 200,
				},
			},
		},
		FilterIndex: []tcvectordb.FilterIndex{
			{FieldName: fieldID, FieldType: tcvectordb.String, IndexType: tcvectordb.PRIMARY},
			{FieldName: fieldContent, FieldType: tcvectordb.String, IndexType: tcvectordb.FILTER},
			{FieldName: fieldSourceID, FieldType: tcvectordb.String, IndexType: tcvectordb.FILTER},
			{FieldName: fieldSourceType, FieldType: tcvectordb.Uint64, IndexType: tcvectordb.FILTER},
			{FieldName: fieldChunkID, FieldType: tcvectordb.String, IndexType: tcvectordb.FILTER},
			{FieldName: fieldKnowledgeID, FieldType: tcvectordb.String, IndexType: tcvectordb.FILTER},
			{FieldName: fieldKnowledgeBaseID, FieldType: tcvectordb.String, IndexType: tcvectordb.FILTER},
			{FieldName: fieldTagID, FieldType: tcvectordb.String, IndexType: tcvectordb.FILTER},
			{FieldName: fieldIsEnabled, FieldType: tcvectordb.Uint64, IndexType: tcvectordb.FILTER},
		},
	}
	_, err = r.client.Database(r.databaseName).CreateCollection(
		ctx,
		collectionName,
		uint32(r.shardsNum),
		uint32(r.replicasNum),
		fmt.Sprintf("WeKnora embeddings collection with dimension %d", dimension),
		indexes,
	)
	if err != nil {
		return fmt.Errorf("tencent vectordb create collection %s: %w", collectionName, err)
	}

	r.initialized.Store(dimension, true)
	return nil
}

func (r *repository) deleteByFilter(ctx context.Context, dimension int, cond string) error {
	if cond == "" {
		return nil
	}
	collectionName := r.collectionName(dimension)
	_, err := r.client.Database(r.databaseName).Collection(collectionName).Delete(ctx, tcvectordb.DeleteDocumentParams{
		Filter: tcvectordb.NewFilter(cond),
	})
	if err != nil {
		return fmt.Errorf("tencent vectordb delete from %s: %w", collectionName, err)
	}
	return nil
}

func (r *repository) updateChunkFields(ctx context.Context, chunkIDs []string, fields map[string]tcvectordb.Field) error {
	collections, err := r.client.Database(r.databaseName).ListCollection(ctx)
	if err != nil {
		return fmt.Errorf("tencent vectordb list collections: %w", err)
	}

	for _, collection := range collections.Collections {
		if !strings.HasPrefix(collection.CollectionName, r.collectionBaseName+"_") {
			continue
		}
		_, err := r.client.Database(r.databaseName).Collection(collection.CollectionName).Update(ctx, tcvectordb.UpdateDocumentParams{
			QueryFilter:  tcvectordb.NewFilter(tcvectordb.In(fieldChunkID, chunkIDs)),
			UpdateFields: fields,
		})
		if err != nil {
			return fmt.Errorf("tencent vectordb update chunks in %s: %w", collection.CollectionName, err)
		}
	}
	return nil
}

func (r *repository) collectionName(dimension int) string {
	return fmt.Sprintf("%s_%d", r.collectionBaseName, dimension)
}

func (r *repository) baseFilter(params types.RetrieveParams) *tcvectordb.Filter {
	conditions := []string{fmt.Sprintf("%s=1", fieldIsEnabled)}
	if len(params.KnowledgeBaseIDs) > 0 {
		conditions = append(conditions, tcvectordb.In(fieldKnowledgeBaseID, params.KnowledgeBaseIDs))
	}
	if len(params.KnowledgeIDs) > 0 {
		conditions = append(conditions, tcvectordb.In(fieldKnowledgeID, params.KnowledgeIDs))
	}
	if len(params.TagIDs) > 0 {
		conditions = append(conditions, tcvectordb.In(fieldTagID, params.TagIDs))
	}
	if len(params.ExcludeKnowledgeIDs) > 0 {
		conditions = append(conditions, tcvectordb.NotIn(fieldKnowledgeID, params.ExcludeKnowledgeIDs))
	}
	if len(params.ExcludeChunkIDs) > 0 {
		conditions = append(conditions, tcvectordb.NotIn(fieldChunkID, params.ExcludeChunkIDs))
	}
	return tcvectordb.NewFilter(strings.Join(conditions, " and "))
}

func (r *repository) retrieveResult(results []*types.IndexWithScore, retrieverType types.RetrieverType) []*types.RetrieveResult {
	return []*types.RetrieveResult{
		{
			Results:             results,
			RetrieverEngineType: types.TencentVectorDBRetrieverEngineType,
			RetrieverType:       retrieverType,
		},
	}
}

func toVectorEmbedding(indexInfo *types.IndexInfo, params map[string]any) *vectorEmbedding {
	embedding := &vectorEmbedding{
		ID:              indexInfo.ChunkID,
		Content:         cleanInvalidUTF8(indexInfo.Content),
		SourceID:        indexInfo.SourceID,
		SourceType:      int(indexInfo.SourceType),
		ChunkID:         indexInfo.ChunkID,
		KnowledgeID:     indexInfo.KnowledgeID,
		KnowledgeBaseID: indexInfo.KnowledgeBaseID,
		TagID:           indexInfo.TagID,
		IsEnabled:       indexInfo.IsEnabled,
	}
	if embedding.ID == "" {
		embedding.ID = indexInfo.SourceID
	}
	if params != nil && slices.Contains(slices.Collect(maps.Keys(params)), fieldVector) {
		if embeddingMap, ok := params[fieldVector].(map[string][]float32); ok {
			embedding.Embedding = lookupEmbedding(embeddingMap, indexInfo)
		}
	}
	if params != nil && slices.Contains(slices.Collect(maps.Keys(params)), "embedding") {
		if embeddingMap, ok := params["embedding"].(map[string][]float32); ok {
			embedding.Embedding = lookupEmbedding(embeddingMap, indexInfo)
		}
	}
	return embedding
}

func lookupEmbedding(embeddingMap map[string][]float32, indexInfo *types.IndexInfo) []float32 {
	if embedding, ok := embeddingMap[indexInfo.SourceID]; ok {
		return embedding
	}
	return embeddingMap[indexInfo.ChunkID]
}

func cleanInvalidUTF8(s string) string {
	var b strings.Builder
	b.Grow(len(s))

	for i := 0; i < len(s); {
		r, size := utf8.DecodeRuneInString(s[i:])
		if r == utf8.RuneError && size == 1 {
			i++
			continue
		}
		if r == 0 {
			i += size
			continue
		}
		b.WriteRune(r)
		i += size
	}
	return b.String()
}

func toDocument(embedding *vectorEmbedding) tcvectordb.Document {
	return tcvectordb.Document{
		Id:     embedding.ID,
		Vector: embedding.Embedding,
		Fields: map[string]tcvectordb.Field{
			fieldContent:         {Val: embedding.Content},
			fieldSourceID:        {Val: embedding.SourceID},
			fieldSourceType:      {Val: uint64(embedding.SourceType)},
			fieldChunkID:         {Val: embedding.ChunkID},
			fieldKnowledgeID:     {Val: embedding.KnowledgeID},
			fieldKnowledgeBaseID: {Val: embedding.KnowledgeBaseID},
			fieldTagID:           {Val: embedding.TagID},
			fieldIsEnabled:       {Val: boolToUint64(embedding.IsEnabled)},
		},
	}
}

func fromDocument(doc tcvectordb.Document) *vectorEmbedding {
	return &vectorEmbedding{
		ID:              doc.Id,
		Content:         fieldString(doc, fieldContent),
		SourceID:        fieldString(doc, fieldSourceID),
		SourceType:      int(fieldUint64(doc, fieldSourceType)),
		ChunkID:         fieldString(doc, fieldChunkID),
		KnowledgeID:     fieldString(doc, fieldKnowledgeID),
		KnowledgeBaseID: fieldString(doc, fieldKnowledgeBaseID),
		TagID:           fieldString(doc, fieldTagID),
		Embedding:       doc.Vector,
		IsEnabled:       fieldUint64(doc, fieldIsEnabled) == 1,
		Score:           float64(doc.Score),
	}
}

func toIndexWithScore(embedding *vectorEmbedding, matchType types.MatchType) *types.IndexWithScore {
	return &types.IndexWithScore{
		ID:              embedding.ID,
		Content:         embedding.Content,
		SourceID:        embedding.SourceID,
		SourceType:      types.SourceType(embedding.SourceType),
		ChunkID:         embedding.ChunkID,
		KnowledgeID:     embedding.KnowledgeID,
		KnowledgeBaseID: embedding.KnowledgeBaseID,
		TagID:           embedding.TagID,
		Score:           embedding.Score,
		MatchType:       matchType,
		IsEnabled:       embedding.IsEnabled,
	}
}

func outputFields() []string {
	return []string{
		fieldID,
		fieldContent,
		fieldSourceID,
		fieldSourceType,
		fieldChunkID,
		fieldKnowledgeID,
		fieldKnowledgeBaseID,
		fieldTagID,
		fieldIsEnabled,
	}
}

func fieldString(doc tcvectordb.Document, name string) string {
	field, ok := doc.Fields[name]
	if !ok {
		return ""
	}
	return field.String()
}

func fieldUint64(doc tcvectordb.Document, name string) uint64 {
	field, ok := doc.Fields[name]
	if !ok {
		return 0
	}
	return field.Uint64()
}

func boolToUint64(v bool) uint64 {
	if v {
		return 1
	}
	return 0
}

func defaultIfZero(v, def int) int {
	if v <= 0 {
		return def
	}
	return v
}
