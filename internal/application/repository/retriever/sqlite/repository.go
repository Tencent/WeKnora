package sqlite

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/Tencent/WeKnora/internal/common"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	sqlite_vec "github.com/asg017/sqlite-vec-go-bindings/cgo"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

// sqliteEmbedding stores metadata alongside the vec0 virtual table rows
type sqliteEmbedding struct {
	ID              uint      `gorm:"primarykey;autoIncrement"`
	CreatedAt       time.Time `gorm:"column:created_at"`
	UpdatedAt       time.Time `gorm:"column:updated_at"`
	SourceID        string    `gorm:"column:source_id;not null;uniqueIndex:idx_sqlite_emb_source"`
	SourceType      int       `gorm:"column:source_type;not null;uniqueIndex:idx_sqlite_emb_source"`
	ChunkID         string    `gorm:"column:chunk_id;index"`
	KnowledgeID     string    `gorm:"column:knowledge_id;index"`
	KnowledgeBaseID string    `gorm:"column:knowledge_base_id;index"`
	TagID           string    `gorm:"column:tag_id;index"`
	Content         string    `gorm:"column:content;not null"`
	Dimension       int       `gorm:"column:dimension;not null"`
	IsEnabled       *bool     `gorm:"column:is_enabled;default:true;index"`
}

func (sqliteEmbedding) TableName() string { return "lite_embeddings" }

type sqliteRepository struct {
	db *gorm.DB
	// vecMu guards vecTables: concurrent BatchSave and retrieval goroutines
	// all consult it, and an unguarded map write is a fatal runtime error.
	vecMu     sync.RWMutex
	vecTables map[int]bool // tracks which vec0 tables have been created (keyed by dimension)
	// ftsUnavailable records that the driver was built without the fts5 module
	// (upstream test runs do not pass -tags sqlite_fts5). Keyword indexing
	// cannot exist in such a build at all, so its writes are skipped instead of
	// failing every ingestion; Lite release builds always pass the tag.
	ftsUnavailable bool
}

func NewSQLiteRetrieveEngineRepository(db *gorm.DB) interfaces.RetrieveEngineRepository {
	logger.GetLogger(context.Background()).Info("[SQLite] Initializing SQLite retriever engine repository with sqlite-vec")

	if err := db.AutoMigrate(&sqliteEmbedding{}); err != nil {
		logger.GetLogger(context.Background()).Errorf("[SQLite] Failed to auto-migrate lite_embeddings: %v", err)
	}

	repo := &sqliteRepository{
		db:        db,
		vecTables: make(map[int]bool),
	}

	if err := repo.initFTS5(); err != nil {
		if errors.Is(err, errFTS5ModuleMissing) {
			repo.ftsUnavailable = true
			logger.GetLogger(context.Background()).Warnf(
				"[SQLite] Keyword index disabled, driver has no fts5 module: %v", err)
		} else {
			logger.GetLogger(context.Background()).Errorf("[SQLite] Failed to initialize FTS5 index: %v", err)
		}
	}
	enableImageChunkIndexes(db)

	repo.ensureExistingVecTables()

	return repo
}

// enableImageChunkIndexes repairs image OCR/caption rows that were indexed
// with is_enabled = 0 because the multimodal indexer never set IsEnabled.
// Retrieval filters on is_enabled, so those rows were never searchable.
// Only rows whose chunk is itself enabled are touched, which leaves chunks a
// user disabled alone; after the first run the statement matches nothing.
func enableImageChunkIndexes(db *gorm.DB) {
	result := db.Exec(`UPDATE lite_embeddings SET is_enabled = 1
		WHERE is_enabled = 0 AND chunk_id IN (
			SELECT id FROM chunks
			WHERE chunk_type IN (?, ?) AND is_enabled = 1
		)`, string(types.ChunkTypeImageOCR), string(types.ChunkTypeImageCaption))
	if result.Error != nil {
		logger.GetLogger(context.Background()).Warnf(
			"[SQLite] Failed to re-enable image chunk indexes: %v", result.Error)
		return
	}
	if result.RowsAffected > 0 {
		logger.GetLogger(context.Background()).Infof(
			"[SQLite] Re-enabled %d image OCR/caption index rows", result.RowsAffected)
	}
}

// errFTS5ModuleMissing reports a driver built without the fts5 module. The
// keyword index cannot exist in such a build, which is a property of the build
// rather than a write that failed at runtime.
var errFTS5ModuleMissing = errors.New("sqlite driver built without the fts5 module")

// fts5InsertSQL is shared by the initial population and every later keyword
// index write, so both always build the same row shape.
const fts5InsertSQL = `INSERT INTO lite_embeddings_fts(
	rowid, content, source_id, chunk_id, knowledge_id, knowledge_base_id) VALUES(?, ?, ?, ?, ?, ?)`

