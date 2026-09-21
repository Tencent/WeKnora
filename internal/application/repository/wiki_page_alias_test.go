package repository

import (
	"context"
	"os"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

// Run with WEKNORA_TEST_POSTGRES_DSN pointing to a test database with pg_trgm
// installed. A transaction-local temporary table shadows wiki_pages; fixtures
// never touch the persistent table. SQLite cannot exercise pg_trgm or JSONB.
func TestFindSimilarPages_ExactAliasesPostgres(t *testing.T) {
	dsn := os.Getenv("WEKNORA_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("set WEKNORA_TEST_POSTGRES_DSN to run PostgreSQL alias regression tests")
	}
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	t.Cleanup(func() { _ = sqlDB.Close() })
	tx := db.Begin()
	require.NoError(t, tx.Error)
	t.Cleanup(func() { _ = tx.Rollback().Error })
	require.NoError(t, tx.Exec(`CREATE TEMP TABLE wiki_pages (
		knowledge_base_id text, slug text, title text, page_type text,
		status text, aliases jsonb, out_links jsonb DEFAULT '[]', deleted_at timestamptz
	) ON COMMIT DROP`).Error)
	require.NoError(t, tx.Exec("SET LOCAL pg_trgm.similarity_threshold = 0.3").Error)
	require.NoError(t, tx.Exec(`INSERT INTO wiki_pages
		(knowledge_base_id, slug, title, page_type, status, aliases, deleted_at) VALUES
		('kb', 'entity/ibm', '国际商业机器公司', 'entity', 'published', '["IBM", " ibm ", "IBM"]', NULL),
		('kb', 'entity/title', 'IBM Services', 'entity', 'published', '[]', NULL),
		('kb', 'entity/dual', 'Acme', 'entity', 'published', '["Acme", "ACME"]', NULL),
		('kb', 'entity/arc-a', 'Alpha Research Center', 'entity', 'published', '["ARC"]', NULL),
		('kb', 'entity/arc-b', 'Advanced Robotics Company', 'entity', 'published', '["ARC"]', NULL),
		('kb', 'concept/arc', 'Abstract Reference Concept', 'concept', 'published', '["ARC"]', NULL),
		('other-kb', 'entity/foreign', 'Foreign', 'entity', 'published', '["IBM"]', NULL),
		('kb', 'entity/archived', 'Archived', 'entity', 'archived', '["IBM"]', NULL),
		('kb', 'entity/deleted', 'Deleted', 'entity', 'published', '["IBM"]', now()),
		('kb', 'summary/source', 'Source', 'summary', 'published', '["IBM"]', NULL),
		('kb', 'entity/null', 'Unrelated Null', 'entity', 'published', NULL, NULL),
		('kb', 'entity/json-null', 'Unrelated JSON Null', 'entity', 'published', 'null', NULL),
		('kb', 'entity/empty', 'Unrelated Empty', 'entity', 'published', '[]', NULL),
		('kb', 'entity/space', 'Unrelated Whitespace', 'entity', 'published', '["  XYZ  "]', NULL)
	`).Error)
	repo := &wikiPageRepository{db: tx}
	cases := []struct {
		name  string
		query string
		types []string
		limit int
		want  []string
	}{
		{"alias only and scope filters", "IBM", nil, 20, []string{"entity/ibm", "entity/title"}},
		{"case and query whitespace", "  iBm  ", nil, 20, []string{"entity/ibm", "entity/title"}},
		{"stored alias whitespace", "xyz", nil, 20, []string{"entity/space"}},
		{"exact alias before fuzzy title", "IBM", nil, 1, []string{"entity/ibm"}},
		{"title and repeated aliases return one page", "Acme", nil, 20, []string{"entity/dual"}},
		{"shared alias preserves distinct candidates", "ARC", nil, 20, []string{"concept/arc", "entity/arc-a", "entity/arc-b"}},
		{"explicit type filter", "ARC", []string{"entity"}, 20, []string{"entity/arc-a", "entity/arc-b"}},
		{"title search preserved", "IBM Services", nil, 20, []string{"entity/title"}},
		{"no alias substring match", "XY", nil, 20, nil},
		{"punctuation preserved", "X.Y.Z", nil, 20, nil},
		{"empty query", "  ", nil, 20, nil},
		{"query is data", "' OR true --", nil, 20, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			pages, err := repo.FindSimilarPages(context.Background(), "kb", tc.query, tc.types, tc.limit)
			require.NoError(t, err)
			var slugs []string
			for _, page := range pages {
				slugs = append(slugs, page.Slug)
			}
			require.Equal(t, tc.want, slugs)
		})
	}

	t.Run("overlapping bounded branches preserve global top-k", func(t *testing.T) {
		require.NoError(t, tx.Exec(`INSERT INTO wiki_pages
			(knowledge_base_id, slug, title, page_type, status, aliases) VALUES
			('kb', 'entity/a-title', 'MATCHKEY', 'entity', 'published', '[]'),
			('kb', 'entity/c-both', 'MATCHKEY', 'entity', 'published', '["MATCHKEY", "matchkey"]'),
			('kb', 'entity/z-alias', 'Different organization', 'entity', 'published', '["MATCHKEY"]'),
			('kb', 'entity/fuzzy', 'MATCHKEY Services', 'entity', 'published', '[]')`).Error)
		want := []string{"entity/a-title", "entity/c-both", "entity/z-alias", "entity/fuzzy"}
		for _, limit := range []int{1, 2, 3, 4} {
			pages, err := repo.FindSimilarPages(context.Background(), "kb", "MATCHKEY", nil, limit)
			require.NoError(t, err)
			var slugs []string
			for _, page := range pages {
				slugs = append(slugs, page.Slug)
			}
			require.Equal(t, want[:limit], slugs)
		}
	})

	// The existing default (20) and maximum (50) still apply to alias matches.
	require.NoError(t, tx.Exec(`INSERT INTO wiki_pages
		(knowledge_base_id, slug, title, page_type, status, aliases)
		SELECT 'kb', 'entity/bulk-' || lpad(i::text, 3, '0'), 'Unrelated ' || i,
		'entity', 'published', '["BULKKEY"]'::jsonb FROM generate_series(1, 60) i`).Error)
	for _, tc := range []struct{ limit, want int }{{0, 20}, {-1, 20}, {100, 50}} {
		pages, err := repo.FindSimilarPages(context.Background(), "kb", "BULKKEY", []string{types.WikiPageTypeEntity}, tc.limit)
		require.NoError(t, err)
		require.Len(t, pages, tc.want)
	}
}
