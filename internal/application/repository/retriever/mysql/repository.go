package mysql

import (
	"context"
	"fmt"
	"math"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/Tencent/WeKnora/internal/common"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type mysqlRepository struct {
	db                     *gorm.DB
	database               string
	tablePrefix            string
	initializedTables      sync.Map
	vectorTypeProbeMu      sync.RWMutex
	vectorTypeProbeDone    bool
	nativeVectorSupported  bool
	vectorProbeMu          sync.RWMutex
	vectorProbeDone        bool
	vectorDistance         *nativeVectorDistance
	vectorDistanceDisabled bool
}

type mysqlEmbedding struct {
	ID              string    `gorm:"column:id;primaryKey"`
	CreatedAt       time.Time `gorm:"column:created_at"`
	UpdatedAt       time.Time `gorm:"column:updated_at"`
	SourceID        string    `gorm:"column:source_id"`
	SourceType      int       `gorm:"column:source_type"`
	ChunkID         string    `gorm:"column:chunk_id"`
	KnowledgeID     string    `gorm:"column:knowledge_id"`
	KnowledgeBaseID string    `gorm:"column:knowledge_base_id"`
	TagID           string    `gorm:"column:tag_id"`
	Content         string    `gorm:"column:content"`
	Dimension       int       `gorm:"column:dimension"`
	Embedding       string    `gorm:"column:embedding"`
	IsEnabled       bool      `gorm:"column:is_enabled"`
}

type keywordRow struct {
	mysqlEmbedding
	Score float64 `gorm:"column:score"`
}

func NewMySQLRetrieveEngineRepository(db *gorm.DB, database string, indexCfg *types.IndexConfig) interfaces.RetrieveEngineRepository {
	prefix := types.ResolveCollectionName(indexCfg, envTablePrefix, defaultTablePrefix)
	repo := &mysqlRepository{
		db:          db,
		database:    database,
		tablePrefix: prefix,
	}
	logger.GetLogger(context.Background()).Infof("[MySQL] Repository initialized: db=%s, table_prefix=%s",
		database, prefix)
	return repo
}

func (r *mysqlRepository) EngineType() types.RetrieverEngineType {
	return types.MySQLRetrieverEngineType
}

func (r *mysqlRepository) Support() []types.RetrieverType {
	return []types.RetrieverType{types.KeywordsRetrieverType, types.VectorRetrieverType}
}

func (r *mysqlRepository) Save(ctx context.Context, indexInfo *types.IndexInfo, params map[string]any) error {
	return r.BatchSave(ctx, []*types.IndexInfo{indexInfo}, params)
}

func (r *mysqlRepository) BatchSave(ctx context.Context, indexInfoList []*types.IndexInfo, params map[string]any) error {
	if len(indexInfoList) == 0 {
		return nil
	}

	groups := make(map[int][]*mysqlEmbedding)
	for _, info := range indexInfoList {
		if info == nil {
			continue
		}
		emb := extractEmbedding(params, info.SourceID)
		if len(emb) == 0 {
			continue
		}
		embeddingJSON, err := vectorToJSON(emb)
		if err != nil {
			return fmt.Errorf("invalid embedding for chunk %s: %w", info.ChunkID, err)
		}
		row := toMySQLEmbedding(info, len(emb), embeddingJSON)
		groups[row.Dimension] = append(groups[row.Dimension], row)
	}

	for dim, rows := range groups {
		if err := r.ensureTable(ctx, dim); err != nil {
			return err
		}
		table := tableName(r.tablePrefix, dim)
		if err := r.insertRows(ctx, table, rows, mysqlInsertUpsert); err != nil {
			return fmt.Errorf("mysql batch save dim=%d: %w", dim, err)
		}
	}
	return nil
}

func (r *mysqlRepository) EstimateStorageSize(_ context.Context, indexInfoList []*types.IndexInfo, params map[string]any) int64 {
	var total int64
	for _, info := range indexInfoList {
		if info == nil {
			continue
		}
		emb := extractEmbedding(params, info.SourceID)
		total += int64(len(info.Content)) + int64(len(emb))*4 + 256
	}
	return total
}

func (r *mysqlRepository) DeleteByChunkIDList(ctx context.Context, chunkIDList []string, dimension int, _ string) error {
	return r.deleteByField(ctx, "chunk_id", chunkIDList, dimension)
}