// initFTS5 prepares the contentless FTS5 keyword index. A failure is returned
// so it is not mistaken for a working index; the table and its content are
// built in one transaction, because a half-populated index silently drops
// keyword recall for every row that was left out.
func (r *sqliteRepository) initFTS5() error {
	var sqlStr string
	err := r.db.Raw(
		"SELECT sql FROM sqlite_master WHERE type='table' AND name='lite_embeddings_fts'",
	).Scan(&sqlStr).Error
	if err != nil {
		return fmt.Errorf("[SQLite] failed to inspect lite_embeddings_fts: %w", err)
	}
	// If the table exists but is not contentless (i.e. uses content='lite_embeddings'), recreate it
	migrate := sqlStr != "" && strings.Contains(sqlStr, "content='lite_embeddings'")
	if migrate {
		logger.GetLogger(context.Background()).Infof(
			"[SQLite] Migrating FTS5 table to contentless table with manual bigram tokenization")
	} else if sqlStr != "" {
		return nil
	}

	err = r.db.Transaction(func(tx *gorm.DB) error {
		if migrate {
			if err := tx.Exec("DROP TABLE IF EXISTS lite_embeddings_fts").Error; err != nil {
				return err
			}
		}
		createSQL := `CREATE VIRTUAL TABLE IF NOT EXISTS lite_embeddings_fts USING fts5(
			content, source_id, chunk_id, knowledge_id, knowledge_base_id,
			content='',
			contentless_delete=1,
			tokenize='unicode61'
		)`
		if err := tx.Exec(createSQL).Error; err != nil {
			return err
		}
		logger.GetLogger(context.Background()).Infof(
			"[SQLite] Populating contentless FTS5 table from lite_embeddings with bigrams")

		// To populate, we need to read all rows and insert them via Go to apply bigram tokenization
		type Row struct {
			ID              uint
			Content         string
			SourceID        string
			ChunkID         string
			KnowledgeID     string
			KnowledgeBaseID string
		}
		var rows []Row
		if err := tx.Raw(
			"SELECT id, content, source_id, chunk_id, knowledge_id, knowledge_base_id FROM lite_embeddings",
		).Scan(&rows).Error; err != nil {
			return err
		}
		for _, row := range rows {
			if err := tx.Exec(fts5InsertSQL, row.ID, tokenizeCJKBigram(row.Content), row.SourceID,
				row.ChunkID, row.KnowledgeID, row.KnowledgeBaseID).Error; err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		if strings.Contains(err.Error(), "no such module: fts5") {
			return fmt.Errorf("%w: %v", errFTS5ModuleMissing, err)
		}
		return fmt.Errorf("[SQLite] failed to initialize lite_embeddings_fts: %w", err)
	}
	return nil
}

func vecTableName(dim int) string {
	return fmt.Sprintf("vec_embeddings_%d", dim)
}

func (r *sqliteRepository) hasVecTable(dim int) bool {
	r.vecMu.RLock()
	defer r.vecMu.RUnlock()
	return r.vecTables[dim]
}

func (r *sqliteRepository) markVecTable(dim int) {
	r.vecMu.Lock()
	defer r.vecMu.Unlock()
	r.vecTables[dim] = true
}

// unmarkVecTable drops the cached "ready" state of a dimension after a write
// against its table failed, so the next write re-checks the table instead of
// trusting a marker that no longer describes it.
func (r *sqliteRepository) unmarkVecTable(dim int) {
	r.vecMu.Lock()
	defer r.vecMu.Unlock()
	delete(r.vecTables, dim)
}

// verifyVecTable rejects a same-named object that is not a vec0 table. It is
// needed because CREATE VIRTUAL TABLE IF NOT EXISTS is a silent no-op when any
// object already occupies the name, and a marker set from that no-op kept the
// dimension unindexed for the rest of the process lifetime.
func (r *sqliteRepository) verifyVecTable(tbl string) error {
	var createSQL string
	if err := r.db.Raw(
		`SELECT COALESCE(sql, '') FROM sqlite_master WHERE type = 'table' AND name = ?`, tbl,
	).Scan(&createSQL).Error; err != nil {
		return fmt.Errorf("[SQLite] failed to inspect %s: %w", tbl, err)
	}
	if createSQL == "" {
		return fmt.Errorf("[SQLite] %s is missing after CREATE VIRTUAL TABLE", tbl)
	}
	if !strings.Contains(strings.ToLower(createSQL), "using vec0") {
		return fmt.Errorf("[SQLite] %s exists but is not a vec0 table: %s", tbl, createSQL)
	}
	return nil
}

// ensureVecTable makes the vec0 table of a dimension usable and returns an
// error when it cannot, so callers never write vectors into a table that does
// not exist. A dimension is only cached as ready once the table was verified.
func (r *sqliteRepository) ensureVecTable(dim int) error {
	if dim <= 0 {
		return nil
	}
	if r.hasVecTable(dim) {
		return nil
	}
	tbl := vecTableName(dim)
	createSQL := fmt.Sprintf(
		`CREATE VIRTUAL TABLE IF NOT EXISTS %s USING vec0(embedding float[%d] distance_metric=cosine)`,
		tbl, dim,
	)
	if err := r.db.Exec(createSQL).Error; err != nil {
		return fmt.Errorf("[SQLite] failed to create vec0 table %s: %w", tbl, err)
	}
	if err := r.verifyVecTable(tbl); err != nil {
		return err
	}
	r.markVecTable(dim)
	return nil
}

func (r *sqliteRepository) ensureExistingVecTables() {
	var dims []int
	if err := r.db.Model(&sqliteEmbedding{}).Distinct("dimension").
		Where("dimension > 0").Pluck("dimension", &dims).Error; err != nil {
		logger.GetLogger(context.Background()).Warnf(
			"[SQLite] Failed to list indexed dimensions: %v", err)
		return
	}
	for _, dim := range dims {
		// A failure here is not cached: the next write retries and reports it.
		if err := r.ensureVecTable(dim); err != nil {
			logger.GetLogger(context.Background()).Errorf(
				"[SQLite] Failed to prepare vec0 table for dim %d: %v", dim, err)
		}
	}
}

func (r *sqliteRepository) EngineType() types.RetrieverEngineType {
	return types.SQLiteRetrieverEngineType
}

func (r *sqliteRepository) Support() []types.RetrieverType {
	return []types.RetrieverType{types.KeywordsRetrieverType, types.VectorRetrieverType}
}

func (r *sqliteRepository) Save(ctx context.Context, indexInfo *types.IndexInfo, params map[string]any) error {
	return r.BatchSave(ctx, []*types.IndexInfo{indexInfo}, params)
}

// BatchSave upserts by (source_id, source_type): rows already indexed for a
// source are replaced together with their FTS and vec0 entries. Callers
// re-index an edited FAQ entry under its old source ID and expect the new
// content to win; ON CONFLICT DO NOTHING kept the stale row instead, and the
// skipped rows also shifted the RETURNING ids GORM assigns back to the slice,
// attaching vectors and FTS entries to the wrong rows.
func (r *sqliteRepository) BatchSave(ctx context.Context, indexInfoList []*types.IndexInfo, params map[string]any) error {
	if len(indexInfoList) == 0 {
		return nil
	}
	type sourceKey struct {
		id  string
		typ int
	}
	positions := make(map[sourceKey]int, len(indexInfoList))
	rows := make([]*sqliteEmbedding, 0, len(indexInfoList))
	embs := make([][]float32, 0, len(indexInfoList))
	for _, info := range indexInfoList {
		row := toSQLiteEmbedding(info)
		emb := extractEmbedding(params, info.SourceID)
		if len(emb) > 0 {
			row.Dimension = len(emb)
		}
		// A source repeated inside one batch keeps its last entry, as an
		// upsert would.
		key := sourceKey{id: row.SourceID, typ: row.SourceType}
		if i, ok := positions[key]; ok {
			rows[i], embs[i] = row, emb
			continue
		}
		positions[key] = len(rows)
		rows = append(rows, row)
		embs = append(embs, emb)
	}

	sourceIDs := make([]string, 0, len(rows))
	for _, row := range rows {
		sourceIDs = append(sourceIDs, row.SourceID)
		// vec0 tables are created outside the transaction below: production
		// SQLite runs on one connection, which the transaction holds.
		if err := r.ensureVecTable(row.Dimension); err != nil {
			return err
		}
	}
	// Replacing a source is one transaction, so a failed insert cannot
	// leave the source with its old rows deleted and no new ones.
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var candidates []sqliteEmbedding
		if err := tx.Where("source_id IN ?", sourceIDs).Find(&candidates).Error; err != nil {
			return err
		}
		existing := make([]sqliteEmbedding, 0, len(candidates))
		existingIDs := make([]uint, 0, len(candidates))
		for _, c := range candidates {
			if _, ok := positions[sourceKey{id: c.SourceID, typ: c.SourceType}]; ok {
				existing = append(existing, c)
				existingIDs = append(existingIDs, c.ID)
			}
		}
		if len(existing) > 0 {
			if err := r.deleteRowsAndVecs(tx, existing); err != nil {
				return err
			}
			if err := tx.Where("id IN ?", existingIDs).Delete(&sqliteEmbedding{}).Error; err != nil {
				return err
			}
		}

		if err := tx.Create(rows).Error; err != nil {
			return err
		}
		// Every index row is written in the same transaction as the chunk row:
		// returning the error rolls the chunk row back instead of committing a
		// row that neither retrieval path can find.
		for i, row := range rows {
			if err := r.syncFTS5Insert(tx, row); err != nil {
				return err
			}
			if len(embs[i]) > 0 {
				if err := r.insertVec(tx, row.ID, row.Dimension, embs[i]); err != nil {
					r.unmarkVecTable(row.Dimension)
					return err
				}
			}
		}
		return nil
	})
}

