package mysql

import (
	"context"
	"database/sql"
	"math"
	"regexp"
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Tencent/WeKnora/internal/types"
)

func TestBuildVectorWhereClause(t *testing.T) {
	params := types.RetrieveParams{
		KnowledgeBaseIDs:    []string{"kb1", "kb2"},
		KnowledgeIDs:        []string{"k1"},
		TagIDs:              []string{"tag1"},
		ExcludeKnowledgeIDs: []string{"k2"},
		ExcludeChunkIDs:     []string{"c1", "c2"},
	}

	where, args := buildVectorWhereClause(params).build()
	for _, want := range []string{
		"knowledge_base_id IN (?,?)",
		"knowledge_id IN (?)",
		"tag_id IN (?)",
		"knowledge_id NOT IN (?)",
		"chunk_id NOT IN (?,?)",
		"is_enabled",
	} {
		if !strings.Contains(where, want) {
			t.Fatalf("where clause missing %q in %q", want, where)
		}
	}
	if len(args) != 7 {
		t.Fatalf("args len = %d, want 7", len(args))
	}
}

func TestBuildVectorCandidateSQLIsBoundedFor1024Dimensions(t *testing.T) {
	params := types.RetrieveParams{
		Embedding:        make([]float32, 1024),
		KnowledgeBaseIDs: []string{"kb1"},
	}
	stmt, args := buildVectorCandidateSQL("weknora_embeddings_1024", params)

	if len(stmt) > 256 {
		t.Fatalf("candidate SQL length = %d, want <= 256: %s", len(stmt), stmt)
	}
	for _, forbidden := range []string{"JSON_EXTRACT", "JSON_ARRAY", "COSINE", "0.0"} {
		if strings.Contains(stmt, forbidden) {
			t.Fatalf("candidate SQL contains dimension-dependent fragment %q: %s", forbidden, stmt)
		}
	}
	if len(args) != 1 || args[0] != "kb1" {
		t.Fatalf("candidate args = %#v, want [kb1]", args)
	}
}

func TestVectorRetrieveRanksExactlyAndDeterministically(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New() error = %v", err)
	}
	defer db.Close()

	repo := &mysqlRepository{db: db, database: "weknora", tablePrefix: defaultTablePrefix}
	params := types.RetrieveParams{
		Embedding:        []float32{1, 0},
		KnowledgeBaseIDs: []string{"kb-1"},
		Threshold:        0.5,
		TopK:             2,
	}
	table := "weknora_embeddings_2"

	mock.ExpectQuery(regexp.QuoteMeta(`SELECT COUNT(1) FROM information_schema.tables
		WHERE TABLE_SCHEMA = ? AND TABLE_NAME = ?`)).
		WithArgs("weknora", table).
		WillReturnRows(sqlmock.NewRows([]string{"COUNT(1)"}).AddRow(1))
	mock.ExpectBegin()

	candidateSQL, _ := buildVectorCandidateSQL(table, params)
	mock.ExpectQuery(regexp.QuoteMeta(candidateSQL)).
		WithArgs("kb-1").
		WillReturnRows(sqlmock.NewRows([]string{"id", "embedding"}).
			AddRow("b", []byte(`[1,0]`)).
			AddRow("c", []byte(`[0.8,0.6]`)).
			AddRow("a", []byte(`[1,0]`)).
			AddRow("below-threshold", []byte(`[0,1]`)))

	hits := []rankedVectorHit{{id: "a", score: 1}, {id: "b", score: 1}}
	metadataSQL, _ := buildVectorMetadataSQL(table, params, hits)
	mock.ExpectQuery(regexp.QuoteMeta(metadataSQL)).
		WithArgs("a", "b", "kb-1").
		WillReturnRows(sqlmock.NewRows(columnsForRetrieve).
			AddRow("b", "content-b", "source-b", int(types.ChunkSourceType),
				"chunk-b", "knowledge-b", "kb-1", "tag-b", true).
			AddRow("a", "content-a", "source-a", int(types.ChunkSourceType),
				"chunk-a", "knowledge-a", "kb-1", "tag-a", nil))
	mock.ExpectCommit()

	got, err := repo.VectorRetrieve(context.Background(), params)
	if err != nil {
		t.Fatalf("VectorRetrieve() error = %v", err)
	}
	if len(got) != 1 || len(got[0].Results) != 2 {
		t.Fatalf("VectorRetrieve() = %#v, want one result group with two hits", got)
	}
	results := got[0].Results
	if results[0].ID != "a" || results[1].ID != "b" {
		t.Fatalf("result order = [%s, %s], want [a, b]", results[0].ID, results[1].ID)
	}
	if results[0].Score != 1 || results[1].Score != 1 {
		t.Fatalf("scores = [%v, %v], want [1, 1]", results[0].Score, results[1].Score)
	}
	if !results[0].IsEnabled {
		t.Fatal("NULL is_enabled should be treated as enabled")
	}
	if results[0].MatchType != types.MatchTypeEmbedding {
		t.Fatalf("match type = %v, want embedding", results[0].MatchType)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations: %v", err)
	}
}