func (r *mysqlRepository) DeleteBySourceIDList(ctx context.Context, sourceIDList []string, dimension int, _ string) error {
	return r.deleteByField(ctx, "source_id", sourceIDList, dimension)
}

func (r *mysqlRepository) DeleteByKnowledgeIDList(ctx context.Context, knowledgeIDList []string, dimension int, _ string) error {
	return r.deleteByField(ctx, "knowledge_id", knowledgeIDList, dimension)
}

func (r *mysqlRepository) deleteByField(ctx context.Context, field string, ids []string, dimension int) error {
	if len(ids) == 0 {
		return nil
	}
	if dimension <= 0 {
		return r.forEachTable(ctx, func(table string) error {
			return r.db.WithContext(ctx).Table(table).Where(quoteIdent(field)+" IN ?", ids).Delete(&mysqlEmbedding{}).Error
		})
	}
	table := tableName(r.tablePrefix, dimension)
	if exists, err := r.tableExists(ctx, table); err != nil {
		return err
	} else if !exists {
		return nil
	}
	return r.db.WithContext(ctx).Table(table).Where(quoteIdent(field)+" IN ?", ids).Delete(&mysqlEmbedding{}).Error
}

func (r *mysqlRepository) Retrieve(ctx context.Context, params types.RetrieveParams) ([]*types.RetrieveResult, error) {
	switch params.RetrieverType {
	case types.VectorRetrieverType:
		return r.vectorRetrieve(ctx, params)
	case types.KeywordsRetrieverType:
		return r.keywordsRetrieve(ctx, params)
	case "":
		var out []*types.RetrieveResult
		keywords, err := r.keywordsRetrieve(ctx, params)
		if err != nil {
			out = append(out, &types.RetrieveResult{
				RetrieverEngineType: types.MySQLRetrieverEngineType,
				RetrieverType:       types.KeywordsRetrieverType,
				Error:               err,
			})
		} else {
			out = append(out, keywords...)
		}
		vectors, err := r.vectorRetrieve(ctx, params)
		if err != nil {
			out = append(out, &types.RetrieveResult{
				RetrieverEngineType: types.MySQLRetrieverEngineType,
				RetrieverType:       types.VectorRetrieverType,
				Error:               err,
			})
		} else {
			out = append(out, vectors...)
		}
		return out, nil
	default:
		return nil, fmt.Errorf("invalid retriever type: %v", params.RetrieverType)
	}
}

func (r *mysqlRepository) vectorRetrieve(ctx context.Context, params types.RetrieveParams) ([]*types.RetrieveResult, error) {
	if len(params.Embedding) == 0 {
		return buildRetrieveResult(nil, types.VectorRetrieverType), nil
	}
	dim := len(params.Embedding)
	table := tableName(r.tablePrefix, dim)
	exists, err := r.tableExists(ctx, table)
	if err != nil {
		return nil, err
	}
	if !exists {
		return buildRetrieveResult(nil, types.VectorRetrieverType), nil
	}
	usesNativeVector, err := r.tableUsesNativeVector(ctx, table)
	if err != nil {
		return nil, err
	}

	aliased := buildBaseFilter(params, "e")
	aliasedWhereClause, aliasedWhereArgs := aliased.build()
	if items, ok, err := r.nativeVectorRetrieve(ctx, table, usesNativeVector, params, aliasedWhereClause, aliasedWhereArgs); ok {
		if err != nil {
			logger.GetLogger(ctx).Warnf("[MySQL] native vector retrieve failed, falling back to Go ranking: %v", err)
		} else {
			return buildRetrieveResult(items, types.VectorRetrieverType), nil
		}
	}

	wb := buildBaseFilter(params, "")
	whereClause, whereArgs := wb.build()
	limit := candidateLimit(params.TopK)

	var rows []*mysqlEmbedding
	if err := r.db.WithContext(ctx).
		Table(table).
		Select(mysqlEmbeddingSelectList("", usesNativeVector)).
		Where(whereClause, whereArgs...).
		Order("updated_at DESC").
		Limit(limit).
		Find(&rows).Error; err != nil {
		return nil, err
	}

	items := applyVectorRanking(rows, params.Embedding, params.TopK, params.Threshold)
	return buildRetrieveResult(items, types.VectorRetrieverType), nil
}