func (r *sqliteRepository) EstimateStorageSize(_ context.Context, indexInfoList []*types.IndexInfo, _ map[string]any) int64 {
	var total int64
	for _, info := range indexInfoList {
		total += int64(len(info.Content)) + 200
	}
	return total
}

func (r *sqliteRepository) DeleteByChunkIDList(ctx context.Context, chunkIDList []string, _ int, _ string) error {
	var rows []sqliteEmbedding
	if err := r.db.WithContext(ctx).Where("chunk_id IN ?", chunkIDList).Find(&rows).Error; err != nil {
		return err
	}
	if err := r.deleteRowsAndVecs(r.db.WithContext(ctx), rows); err != nil {
		return err
	}
	return r.db.WithContext(ctx).Where("chunk_id IN ?", chunkIDList).Delete(&sqliteEmbedding{}).Error
}

func (r *sqliteRepository) DeleteBySourceIDList(ctx context.Context, sourceIDList []string, _ int, _ string) error {
	var rows []sqliteEmbedding
	if err := r.db.WithContext(ctx).Where("source_id IN ?", sourceIDList).Find(&rows).Error; err != nil {
		return err
	}
	if err := r.deleteRowsAndVecs(r.db.WithContext(ctx), rows); err != nil {
		return err
	}
	return r.db.WithContext(ctx).Where("source_id IN ?", sourceIDList).Delete(&sqliteEmbedding{}).Error
}