func TestVectorRetrieveRejectsStoredDimensionMismatch(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New() error = %v", err)
	}
	defer db.Close()

	repo := &mysqlRepository{db: db, database: "weknora", tablePrefix: defaultTablePrefix}
	params := types.RetrieveParams{
		Embedding:        []float32{1, 0},
		KnowledgeBaseIDs: []string{"kb-1"},
		TopK:             1,
	}
	table := "weknora_embeddings_2"
	mock.ExpectQuery(regexp.QuoteMeta(`SELECT COUNT(1) FROM information_schema.tables
		WHERE TABLE_SCHEMA = ? AND TABLE_NAME = ?`)).
		WithArgs("weknora", table).
		WillReturnRows(sqlmock.NewRows([]string{"COUNT(1)"}).AddRow(1))
	mock.ExpectBegin()
	candidateSQL, _ := buildVectorCandidateSQL(table, params)
	mock.ExpectQuery(regexp.QuoteMeta(candidateSQL)).
		WithArgs("kb-1").
		WillReturnRows(sqlmock.NewRows([]string{"id", "embedding"}).
			AddRow("bad-dimension", []byte(`[1]`)))
	mock.ExpectRollback()

	_, err = repo.VectorRetrieve(context.Background(), params)
	if err == nil || !strings.Contains(err.Error(), "dimension 1 does not match query dimension 2") {
		t.Fatalf("VectorRetrieve() error = %v, want explicit dimension mismatch", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations: %v", err)
	}
}

func TestVectorRetrieveRejectsNonFiniteQueryBeforeDatabaseAccess(t *testing.T) {
	repo := &mysqlRepository{}
	_, err := repo.VectorRetrieve(context.Background(), types.RetrieveParams{
		Embedding: []float32{float32(math.NaN())},
	})
	if err == nil || !strings.Contains(err.Error(), "non-finite") {
		t.Fatalf("VectorRetrieve() error = %v, want non-finite validation error", err)
	}
}

func TestVectorRetrieveRejectsZeroNormQueryBeforeDatabaseAccess(t *testing.T) {
	repo := &mysqlRepository{}
	_, err := repo.VectorRetrieve(context.Background(), types.RetrieveParams{
		Embedding: []float32{0, 0},
	})
	if err == nil || !strings.Contains(err.Error(), "zero norm") {
		t.Fatalf("VectorRetrieve() error = %v, want zero-norm validation error", err)
	}
}

func TestRankVectorCandidatesAppliesThreshold(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New() error = %v", err)
	}
	defer db.Close()

	mock.ExpectQuery("candidates").WillReturnRows(sqlmock.NewRows([]string{"id", "embedding"}).
		AddRow("same", []byte(`[1,0]`)).
		AddRow("above", []byte(`[0.8,0.6]`)).
		AddRow("below", []byte(`[0,1]`)))
	rows, err := db.QueryContext(context.Background(), "candidates")
	if err != nil {
		t.Fatalf("query candidates: %v", err)
	}
	defer rows.Close()

	query := []float32{1, 0}
	queryNorm, err := validateQueryEmbedding(query)
	if err != nil {
		t.Fatalf("validateQueryEmbedding() error = %v", err)
	}
	hits, err := rankVectorCandidates(rows, query, queryNorm, 0.5, 10)
	if err != nil {
		t.Fatalf("rankVectorCandidates() error = %v", err)
	}
	if len(hits) != 2 || hits[0].id != "same" || hits[1].id != "above" {
		t.Fatalf("rankVectorCandidates() = %#v, want same and above", hits)
	}
}