func (r *mysqlRepository) nativeVectorRetrieve(
	ctx context.Context,
	table string,
	usesNativeVector bool,
	params types.RetrieveParams,
	whereClause string,
	whereArgs []any,
) ([]*types.IndexWithScore, bool, error) {
	native := r.detectVectorDistance(ctx)
	if native == nil {
		return nil, false, nil
	}
	stmt, args, ok, err := buildNativeVectorRetrieveStatement(table, usesNativeVector, params, whereClause, whereArgs, native)
	if err != nil {
		return nil, true, err
	}
	if !ok {
		return nil, false, nil
	}

	var rows []keywordRow
	if err := r.db.WithContext(ctx).Raw(stmt, args...).Scan(&rows).Error; err != nil {
		r.disableVectorDistance(ctx, err)
		return nil, true, err
	}
	items := make([]*types.IndexWithScore, 0, len(rows))
	for _, row := range rows {
		items = append(items, row.mysqlEmbedding.toIndexWithScore(row.Score, types.MatchTypeEmbedding))
	}
	return items, true, nil
}

func buildNativeVectorRetrieveStatement(
	table string,
	usesNativeVector bool,
	params types.RetrieveParams,
	whereClause string,
	whereArgs []any,
	native *nativeVectorDistance,
) (string, []any, bool, error) {
	if native == nil {
		return "", nil, false, nil
	}
	scoreTemplate := native.scoreExprJSON
	if usesNativeVector {
		scoreTemplate = native.scoreExprNative
	}
	if scoreTemplate == "" {
		return "", nil, false, nil
	}
	queryJSON, err := vectorToJSON(params.Embedding)
	if err != nil {
		return "", nil, true, err
	}
	topK := params.TopK
	if topK <= 0 {
		topK = 10
	}
	scoreExpr := fmt.Sprintf(scoreTemplate, "e.`embedding`")
	stmt := fmt.Sprintf(
		`SELECT ranked.*
FROM (
SELECT %s, %s AS score
FROM %s e
WHERE %s
) ranked
WHERE ranked.score >= ?
ORDER BY ranked.score DESC LIMIT %d`,
		mysqlEmbeddingSelectList("e", usesNativeVector),
		scoreExpr,
		quoteIdent(table),
		whereClause,
		topK,
	)
	args := make([]any, 0, len(whereArgs)+2)
	args = append(args, queryJSON)
	args = append(args, whereArgs...)
	args = append(args, params.Threshold)
	return stmt, args, true, nil
}

type nativeVectorDistance struct {
	name            string
	probeSQL        string
	scoreExprNative string
	scoreExprJSON   string
}

var nativeVectorDistanceCandidates = []nativeVectorDistance{
	{
		name:            "distance_string_to_vector",
		probeSQL:        `SELECT DISTANCE(STRING_TO_VECTOR('[1,0]'), STRING_TO_VECTOR('[1,0]'), 'COSINE')`,
		scoreExprNative: `1 - DISTANCE(%s, STRING_TO_VECTOR(?), 'COSINE')`,
		scoreExprJSON:   `1 - DISTANCE(STRING_TO_VECTOR(CAST(%s AS CHAR)), STRING_TO_VECTOR(?), 'COSINE')`,
	},
	{
		name:            "vector_distance_string_to_vector",
		probeSQL:        `SELECT VECTOR_DISTANCE(STRING_TO_VECTOR('[1,0]'), STRING_TO_VECTOR('[1,0]'), 'COSINE')`,
		scoreExprNative: `1 - VECTOR_DISTANCE(%s, STRING_TO_VECTOR(?), 'COSINE')`,
		scoreExprJSON:   `1 - VECTOR_DISTANCE(STRING_TO_VECTOR(CAST(%s AS CHAR)), STRING_TO_VECTOR(?), 'COSINE')`,
	},
	{
		name:          "vector_distance_json_string",
		probeSQL:      `SELECT VECTOR_DISTANCE('[1,0]', '[1,0]', 'COSINE')`,
		scoreExprJSON: `1 - VECTOR_DISTANCE(CAST(%s AS CHAR), ?, 'COSINE')`,
	},
	{
		name:          "vec_distance_cosine",
		probeSQL:      `SELECT VEC_DISTANCE_COSINE('[1,0]', '[1,0]')`,
		scoreExprJSON: `1 - VEC_DISTANCE_COSINE(CAST(%s AS CHAR), ?)`,
	},
	{
		name:          "cosine_distance",
		probeSQL:      `SELECT COSINE_DISTANCE('[1,0]', '[1,0]')`,
		scoreExprJSON: `1 - COSINE_DISTANCE(CAST(%s AS CHAR), ?)`,
	},
}

