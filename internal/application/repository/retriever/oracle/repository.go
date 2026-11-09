// Package oracle implements the retriever repository on Oracle AI Database
// (23ai/26ai): AI Vector Search (VECTOR column + HNSW index, cosine) for
// vector retrieval and Oracle Text (CONTEXT index, WORLD_LEXER) for keywords.
//
// Like Doris, rows live in one table per embedding dimension: <BASE>_<dim>.
package oracle

import (
	"context"
	"database/sql"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/google/uuid"
)

const (
	defaultTableBaseName = "weknora_embeddings"
	envOracleTablePrefix = "ORACLE_TABLE_PREFIX"
)

type oracleRepository struct {
	db                *sql.DB
	tableBase         string
	initializedTables sync.Map // dim -> true
	ddlMu             sync.Mutex
}

// NewOracleRetrieveEngineRepository wraps an open *sql.DB (see
// utils.OpenOracleDB). indexCfg may be nil (env path: ORACLE_TABLE_PREFIX).
func NewOracleRetrieveEngineRepository(db *sql.DB, indexCfg *types.IndexConfig) interfaces.RetrieveEngineRepository {
	base := types.ResolveCollectionName(indexCfg, envOracleTablePrefix, defaultTableBaseName)
	repo := &oracleRepository{db: db, tableBase: sanitizeTableBase(base, defaultTableBaseName)}
	logger.GetLogger(context.Background()).Infof("[Oracle] Repository initialized: table base=%s", repo.tableBase)
	return repo
}

func (r *oracleRepository) EngineType() types.RetrieverEngineType {
	return types.OracleRetrieverEngineType
}

func (r *oracleRepository) Support() []types.RetrieverType {
	return []types.RetrieverType{types.KeywordsRetrieverType, types.VectorRetrieverType}
}

// ---------------------------------------------------------------------------
// Schema
// ---------------------------------------------------------------------------

// ensureTable makes sure the table and all its indexes exist, once per
// dimension per process. Index creation runs even when the table already
// exists, so an index lost to an earlier failure is created on the next start.
func (r *oracleRepository) ensureTable(ctx context.Context, dim int) error {
	if _, ok := r.initializedTables.Load(dim); ok {
		return nil
	}
	if dim <= 0 || dim > 65535 {
		return fmt.Errorf("invalid embedding dimension %d", dim)
	}
	// Documents are processed concurrently; serialize DDL within the process.
	r.ddlMu.Lock()
	defer r.ddlMu.Unlock()
	if _, ok := r.initializedTables.Load(dim); ok {
		return nil
	}

	// One connection so the session setting applies to every DDL below.
	conn, err := r.db.Conn(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close() }()
	// Other processes (app replicas) may run DDL on the same table: wait for
	// their locks instead of failing with ORA-00054 (resource busy).
	if _, err := conn.ExecContext(ctx, "ALTER SESSION SET DDL_LOCK_TIMEOUT = 60"); err != nil {
		logger.GetLogger(ctx).Warnf("[Oracle] Cannot set DDL_LOCK_TIMEOUT: %v", err)
	}

	table := tableName(r.tableBase, dim)
	var n int
	if err := conn.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM user_tables WHERE table_name = :1", table).Scan(&n); err != nil {
		return fmt.Errorf("check table %s: %w", table, err)
	}
	if n == 0 {
		if err := createTable(ctx, conn, table, dim); err != nil {
			return err
		}
	}
	ensureIndexes(ctx, conn, table)
	r.initializedTables.Store(dim, true)
	return nil
}

func (r *oracleRepository) tableExists(ctx context.Context, table string) (bool, error) {
	var n int
	err := r.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM user_tables WHERE table_name = :1", table).Scan(&n)
	return n > 0, err
}

