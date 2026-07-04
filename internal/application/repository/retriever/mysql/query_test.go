package mysql

import (
	"context"
	"errors"
	"math"
	"regexp"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	gormmysql "gorm.io/driver/mysql"
	"gorm.io/gorm"
)

func newMockMySQLRepository(t *testing.T) (*mysqlRepository, sqlmock.Sqlmock, func()) {
	t.Helper()
	db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherRegexp))
	require.NoError(t, err)

	gdb, err := gorm.Open(gormmysql.New(gormmysql.Config{
		Conn:                      db,
		SkipInitializeWithVersion: true,
	}), &gorm.Config{})
	require.NoError(t, err)

	return &mysqlRepository{
			db:          gdb,
			database:    "weknora",
			tablePrefix: "weknora_embeddings",
		}, mock, func() {
			_ = db.Close()
		}
}

func TestTableNameAndQuoteIdent(t *testing.T) {
	assert.Equal(t, "weknora_embeddings_768", tableName("", 768))
	assert.Equal(t, "custom_1024", tableName("custom", 1024))
	assert.Equal(t, "`plain`", quoteIdent("plain"))
	assert.Equal(t, "`a``b`", quoteIdent("a`b"))
}

func TestWhereBuilder(t *testing.T) {
	t.Run("empty", func(t *testing.T) {
		clause, args := newWhereBuilder("").build()
		assert.Equal(t, "1 = 1", clause)
		assert.Nil(t, args)
	})

	t.Run("base filter with alias", func(t *testing.T) {
		w := buildBaseFilter(types.RetrieveParams{
			KnowledgeBaseIDs:    []string{"kb1", "kb2"},
			KnowledgeIDs:        []string{"k1"},
			TagIDs:              []string{"tag1"},
			ExcludeKnowledgeIDs: []string{"old"},
			ExcludeChunkIDs:     []string{"chunk9"},
		}, "e")
		clause, args := w.build()
		assert.Contains(t, clause, "e.`is_enabled` = ?")
		assert.Contains(t, clause, "e.`knowledge_base_id` IN (?, ?)")
		assert.Contains(t, clause, "e.`knowledge_id` IN (?)")
		assert.Contains(t, clause, "e.`tag_id` IN (?)")
		assert.Contains(t, clause, "e.`knowledge_id` NOT IN (?)")
		assert.Contains(t, clause, "e.`chunk_id` NOT IN (?)")
		assert.Equal(t, []any{true, "kb1", "kb2", "k1", "tag1", "old", "chunk9"}, args)
	})
}

func TestVectorJSONRoundTrip(t *testing.T) {
	raw, err := vectorToJSON([]float32{1, -2.5, 0.25})
	require.NoError(t, err)
	assert.Equal(t, `[1,-2.5,0.25]`, raw)

	got, err := vectorFromJSON(raw)
	require.NoError(t, err)
	assert.Equal(t, []float32{1, -2.5, 0.25}, got)
}

func TestMySQLEmbeddingSelectList(t *testing.T) {
	jsonList := mysqlEmbeddingSelectList("e", false)
	assert.Contains(t, jsonList, "e.`embedding`")
	assert.NotContains(t, jsonList, "VECTOR_TO_STRING")

	vectorList := mysqlEmbeddingSelectList("e", true)
	assert.Contains(t, vectorList, "VECTOR_TO_STRING(e.`embedding`) AS `embedding`")
	assert.NotContains(t, vectorList, "e.`embedding`, VECTOR_TO_STRING")
}

func TestVectorJSONRejectsNonFinite(t *testing.T) {
	_, err := vectorToJSON([]float32{float32(math.NaN())})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not finite")

	_, err = vectorToJSON([]float32{float32(math.Inf(1))})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not finite")
}