func TestRankVectorCandidatesZeroThresholdDoesNotFilterNegativeSimilarity(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New() error = %v", err)
	}
	defer db.Close()

	mock.ExpectQuery("candidates").WillReturnRows(sqlmock.NewRows([]string{"id", "embedding"}).
		AddRow("anti-correlated", []byte(`[-1,0]`)))
	rows, err := db.QueryContext(context.Background(), "candidates")
	if err != nil {
		t.Fatalf("query candidates: %v", err)
	}
	defer rows.Close()

	query := []float32{1, 0}
	queryNorm, err := validateQueryEmbedding(query)
	if err != nil {
		t.Fatalf("validateQueryEmbedding() error = %v", err)
	}
	hits, err := rankVectorCandidates(rows, query, queryNorm, 0, 1)
	if err != nil {
		t.Fatalf("rankVectorCandidates() error = %v", err)
	}
	if len(hits) != 1 || hits[0].id != "anti-correlated" || hits[0].score >= 0 {
		t.Fatalf("rankVectorCandidates() = %#v, want the negative-similarity candidate", hits)
	}
}

func TestBuildKeywordRetrieveSQLPlacesFullTextQueryBeforeFilters(t *testing.T) {
	params := types.RetrieveParams{
		Query:            "hello mysql",
		KnowledgeBaseIDs: []string{"kb1", "kb2"},
		KnowledgeIDs:     []string{"k1"},
		TagIDs:           []string{"tag1"},
		TopK:             3,
		Threshold:        0.42,
	}

	stmt, args := buildKeywordRetrieveSQL("weknora_embeddings_3", params)
	for _, want := range []string{
		"MATCH(content) AGAINST(? IN NATURAL LANGUAGE MODE) AS score",
		"knowledge_base_id IN (?,?)",
		"knowledge_id IN (?)",
		"tag_id IN (?)",
		"HAVING score >= ?",
		"ORDER BY score DESC, id COLLATE utf8mb4_bin ASC",
		"LIMIT 3",
	} {
		if !strings.Contains(stmt, want) {
			t.Fatalf("keyword SQL missing %q in %s", want, stmt)
		}
	}

	wantArgs := []interface{}{"hello mysql", "kb1", "kb2", "k1", "tag1", "hello mysql", 0.42}
	if len(args) != len(wantArgs) {
		t.Fatalf("args len = %d, want %d: %#v", len(args), len(wantArgs), args)
	}
	for i := range wantArgs {
		if args[i] != wantArgs[i] {
			t.Fatalf("args[%d] = %#v, want %#v; all args=%#v", i, args[i], wantArgs[i], args)
		}
	}
}

func TestBatchUpdateChunkEnabledStatusUpdatesEveryEmbeddingTable(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New() error = %v", err)
	}
	defer db.Close()
	mock.MatchExpectationsInOrder(false)

	repo := &mysqlRepository{db: db, database: "weknora", tablePrefix: defaultTablePrefix}
	mock.ExpectQuery(regexp.QuoteMeta(`SELECT TABLE_NAME FROM information_schema.tables
		WHERE TABLE_SCHEMA = ? AND TABLE_NAME LIKE ? ESCAPE '\\'`)).
		WithArgs("weknora", escapeLikePattern(defaultTablePrefix)+"%").
		WillReturnRows(sqlmock.NewRows([]string{"TABLE_NAME"}).AddRow("weknora_embeddings_768"))
	mock.ExpectExec(regexp.QuoteMeta("UPDATE `weknora_embeddings_768` SET is_enabled = ? WHERE chunk_id IN (?)")).
		WithArgs(true, "chunk-enabled").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(regexp.QuoteMeta("UPDATE `weknora_embeddings_768` SET is_enabled = ? WHERE chunk_id IN (?)")).
		WithArgs(false, "chunk-disabled").
		WillReturnResult(sqlmock.NewResult(0, 1))

	err = repo.BatchUpdateChunkEnabledStatus(context.Background(), map[string]bool{
		"chunk-enabled":  true,
		"chunk-disabled": false,
	})
	if err != nil {
		t.Fatalf("BatchUpdateChunkEnabledStatus() error = %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations: %v", err)
	}
}