func (r *sqliteRepository) DeleteByKnowledgeIDList(ctx context.Context, knowledgeIDList []string, _ int, _ string) error {
	var rows []sqliteEmbedding
	if err := r.db.WithContext(ctx).Where("knowledge_id IN ?", knowledgeIDList).Find(&rows).Error; err != nil {
		return err
	}
	if err := r.deleteRowsAndVecs(r.db.WithContext(ctx), rows); err != nil {
		return err
	}
	return r.db.WithContext(ctx).Where("knowledge_id IN ?", knowledgeIDList).Delete(&sqliteEmbedding{}).Error
}

func (r *sqliteRepository) CopyIndices(ctx context.Context,
	_ string,
	sourceToTargetKBIDMap map[string]string,
	sourceToTargetChunkIDMap map[string]string,
	targetKnowledgeBaseID string,
	_ int, _ string,
) error {
	sourceChunkIDs := make([]string, 0, len(sourceToTargetChunkIDMap))
	for sourceChunkID := range sourceToTargetChunkIDMap {
		sourceChunkIDs = append(sourceChunkIDs, sourceChunkID)
	}
	const batchSize = 500
	// A copied chunk is only usable when its row, keyword index row and vector
	// row all land: retrieval joins both index tables on lite_embeddings.id, so
	// a dropped write leaves a chunk neither path can find while the copy still
	// reports success. Every failing row is collected and the loop keeps going,
	// so the caller learns all the rows that were lost, not just the first.
	var copyErrs []error
	for start := 0; start < len(sourceChunkIDs); start += batchSize {
		end := min(start+batchSize, len(sourceChunkIDs))
		// Every row of a chunk is copied: besides the chunk itself, generated
		// questions are indexed as extra rows under the same chunk_id.
		var sources []sqliteEmbedding
		if err := r.db.WithContext(ctx).
			Where("chunk_id IN ?", sourceChunkIDs[start:end]).
			Order("id").
			Find(&sources).Error; err != nil {
			return err
		}
		for _, src := range sources {
			targetChunkID := sourceToTargetChunkIDMap[src.ChunkID]
			newRow := sqliteEmbedding{
				SourceID:        copiedSourceID(src.SourceID, src.ChunkID, targetChunkID),
				SourceType:      src.SourceType,
				ChunkID:         targetChunkID,
				KnowledgeID:     sourceToTargetKBIDMap[src.KnowledgeID],
				KnowledgeBaseID: targetKnowledgeBaseID,
				TagID:           src.TagID,
				Content:         src.Content,
				Dimension:       src.Dimension,
				IsEnabled:       src.IsEnabled,
			}
			// The source and target chunk IDs locate the lost row without
			// quoting its content into the error.
			if err := r.db.WithContext(ctx).Create(&newRow).Error; err != nil {
				copyErrs = append(copyErrs, fmt.Errorf(
					"[SQLite] CopyIndices: failed to copy source %s (chunk %s -> %s): %w",
					src.SourceID, src.ChunkID, targetChunkID, err))
				continue
			}
			if err := r.syncFTS5Insert(r.db.WithContext(ctx), &newRow); err != nil {
				copyErrs = append(copyErrs, fmt.Errorf(
					"[SQLite] CopyIndices: failed to copy keyword index of source %s (chunk %s -> %s): %w",
					src.SourceID, src.ChunkID, targetChunkID, err))
			}
			if src.Dimension > 0 {
				if err := r.copyVec(ctx, src.ID, newRow.ID, src.Dimension); err != nil {
					copyErrs = append(copyErrs, fmt.Errorf(
						"[SQLite] CopyIndices: failed to copy vector of source %s (chunk %s -> %s): %w",
						src.SourceID, src.ChunkID, targetChunkID, err))
				}
			}
		}
	}
	return errors.Join(copyErrs...)
}

