package repository

import (
	"context"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

// wikiLogEntriesTestDDL mirrors migrations/versioned/000040_wiki_log_entries.up.sql
// but uses SQLite-compatible types so we can exercise the repo layer without
// spinning up Postgres. INTEGER PRIMARY KEY AUTOINCREMENT gives us the same
// monotonically-increasing ID semantics the production schema relies on for
// cursor pagination.
const wikiLogEntriesTestDDL = `
CREATE TABLE IF NOT EXISTS wiki_log_entries (
    id                INTEGER PRIMARY KEY AUTOINCREMENT,
    tenant_id         INTEGER NOT NULL,
    knowledge_base_id VARCHAR(36) NOT NULL,
    action            VARCHAR(32) NOT NULL,
    knowledge_id      VARCHAR(36) NOT NULL DEFAULT '',
    doc_title         TEXT NOT NULL DEFAULT '',
    summary           TEXT NOT NULL DEFAULT '',
    pages_affected    TEXT NOT NULL DEFAULT '[]',
    created_at        DATETIME DEFAULT CURRENT_TIMESTAMP
);
`

func setupWikiLogTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.Exec(wikiLogEntriesTestDDL).Error)
	return db
}

func makeLogEntry(kbID, action, knowledgeID, title string, pages ...string) *types.WikiLogEntry {
	refs := make(types.WikiLogPageRefs, 0, len(pages))
	for _, slug := range pages {
		// Tests only care about round-tripping; title is derived from
		// the slug so assertions don't have to spell out both fields.
		refs = append(refs, types.WikiLogPageRef{Slug: slug, Title: "title-of-" + slug})
	}
	return &types.WikiLogEntry{
		TenantID:        1,
		KnowledgeBaseID: kbID,
		Action:          action,
		KnowledgeID:     knowledgeID,
		DocTitle:        title,
		Summary:         "summary for " + title,
		PagesAffected:   refs,
	}
}

// TestWikiLogEntryRepository_AppendBatch_WritesAllRows verifies a batch
// insert persists every entry with a monotonic ID and respects the caller-
// provided column values.
func TestWikiLogEntryRepository_AppendBatch_WritesAllRows(t *testing.T) {
	db := setupWikiLogTestDB(t)
	repo := NewWikiLogEntryRepository(db)
	ctx := context.Background()

	entries := []*types.WikiLogEntry{
		makeLogEntry("kb-a", "ingest", "k1", "Doc 1", "entity/a", "summary/k1"),
		makeLogEntry("kb-a", "ingest", "k2", "Doc 2"),
		makeLogEntry("kb-a", "retract", "k3", "Doc 3", "concept/c"),
	}

	require.NoError(t, repo.AppendBatch(ctx, entries))

	// Every row should have a non-zero ID assigned by the DB, and IDs
	// should increase in insertion order so the cursor pagination logic
	// can rely on them.
	for i := range entries {
		assert.NotZero(t, entries[i].ID, "row %d should have a DB-assigned ID", i)
		if i > 0 {
			assert.Greater(t, entries[i].ID, entries[i-1].ID,
				"AppendBatch should preserve insertion order")
		}
	}

	// Round-trip sanity: select back and confirm payload was preserved.
	var got []types.WikiLogEntry
	require.NoError(t, db.Order("id ASC").Find(&got).Error)
	require.Len(t, got, len(entries))
	assert.Equal(t, "ingest", got[0].Action)
	assert.Equal(t, "Doc 1", got[0].DocTitle)
	assert.Equal(t, types.WikiLogPageRefs{
		{Slug: "entity/a", Title: "title-of-entity/a"},
		{Slug: "summary/k1", Title: "title-of-summary/k1"},
	}, got[0].PagesAffected)
	assert.Equal(t, "retract", got[2].Action)
}

// TestWikiLogEntryRepository_AppendBatch_Empty asserts an empty batch is a
// cheap no-op — callers at the end of a wiki ingest pass can invoke it
// unconditionally without a DB round trip.
func TestWikiLogEntryRepository_AppendBatch_Empty(t *testing.T) {
	db := setupWikiLogTestDB(t)
	repo := NewWikiLogEntryRepository(db)
	ctx := context.Background()

	require.NoError(t, repo.AppendBatch(ctx, nil))
	require.NoError(t, repo.AppendBatch(ctx, []*types.WikiLogEntry{}))

	var count int64
	require.NoError(t, db.Model(&types.WikiLogEntry{}).Count(&count).Error)
	assert.Equal(t, int64(0), count)
}

