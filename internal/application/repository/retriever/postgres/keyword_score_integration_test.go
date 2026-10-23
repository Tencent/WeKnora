package postgres

import (
	"context"
	"os"
	"testing"

	"github.com/stretchr/testify/require"
	gormpostgres "gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// TestKeywordScoreCatalogCompatibility checks the actual PostgreSQL catalog
// query, including preference and signatures, rather than mocking its result.
// Set WEKNORA_RETRIEVER_TEST_POSTGRES_DSN to a disposable PostgreSQL database
// without pg_search installed. Fixtures are rolled back after each subtest;
// existing scoring schemas are never replaced or dropped.
func TestKeywordScoreCatalogCompatibility(t *testing.T) {
	dsn := os.Getenv("WEKNORA_RETRIEVER_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("set WEKNORA_RETRIEVER_TEST_POSTGRES_DSN to run PostgreSQL catalog tests")
	}
	db, err := gorm.Open(gormpostgres.Open(dsn), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	t.Cleanup(func() { _ = sqlDB.Close() })

	var existing int
	require.NoError(t, db.Raw(`SELECT count(*) FROM pg_catalog.pg_namespace
		WHERE nspname IN ('paradedb', 'pgsearch', 'unrelated_score_test')`).Scan(&existing).Error)
	require.Zero(t, existing, "use a disposable database without existing scoring schemas")

	paradedb := "CREATE FUNCTION paradedb.score(anyelement) RETURNS real LANGUAGE sql AS 'SELECT 1::real'"
	pgsearch := "CREATE FUNCTION pgsearch.score(anyelement) RETURNS real LANGUAGE sql AS 'SELECT 1::real'"
	for _, tc := range []struct {
		name      string
		functions []string
		want      string
	}{
		{name: "ParadeDB", functions: []string{paradedb}, want: "paradedb.score"},
		{name: "PolarDB_layout", functions: []string{pgsearch}, want: "pgsearch.score"},
		{name: "prefer_ParadeDB", functions: []string{pgsearch, paradedb}, want: "paradedb.score"},
		{name: "missing_functions"},
		{
			name: "incompatible_argument_falls_back",
			functions: []string{
				"CREATE FUNCTION paradedb.score(integer) RETURNS real LANGUAGE sql AS 'SELECT 1::real'",
				pgsearch,
			},
			want: "pgsearch.score",
		},
		{
			name: "incompatible_result_falls_back",
			functions: []string{
				"CREATE FUNCTION paradedb.score(anyelement) RETURNS double precision LANGUAGE sql AS 'SELECT 1::real'",
				pgsearch,
			},
			want: "pgsearch.score",
		},
		{
			name: "reject_wrong_arity",
			functions: []string{
				"CREATE FUNCTION pgsearch.score(anyelement, integer) RETURNS real LANGUAGE sql AS 'SELECT 1::real'",
			},
		},
		{
			name: "reject_unlisted_schema",
			functions: []string{
				"CREATE FUNCTION unrelated_score_test.score(anyelement) RETURNS real LANGUAGE sql AS 'SELECT 1::real'",
			},
		},
		{
			name: "reject_wrong_result",
			functions: []string{
				"CREATE FUNCTION pgsearch.score(anyelement) RETURNS double precision LANGUAGE sql AS 'SELECT 1::real'",
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tx := db.Begin()
			require.NoError(t, tx.Error)
			t.Cleanup(func() { require.NoError(t, tx.Rollback().Error) })
			for _, schema := range []string{"paradedb", "pgsearch", "unrelated_score_test"} {
				require.NoError(t, tx.Exec("CREATE SCHEMA "+schema).Error)
			}
			for _, ddl := range tc.functions {
				require.NoError(t, tx.Exec(ddl).Error)
			}
			repo := &pgRepository{db: tx}
			function, err := repo.resolveKeywordScoreFunction(context.Background())
			if tc.want == "" {
				require.ErrorContains(t, err, "check pg_search extension compatibility")
				require.Empty(t, function)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tc.want, function)
		})
	}
}