func TestBatchUpdateChunkTagIDUpdatesEveryEmbeddingTable(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New() error = %v", err)
	}
	defer db.Close()

	repo := &mysqlRepository{db: db, database: "weknora", tablePrefix: defaultTablePrefix}
	mock.ExpectQuery(regexp.QuoteMeta(`SELECT TABLE_NAME FROM information_schema.tables
		WHERE TABLE_SCHEMA = ? AND TABLE_NAME LIKE ? ESCAPE '\\'`)).
		WithArgs("weknora", escapeLikePattern(defaultTablePrefix)+"%").
		WillReturnRows(sqlmock.NewRows([]string{"TABLE_NAME"}).AddRow("weknora_embeddings_1024"))
	mock.ExpectExec(regexp.QuoteMeta("UPDATE `weknora_embeddings_1024` SET tag_id = ? WHERE chunk_id IN (?)")).
		WithArgs("tag-a", "chunk-a").
		WillReturnResult(sqlmock.NewResult(0, 1))

	err = repo.BatchUpdateChunkTagID(context.Background(), map[string]string{"chunk-a": "tag-a"})
	if err != nil {
		t.Fatalf("BatchUpdateChunkTagID() error = %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations: %v", err)
	}
}

func TestInsertRowsUpsertsMutableFields(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New() error = %v", err)
	}
	defer db.Close()

	repo := &mysqlRepository{db: db}
	mock.ExpectExec("ON DUPLICATE KEY UPDATE .*content=VALUES\\(content\\).*embedding=VALUES\\(embedding\\)").
		WithArgs(
			"faq-1", "updated content", "faq-1", int(types.ChunkSourceType),
			"chunk-1", "knowledge-1", "kb-1", "tag-1", true, "[0.1,0.2]",
		).
		WillReturnResult(sqlmock.NewResult(0, 1))

	err = repo.insertRows(context.Background(), "weknora_embeddings_2", []*MysqlVectorEmbedding{{
		ID:              "faq-1",
		Content:         "updated content",
		SourceID:        "faq-1",
		SourceType:      int(types.ChunkSourceType),
		ChunkID:         "chunk-1",
		KnowledgeID:     "knowledge-1",
		KnowledgeBaseID: "kb-1",
		TagID:           "tag-1",
		IsEnabled:       true,
		Embedding:       []float32{0.1, 0.2},
	}})
	if err != nil {
		t.Fatalf("insertRows() error = %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations: %v", err)
	}
}

func TestBatchSaveRejectsKeywordOnlyRowsWithoutDimension(t *testing.T) {
	repo := &mysqlRepository{}
	err := repo.BatchSave(context.Background(), []*types.IndexInfo{{
		ID:       "keyword-only",
		Content:  "keyword-only content",
		SourceID: "keyword-only",
	}}, nil)
	if err == nil || !strings.Contains(err.Error(), "positive dimension") {
		t.Fatalf("BatchSave() error = %v, want positive-dimension error", err)
	}
}