// TestWikiLogEntryRepository_List_CursorPagination walks the feed with a
// limit smaller than the population size and confirms:
//   - newest entries surface first (id DESC order),
//   - passing next_cursor returns the next batch strictly older than it,
//   - end-of-feed is signalled by an empty cursor.
func TestWikiLogEntryRepository_List_CursorPagination(t *testing.T) {
	db := setupWikiLogTestDB(t)
	repo := NewWikiLogEntryRepository(db)
	ctx := context.Background()

	// 5 entries in one KB + a sibling row in another KB to ensure the
	// WHERE filter actually narrows by KB.
	entries := []*types.WikiLogEntry{
		makeLogEntry("kb-a", "ingest", "k1", "Doc 1"),
		makeLogEntry("kb-a", "ingest", "k2", "Doc 2"),
		makeLogEntry("kb-a", "ingest", "k3", "Doc 3"),
		makeLogEntry("kb-a", "ingest", "k4", "Doc 4"),
		makeLogEntry("kb-a", "retract", "k5", "Doc 5"),
	}
	require.NoError(t, repo.AppendBatch(ctx, entries))

	other := makeLogEntry("kb-other", "ingest", "kx", "Other doc")
	require.NoError(t, repo.AppendBatch(ctx, []*types.WikiLogEntry{other}))

	// First page: latest 2 of kb-a, newest first.
	page1, cursor1, err := repo.List(ctx, "kb-a", "", 2)
	require.NoError(t, err)
	require.Len(t, page1, 2)
	assert.Equal(t, "Doc 5", page1[0].DocTitle)
	assert.Equal(t, "Doc 4", page1[1].DocTitle)
	assert.NotEmpty(t, cursor1, "should emit next_cursor when the page is full")

	// Second page continues strictly older than cursor1.
	page2, cursor2, err := repo.List(ctx, "kb-a", cursor1, 2)
	require.NoError(t, err)
	require.Len(t, page2, 2)
	assert.Equal(t, "Doc 3", page2[0].DocTitle)
	assert.Equal(t, "Doc 2", page2[1].DocTitle)
	assert.NotEmpty(t, cursor2)

	// Third (final) page: one row remains, so cursor is empty — the repo
	// must NOT emit a cursor for a short page, otherwise the frontend
	// would fire a doomed follow-up request.
	page3, cursor3, err := repo.List(ctx, "kb-a", cursor2, 2)
	require.NoError(t, err)
	require.Len(t, page3, 1)
	assert.Equal(t, "Doc 1", page3[0].DocTitle)
	assert.Empty(t, cursor3, "short page should signal end-of-feed with an empty cursor")

	// Sibling KB row must NOT leak into kb-a's feed.
	for _, p := range append(append(page1, page2...), page3...) {
		assert.Equal(t, "kb-a", p.KnowledgeBaseID)
	}
}

// TestWikiLogEntryRepository_List_InvalidCursor rejects a non-numeric
// cursor rather than silently returning the newest page, so clients
// notice bugs before they turn into phantom paginations.
func TestWikiLogEntryRepository_List_InvalidCursor(t *testing.T) {
	db := setupWikiLogTestDB(t)
	repo := NewWikiLogEntryRepository(db)
	ctx := context.Background()

	_, _, err := repo.List(ctx, "kb-a", "not-a-number", 10)
	assert.Error(t, err)
}

// TestWikiLogEntryRepository_DeleteByKB purges one KB's rows without
// touching others. Used during KB deletion so the log table does not
// accumulate orphans after the parent KB is gone.
func TestWikiLogEntryRepository_DeleteByKB(t *testing.T) {
	db := setupWikiLogTestDB(t)
	repo := NewWikiLogEntryRepository(db)
	ctx := context.Background()

	require.NoError(t, repo.AppendBatch(ctx, []*types.WikiLogEntry{
		makeLogEntry("kb-a", "ingest", "k1", "Doc 1"),
		makeLogEntry("kb-a", "ingest", "k2", "Doc 2"),
		makeLogEntry("kb-b", "ingest", "k3", "Doc 3"),
	}))

	require.NoError(t, repo.DeleteByKB(ctx, "kb-a"))

	aEntries, _, err := repo.List(ctx, "kb-a", "", 100)
	require.NoError(t, err)
	assert.Empty(t, aEntries)

	bEntries, _, err := repo.List(ctx, "kb-b", "", 100)
	require.NoError(t, err)
	assert.Len(t, bEntries, 1)
}

// TestWikiLogPageRefs_ScanAcceptsLegacyStringArray covers the backward-
// compat path in the Scan method. Any rows the ingest pipeline wrote
// before pages_affected gained a title field are stored as a plain JSON
// string array; the repo must decode them as slug-only refs so the log
// feed renders without a data migration.
func TestWikiLogPageRefs_ScanAcceptsLegacyStringArray(t *testing.T) {
	var refs types.WikiLogPageRefs
	require.NoError(t, refs.Scan([]byte(`["entity/acme","concept/rag"]`)))
	require.Len(t, refs, 2)
	assert.Equal(t, "entity/acme", refs[0].Slug)
	assert.Empty(t, refs[0].Title, "legacy rows have no title until re-ingest overwrites them")
	assert.Equal(t, "concept/rag", refs[1].Slug)
}