// copiedSourceID maps a source row's SourceID onto the target chunk the way
// the Postgres engine does, so the copy can later be deleted or re-indexed by
// source ID: a chunk row uses the chunk ID and a generated question keeps its
// "{chunkID}-{questionID}" suffix.
func copiedSourceID(sourceID, sourceChunkID, targetChunkID string) string {
	if sourceID == sourceChunkID {
		return targetChunkID
	}
	if questionID, ok := strings.CutPrefix(sourceID, sourceChunkID+"-"); ok {
		return targetChunkID + "-" + questionID
	}
	return uuid.New().String()
}

// Both batch updates write one statement per chunk, so a failing statement only
// affects its own row. Errors are therefore accumulated and the whole batch is
// attempted (like the Milvus and Qdrant engines do) instead of stopping at the
// first failure and leaving an arbitrary prefix of the map applied, only to be
// reported as a blanket failure.
func (r *sqliteRepository) BatchUpdateChunkEnabledStatus(ctx context.Context, chunkStatusMap map[string]bool) error {
	var updateErrs []error
	for chunkID, enabled := range chunkStatusMap {
		if err := r.updateChunkIndexColumn(ctx, "is_enabled", chunkID, enabled); err != nil {
			updateErrs = append(updateErrs,
				fmt.Errorf("[SQLite] failed to update is_enabled for chunk %s: %w", chunkID, err))
		}
	}
	return errors.Join(updateErrs...)
}

func (r *sqliteRepository) BatchUpdateChunkTagID(ctx context.Context, chunkTagMap map[string]string) error {
	var updateErrs []error
	for chunkID, tagID := range chunkTagMap {
		if err := r.updateChunkIndexColumn(ctx, "tag_id", chunkID, tagID); err != nil {
			updateErrs = append(updateErrs,
				fmt.Errorf("[SQLite] failed to update tag_id for chunk %s: %w", chunkID, err))
		}
	}
	return errors.Join(updateErrs...)
}

// updateChunkIndexColumn applies a single per-chunk UPDATE to the retrieval
// index copy. A statement matching no row is not an error -- that row may never
// have reached the index -- but it would otherwise stay completely invisible,
// so it is warned about with the table and chunk it was meant for.
func (r *sqliteRepository) updateChunkIndexColumn(
	ctx context.Context, column, chunkID string, value any,
) error {
	result := r.db.WithContext(ctx).Model(&sqliteEmbedding{}).
		Where("chunk_id = ?", chunkID).
		Update(column, value)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		logger.GetLogger(ctx).Warnf(
			"[SQLite] update of %s.%s matched 0 rows for chunk %s",
			sqliteEmbedding{}.TableName(), column, chunkID,
		)
	}
	return nil
}

// --- Retrieve ---

func (r *sqliteRepository) Retrieve(ctx context.Context, params types.RetrieveParams) ([]*types.RetrieveResult, error) {
	var results []*types.RetrieveResult

	if params.RetrieverType == types.KeywordsRetrieverType || params.RetrieverType == "" {
		res, err := r.keywordsRetrieve(ctx, params)
		if err != nil {
			return nil, err
		}
		results = append(results, res...)
	}

	if params.RetrieverType == types.VectorRetrieverType || params.RetrieverType == "" {
		res, err := r.vectorRetrieve(ctx, params)
		if err != nil {
			return nil, err
		}
		results = append(results, res...)
	}

	return results, nil
}