func TestInsertRowsStoresKeywordOnlyEmbeddingAsNull(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New() error = %v", err)
	}
	defer db.Close()

	repo := &mysqlRepository{db: db}
	mock.ExpectExec("INSERT INTO").
		WithArgs(
			"keyword-only", "keyword-only content", "keyword-only", int(types.ChunkSourceType),
			"chunk-1", "knowledge-1", "kb-1", "tag-1", true, nil,
		).
		WillReturnResult(sqlmock.NewResult(0, 1))
	err = repo.insertRows(context.Background(), "weknora_embeddings_768", []*MysqlVectorEmbedding{{
		ID:              "keyword-only",
		Content:         "keyword-only content",
		SourceID:        "keyword-only",
		SourceType:      int(types.ChunkSourceType),
		ChunkID:         "chunk-1",
		KnowledgeID:     "knowledge-1",
		KnowledgeBaseID: "kb-1",
		TagID:           "tag-1",
		IsEnabled:       true,
	}})
	if err != nil {
		t.Fatalf("insertRows() error = %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations: %v", err)
	}
}

func TestInsertRowsKeepsSQLBoundedFor1024Dimensions(t *testing.T) {
	var actualSQL string
	matcher := sqlmock.QueryMatcherFunc(func(_, actual string) error {
		actualSQL = actual
		return nil
	})
	db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(matcher))
	if err != nil {
		t.Fatalf("sqlmock.New() error = %v", err)
	}
	defer db.Close()

	vector := make([]float32, 1024)
	for i := range vector {
		vector[i] = float32(i) / 1024
	}
	repo := &mysqlRepository{db: db}
	mock.ExpectExec("capture insert").
		WithArgs(
			"id", "content", "source", int(types.ChunkSourceType),
			"chunk", "knowledge", "kb", "tag", true, sqlmock.AnyArg(),
		).
		WillReturnResult(sqlmock.NewResult(0, 1))

	err = repo.insertRows(context.Background(), "weknora_embeddings_1024", []*MysqlVectorEmbedding{{
		ID:              "id",
		Content:         "content",
		SourceID:        "source",
		SourceType:      int(types.ChunkSourceType),
		ChunkID:         "chunk",
		KnowledgeID:     "knowledge",
		KnowledgeBaseID: "kb",
		TagID:           "tag",
		IsEnabled:       true,
		Embedding:       vector,
	}})
	if err != nil {
		t.Fatalf("insertRows() error = %v", err)
	}
	if len(actualSQL) > 1024 {
		t.Fatalf("insert SQL length = %d, want <= 1024", len(actualSQL))
	}
	if strings.Contains(actualSQL, "JSON_ARRAY") || strings.Count(actualSQL, "?") != 10 {
		t.Fatalf("insert SQL is not fully parameterized: %s", actualSQL)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations: %v", err)
	}
}

func TestDeleteBySourceIDListNoopsWhenDimensionTableMissing(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New() error = %v", err)
	}
	defer db.Close()

	repo := &mysqlRepository{db: db, database: "weknora", tablePrefix: defaultTablePrefix}
	mock.ExpectQuery(regexp.QuoteMeta(`SELECT COUNT(1) FROM information_schema.tables
		WHERE TABLE_SCHEMA = ? AND TABLE_NAME = ?`)).
		WithArgs("weknora", "weknora_embeddings_768").
		WillReturnRows(sqlmock.NewRows([]string{"COUNT(1)"}).AddRow(0))

	if err := repo.DeleteBySourceIDList(context.Background(), []string{"faq-1"}, 768, ""); err != nil {
		t.Fatalf("DeleteBySourceIDList() error = %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations: %v", err)
	}
}

func TestLimitTopKByScoreSortsGlobally(t *testing.T) {
	rows := []*types.IndexWithScore{
		{ID: "low", Score: 0.1},
		{ID: "high", Score: 0.9},
		{ID: "mid", Score: 0.5},
	}

	got := limitTopKByScore(rows, 2)
	if len(got) != 2 {
		t.Fatalf("len = %d, want 2", len(got))
	}
	if got[0].ID != "high" || got[1].ID != "mid" {
		t.Fatalf("unexpected order: %#v", got)
	}
}

func TestLimitTopKByScoreBreaksTiesByID(t *testing.T) {
	rows := []*types.IndexWithScore{
		{ID: "b", Score: 1},
		{ID: "a", Score: 1},
	}

	got := limitTopKByScore(rows, 1)
	if len(got) != 1 || got[0].ID != "a" {
		t.Fatalf("unexpected tie order: %#v", got)
	}
}