func createTable(ctx context.Context, db execer, table string, dim int) error {
	logger.GetLogger(ctx).Infof("[Oracle] Creating table %s (VECTOR(%d, FLOAT32))", table, dim)
	ddl := fmt.Sprintf(`CREATE TABLE IF NOT EXISTS %s (
		id VARCHAR2(255) PRIMARY KEY,
		content CLOB,
		source_id VARCHAR2(255),
		source_type NUMBER(10) DEFAULT 0 NOT NULL,
		chunk_id VARCHAR2(255),
		knowledge_id VARCHAR2(255),
		knowledge_base_id VARCHAR2(255),
		tag_id VARCHAR2(255),
		is_enabled NUMBER(1) DEFAULT 1 NOT NULL,
		embedding VECTOR(%d, FLOAT32) NOT NULL)`, table, dim)
	if _, err := db.ExecContext(ctx, ddl); err != nil {
		return fmt.Errorf("create table %s: %w", table, err)
	}
	return nil
}

// ensureIndexes creates the table's indexes idempotently. None is mandatory
// for correctness: without the HNSW index (needs vector_memory_size > 0)
// vector search falls back to an exact scan, without the Oracle Text index
// (needs CTXSYS) keyword search returns nothing, and without the B-tree
// indexes filters and deletes get slower. Every failure is logged.
func ensureIndexes(ctx context.Context, db execer, table string) {
	for _, idx := range []struct{ suffix, col string }{
		{"CHK", fieldChunkID}, {"KNW", fieldKnowledgeID}, {"KB", fieldKnowledgeBaseID}, {"SRC", fieldSourceID},
	} {
		execIndexDDL(ctx, db, fmt.Sprintf("CREATE INDEX %s_%s ON %s (%s)", table, idx.suffix, table, idx.col))
	}

	// Oracle Text with WORLD_LEXER segments Japanese/Chinese/Korean as well as
	// space-delimited languages. SYNC (ON COMMIT) makes new rows searchable at once.
	lexer := table + "_LEX"
	lexerOK := execIndexDDL(ctx, db, fmt.Sprintf(`BEGIN
		CTX_DDL.CREATE_PREFERENCE('%s', 'WORLD_LEXER');
	EXCEPTION WHEN OTHERS THEN
		IF SQLCODE != -20000 THEN RAISE; END IF; -- DRG-10701: preference already exists
	END;`, lexer))
	textParams := "SYNC (ON COMMIT)"
	if lexerOK {
		textParams = "LEXER " + lexer + " " + textParams
	}
	execIndexDDL(ctx, db, fmt.Sprintf(
		"CREATE INDEX %s_TXT ON %s (content) INDEXTYPE IS CTXSYS.CONTEXT PARAMETERS ('%s')",
		table, table, textParams))

	execIndexDDL(ctx, db, fmt.Sprintf(`CREATE VECTOR INDEX %s_VEC ON %s (embedding)
		ORGANIZATION INMEMORY NEIGHBOR GRAPH DISTANCE COSINE WITH TARGET ACCURACY 95`, table, table))
}

// execIndexDDL runs one index DDL. "Already exists" outcomes count as success:
// ORA-00955 (name already used) and ORA-01408 (column list already indexed).
func execIndexDDL(ctx context.Context, db execer, ddl string) bool {
	_, err := db.ExecContext(ctx, ddl)
	if err == nil || strings.Contains(err.Error(), "ORA-00955") || strings.Contains(err.Error(), "ORA-01408") {
		return true
	}
	logger.GetLogger(ctx).Warnf(
		"[Oracle] Index DDL failed (search still works, possibly slower): %v; DDL: %s", err, ddl)
	return false
}

// listEmbeddingTables returns every <BASE>_<digits> table of this repository.
func (r *oracleRepository) listEmbeddingTables(ctx context.Context) ([]string, error) {
	rows, err := r.db.QueryContext(ctx, "SELECT table_name FROM user_tables WHERE table_name LIKE :1 ESCAPE '\\'",
		strings.ReplaceAll(r.tableBase, "_", "\\_")+"\\_%")
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []string
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			return nil, err
		}
		suffix := strings.TrimPrefix(n, r.tableBase+"_")
		if suffix != "" && strings.Trim(suffix, "0123456789") == "" {
			out = append(out, n)
		}
	}
	return out, rows.Err()
}

// ---------------------------------------------------------------------------
// Writes
// ---------------------------------------------------------------------------