func TestApplyVectorRankingSortsAndThresholds(t *testing.T) {
	perfect, err := vectorToJSON([]float32{1, 0})
	require.NoError(t, err)
	orthogonal, err := vectorToJSON([]float32{0, 1})
	require.NoError(t, err)
	diagonal, err := vectorToJSON([]float32{1, 1})
	require.NoError(t, err)

	rows := []*mysqlEmbedding{
		{ID: "orthogonal", Embedding: orthogonal, IsEnabled: true},
		{ID: "perfect", Embedding: perfect, IsEnabled: true},
		{ID: "diagonal", Embedding: diagonal, IsEnabled: true},
		{ID: "bad-json", Embedding: "not-json", IsEnabled: true},
	}

	got := applyVectorRanking(rows, []float32{1, 0}, 2, 0.5)
	require.Len(t, got, 2)
	assert.Equal(t, "perfect", got[0].ID)
	assert.InDelta(t, 1.0, got[0].Score, 1e-9)
	assert.Equal(t, "diagonal", got[1].ID)
	assert.InDelta(t, math.Sqrt(0.5), got[1].Score, 1e-6)
	assert.Equal(t, types.MatchTypeEmbedding, got[0].MatchType)
}

func TestMergeAndLimit(t *testing.T) {
	got := mergeAndLimit([]*types.IndexWithScore{
		{ID: "low", Score: 0.1},
		{ID: "high", Score: 0.9},
		{ID: "mid", Score: 0.5},
	}, 2)
	require.Len(t, got, 2)
	assert.Equal(t, "high", got[0].ID)
	assert.Equal(t, "mid", got[1].ID)
}

func TestExtractEmbedding(t *testing.T) {
	embedding := []float32{1, 2, 3}
	got := extractEmbedding(map[string]any{
		fieldEmbedding: map[string][]float32{"source": embedding},
	}, "source")
	assert.Equal(t, embedding, got)
	assert.Nil(t, extractEmbedding(nil, "source"))
	assert.Nil(t, extractEmbedding(map[string]any{fieldEmbedding: "bad"}, "source"))
}

func TestNativeVectorDistanceCandidates(t *testing.T) {
	require.NotEmpty(t, nativeVectorDistanceCandidates)
	for _, candidate := range nativeVectorDistanceCandidates {
		assert.NotEmpty(t, candidate.name)
		assert.Contains(t, candidate.probeSQL, "SELECT")
		if candidate.scoreExprNative != "" {
			assert.Contains(t, candidate.scoreExprNative, "%s")
		}
		if candidate.scoreExprJSON != "" {
			assert.Contains(t, candidate.scoreExprJSON, "%s")
		}
	}
}

func TestBuildNativeVectorRetrieveStatement(t *testing.T) {
	params := types.RetrieveParams{
		Embedding: []float32{1, 0, 0},
		TopK:      3,
		Threshold: 0.72,
	}
	native := &nativeVectorDistance{
		name:            "heatwave",
		scoreExprNative: `1 - DISTANCE(%s, STRING_TO_VECTOR(?), 'COSINE')`,
		scoreExprJSON:   `1 - DISTANCE(STRING_TO_VECTOR(CAST(%s AS CHAR)), STRING_TO_VECTOR(?), 'COSINE')`,
	}

	stmt, args, ok, err := buildNativeVectorRetrieveStatement(
		"weknora_embeddings_3", true, params,
		"e.`is_enabled` = ? AND e.`knowledge_base_id` IN (?)",
		[]any{true, "kb-1"}, native,
	)
	require.NoError(t, err)
	require.True(t, ok)
	assert.Contains(t, stmt, "1 - DISTANCE(e.`embedding`, STRING_TO_VECTOR(?), 'COSINE') AS score")
	assert.Contains(t, stmt, "ORDER BY ranked.score DESC LIMIT 3")
	assert.Contains(t, stmt, "WHERE e.`is_enabled` = ? AND e.`knowledge_base_id` IN (?)")
	assert.Equal(t, []any{`[1,0,0]`, true, "kb-1", 0.72}, args)

	stmt, _, ok, err = buildNativeVectorRetrieveStatement(
		"weknora_embeddings_3", false, params, "1 = 1", nil, native,
	)
	require.NoError(t, err)
	require.True(t, ok)
	assert.Contains(t, stmt, "STRING_TO_VECTOR(CAST(e.`embedding` AS CHAR))")
}