func TestListEmbeddingTablesFiltersNonCanonicalDimensions(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New() error = %v", err)
	}
	defer db.Close()

	repo := &mysqlRepository{db: db, database: "weknora", tablePrefix: defaultTablePrefix}
	mock.ExpectQuery(regexp.QuoteMeta(`SELECT TABLE_NAME FROM information_schema.tables
		WHERE TABLE_SCHEMA = ? AND TABLE_NAME LIKE ? ESCAPE '\\'`)).
		WithArgs("weknora", escapeLikePattern(defaultTablePrefix)+"%").
		WillReturnRows(sqlmock.NewRows([]string{"TABLE_NAME"}).
			AddRow("weknora_embeddings_1024").
			AddRow("weknora_embeddings_768_backup").
			AddRow("weknora_embeddings_keywords").
			AddRow("weknora_embeddings_0").
			AddRow("weknora_embeddings_-1").
			AddRow("weknora_embeddings_0768").
			AddRow("weknora_embeddings_768"))

	got, err := repo.listEmbeddingTables(context.Background())
	if err != nil {
		t.Fatalf("listEmbeddingTables() error = %v", err)
	}
	want := []string{"weknora_embeddings_768", "weknora_embeddings_1024"}
	if len(got) != len(want) {
		t.Fatalf("listEmbeddingTables() = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("listEmbeddingTables()[%d] = %q, want %q", i, got[i], want[i])
		}
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations: %v", err)
	}
}

func TestScanRetrieveRowsHandlesNullableMetadata(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New() error = %v", err)
	}
	defer db.Close()

	mock.ExpectQuery("SELECT").WillReturnRows(sqlmock.NewRows([]string{
		"id", "content", "source_id", "source_type", "chunk_id",
		"knowledge_id", "knowledge_base_id", "tag_id", "is_enabled", "score",
	}).AddRow(
		"id-1", nil, nil, nil, nil,
		nil, nil, nil, nil, 0.8,
	))
	rows, err := db.QueryContext(context.Background(), "SELECT")
	if err != nil {
		t.Fatalf("query rows: %v", err)
	}
	defer rows.Close()

	got, err := scanRetrieveRows(rows, types.MatchTypeKeywords)
	if err != nil {
		t.Fatalf("scanRetrieveRows() error = %v", err)
	}
	if len(got) != 1 || !got[0].IsEnabled ||
		got[0].Content != "" || got[0].SourceID != "" ||
		got[0].SourceType != 0 || got[0].ChunkID != "" ||
		got[0].KnowledgeID != "" || got[0].KnowledgeBaseID != "" ||
		got[0].TagID != "" {
		t.Fatalf("unexpected rows: %#v", got)
	}
}

func TestScanCopyRowsHandlesNullableMetadata(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New() error = %v", err)
	}
	defer db.Close()

	mock.ExpectQuery("SELECT").WillReturnRows(sqlmock.NewRows(columnsForCopy).AddRow(
		"id-1", nil, nil, nil, nil, nil, "kb-1", nil, nil, []byte(`[1,0]`),
	))
	rows, err := db.QueryContext(context.Background(), "SELECT")
	if err != nil {
		t.Fatalf("query rows: %v", err)
	}
	defer rows.Close()

	got, err := scanCopyRows(rows)
	if err != nil {
		t.Fatalf("scanCopyRows() error = %v", err)
	}
	if len(got) != 1 || got[0].ID != "id-1" || got[0].KnowledgeBaseID != "kb-1" ||
		got[0].Content != "" || got[0].SourceID != "" ||
		got[0].SourceType != 0 || got[0].ChunkID != "" ||
		got[0].KnowledgeID != "" || got[0].TagID != "" ||
		!got[0].IsEnabled || len(got[0].Embedding) != 2 {
		t.Fatalf("unexpected copy rows: %#v", got)
	}
}