func (r *mysqlRepository) detectVectorDistance(ctx context.Context) *nativeVectorDistance {
	r.vectorProbeMu.RLock()
	if r.vectorProbeDone {
		native := r.vectorDistance
		r.vectorProbeMu.RUnlock()
		return native
	}
	r.vectorProbeMu.RUnlock()

	r.vectorProbeMu.Lock()
	defer r.vectorProbeMu.Unlock()
	if r.vectorProbeDone {
		return r.vectorDistance
	}

	probeCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	for i := range nativeVectorDistanceCandidates {
		candidate := &nativeVectorDistanceCandidates[i]
		var distance float64
		if err := r.db.WithContext(probeCtx).Raw(candidate.probeSQL).Scan(&distance).Error; err != nil {
			continue
		}
		if math.IsNaN(distance) || math.IsInf(distance, 0) {
			continue
		}
		r.vectorDistance = candidate
		logger.GetLogger(ctx).Infof("[MySQL] Native vector distance enabled: %s", candidate.name)
		break
	}
	if r.vectorDistance == nil {
		logger.GetLogger(ctx).Infof("[MySQL] Native vector distance unavailable; using Go cosine ranking")
	}
	r.vectorProbeDone = true
	return r.vectorDistance
}

func (r *mysqlRepository) disableVectorDistance(ctx context.Context, cause error) {
	r.vectorProbeMu.Lock()
	defer r.vectorProbeMu.Unlock()
	if r.vectorDistanceDisabled {
		return
	}
	if r.vectorDistance != nil {
		logger.GetLogger(ctx).Warnf("[MySQL] Native vector distance disabled; using Go cosine ranking: %v", cause)
	}
	r.vectorDistance = nil
	r.vectorDistanceDisabled = true
	r.vectorProbeDone = true
}

func (r *mysqlRepository) keywordsRetrieve(ctx context.Context, params types.RetrieveParams) ([]*types.RetrieveResult, error) {
	query := strings.TrimSpace(params.Query)
	if query == "" {
		return buildRetrieveResult(nil, types.KeywordsRetrieverType), nil
	}

	tables, err := r.listEmbeddingTables(ctx)
	if err != nil {
		return nil, err
	}
	if len(tables) == 0 {
		return buildRetrieveResult(nil, types.KeywordsRetrieverType), nil
	}

	wb := buildBaseFilter(params, "e")
	whereClause, whereArgs := wb.build()
	var all []*types.IndexWithScore
	for _, table := range tables {
		usesNativeVector, err := r.tableUsesNativeVector(ctx, table)
		if err != nil {
			logger.GetLogger(ctx).Warnf("[MySQL] keyword retrieve in %s failed to inspect embedding column: %v", table, err)
			continue
		}
		rows, err := r.fulltextKeywords(ctx, table, usesNativeVector, query, whereClause, whereArgs, params.TopK)
		if err != nil || len(rows) == 0 {
			rows, err = r.likeKeywords(ctx, table, usesNativeVector, query, whereClause, whereArgs, params.TopK)
			if err != nil {
				logger.GetLogger(ctx).Warnf("[MySQL] keyword retrieve in %s failed: %v", table, err)
				continue
			}
		}
		for _, row := range rows {
			all = append(all, row.mysqlEmbedding.toIndexWithScore(row.Score, types.MatchTypeKeywords))
		}
	}
	return buildRetrieveResult(mergeAndLimit(all, params.TopK), types.KeywordsRetrieverType), nil
}