// --- Keywords retrieval via FTS5 ---
func (r *sqliteRepository) keywordsRetrieve(ctx context.Context, params types.RetrieveParams) ([]*types.RetrieveResult, error) {
	if params.Query == "" {
		return nil, nil
	}

	ftsQuery := sanitizeFTS5Query(params.Query)

	sql := `
		SELECT e.id, e.source_id, e.source_type, e.chunk_id,
			e.knowledge_id, e.knowledge_base_id, e.tag_id,
			e.content,
			(bm25(lite_embeddings_fts) * -1000000.0) AS score
		FROM lite_embeddings_fts
		JOIN lite_embeddings e ON e.id = lite_embeddings_fts.rowid
		WHERE lite_embeddings_fts MATCH ?
		AND (e.is_enabled IS NULL OR e.is_enabled = 1)
	`

	args := []interface{}{ftsQuery}

	for _, wp := range buildFilterWhere(params, "e") {
		sql += " AND " + wp.clause
		args = append(args, wp.args...)
	}

	sql += " ORDER BY score DESC LIMIT ?"
	args = append(args, params.TopK)

	type ftsResult struct {
		ID              uint
		SourceID        string
		SourceType      int
		ChunkID         string
		KnowledgeID     string
		KnowledgeBaseID string
		TagID           string
		Content         string
		Score           float64
	}

	var rows []ftsResult
	if err := r.db.WithContext(ctx).Raw(sql, args...).Scan(&rows).Error; err != nil {
		return nil, fmt.Errorf("FTS5 query failed: %w", err)
	}

	logger.GetLogger(ctx).Infof("[SQLite] keywordsRetrieve: query=%q, ftsQuery=%q, matched=%d rows", params.Query, ftsQuery, len(rows))

	items := make([]*types.IndexWithScore, len(rows))
	for i, row := range rows {

		// bm25 is originally negative and very small, we multiplied it by -1000000.0 in SQL
		// to make it positive and human-readable.
		score := row.Score

		logger.GetLogger(ctx).Infof("[SQLite] keywordsRetrieve: #%d chunk_id=%s, score=%.4f, content_preview=%.60s",
			i+1, row.ChunkID, score, row.Content)

		items[i] = &types.IndexWithScore{
			ID:              fmt.Sprintf("%d", row.ID),
			SourceID:        row.SourceID,
			SourceType:      types.SourceType(row.SourceType),
			ChunkID:         row.ChunkID,
			KnowledgeID:     row.KnowledgeID,
			KnowledgeBaseID: row.KnowledgeBaseID,
			TagID:           row.TagID,
			Content:         row.Content,
			Score:           score,
			MatchType:       types.MatchTypeKeywords,
		}
	}

	return []*types.RetrieveResult{{
		Results:             items,
		RetrieverEngineType: types.SQLiteRetrieverEngineType,
		RetrieverType:       types.KeywordsRetrieverType,
	}}, nil
}

func (r *sqliteRepository) vectorRetrieve(ctx context.Context, params types.RetrieveParams) ([]*types.RetrieveResult, error) {
	if len(params.Embedding) == 0 {
		return nil, nil
	}

	dim := len(params.Embedding)
	if err := r.ensureVecTable(dim); err != nil {
		return nil, err
	}

	queryBlob, err := sqlite_vec.SerializeFloat32(params.Embedding)
	if err != nil {
		return nil, fmt.Errorf("serialize query vector failed: %w", err)
	}

	tbl := vecTableName(dim)

	// ⚠️ sqlite-vec 要求必须有 k = ?
	vecSQL := fmt.Sprintf(`
		SELECT v.rowid, v.distance,
			e.source_id, e.source_type, e.chunk_id,
			e.knowledge_id, e.knowledge_base_id,
			e.tag_id, e.content
		FROM %s v
		JOIN lite_embeddings e ON e.id = v.rowid
		WHERE v.embedding MATCH ?
		AND k = ?
		AND v.rowid IN (
			SELECT filtered.id
			FROM lite_embeddings filtered
			WHERE (filtered.is_enabled IS NULL OR filtered.is_enabled = 1)
	`, tbl)

	args := []interface{}{
		queryBlob,
		params.TopK, // 这里就是 k
	}

	// 追加过滤条件
	for _, wp := range buildFilterWhere(params, "filtered") {
		vecSQL += " AND " + wp.clause
		args = append(args, wp.args...)
	}

	// ⚠️ 这里仍然建议加 ORDER BY，虽然 vec0 已经按距离返回
	vecSQL += ") ORDER BY v.distance ASC"

	type row struct {
		Rowid           uint
		Distance        float64
		SourceID        string
		SourceType      int
		ChunkID         string
		KnowledgeID     string
		KnowledgeBaseID string
		TagID           string
		Content         string
	}

	var rows []row
	if err := r.db.WithContext(ctx).
		Raw(vecSQL, args...).
		Scan(&rows).Error; err != nil {
		return nil, fmt.Errorf("sqlite-vec query failed: %w", err)
	}

	logger.GetLogger(ctx).Infof("[SQLite] vectorRetrieve: query_dim=%d, threshold=%.4f, matched=%d rows", dim, params.Threshold, len(rows))

	items := make([]*types.IndexWithScore, 0, len(rows))

	for i, v := range rows {
		// cosine distance = 1 - cosine_similarity
		score := 1 - v.Distance
		if params.Threshold > 0 && score < params.Threshold {
			continue
		}

		logger.GetLogger(ctx).Infof("[SQLite] vectorRetrieve: #%d chunk_id=%s, distance=%.4f, score=%.4f, content_preview=%.60s",
			i+1, v.ChunkID, v.Distance, score, v.Content)

		items = append(items, &types.IndexWithScore{
			ID:              fmt.Sprintf("%d", v.Rowid),
			SourceID:        v.SourceID,
			SourceType:      types.SourceType(v.SourceType),
			ChunkID:         v.ChunkID,
			KnowledgeID:     v.KnowledgeID,
			KnowledgeBaseID: v.KnowledgeBaseID,
			TagID:           v.TagID,
			Content:         v.Content,
			Score:           score,
			MatchType:       types.MatchTypeEmbedding,
		})
	}

	return []*types.RetrieveResult{{
		Results:             items,
		RetrieverEngineType: types.SQLiteRetrieverEngineType,
		RetrieverType:       types.VectorRetrieverType,
	}}, nil
}