type row struct {
	types.IndexInfo
	embedding []float32
}

func (r *oracleRepository) Save(ctx context.Context, info *types.IndexInfo, params map[string]any) error {
	return r.BatchSave(ctx, []*types.IndexInfo{info}, params)
}

// BatchSave upserts rows (delete by id + insert, in one transaction per dimension).
func (r *oracleRepository) BatchSave(ctx context.Context, infos []*types.IndexInfo, params map[string]any) error {
	log := logger.GetLogger(ctx)
	embeddings, _ := params[fieldEmbedding].(map[string][]float32)
	groups := map[int][]row{}
	for _, info := range infos {
		vec := embeddings[info.SourceID]
		if len(vec) == 0 {
			log.Warnf("[Oracle] Skipping empty embedding for chunk %s", info.ChunkID)
			continue
		}
		for i, v := range vec {
			if math.IsNaN(float64(v)) || math.IsInf(float64(v), 0) {
				return fmt.Errorf("invalid embedding for chunk %s: element %d is not finite", info.ChunkID, i)
			}
		}
		rw := row{IndexInfo: *info, embedding: vec}
		if rw.ID == "" {
			rw.ID = rw.SourceID
		}
		if rw.ID == "" {
			rw.ID = uuid.New().String()
		}
		groups[len(vec)] = append(groups[len(vec)], rw)
	}
	for dim, rows := range groups {
		rows = dedupeByID(rows)
		if err := r.ensureTable(ctx, dim); err != nil {
			return err
		}
		if err := r.replaceRows(ctx, tableName(r.tableBase, dim), rows); err != nil {
			return fmt.Errorf("batch save dim=%d: %w", dim, err)
		}
		log.Infof("[Oracle] Saved %d rows to %s", len(rows), tableName(r.tableBase, dim))
	}
	return nil
}

// writeBatchSize bounds rows per multi-row statement (10 binds per row, far
// below Oracle's 65535-bind limit) to cut round trips on high-latency links.
const writeBatchSize = 100

// replaceRows expects rows already deduplicated by ID (see dedupeByID).
func (r *oracleRepository) replaceRows(ctx context.Context, table string, rows []row) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint:errcheck // no-op after Commit

	ids := make([]string, len(rows))
	for i, rw := range rows {
		ids[i] = rw.ID
	}
	if err := deleteIn(ctx, tx, table, fieldID, ids); err != nil {
		return err
	}
	cols := strings.Join(insertColumns, ", ")
	dim := len(rows[0].embedding)
	for start := 0; start < len(rows); start += writeBatchSize {
		batch := rows[start:min(start+writeBatchSize, len(rows))]
		b := &binds{}
		var sb strings.Builder
		sb.WriteString("INSERT ALL")
		for _, rw := range batch {
			fmt.Fprintf(&sb, " INTO %s (%s) VALUES (%s, %s, %s, %s, %s, %s, %s, %s, %s, TO_VECTOR(%s, %d, FLOAT32))",
				table, cols,
				b.add(rw.ID), b.text(rw.Content), b.add(rw.SourceID),
				b.add(int(rw.SourceType)), b.add(rw.ChunkID), b.add(rw.KnowledgeID), b.add(rw.KnowledgeBaseID),
				b.add(rw.TagID), b.add(boolToInt(rw.IsEnabled)), b.text(vectorLiteral(rw.embedding)), dim)
		}
		sb.WriteString(" SELECT 1 FROM DUAL")
		if _, err := tx.ExecContext(ctx, sb.String(), b.args...); err != nil {
			return fmt.Errorf("insert %d rows: %w", len(batch), err)
		}
	}
	return tx.Commit()
}

// dedupeByID keeps the last row per ID so one batch never violates the PK.
func dedupeByID(rows []row) []row {
	pos := make(map[string]int, len(rows))
	out := rows[:0:0]
	for _, rw := range rows {
		if i, ok := pos[rw.ID]; ok {
			out[i] = rw
			continue
		}
		pos[rw.ID] = len(out)
		out = append(out, rw)
	}
	return out
}

type execer interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