func (r *mysqlRepository) CopyIndices(ctx context.Context,
	sourceKnowledgeBaseID string,
	sourceToTargetKBIDMap map[string]string,
	sourceToTargetChunkIDMap map[string]string,
	targetKnowledgeBaseID string,
	dimension int,
	_ string,
) error {
	if len(sourceToTargetChunkIDMap) == 0 {
		return nil
	}
	table := tableName(r.tablePrefix, dimension)
	if exists, err := r.tableExists(ctx, table); err != nil {
		return err
	} else if !exists {
		return nil
	}

	const pageSize = 100
	offset := 0
	usesNativeVector, err := r.tableUsesNativeVector(ctx, table)
	if err != nil {
		return err
	}
	for {
		var rows []*mysqlEmbedding
		if err := r.db.WithContext(ctx).Table(table).
			Select(mysqlEmbeddingSelectList("", usesNativeVector)).
			Where("knowledge_base_id = ?", sourceKnowledgeBaseID).
			Order("id ASC").
			Limit(pageSize).
			Offset(offset).
			Find(&rows).Error; err != nil {
			return err
		}
		if len(rows) == 0 {
			return nil
		}

		targets := make([]*mysqlEmbedding, 0, len(rows))
		for _, src := range rows {
			targetChunkID, ok := sourceToTargetChunkIDMap[src.ChunkID]
			if !ok {
				continue
			}
			targetKnowledgeID, ok := sourceToTargetKBIDMap[src.KnowledgeID]
			if !ok {
				continue
			}
			dst := *src
			dst.ID = uuid.New().String()
			dst.SourceID = translateSourceID(src.SourceID, src.ChunkID, targetChunkID)
			dst.ChunkID = targetChunkID
			dst.KnowledgeID = targetKnowledgeID
			dst.KnowledgeBaseID = targetKnowledgeBaseID
			targets = append(targets, &dst)
		}
		if len(targets) > 0 {
			if err := r.insertRows(ctx, table, targets, mysqlInsertIgnore); err != nil {
				return err
			}
		}
		if len(rows) < pageSize {
			return nil
		}
		offset += pageSize
	}
}

func (r *mysqlRepository) BatchUpdateChunkEnabledStatus(ctx context.Context, chunkStatusMap map[string]bool) error {
	if len(chunkStatusMap) == 0 {
		return nil
	}
	return r.forEachTable(ctx, func(table string) error {
		for chunkID, enabled := range chunkStatusMap {
			if err := r.db.WithContext(ctx).Table(table).
				Where("chunk_id = ?", chunkID).
				Update("is_enabled", enabled).Error; err != nil {
				return err
			}
		}
		return nil
	})
}

func (r *mysqlRepository) BatchUpdateChunkTagID(ctx context.Context, chunkTagMap map[string]string) error {
	if len(chunkTagMap) == 0 {
		return nil
	}
	return r.forEachTable(ctx, func(table string) error {
		for chunkID, tagID := range chunkTagMap {
			if err := r.db.WithContext(ctx).Table(table).
				Where("chunk_id = ?", chunkID).
				Update("tag_id", tagID).Error; err != nil {
				return err
			}
		}
		return nil
	})
}

func toMySQLEmbedding(info *types.IndexInfo, dim int, embeddingJSON string) *mysqlEmbedding {
	id := info.ID
	if id == "" {
		id = uuid.New().String()
	}
	return &mysqlEmbedding{
		ID:              id,
		SourceID:        info.SourceID,
		SourceType:      int(info.SourceType),
		ChunkID:         info.ChunkID,
		KnowledgeID:     info.KnowledgeID,
		KnowledgeBaseID: info.KnowledgeBaseID,
		TagID:           info.TagID,
		Content:         common.CleanInvalidUTF8(info.Content),
		Dimension:       dim,
		Embedding:       embeddingJSON,
		IsEnabled:       info.IsEnabled,
	}
}

func (r *mysqlRepository) ensureTable(ctx context.Context, dim int) error {
	if dim <= 0 {
		return fmt.Errorf("mysql: invalid embedding dimension %d", dim)
	}
	if _, ok := r.initializedTables.Load(dim); ok {
		return nil
	}
	table := tableName(r.tablePrefix, dim)
	if exists, err := r.tableExists(ctx, table); err != nil {
		return err
	} else if !exists {
		if err := r.createTable(ctx, table, dim); err != nil {
			return err
		}
	}
	r.initializedTables.Store(dim, true)
	return nil
}

