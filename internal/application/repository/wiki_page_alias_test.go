package repository

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// Run with WEKNORA_TEST_POSTGRES_DSN pointing to a test database with pg_trgm
// installed. A transaction-local temporary table shadows wiki_pages; fixtures
// never touch the persistent table. SQLite cannot exercise pg_trgm or JSONB.
// App CI provides a temporary PostgreSQL service and requires this test to run;
// local runs without a DSN remain optional unless WEKNORA_REQUIRE_POSTGRES_TESTS=1.
func TestFindSimilarPages_ExactAliasesPostgres(t *testing.T) {
	dsn := os.Getenv("WEKNORA_TEST_POSTGRES_DSN")
	if dsn == "" {
		if os.Getenv("WEKNORA_REQUIRE_POSTGRES_TESTS") == "1" {
			t.Fatal("WEKNORA_TEST_POSTGRES_DSN is required for the PostgreSQL CI test")
		}
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
		{
			"shared alias preserves distinct candidates", "ARC", nil, 20,
			[]string{"concept/arc", "entity/arc-a", "entity/arc-b"},
		},
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

	t.Run("batch isolates terms and executes one query", func(t *testing.T) {
		queries := []string{"IBM", " iBm ", "xyz", "ARC", "XY", "", "' OR true --"}
		for _, limit := range []int{1, 2, 20} {
			want := make(map[string][]*types.WikiPageLite)
			for _, query := range []string{"ibm", "xyz", "arc"} {
				pages, err := repo.FindSimilarPages(context.Background(), "kb", query, nil, limit)
				require.NoError(t, err)
				want[query] = pages
			}
			recorder := &wikiAliasQueryRecorder{Interface: logger.Default.LogMode(logger.Silent)}
			batchRepo := &wikiPageRepository{db: tx.Session(&gorm.Session{Logger: recorder})}
			got, err := batchRepo.FindSimilarPagesBatch(context.Background(), "kb", queries, nil, limit)
			require.NoError(t, err)
			require.Equal(t, want, got)
			require.Equal(t, 1, recorder.calls, "one SQL statement for the entire term batch")
			require.Equal(t, 1, strings.Count(recorder.sql, "CROSS JOIN LATERAL jsonb_array_elements_text("))
			require.Contains(t, recorder.sql, "alias_hits AS MATERIALIZED")
		}
		got, err := repo.FindSimilarPagesBatch(context.Background(), "kb", []string{"ARC", "IBM"},
			[]string{types.WikiPageTypeConcept}, 20)
		require.NoError(t, err)
		require.Len(t, got, 1)
		require.Equal(t, "concept/arc", got["arc"][0].Slug)

		emptyRepo := &wikiPageRepository{} // Empty input must not touch the database.
		empty, err := emptyRepo.FindSimilarPagesBatch(context.Background(), "kb", []string{"", "  "}, nil, 20)
		require.NoError(t, err)
		require.Empty(t, empty)
		cancelled, cancel := context.WithCancel(context.Background())
		cancel()
		_, err = repo.FindSimilarPagesBatch(cancelled, "kb", []string{"IBM"}, nil, 20)
		require.Error(t, err)
	})

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
		pages, err := repo.FindSimilarPages(
			context.Background(), "kb", "BULKKEY", []string{types.WikiPageTypeEntity}, tc.limit,
		)
		require.NoError(t, err)
		require.Len(t, pages, tc.want)
		batch, err := repo.FindSimilarPagesBatch(context.Background(), "kb", []string{"BULKKEY", "IBM"},
			[]string{types.WikiPageTypeEntity}, tc.limit)
		require.NoError(t, err)
		require.Len(t, batch["bulkkey"], tc.want)
		require.Len(t, batch["ibm"], 2, "each term has an independent limit")
	}
}

type wikiAliasQueryRecorder struct {
	logger.Interface
	calls int
	sql   string
}

func (r *wikiAliasQueryRecorder) Trace(_ context.Context, _ time.Time, fc func() (string, int64), _ error) {
	r.calls++
	r.sql, _ = fc()
}