func TestBuildNativeVectorRetrieveStatementSkipsUnsupportedStorage(t *testing.T) {
	stmt, args, ok, err := buildNativeVectorRetrieveStatement(
		"weknora_embeddings_3", false,
		types.RetrieveParams{Embedding: []float32{1, 0}, TopK: 1},
		"1 = 1", nil,
		&nativeVectorDistance{
			name:            "native-only",
			scoreExprNative: `1 - DISTANCE(%s, STRING_TO_VECTOR(?), 'COSINE')`,
		},
	)
	require.NoError(t, err)
	assert.False(t, ok)
	assert.Empty(t, stmt)
	assert.Nil(t, args)
}

func TestDetectNativeVectorTypeCachesResult(t *testing.T) {
	repo, mock, cleanup := newMockMySQLRepository(t)
	defer cleanup()

	mock.ExpectQuery(regexp.QuoteMeta("SELECT VECTOR_TO_STRING(STRING_TO_VECTOR('[1,0]'))")).
		WillReturnRows(sqlmock.NewRows([]string{"v"}).AddRow("[1, 0]"))

	assert.True(t, repo.detectNativeVectorType(context.Background()))
	assert.True(t, repo.detectNativeVectorType(context.Background()))
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestDetectNativeVectorTypeFallsBackToJSON(t *testing.T) {
	repo, mock, cleanup := newMockMySQLRepository(t)
	defer cleanup()

	mock.ExpectQuery(regexp.QuoteMeta("SELECT VECTOR_TO_STRING(STRING_TO_VECTOR('[1,0]'))")).
		WillReturnError(errors.New("vector unsupported"))

	assert.False(t, repo.detectNativeVectorType(context.Background()))
	assert.False(t, repo.detectNativeVectorType(context.Background()))
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestNativeVectorRetrieveDisablesDistanceOnRuntimeFailure(t *testing.T) {
	repo, mock, cleanup := newMockMySQLRepository(t)
	defer cleanup()
	repo.vectorProbeDone = true
	repo.vectorDistance = &nativeVectorDistance{
		name:            "heatwave",
		scoreExprNative: `1 - DISTANCE(%s, STRING_TO_VECTOR(?), 'COSINE')`,
	}

	mock.ExpectQuery(regexp.QuoteMeta("SELECT ranked.*")).
		WithArgs(`[1,0]`, true, 0.5).
		WillReturnError(errors.New("distance function unavailable"))

	items, ok, err := repo.nativeVectorRetrieve(
		context.Background(),
		"weknora_embeddings_2",
		true,
		types.RetrieveParams{Embedding: []float32{1, 0}, TopK: 2, Threshold: 0.5},
		"e.`is_enabled` = ?",
		[]any{true},
	)
	require.Error(t, err)
	assert.True(t, ok)
	assert.Nil(t, items)
	assert.Nil(t, repo.detectVectorDistance(context.Background()))
	assert.True(t, repo.vectorDistanceDisabled)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func BenchmarkApplyVectorRanking768(b *testing.B) {
	const (
		dim      = 768
		rowCount = 1000
	)

	query := make([]float32, dim)
	for i := range query {
		query[i] = float32(i%17+1) / 17
	}

	rows := make([]*mysqlEmbedding, rowCount)
	for row := range rows {
		vec := make([]float32, dim)
		for i := range vec {
			vec[i] = float32((row+i)%23+1) / 23
		}
		raw, err := vectorToJSON(vec)
		require.NoError(b, err)
		rows[row] = &mysqlEmbedding{
			ID:        string(rune('a' + row%26)),
			Embedding: raw,
			IsEnabled: true,
		}
	}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		got := applyVectorRanking(rows, query, 10, 0)
		if len(got) == 0 {
			b.Fatal("expected ranked results")
		}
	}
}