func (r *mysqlRepository) createTable(ctx context.Context, table string, dim int) error {
	if r.detectNativeVectorType(ctx) {
		if err := r.createTableWithEmbeddingType(ctx, table, fmt.Sprintf("VECTOR(%d)", dim)); err == nil {
			logger.GetLogger(ctx).Infof("[MySQL] Created native VECTOR embedding table %s(dim=%d)", table, dim)
			return nil
		} else {
			logger.GetLogger(ctx).Warnf("[MySQL] create native VECTOR table %s failed, falling back to JSON embedding storage: %v", table, err)
		}
	}
	return r.createTableWithEmbeddingType(ctx, table, "JSON")
}

func (r *mysqlRepository) createTableWithEmbeddingType(ctx context.Context, table, embeddingType string) error {
	ddl := fmt.Sprintf(`CREATE TABLE IF NOT EXISTS %s (
    id VARCHAR(64) NOT NULL,
    source_id VARCHAR(255) NOT NULL,
    source_type INT NOT NULL,
    chunk_id VARCHAR(64),
    knowledge_id VARCHAR(64),
    knowledge_base_id VARCHAR(64),
    tag_id VARCHAR(64),
    is_enabled BOOLEAN NOT NULL DEFAULT TRUE,
    content LONGTEXT NOT NULL,
    dimension INT NOT NULL,
    embedding %s NOT NULL,
    created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    updated_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6),
    PRIMARY KEY (id),
    UNIQUE KEY uk_source (source_id, source_type),
    KEY idx_chunk (chunk_id),
    KEY idx_knowledge (knowledge_id),
    KEY idx_kb (knowledge_base_id),
    KEY idx_tag (tag_id),
    KEY idx_enabled (is_enabled),
    KEY idx_updated_at (updated_at),
    FULLTEXT KEY ft_content (content)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci`, quoteIdent(table), embeddingType)
	return r.db.WithContext(ctx).Exec(ddl).Error
}

func (r *mysqlRepository) detectNativeVectorType(ctx context.Context) bool {
	r.vectorTypeProbeMu.RLock()
	if r.vectorTypeProbeDone {
		supported := r.nativeVectorSupported
		r.vectorTypeProbeMu.RUnlock()
		return supported
	}
	r.vectorTypeProbeMu.RUnlock()

	r.vectorTypeProbeMu.Lock()
	defer r.vectorTypeProbeMu.Unlock()
	if r.vectorTypeProbeDone {
		return r.nativeVectorSupported
	}

	probeCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	var out string
	if err := r.db.WithContext(probeCtx).
		Raw(`SELECT VECTOR_TO_STRING(STRING_TO_VECTOR('[1,0]'))`).
		Scan(&out).Error; err != nil {
		logger.GetLogger(ctx).Infof("[MySQL] Native VECTOR type unavailable; using JSON embedding storage")
	} else {
		r.nativeVectorSupported = true
		logger.GetLogger(ctx).Infof("[MySQL] Native VECTOR type enabled for new embedding tables")
	}
	r.vectorTypeProbeDone = true
	return r.nativeVectorSupported
}

func (r *mysqlRepository) tableExists(ctx context.Context, table string) (bool, error) {
	dbName := r.database
	if dbName == "" {
		dbName = os.Getenv("MYSQL_DATABASE")
	}
	if dbName == "" {
		dbName = os.Getenv("DB_NAME")
	}
	var n int64
	err := r.db.WithContext(ctx).Raw(
		`SELECT COUNT(1) FROM information_schema.tables WHERE table_schema = DATABASE() AND table_name = ?`,
		table,
	).Scan(&n).Error
	if err != nil && dbName != "" {
		err = r.db.WithContext(ctx).Raw(
			`SELECT COUNT(1) FROM information_schema.tables WHERE table_schema = ? AND table_name = ?`,
			dbName, table,
		).Scan(&n).Error
	}
	return n > 0, err
}

func (r *mysqlRepository) tableUsesNativeVector(ctx context.Context, table string) (bool, error) {
	var dataType string
	err := r.db.WithContext(ctx).Raw(
		`SELECT DATA_TYPE FROM information_schema.columns WHERE table_schema = DATABASE() AND table_name = ? AND column_name = 'embedding' LIMIT 1`,
		table,
	).Scan(&dataType).Error
	if err != nil {
		return false, err
	}
	return strings.EqualFold(dataType, "vector"), nil
}

