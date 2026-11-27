package repository

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

// TestWikiSearchLiteralPostgresMatchesMetacharQueries pins the GET
// /knowledgebase/{kb_id}/wiki/search contract on real PostgreSQL: the search
// box sends plain text, so metacharacter inputs ("func(", "arr[0", "C++", a
// trailing backslash) must match literally instead of failing the whole
// request with "invalid regular expression" (SQLSTATE 2201b). The handler
// routes this endpoint through SearchLiteral, which quotes the query for the
// Postgres ~* branch the same way the SQLite branch escapes LIKE wildcards.
//
// Skips unless WEKNORA_WIKI_TEST_POSTGRES_DSN is set (same pattern as
// TestReplaceKnowledgePostgresRollbackAndCommit); runs against a real
// PostgreSQL instance when the DSN is provided.
func TestWikiSearchLiteralPostgresMatchesMetacharQueries(t *testing.T) {
	dsn := os.Getenv("WEKNORA_WIKI_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("set WEKNORA_WIKI_TEST_POSTGRES_DSN to run PostgreSQL wiki search tests")
	}
	admin, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	adminSQL, err := admin.DB()
	require.NoError(t, err)
	schema := "wiki_search_literal_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	require.NoError(t, admin.Exec("CREATE SCHEMA "+schema).Error)
	t.Cleanup(func() {
		require.NoError(t, admin.Exec("DROP SCHEMA "+schema+" CASCADE").Error)
		require.NoError(t, adminSQL.Close())
	})
	separator := " "
	if strings.Contains(dsn, "://") {
		separator = "?"
		if strings.Contains(dsn, "?") {
			separator = "&"
		}
	}
	db, err := gorm.Open(postgres.Open(dsn+separator+"search_path="+schema), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, sqlDB.Close()) })
	require.NoError(t, db.AutoMigrate(&types.WikiFolder{}, &types.WikiPage{}, &types.WikiPageRevision{}))

	ctx := context.Background()
	repo := NewWikiPageRepository(db)
	require.NoError(t, repo.Create(ctx, &types.WikiPage{
		ID: "p-deploy", TenantID: 1, KnowledgeBaseID: "kb-1",
		Slug: "entity/deploy", Title: "部署手册", PageType: types.WikiPageTypeEntity,
		Status:  types.WikiPageStatusPublished,
		Content: "调用 func( 读取 arr[0]，C++ 客户端备份在 dir\\backup",
		Version: 1,
	}))

	for _, q := range []string{"func(", "arr[0", "C++", `dir\`, "部署"} {
		pages, err := repo.SearchLiteral(ctx, "kb-1", q, 10)
		require.NoError(t, err, "query %q must match literally instead of raising a regex error", q)
		require.Len(t, pages, 1, "query %q should hit the seeded page as literal text", q)
		require.Equal(t, "p-deploy", pages[0].ID)
	}

	// The raw-regex path stays for opt-in regex callers (agent tool with
	// regex=true); there a malformed pattern still surfaces the driver
	// error instead of silently changing its meaning.
	_, err = repo.Search(ctx, "kb-1", "func(", 10)
	require.Error(t, err, "raw regex search is expected to reject an unbalanced pattern")
}
