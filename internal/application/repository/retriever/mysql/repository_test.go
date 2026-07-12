package mysql

import (
	"context"
	"regexp"
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Tencent/WeKnora/internal/types"
)

func TestBuildVectorWhereClause(t *testing.T) {
	params := types.RetrieveParams{
		KnowledgeBaseIDs: []string{"kb1", "kb2"},
		KnowledgeIDs:     []string{"k1"},
		TagIDs:           []string{"tag1"},
	}

	where, args := buildVectorWhereClause(params).build()
	for _, want := range []string{
		"knowledge_base_id IN (?,?)",
		"knowledge_id IN (?)",
		"tag_id IN (?)",
		"is_enabled",
	} {
		if !strings.Contains(where, want) {
			t.Fatalf("where clause missing %q in %q", want, where)
		}
	}
	if len(args) != 4 {
		t.Fatalf("args len = %d, want 4", len(args))
	}
}

func TestBuildKeywordRetrieveSQLPlacesFullTextQueryBeforeFilters(t *testing.T) {
	params := types.RetrieveParams{
		Query:            "hello mysql",
		KnowledgeBaseIDs: []string{"kb1", "kb2"},
		KnowledgeIDs:     []string{"k1"},
		TagIDs:           []string{"tag1"},
		TopK:             3,
	}

	stmt, args := buildKeywordRetrieveSQL("weknora_embeddings_3", params)
	for _, want := range []string{
		"MATCH(content) AGAINST(? IN NATURAL LANGUAGE MODE) AS score",
		"knowledge_base_id IN (?,?)",
		"knowledge_id IN (?)",
		"tag_id IN (?)",
		"LIMIT 3",
	} {
		if !strings.Contains(stmt, want) {
			t.Fatalf("keyword SQL missing %q in %s", want, stmt)
		}
	}

	wantArgs := []interface{}{"hello mysql", "kb1", "kb2", "k1", "tag1", "hello mysql"}
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
		WHERE TABLE_SCHEMA = ? AND TABLE_NAME LIKE ?`)).
		WithArgs("weknora", defaultTablePrefix+"%").
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
		WHERE TABLE_SCHEMA = ? AND TABLE_NAME LIKE ?`)).
		WithArgs("weknora", defaultTablePrefix+"%").
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