func (r *mysqlRepository) listEmbeddingTables(ctx context.Context) ([]string, error) {
	pattern := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(r.tablePrefix) + `\_%`
	var tables []string
	err := r.db.WithContext(ctx).Raw(
		`SELECT table_name FROM information_schema.tables WHERE table_schema = DATABASE() AND table_name LIKE ? ESCAPE '\\'`,
		pattern,
	).Scan(&tables).Error
	return tables, err
}

func (r *mysqlRepository) forEachTable(ctx context.Context, fn func(table string) error) error {
	tables, err := r.listEmbeddingTables(ctx)
	if err != nil {
		return err
	}
	for _, table := range tables {
		if err := fn(table); err != nil {
			return err
		}
	}
	return nil
}

func (r *mysqlRepository) fulltextKeywords(
	ctx context.Context,
	table string,
	usesNativeVector bool,
	query, whereClause string,
	whereArgs []any,
	topK int,
) ([]keywordRow, error) {
	if topK <= 0 {
		topK = 10
	}
	stmt := fmt.Sprintf(
		`SELECT %s, MATCH(e.content) AGAINST (? IN NATURAL LANGUAGE MODE) AS score
FROM %s e
WHERE %s AND MATCH(e.content) AGAINST (? IN NATURAL LANGUAGE MODE)
ORDER BY score DESC LIMIT %d`,
		mysqlEmbeddingSelectList("e", usesNativeVector),
		quoteIdent(table),
		whereClause,
		topK,
	)
	args := make([]any, 0, len(whereArgs)+2)
	args = append(args, query)
	args = append(args, whereArgs...)
	args = append(args, query)
	var rows []keywordRow
	err := r.db.WithContext(ctx).Raw(stmt, args...).Scan(&rows).Error
	return rows, err
}

func (r *mysqlRepository) likeKeywords(
	ctx context.Context,
	table string,
	usesNativeVector bool,
	query, whereClause string,
	whereArgs []any,
	topK int,
) ([]keywordRow, error) {
	if topK <= 0 {
		topK = 10
	}
	stmt := fmt.Sprintf(
		`SELECT %s, 1.0 AS score
FROM %s e
WHERE %s AND LOWER(e.content) LIKE LOWER(?)
ESCAPE '\\'
ORDER BY e.updated_at DESC LIMIT %d`,
		mysqlEmbeddingSelectList("e", usesNativeVector),
		quoteIdent(table),
		whereClause,
		topK,
	)
	args := append([]any{}, whereArgs...)
	args = append(args, "%"+escapeLike(query)+"%")
	var rows []keywordRow
	err := r.db.WithContext(ctx).Raw(stmt, args...).Scan(&rows).Error
	return rows, err
}

type mysqlInsertMode int

const (
	mysqlInsertUpsert mysqlInsertMode = iota
	mysqlInsertIgnore
)

func (r *mysqlRepository) insertRows(ctx context.Context, table string, rows []*mysqlEmbedding, mode mysqlInsertMode) error {
	usesNativeVector, err := r.tableUsesNativeVector(ctx, table)
	if err != nil {
		return err
	}
	if usesNativeVector {
		return r.insertNativeVectorRows(ctx, table, rows, mode)
	}
	return r.insertJSONRows(ctx, table, rows, mode)
}

func (r *mysqlRepository) insertJSONRows(ctx context.Context, table string, rows []*mysqlEmbedding, mode mysqlInsertMode) error {
	if len(rows) == 0 {
		return nil
	}
	db := r.db.WithContext(ctx).Table(table)
	switch mode {
	case mysqlInsertUpsert:
		db = db.Clauses(clause.OnConflict{
			Columns: []clause.Column{{Name: "source_id"}, {Name: "source_type"}},
			DoUpdates: clause.AssignmentColumns([]string{
				"content",
				"chunk_id",
				"knowledge_id",
				"knowledge_base_id",
				"tag_id",
				"is_enabled",
				"dimension",
				"embedding",
			}),
		})
	case mysqlInsertIgnore:
		db = db.Clauses(clause.OnConflict{DoNothing: true})
	default:
		return fmt.Errorf("mysql: unsupported insert mode %d", mode)
	}
	return db.Create(rows).Error
}