func deleteIn(ctx context.Context, db execer, table, field string, values []string) error {
	for _, chunk := range chunkStrings(values, maxInListSize) {
		b := &binds{}
		q := fmt.Sprintf("DELETE FROM %s WHERE %s IN (%s)", table, field, b.in(chunk))
		if _, err := db.ExecContext(ctx, q, b.args...); err != nil {
			return fmt.Errorf("delete by %s: %w", field, err)
		}
	}
	return nil
}

func (r *oracleRepository) deleteByField(ctx context.Context, field string, ids []string, dim int) error {
	if len(ids) == 0 {
		return nil
	}
	table := tableName(r.tableBase, dim)
	if ok, err := r.tableExists(ctx, table); err != nil || !ok {
		return err
	}
	return deleteIn(ctx, r.db, table, field, ids)
}

func (r *oracleRepository) DeleteByChunkIDList(ctx context.Context, ids []string, dim int, _ string) error {
	return r.deleteByField(ctx, fieldChunkID, ids, dim)
}

func (r *oracleRepository) DeleteBySourceIDList(ctx context.Context, ids []string, dim int, _ string) error {
	return r.deleteByField(ctx, fieldSourceID, ids, dim)
}

func (r *oracleRepository) DeleteByKnowledgeIDList(ctx context.Context, ids []string, dim int, _ string) error {
	return r.deleteByField(ctx, fieldKnowledgeID, ids, dim)
}

// BatchUpdateChunkEnabledStatus updates is_enabled in every dimension table.
func (r *oracleRepository) BatchUpdateChunkEnabledStatus(ctx context.Context, m map[string]bool) error {
	byValue := map[any][]string{}
	for id, v := range m {
		byValue[boolToInt(v)] = append(byValue[boolToInt(v)], id)
	}
	return r.updateChunks(ctx, fieldIsEnabled, byValue)
}

// BatchUpdateChunkTagID updates tag_id in every dimension table.
func (r *oracleRepository) BatchUpdateChunkTagID(ctx context.Context, m map[string]string) error {
	byValue := map[any][]string{}
	for id, v := range m {
		byValue[v] = append(byValue[v], id)
	}
	return r.updateChunks(ctx, fieldTagID, byValue)
}

func (r *oracleRepository) updateChunks(ctx context.Context, field string, byValue map[any][]string) error {
	if len(byValue) == 0 {
		return nil
	}
	tables, err := r.listEmbeddingTables(ctx)
	if err != nil {
		return fmt.Errorf("list tables: %w", err)
	}
	for _, table := range tables {
		for value, ids := range byValue {
			for _, chunk := range chunkStrings(ids, maxInListSize) {
				b := &binds{}
				q := fmt.Sprintf("UPDATE %s SET %s = %s WHERE %s IN (%s)",
					table, field, b.add(value), fieldChunkID, b.in(chunk))
				if _, err := r.db.ExecContext(ctx, q, b.args...); err != nil {
					return fmt.Errorf("update %s in %s: %w", field, table, err)
				}
			}
		}
	}
	return nil
}

// MoveKnowledgeIndices re-parents one knowledge's rows to another KB in place.
func (r *oracleRepository) MoveKnowledgeIndices(ctx context.Context,
	sourceKB, targetKB, knowledgeID string, _ []string, dim int, _ string,
) error {
	if dim <= 0 {
		return fmt.Errorf("invalid embedding dimension")
	}
	_, err := r.db.ExecContext(ctx, fmt.Sprintf(
		"UPDATE %s SET knowledge_base_id = :1, tag_id = NULL WHERE knowledge_base_id = :2 AND knowledge_id = :3",
		tableName(r.tableBase, dim)), targetKB, sourceKB, knowledgeID)
	return err
}