// --- Internal helpers ---

func toSQLiteEmbedding(info *types.IndexInfo) *sqliteEmbedding {
	enabled := info.IsEnabled
	return &sqliteEmbedding{
		SourceID:        info.SourceID,
		SourceType:      int(info.SourceType),
		ChunkID:         info.ChunkID,
		KnowledgeID:     info.KnowledgeID,
		KnowledgeBaseID: info.KnowledgeBaseID,
		TagID:           info.TagID,
		Content:         common.CleanInvalidUTF8(info.Content),
		Dimension:       0,
		IsEnabled:       &enabled,
	}
}

func extractEmbedding(params map[string]any, sourceID string) []float32 {
	if params == nil {
		return nil
	}
	embMap, ok := params["embedding"].(map[string][]float32)
	if !ok {
		return nil
	}
	return embMap[sourceID]
}

// insertVec writes a row's vector through db. The vec0 table for dim must
// already exist (ensureVecTable), since db may be a transaction. Failures are
// returned so the caller's transaction rolls back instead of committing a
// chunk row whose vector row was dropped.
func (r *sqliteRepository) insertVec(db *gorm.DB, rowID uint, dim int, emb []float32) error {
	if rowID == 0 {
		return errors.New("[SQLite] cannot insert a vector row without a row id")
	}
	blob, err := sqlite_vec.SerializeFloat32(emb)
	if err != nil {
		return fmt.Errorf("[SQLite] failed to serialize embedding of row %d: %w", rowID, err)
	}
	tbl := vecTableName(dim)
	if err := db.Exec(
		fmt.Sprintf("INSERT INTO %s(rowid, embedding) VALUES (?, ?)", tbl), rowID, blob,
	).Error; err != nil {
		return fmt.Errorf("[SQLite] failed to insert vector row %d into %s: %w", rowID, tbl, err)
	}
	return nil
}

// deleteRowsAndVecs removes the vec0 and FTS copies of the given rows. Every
// failure is returned: deleting only the lite_embeddings rows would leave the
// retrieval indexes holding rows that no longer exist.
func (r *sqliteRepository) deleteRowsAndVecs(db *gorm.DB, rows []sqliteEmbedding) error {
	dimIDs := make(map[int][]uint)
	for _, row := range rows {
		if row.Dimension > 0 {
			dimIDs[row.Dimension] = append(dimIDs[row.Dimension], row.ID)
		}
	}
	for dim, ids := range dimIDs {
		if !r.hasVecTable(dim) {
			continue
		}
		tbl := vecTableName(dim)
		for _, id := range ids {
			if err := db.Exec(fmt.Sprintf("DELETE FROM %s WHERE rowid = ?", tbl), id).Error; err != nil {
				return fmt.Errorf("[SQLite] failed to delete vector row %d from %s: %w", id, tbl, err)
			}
		}
	}
	if r.ftsUnavailable {
		return nil
	}
	for _, row := range rows {
		if err := db.Exec("DELETE FROM lite_embeddings_fts WHERE rowid = ?", row.ID).Error; err != nil {
			return fmt.Errorf("[SQLite] failed to delete keyword index row %d: %w", row.ID, err)
		}
	}
	return nil
}

// copyVec duplicates a vector row for a copied chunk. It reports an error when
// the destination table cannot be prepared or the source row has no vector
// row, so a clone cannot silently end up without vectors.
func (r *sqliteRepository) copyVec(ctx context.Context, srcID, dstID uint, dim int) error {
	if err := r.ensureVecTable(dim); err != nil {
		return err
	}
	tbl := vecTableName(dim)
	result := r.db.WithContext(ctx).Exec(fmt.Sprintf(
		"INSERT INTO %s(rowid, embedding) SELECT ?, embedding FROM %s WHERE rowid = ?",
		tbl, tbl,
	), dstID, srcID)
	if result.Error != nil {
		r.unmarkVecTable(dim)
		return fmt.Errorf("[SQLite] failed to copy vector row %d -> %d in %s: %w",
			srcID, dstID, tbl, result.Error)
	}
	if result.RowsAffected == 0 {
		return fmt.Errorf("[SQLite] source row %d has no vector row in %s", srcID, tbl)
	}
	return nil
}

