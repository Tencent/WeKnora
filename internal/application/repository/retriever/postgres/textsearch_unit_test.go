package postgres

import (
	"context"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
	pgdriver "gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func TestTextSearchKeywordQueryRetainsScopeAndPositiveScore(t *testing.T) {
	conn, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherRegexp))
	require.NoError(t, err)
	t.Cleanup(func() {
		mock.ExpectClose()
		require.NoError(t, conn.Close())
	})
	db, err := gorm.Open(pgdriver.New(pgdriver.Config{Conn: conn}), &gorm.Config{DisableAutomaticPing: true})
	require.NoError(t, err)
	repo := &pgRepository{db: db, textSearch: true}
	mock.ExpectQuery(`WITH ranked AS MATERIALIZED .*knowledge_base_id.*knowledge_id.*tag_id.*`+
		`is_enabled.*ORDER BY search_content <@>.*`).
		WithArgs("invoice", "kb-allowed", "doc-allowed", "tag-allowed", true, "invoice", 1).
		WillReturnRows(sqlmock.NewRows([]string{"id", "source_id", "content", "score"}).
			AddRow(1, "row-allowed", "invoice", 2.5))
	result, err := repo.KeywordsRetrieve(context.Background(), types.RetrieveParams{
		Query: "invoice", TopK: 1, KnowledgeBaseIDs: []string{"kb-allowed"},
		KnowledgeIDs: []string{"doc-allowed"}, TagIDs: []string{"tag-allowed"},
	})
	require.NoError(t, err)
	require.Len(t, result, 1)
	require.Len(t, result[0].Results, 1)
	require.Equal(t, 2.5, result[0].Results[0].Score)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestTextSearchStoresNullableVectorsAndKeepsDisabledState(t *testing.T) {
	row := toDBVectorEmbedding(&types.IndexInfo{Content: "原文", IsEnabled: false}, nil, true)
	require.Nil(t, row.Embedding)
	require.NotNil(t, row.SearchContent)
	require.Equal(t, "原文", row.Content)
	require.False(t, row.IsEnabled)
	legacy := toDBVectorEmbedding(&types.IndexInfo{Content: "original"}, nil, false)
	require.Nil(t, legacy.SearchContent)
}