// CopyIndices duplicates a KB's rows server-side (INSERT ... SELECT), so the
// vectors never leave the database.
func (r *oracleRepository) CopyIndices(ctx context.Context,
	sourceKB string, knowledgeMap, chunkMap map[string]string, targetKB string, dim int, _ string,
) error {
	if len(chunkMap) == 0 {
		return nil
	}
	if err := r.ensureTable(ctx, dim); err != nil {
		return err
	}
	table := tableName(r.tableBase, dim)
	copied := 0
	// Page through the source rows like Doris does, so a large knowledge base
	// is never held in memory at once. Each page's cursor is closed before
	// its rows are written; the writes go to targetKB, so they do not shift
	// the OFFSET of the source pages.
	for offset := 0; ; offset += writeBatchSize {
		sources, err := r.copySourcePage(ctx, table, sourceKB, offset)
		if err != nil {
			return err
		}
		maps := make([]copyMapping, 0, len(sources))
		for _, s := range sources {
			targetChunk, ok := chunkMap[s.chunkID]
			if !ok {
				continue
			}
			targetKnowledge, ok := knowledgeMap[s.knowledgeID]
			if !ok {
				continue
			}
			maps = append(maps, copyMapping{
				s.id, uuid.New().String(), translateSourceID(s.sourceID, s.chunkID, targetChunk),
				targetChunk, targetKnowledge,
			})
		}
		n, err := r.copyBatch(ctx, table, targetKB, maps)
		if err != nil {
			return err
		}
		copied += n
		if len(sources) < writeBatchSize {
			break
		}
	}
	logger.GetLogger(ctx).Infof("[Oracle] CopyIndices done, dim=%d, copied=%d", dim, copied)
	return nil
}

type copySource struct{ id, sourceID, chunkID, knowledgeID string }

type copyMapping struct{ oldID, newID, sourceID, chunkID, knowledgeID string }

