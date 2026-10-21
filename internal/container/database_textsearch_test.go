package container

import (
	"database/sql"
	"net/url"
	"os"
	"strings"
	"testing"

	pgrepo "github.com/Tencent/WeKnora/internal/application/repository/retriever/postgres"
	"github.com/Tencent/WeKnora/internal/config"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

func TestInitDatabaseTextSearch(t *testing.T) {
	dsn := os.Getenv("WEKNORA_TEXTSEARCH_TEST_DSN")
	if dsn == "" {
		t.Skip("set WEKNORA_TEXTSEARCH_TEST_DSN after running the retrieval fixture test")
	}
	u, err := url.Parse(dsn)
	require.NoError(t, err)
	require.Equal(t, "weknora_textsearch_test", strings.TrimPrefix(u.Path, "/"))
	require.NotNil(t, u.User)
	password, _ := u.User.Password()
	for key, value := range map[string]string{
		"DB_DRIVER": "postgres", "DB_HOST": u.Hostname(), "DB_PORT": u.Port(),
		"DB_USER": u.User.Username(), "DB_PASSWORD": password, "DB_NAME": "weknora_textsearch_test",
		"DB_SSLMODE": "disable", "POSTGRES_SEARCH_EXTENSION": "pg_textsearch",
		"RETRIEVE_DRIVER": "postgres", "AUTO_MIGRATE": "true", "AUTO_RECOVER_DIRTY": "false",
	} {
		t.Setenv(key, value)
	}
	t.Chdir("../..")
	seed, err := sql.Open("postgres", dsn)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, seed.Close()) })
	_, err = seed.Exec("UPDATE embeddings SET search_content = NULL WHERE source_id = 'wanted'")
	require.NoError(t, err)
	db, err := initDatabase(&config.Config{})
	require.NoError(t, err)
	pool, err := db.DB()
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, pool.Close()) })
	var pending int
	require.NoError(t, pool.QueryRow("SELECT count(*) FROM embeddings WHERE search_content IS NULL").Scan(&pending))
	require.Zero(t, pending)
	results, err := pgrepo.NewPostgresRetrieveEngineRepository(db).Retrieve(t.Context(), types.RetrieveParams{
		Query: "售后", TopK: 1, RetrieverType: types.KeywordsRetrieverType,
		KnowledgeBaseIDs: []string{"kb-a"}, KnowledgeIDs: []string{"doc-a"}, TagIDs: []string{"tag-a"},
	})
	require.NoError(t, err)
	require.Len(t, results[0].Results, 1)
	require.Equal(t, "wanted", results[0].Results[0].SourceID)
}

func TestInitDatabaseRejectsInvalidPostgresSettings(t *testing.T) {
	t.Setenv("DB_DRIVER", "postgres")
	t.Setenv("DB_SSLMODE", "not-a-mode")
	_, err := initDatabase(&config.Config{})
	require.ErrorContains(t, err, "invalid DB_SSLMODE")
	t.Setenv("DB_SSLMODE", "disable")
	t.Setenv("POSTGRES_SEARCH_EXTENSION", "other-extension")
	_, err = initDatabase(&config.Config{})
	require.ErrorContains(t, err, "invalid POSTGRES_SEARCH_EXTENSION")
}