func TestScanCopyRowsHandlesNullEmbedding(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New() error = %v", err)
	}
	defer db.Close()

	mock.ExpectQuery("SELECT").WillReturnRows(sqlmock.NewRows(columnsForCopy).AddRow(
		"id-1", "keyword-only", "source-1", int(types.ChunkSourceType),
		"chunk-1", "knowledge-1", "kb-1", "tag-1", true, nil,
	))
	rows, err := db.QueryContext(context.Background(), "SELECT")
	if err != nil {
		t.Fatalf("query rows: %v", err)
	}
	defer rows.Close()

	got, err := scanCopyRows(rows)
	if err != nil {
		t.Fatalf("scanCopyRows() error = %v", err)
	}
	if len(got) != 1 || got[0].ID != "id-1" || got[0].Embedding != nil {
		t.Fatalf("unexpected copy rows: %#v", got)
	}
}

func TestCalculateStorageSizeAccountsForMySQLBinaryJSON(t *testing.T) {
	embedding := &MysqlVectorEmbedding{
		ID:              "id",
		Content:         "content",
		SourceID:        "source",
		ChunkID:         "chunk",
		KnowledgeID:     "knowledge",
		KnowledgeBaseID: "kb",
		TagID:           "tag",
		Embedding:       make([]float32, 1024),
	}
	got := calculateStorageSize(embedding)
	const binaryJSONBytes = 13*1024 + 9
	minimum := int64(binaryJSONBytes + len(embedding.ID) + len(embedding.Content))
	if got < minimum {
		t.Fatalf("calculateStorageSize() = %d, want at least %d", got, minimum)
	}
}

func TestVectorReadTransactionOptionsUseRepeatableRead(t *testing.T) {
	options := vectorReadTransactionOptions()
	if !options.ReadOnly || options.Isolation != sql.LevelRepeatableRead {
		t.Fatalf("vector read transaction options = %+v", options)
	}
}

func TestToMysqlVectorEmbedding(t *testing.T) {
	info := &types.IndexInfo{
		ID:              "idx1",
		Content:         "hello mysql",
		SourceID:        "source1",
		SourceType:      types.ChunkSourceType,
		ChunkID:         "chunk1",
		KnowledgeID:     "knowledge1",
		KnowledgeBaseID: "kb1",
		TagID:           "tag1",
		IsEnabled:       true,
	}
	params := map[string]any{
		"embedding": map[string][]float32{
			"source1": {0.1, 0.2},
		},
	}

	got := toMysqlVectorEmbedding(info, params)
	if got.ID != info.ID || got.Content != info.Content || got.ChunkID != info.ChunkID {
		t.Fatalf("unexpected embedding: %#v", got)
	}
	if len(got.Embedding) != 2 || got.Embedding[0] != 0.1 || got.Embedding[1] != 0.2 {
		t.Fatalf("embedding = %#v", got.Embedding)
	}
}

func TestTranslateSourceID(t *testing.T) {
	if got := translateSourceID("old", "old", "new"); got != "new" {
		t.Fatalf("translateSourceID exact = %q", got)
	}
	if got := translateSourceID("old-question1", "old", "new"); got != "new-question1" {
		t.Fatalf("translateSourceID question = %q", got)
	}
	if got := translateSourceID("external", "old", "new"); got == "" || got == "external" {
		t.Fatalf("translateSourceID fallback = %q", got)
	}
}

func TestBuildRetrieveResult(t *testing.T) {
	results := []*types.IndexWithScore{{ID: "idx1", Score: 0.9}}
	got := buildRetrieveResult(results, types.VectorRetrieverType)
	if len(got) != 1 {
		t.Fatalf("len(result) = %d, want 1", len(got))
	}
	if got[0].RetrieverEngineType != types.MySQLRetrieverEngineType {
		t.Fatalf("engine = %q", got[0].RetrieverEngineType)
	}
	if got[0].RetrieverType != types.VectorRetrieverType {
		t.Fatalf("retriever type = %q", got[0].RetrieverType)
	}
	if got[0].Results[0].ID != "idx1" {
		t.Fatalf("result ID = %q", got[0].Results[0].ID)
	}
}
