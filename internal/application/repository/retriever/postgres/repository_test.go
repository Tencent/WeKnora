package postgres

import (
	"context"
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
	postgresDriver "gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func TestKeywordRetrieveIncludesDisabledOnlyWhenRequested(t *testing.T) {
	for _, testCase := range []struct {
		name            string
		includeDisabled bool
		wantFilter      bool
	}{
		{name: "default enabled-only", wantFilter: true},
		{name: "administrative opt-in", includeDisabled: true},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			var query string
			database, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherFunc(
				func(_, actual string) error {
					query = actual
					return nil
				},
			)))
			require.NoError(t, err)
			t.Cleanup(func() { _ = database.Close() })

			db, err := gorm.Open(postgresDriver.New(postgresDriver.Config{Conn: database}), &gorm.Config{
				DisableAutomaticPing: true,
			})
			require.NoError(t, err)
			mock.ExpectQuery("capture").WillReturnRows(sqlmock.NewRows([]string{
				"score", "id", "content", "source_id", "source_type", "chunk_id",
				"knowledge_id", "knowledge_base_id", "tag_id",
			}))

			repository := &pgRepository{db: db}
			_, err = repository.KeywordsRetrieve(context.Background(), types.RetrieveParams{
				Query:           "refund",
				TopK:            10,
				IncludeDisabled: testCase.includeDisabled,
			})
			require.NoError(t, err)
			require.NoError(t, mock.ExpectationsWereMet())
			require.Equal(t, testCase.wantFilter, strings.Contains(query, "is_enabled"))
		})
	}
}