func (r *mysqlRepository) insertNativeVectorRows(ctx context.Context, table string, rows []*mysqlEmbedding, mode mysqlInsertMode) error {
	const batchSize = 100
	for start := 0; start < len(rows); start += batchSize {
		end := start + batchSize
		if end > len(rows) {
			end = len(rows)
		}
		if err := r.insertNativeVectorBatch(ctx, table, rows[start:end], mode); err != nil {
			return err
		}
	}
	return nil
}

func (r *mysqlRepository) insertNativeVectorBatch(ctx context.Context, table string, rows []*mysqlEmbedding, mode mysqlInsertMode) error {
	if len(rows) == 0 {
		return nil
	}

	insertVerb := "INSERT INTO"
	if mode == mysqlInsertIgnore {
		insertVerb = "INSERT IGNORE INTO"
	}
	columns := []string{
		"id",
		"source_id",
		"source_type",
		"chunk_id",
		"knowledge_id",
		"knowledge_base_id",
		"tag_id",
		"is_enabled",
		"content",
		"dimension",
		"embedding",
	}
	columnSQL := make([]string, len(columns))
	for i, column := range columns {
		columnSQL[i] = quoteIdent(column)
	}
	valueSQL := make([]string, 0, len(rows))
	args := make([]any, 0, len(rows)*len(columns))
	for _, row := range rows {
		valueSQL = append(valueSQL, "(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, STRING_TO_VECTOR(?))")
		args = append(args,
			row.ID,
			row.SourceID,
			row.SourceType,
			row.ChunkID,
			row.KnowledgeID,
			row.KnowledgeBaseID,
			row.TagID,
			row.IsEnabled,
			row.Content,
			row.Dimension,
			row.Embedding,
		)
	}

	stmt := fmt.Sprintf(
		"%s %s (%s) VALUES %s",
		insertVerb,
		quoteIdent(table),
		strings.Join(columnSQL, ", "),
		strings.Join(valueSQL, ", "),
	)
	switch mode {
	case mysqlInsertUpsert:
		stmt += ` ON DUPLICATE KEY UPDATE
    content = VALUES(content),
    chunk_id = VALUES(chunk_id),
    knowledge_id = VALUES(knowledge_id),
    knowledge_base_id = VALUES(knowledge_base_id),
    tag_id = VALUES(tag_id),
    is_enabled = VALUES(is_enabled),
    dimension = VALUES(dimension),
    embedding = VALUES(embedding),
    updated_at = CURRENT_TIMESTAMP(6)`
	case mysqlInsertIgnore:
	default:
		return fmt.Errorf("mysql: unsupported insert mode %d", mode)
	}
	return r.db.WithContext(ctx).Exec(stmt, args...).Error
}

func (e mysqlEmbedding) toIndexWithScore(score float64, matchType types.MatchType) *types.IndexWithScore {
	return &types.IndexWithScore{
		ID:              e.ID,
		Content:         e.Content,
		SourceID:        e.SourceID,
		SourceType:      types.SourceType(e.SourceType),
		ChunkID:         e.ChunkID,
		KnowledgeID:     e.KnowledgeID,
		KnowledgeBaseID: e.KnowledgeBaseID,
		TagID:           e.TagID,
		Score:           score,
		MatchType:       matchType,
		IsEnabled:       e.IsEnabled,
	}
}

func buildRetrieveResult(results []*types.IndexWithScore, retrieverType types.RetrieverType) []*types.RetrieveResult {
	return []*types.RetrieveResult{{
		Results:             results,
		RetrieverEngineType: types.MySQLRetrieverEngineType,
		RetrieverType:       retrieverType,
	}}
}

func candidateLimit(topK int) int {
	if topK <= 0 {
		return 200
	}
	limit := topK * 50
	if limit < 200 {
		return 200
	}
	if limit > 2000 {
		return 2000
	}
	return limit
}

func escapeLike(s string) string {
	replacer := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
	return replacer.Replace(s)
}

func translateSourceID(originalSourceID, sourceChunkID, targetChunkID string) string {
	switch {
	case originalSourceID == sourceChunkID:
		return targetChunkID
	case strings.HasPrefix(originalSourceID, sourceChunkID+"-"):
		return targetChunkID + "-" + strings.TrimPrefix(originalSourceID, sourceChunkID+"-")
	default:
		return uuid.New().String()
	}
}