// syncFTS5Insert mirrors a lite_embeddings row into the keyword index. The
// error is returned so the caller's transaction rolls back instead of leaving
// the chunk unfindable by both retrieval paths.
func (r *sqliteRepository) syncFTS5Insert(db *gorm.DB, row *sqliteEmbedding) error {
	if r.ftsUnavailable {
		return nil
	}
	if row.ID == 0 {
		return fmt.Errorf("[SQLite] cannot index chunk %s: row has no id", row.SourceID)
	}
	if err := db.Exec(fts5InsertSQL, row.ID, tokenizeCJKBigram(row.Content), row.SourceID, row.ChunkID,
		row.KnowledgeID, row.KnowledgeBaseID).Error; err != nil {
		return fmt.Errorf("[SQLite] failed to insert keyword index row for %s: %w", row.SourceID, err)
	}
	return nil
}

type whereClause struct {
	clause string
	args   []interface{}
}

func buildFilterWhere(params types.RetrieveParams, tableAlias string) []whereClause {
	var parts []whereClause
	if len(params.KnowledgeBaseIDs) > 0 {
		parts = append(parts, whereClause{
			clause: tableAlias + ".knowledge_base_id IN (" + placeholders(len(params.KnowledgeBaseIDs)) + ")",
			args:   toInterfaceSlice(params.KnowledgeBaseIDs),
		})
	}
	if len(params.KnowledgeIDs) > 0 {
		parts = append(parts, whereClause{
			clause: tableAlias + ".knowledge_id IN (" + placeholders(len(params.KnowledgeIDs)) + ")",
			args:   toInterfaceSlice(params.KnowledgeIDs),
		})
	}
	if len(params.TagIDs) > 0 {
		parts = append(parts, whereClause{
			clause: tableAlias + ".tag_id IN (" + placeholders(len(params.TagIDs)) + ")",
			args:   toInterfaceSlice(params.TagIDs),
		})
	}
	return parts
}

func placeholders(n int) string {
	p := make([]string, n)
	for i := range p {
		p[i] = "?"
	}
	return strings.Join(p, ",")
}

func toInterfaceSlice(ss []string) []interface{} {
	out := make([]interface{}, len(ss))
	for i, s := range ss {
		out[i] = s
	}
	return out
}

// tokenizeCJKBigram splits continuous CJK character sequences into overlapping bigrams.
// Non-CJK words are kept intact. This approach maximizes recall for CJK search.
func tokenizeCJKBigram(text string) string {
	var parts []string
	var currentCJK []rune
	var currentNonCJK strings.Builder

	flushCJK := func() {
		if len(currentCJK) == 0 {
			return
		}
		if len(currentCJK) == 1 {
			parts = append(parts, string(currentCJK[0]))
		} else {
			for i := 0; i < len(currentCJK)-1; i++ {
				parts = append(parts, string(currentCJK[i])+string(currentCJK[i+1]))
			}
		}
		currentCJK = currentCJK[:0]
	}

	flushNonCJK := func() {
		if currentNonCJK.Len() > 0 {
			parts = append(parts, currentNonCJK.String())
			currentNonCJK.Reset()
		}
	}

	for _, r := range text {
		// unicode.Han covers Chinese characters
		if unicode.Is(unicode.Han, r) {
			flushNonCJK()
			currentCJK = append(currentCJK, r)
		} else if unicode.IsSpace(r) || unicode.IsPunct(r) || unicode.IsSymbol(r) {
			// Delimiters
			flushCJK()
			flushNonCJK()
		} else {
			// Alphanumeric or other languages
			flushCJK()
			currentNonCJK.WriteRune(r)
		}
	}
	flushCJK()
	flushNonCJK()

	return strings.Join(parts, " ")
}

// sanitizeFTS5Query builds an FTS5 query from user input by applying bigram tokenization.
func sanitizeFTS5Query(q string) string {
	q = strings.TrimSpace(q)
	if q == "" {
		return q
	}

	// Tokenize input query with bigrams (same as during indexing)
	tokenized := tokenizeCJKBigram(q)
	fields := strings.Fields(tokenized)

	var parts []string
	for _, f := range fields {
		if f == "" {
			continue
		}
		term := `"` + strings.ReplaceAll(f, `"`, `""`) + `"`
		// The index holds CJK text as bigrams, so a lone Han character
		// (the 股 of "A股") was never a token; match bigrams starting with it.
		if runes := []rune(f); len(runes) == 1 && unicode.Is(unicode.Han, runes[0]) {
			term += "*"
		}
		parts = append(parts, term)
	}

	if len(parts) == 0 {
		return ""
	}

	// Use OR because we want fuzzy match across multiple bigrams/words.
	// Only the content column is searched: the FTS table also indexes
	// source, chunk, knowledge and KB IDs, so a query for "2023" could match
	// a UUID fragment.
	return "content : (" + strings.Join(parts, " OR ") + ")"
}