// copySourcePage reads one page (writeBatchSize rows) of a knowledge base's
// rows, ordered by id. The cursor is closed when it returns.
func (r *oracleRepository) copySourcePage(ctx context.Context, table, sourceKB string, offset int,
) ([]copySource, error) {
	rows, err := r.db.QueryContext(ctx, fmt.Sprintf(
		"SELECT id, source_id, chunk_id, knowledge_id FROM %s WHERE knowledge_base_id = :1 "+
			"ORDER BY id OFFSET :2 ROWS FETCH NEXT :3 ROWS ONLY", table), sourceKB, offset, writeBatchSize)
	if err != nil {
		return nil, fmt.Errorf("copy indices scan: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var sources []copySource
	for rows.Next() {
		var s copySource
		var sourceID, chunkID, knowledgeID sql.NullString
		if err := rows.Scan(&s.id, &sourceID, &chunkID, &knowledgeID); err != nil {
			return nil, err
		}
		s.sourceID, s.chunkID, s.knowledgeID = sourceID.String, chunkID.String, knowledgeID.String
		sources = append(sources, s)
	}
	return sources, rows.Err()
}

// copyBatch copies one batch with a single INSERT ... SELECT joined to an
// inline id-mapping table, so vectors are copied server-side without per-row
// round trips. It returns the number of rows inserted.
func (r *oracleRepository) copyBatch(ctx context.Context, table, targetKB string, batch []copyMapping) (int, error) {
	if len(batch) == 0 {
		return 0, nil
	}
	b := &binds{}
	kb := b.add(targetKB)
	selects := make([]string, len(batch))
	for i, m := range batch {
		selects[i] = fmt.Sprintf("SELECT %s AS old_id, %s AS new_id, %s AS new_source, "+
			"%s AS new_chunk, %s AS new_knowledge FROM dual",
			b.add(m.oldID), b.add(m.newID), b.add(m.sourceID), b.add(m.chunkID), b.add(m.knowledgeID))
	}
	q := fmt.Sprintf(`INSERT INTO %[1]s (%[2]s)
		SELECT m.new_id, s.content, m.new_source, s.source_type, m.new_chunk, m.new_knowledge, %[3]s,
			s.tag_id, s.is_enabled, s.embedding
		FROM %[1]s s JOIN (%[4]s) m ON s.id = m.old_id`,
		table, strings.Join(insertColumns, ", "), kb, strings.Join(selects, " UNION ALL "))
	res, err := r.db.ExecContext(ctx, q, b.args...)
	if err != nil {
		return 0, fmt.Errorf("copy indices insert: %w", err)
	}
	n, _ := res.RowsAffected()
	return int(n), nil
}

// translateSourceID mirrors the Qdrant/Doris rule: plain chunk -> target chunk,
// generated question "<chunk>-<q>" -> "<target>-<q>", anything else -> new UUID.
func translateSourceID(sourceID, sourceChunkID, targetChunkID string) string {
	switch {
	case sourceID == sourceChunkID:
		return targetChunkID
	case strings.HasPrefix(sourceID, sourceChunkID+"-"):
		return targetChunkID + "-" + strings.TrimPrefix(sourceID, sourceChunkID+"-")
	default:
		return uuid.New().String()
	}
}

// ---------------------------------------------------------------------------
// Retrieval
// ---------------------------------------------------------------------------

func (r *oracleRepository) Retrieve(ctx context.Context, params types.RetrieveParams) ([]*types.RetrieveResult, error) {
	switch params.RetrieverType {
	case types.VectorRetrieverType:
		return r.vectorRetrieve(ctx, params)
	case types.KeywordsRetrieverType:
		return r.keywordsRetrieve(ctx, params)
	}
	return nil, fmt.Errorf("invalid retriever type: %v", params.RetrieverType)
}

// vectorRetrieve runs an approximate (HNSW) cosine search. The similarity
// threshold is applied outside the FETCH APPROX subquery so it doesn't stop
// the optimizer from using the vector index.
func (r *oracleRepository) vectorRetrieve(
	ctx context.Context, params types.RetrieveParams,
) ([]*types.RetrieveResult, error) {
	dim := len(params.Embedding)
	table := tableName(r.tableBase, dim)
	if dim == 0 {
		return result(nil, types.VectorRetrieverType), nil
	}
	if ok, err := r.tableExists(ctx, table); err != nil {
		return nil, fmt.Errorf("check table %s: %w", table, err)
	} else if !ok {
		return result(nil, types.VectorRetrieverType), nil
	}
	vec := vectorLiteral(params.Embedding)
	b := &binds{}
	cols := strings.Join(columnsForRetrieve, ", ")
	distSelect := fmt.Sprintf("VECTOR_DISTANCE(embedding, TO_VECTOR(%s, %d, FLOAT32), COSINE)", b.text(vec), dim)
	where := whereFilter(b, params)
	distOrder := fmt.Sprintf("VECTOR_DISTANCE(embedding, TO_VECTOR(%s, %d, FLOAT32), COSINE)", b.text(vec), dim)
	q := fmt.Sprintf(`SELECT %[1]s, 1 - dist AS score FROM (
		SELECT %[1]s, %[2]s AS dist FROM %[3]s WHERE %[4]s
		ORDER BY %[5]s FETCH APPROX FIRST %[6]d ROWS ONLY
	) WHERE 1 - dist >= %[7]s ORDER BY dist`,
		cols, distSelect, table, where, distOrder, validTopK(params.TopK), b.add(params.Threshold))
	rows, err := r.db.QueryContext(ctx, q, b.args...)
	if err != nil {
		return nil, fmt.Errorf("vector retrieve %s: %w", table, err)
	}
	defer func() { _ = rows.Close() }()
	out, err := scanResults(rows, types.MatchTypeEmbedding)
	if err != nil {
		return nil, err
	}
	logger.GetLogger(ctx).Infof("[Oracle] Vector retrieval found %d results in %s", len(out), table)
	return result(out, types.VectorRetrieverType), nil
}

// keywordsRetrieve uses Oracle Text CONTAINS across all dimension tables;
// score is SCORE(1) scaled to [0,1].
func (r *oracleRepository) keywordsRetrieve(
	ctx context.Context, params types.RetrieveParams,
) ([]*types.RetrieveResult, error) {
	log := logger.GetLogger(ctx)
	textQuery := buildTextQuery(params.Query)
	if textQuery == "" {
		return result(nil, types.KeywordsRetrieverType), nil
	}
	tables, err := r.listEmbeddingTables(ctx)
	if err != nil {
		return nil, fmt.Errorf("list tables: %w", err)
	}
	topK := validTopK(params.TopK)
	var all []*types.IndexWithScore
	for _, table := range tables {
		b := &binds{}
		contains := fmt.Sprintf("CONTAINS(content, %s, 1) > 0", b.add(textQuery))
		q := fmt.Sprintf("SELECT %s, SCORE(1) / 100 AS score FROM %s WHERE %s AND %s "+
			"ORDER BY SCORE(1) DESC FETCH FIRST %d ROWS ONLY",
			strings.Join(columnsForRetrieve, ", "), table, contains, whereFilter(b, params), topK)
		rows, err := r.db.QueryContext(ctx, q, b.args...)
		if err != nil {
			log.Warnf("[Oracle] Keyword retrieve in %s failed: %v", table, err)
			continue
		}
		batch, err := scanResults(rows, types.MatchTypeKeywords)
		_ = rows.Close()
		if err != nil {
			return nil, err
		}
		all = append(all, batch...)
	}
	if len(tables) > 1 {
		sort.SliceStable(all, func(i, j int) bool { return all[i].Score > all[j].Score })
	}
	if len(all) > topK {
		all = all[:topK]
	}
	log.Infof("[Oracle] Keywords retrieval found %d results across %d tables", len(all), len(tables))
	return result(all, types.KeywordsRetrieverType), nil
}

func scanResults(rows *sql.Rows, matchType types.MatchType) ([]*types.IndexWithScore, error) {
	var out []*types.IndexWithScore
	for rows.Next() {
		var (
			id                                                   string
			content, sourceID, chunkID, knowledgeID, kbID, tagID sql.NullString
			sourceType, isEnabled                                int
			score                                                float64
		)
		err := rows.Scan(&id, &content, &sourceID, &sourceType,
			&chunkID, &knowledgeID, &kbID, &tagID, &isEnabled, &score)
		if err != nil {
			return nil, fmt.Errorf("scan row: %w", err)
		}
		out = append(out, &types.IndexWithScore{
			ID: id, Content: content.String, SourceID: sourceID.String, SourceType: types.SourceType(sourceType),
			ChunkID: chunkID.String, KnowledgeID: knowledgeID.String, KnowledgeBaseID: kbID.String, TagID: tagID.String,
			Score: math.Max(0, math.Min(1, score)), MatchType: matchType,
		})
	}
	return out, rows.Err()
}

func result(results []*types.IndexWithScore, rt types.RetrieverType) []*types.RetrieveResult {
	return []*types.RetrieveResult{{
		Results:             results,
		RetrieverEngineType: types.OracleRetrieverEngineType,
		RetrieverType:       rt,
	}}
}

// EstimateStorageSize: payload bytes + vector (dim*4) + HNSW neighbours + row overhead.
func (r *oracleRepository) EstimateStorageSize(
	_ context.Context, infos []*types.IndexInfo, params map[string]any,
) int64 {
	embeddings, _ := params[fieldEmbedding].(map[string][]float32)
	var total int64
	for _, info := range infos {
		total += int64(len(info.Content)+len(info.SourceID)+len(info.ChunkID)+
			len(info.KnowledgeID)+len(info.KnowledgeBaseID)+len(info.TagID)) + 8 + 24
		if dim := len(embeddings[info.SourceID]); dim > 0 {
			total += int64(dim)*4 + 32*2*8
		}
	}
	return total
}

// vectorLiteral renders "[v1,v2,...]" for TO_VECTOR (bound via binds.text).
//
// ponytail: go-ora v2.9.0's native Vector bind corrupts values ([1,0,0] is
// stored as [1,NaN,NaN]); switch to go_ora.NewVector once that is fixed.
func vectorLiteral(vec []float32) string {
	var sb strings.Builder
	sb.Grow(len(vec)*12 + 2)
	sb.WriteByte('[')
	for i, v := range vec {
		if i > 0 {
			sb.WriteByte(',')
		}
		sb.WriteString(strconv.FormatFloat(float64(v), 'g', -1, 32))
	}
	sb.WriteByte(']')
	return sb.String()
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
