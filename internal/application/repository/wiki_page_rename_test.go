package repository

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/postgres"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

func newWikiRenameDB(t *testing.T, dialect string) *gorm.DB {
	t.Helper()
	var db *gorm.DB
	var err error
	if dialect == "postgres" {
		dsn := os.Getenv("WEKNORA_WIKI_RENAME_TEST_DSN")
		if dsn == "" {
			t.Skip("set WEKNORA_WIKI_RENAME_TEST_DSN to test PostgreSQL in an isolated temporary schema")
		}
		cfg, parseErr := pgx.ParseConfig(dsn)
		require.NoError(t, parseErr)
		root := stdlib.OpenDB(*cfg)
		schema := "wiki_rename_" + strings.ReplaceAll(uuid.NewString(), "-", "")
		_, err = root.Exec(`CREATE SCHEMA "` + schema + `"`)
		require.NoError(t, err)
		t.Cleanup(func() {
			_, dropErr := root.Exec(`DROP SCHEMA "` + schema + `" CASCADE`)
			require.NoError(t, dropErr)
			require.NoError(t, root.Close())
		})
		cfg.RuntimeParams["search_path"] = schema
		db, err = gorm.Open(postgres.New(postgres.Config{Conn: stdlib.OpenDB(*cfg)}), &gorm.Config{})
	} else {
		db, err = gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	}
	require.NoError(t, err)
	pool, err := db.DB()
	require.NoError(t, err)
	if dialect == "sqlite" {
		pool.SetMaxOpenConns(1)
		require.NoError(t, db.Exec("PRAGMA foreign_keys = ON").Error)
	}
	t.Cleanup(func() { require.NoError(t, pool.Close()) })
	require.NoError(t, db.AutoMigrate(&types.WikiPage{}, &types.WikiPageRevision{}, &types.WikiPageIssue{}))
	// AutoMigrate's legacy struct tag is slug-only. Exercise the actual live
	// migration constraint, including cross-KB duplicates and deleted slugs.
	require.NoError(t, db.Migrator().DropIndex(&types.WikiPage{}, "idx_kb_slug"))
	require.NoError(t, db.Exec(`CREATE UNIQUE INDEX idx_wiki_pages_kb_slug
		ON wiki_pages (knowledge_base_id, slug) WHERE deleted_at IS NULL`).Error)
	return db
}

func withWikiRenameDB(t *testing.T, test func(*testing.T, *gorm.DB)) {
	t.Helper()
	for _, dialect := range []string{"sqlite", "postgres"} {
		t.Run(dialect, func(t *testing.T) { test(t, newWikiRenameDB(t, dialect)) })
	}
}

func wikiRenameRequest(page *types.WikiPage) interfaces.WikiPageRenameRequest {
	return interfaces.WikiPageRenameRequest{
		KnowledgeBaseID: page.KnowledgeBaseID, PageID: page.ID,
		OldSlug: page.Slug, NewSlug: "concept/new",
	}
}

type wikiRenameSnapshot struct {
	Pages     []types.WikiPage
	Revisions []types.WikiPageRevision
	Issues    []types.WikiPageIssue
}

func snapshotWikiRename(t *testing.T, db *gorm.DB) wikiRenameSnapshot {
	t.Helper()
	var snapshot wikiRenameSnapshot
	order := clause.OrderByColumn{Column: clause.Column{Name: "id"}}
	require.NoError(t, db.Unscoped().Order(order).Find(&snapshot.Pages).Error)
	require.NoError(t, db.Order(order).Find(&snapshot.Revisions).Error)
	require.NoError(t, db.Unscoped().Order(order).Find(&snapshot.Issues).Error)
	return snapshot
}

func TestWikiRenamePreservesIdentityHistoryMasterySourcesAndLinks(t *testing.T) {
	withWikiRenameDB(t, func(t *testing.T, db *gorm.DB) {
		ctx := context.Background()
		repo := NewWikiPageRepository(db)
		page := makeWikiPage("kb-a", "concept/old", types.WikiPageTypeConcept, types.WikiPageStatusPublished)
		page.Version = 7
		page.Content = "Self [[concept/old]] and [[concept/target|Target]]."
		page.Summary = "See [[concept/old| original label ]]."
		page.InLinks = types.StringArray{"concept/old", "concept/from", "concept/new"}
		page.OutLinks = types.StringArray{"concept/old", "concept/target"}
		page.ParentSlug = "concept/old"
		page.SourceRefs = types.StringArray{"source-uuid|concept/old", "another-source"}
		page.ChunkRefs = types.StringArray{"source-chunk-uuid"}
		page.Aliases = types.StringArray{"Original", "concept/old"}
		page.PageMetadata = types.JSON(`{"custom":"concept/old"}`)
		page.FolderID, page.WikiPath, page.Depth, page.SortOrder = "folder", "concept/folder/original", 1, 3
		page.CategoryPath = types.StringArray{"folder"}
		page.LastEditSource, page.LastEditorID = types.WikiEditSourceUser, "original-editor"
		incoming := makeWikiPage("kb-a", "concept/from", types.WikiPageTypeConcept, types.WikiPageStatusPublished)
		incoming.Content = "[[ Concept/Old | Label $1 ]] [[concept/old|]] [[concept/old-extra]] concept/old"
		incoming.OutLinks = types.StringArray{"concept/old", "concept/new", "concept/old-extra"}
		target := makeWikiPage("kb-a", "concept/target", types.WikiPageTypeConcept, types.WikiPageStatusPublished)
		target.InLinks = types.StringArray{"concept/old", "concept/elsewhere"}
		uncached := makeWikiPage("kb-a", "concept/uncached", types.WikiPageTypeConcept, types.WikiPageStatusPublished)
		uncached.Content = "[[concept/old]]"
		archived := makeWikiPage("kb-a", "concept/archived", types.WikiPageTypeConcept, types.WikiPageStatusArchived)
		archived.Content, archived.OutLinks = "[[concept/old]]", types.StringArray{"concept/old"}
		child := makeWikiPage("kb-a", "concept/child", types.WikiPageTypeConcept, types.WikiPageStatusDraft)
		child.ParentSlug = "concept/old"
		other := makeWikiPage("kb-b", "concept/old", types.WikiPageTypeConcept, types.WikiPageStatusPublished)
		other.Content, other.ParentSlug = "[[concept/old]]", "concept/old"
		unrelated := makeWikiPage("kb-a", "concept/unrelated", types.WikiPageTypeConcept, types.WikiPageStatusPublished)
		deleted := makeWikiPage("kb-a", "concept/deleted", types.WikiPageTypeConcept, types.WikiPageStatusPublished)
		deleted.Content = "[[concept/old]]"
		deleted.DeletedAt = gorm.DeletedAt{Time: time.Now(), Valid: true}
		for _, p := range []*types.WikiPage{page, incoming, target, uncached, archived, child, other, unrelated, deleted} {
			require.NoError(t, repo.Create(ctx, p))
		}
		revision := makeWikiRevision(page, 6, types.WikiEditSourceUser)
		require.NoError(t, db.Create(revision).Error)
		for _, kbID := range []string{"kb-a", "kb-b"} {
			require.NoError(t, repo.CreateIssue(ctx, &types.WikiPageIssue{
				ID: uuid.NewString(), TenantID: 1, KnowledgeBaseID: kbID, Slug: page.Slug,
				IssueType: "source", Description: "concept/old", Status: "resolved", ReportedBy: "user",
				SuspectedKnowledgeIDs: types.StringArray{"source-uuid"},
			}))
		}
		// A test-only learning row proves that a UUID-keyed personal relation
		// survives without depending on another agent's learning implementation.
		require.NoError(t, db.Exec(`CREATE TABLE rename_mastery_probe (
			page_id VARCHAR(36) PRIMARY KEY REFERENCES wiki_pages(id), p_mastery DOUBLE PRECISION, answers INTEGER)`).Error)
		require.NoError(t, db.Exec("INSERT INTO rename_mastery_probe VALUES (?, ?, ?)", page.ID, 0.87, 5).Error)
		before := snapshotWikiRename(t, db)
		original, err := repo.GetByID(ctx, page.ID)
		require.NoError(t, err)

		result, err := repo.(interfaces.WikiPageRenamer).RenamePage(ctx, wikiRenameRequest(page))
		require.NoError(t, err)
		require.Equal(t, page.ID, result.Page.ID)
		require.Len(t, result.AffectedPages, 6)
		stored, err := repo.GetBySlug(ctx, "kb-a", "concept/new")
		require.NoError(t, err)
		require.Equal(t, page.ID, stored.ID)
		require.Equal(t, 7, stored.Version)
		require.Equal(t, original.CreatedAt, stored.CreatedAt)
		require.Equal(t, original.SourceRefs, stored.SourceRefs)
		require.Equal(t, original.ChunkRefs, stored.ChunkRefs)
		require.Equal(t, original.Aliases, stored.Aliases)
		require.Equal(t, original.PageMetadata, stored.PageMetadata)
		require.Equal(t, original.CategoryPath, stored.CategoryPath)
		require.Equal(t, original.FolderID, stored.FolderID)
		require.Equal(t, original.WikiPath, stored.WikiPath)
		require.Equal(t, original.SortOrder, stored.SortOrder)
		require.Equal(t, original.Depth, stored.Depth)
		require.Equal(t, original.LastEditSource, stored.LastEditSource)
		require.Equal(t, original.LastEditorID, stored.LastEditorID)
		require.Equal(t, "concept/new", stored.ParentSlug)
		require.Equal(t, "Self [[concept/new]] and [[concept/target|Target]].", stored.Content)
		require.Equal(t, "See [[concept/new| original label ]].", stored.Summary)
		require.ElementsMatch(t, []string{"concept/new", "concept/from", "concept/uncached", "concept/archived"}, stored.InLinks)
		require.Equal(t, types.StringArray{"concept/new", "concept/target"}, stored.OutLinks)
		_, err = repo.GetBySlug(ctx, "kb-a", "concept/old")
		require.ErrorIs(t, err, ErrWikiPageNotFound)
		from, err := repo.GetByID(ctx, incoming.ID)
		require.NoError(t, err)
		require.Equal(t, "[[concept/new| Label $1 ]] [[concept/new|]] [[concept/old-extra]] concept/old", from.Content)
		require.Equal(t, types.StringArray{"concept/new", "concept/old-extra"}, from.OutLinks)
		targetAfter, err := repo.GetByID(ctx, target.ID)
		require.NoError(t, err)
		require.Equal(t, types.StringArray{"concept/new", "concept/elsewhere"}, targetAfter.InLinks)
		childAfter, err := repo.GetByID(ctx, child.ID)
		require.NoError(t, err)
		require.Equal(t, "concept/new", childAfter.ParentSlug)
		uncachedAfter, err := repo.GetByID(ctx, uncached.ID)
		require.NoError(t, err)
		require.Equal(t, types.StringArray{"concept/new"}, uncachedAfter.OutLinks)
		after := snapshotWikiRename(t, db)
		require.Equal(t, before.Revisions, after.Revisions)
		for i, p := range before.Pages {
			if p.ID == other.ID || p.ID == unrelated.ID || p.ID == deleted.ID {
				require.Equal(t, p, after.Pages[i])
			}
			require.Equal(t, p.Version, after.Pages[i].Version)
		}
		for i, issue := range after.Issues {
			if issue.KnowledgeBaseID == "kb-a" {
				require.Equal(t, "concept/new", issue.Slug)
				require.Equal(t, before.Issues[i].Status, issue.Status)
				require.Equal(t, before.Issues[i].Description, issue.Description)
				require.Equal(t, before.Issues[i].SuspectedKnowledgeIDs, issue.SuspectedKnowledgeIDs)
			} else {
				require.Equal(t, before.Issues[i], issue)
			}
		}
		var mastery struct {
			PMastery float64
			Answers  int
		}
		require.NoError(t, db.Table("rename_mastery_probe").Where("page_id = ?", stored.ID).Take(&mastery).Error)
		require.Equal(t, 0.87, mastery.PMastery)
		require.Equal(t, 5, mastery.Answers)
	})
}

func TestWikiRenameCollisionIsAtomic(t *testing.T) {
	withWikiRenameDB(t, func(t *testing.T, db *gorm.DB) {
		repo := NewWikiPageRepository(db)
		page := makeWikiPage("kb-a", "concept/old", types.WikiPageTypeConcept, types.WikiPageStatusPublished)
		target := makeWikiPage("kb-a", "concept/new", types.WikiPageTypeConcept, types.WikiPageStatusArchived)
		target.Content = "[[concept/old]]"
		require.NoError(t, db.Create(page).Error)
		require.NoError(t, db.Create(target).Error)
		before := snapshotWikiRename(t, db)
		result, err := repo.(interfaces.WikiPageRenamer).RenamePage(context.Background(), wikiRenameRequest(page))
		require.ErrorIs(t, err, ErrWikiPageSlugConflict)
		require.Nil(t, result)
		require.Equal(t, before, snapshotWikiRename(t, db))
	})
}

func TestWikiRenameUsesLiveKBSlugConstraint(t *testing.T) {
	withWikiRenameDB(t, func(t *testing.T, db *gorm.DB) {
		page := makeWikiPage("kb-a", "concept/old", types.WikiPageTypeConcept, types.WikiPageStatusPublished)
		deleted := makeWikiPage("kb-a", "concept/new", types.WikiPageTypeConcept, types.WikiPageStatusPublished)
		deleted.DeletedAt = gorm.DeletedAt{Time: time.Now(), Valid: true}
		other := makeWikiPage("kb-b", "concept/new", types.WikiPageTypeConcept, types.WikiPageStatusPublished)
		for _, p := range []*types.WikiPage{page, deleted, other} {
			require.NoError(t, db.Create(p).Error)
		}
		result, err := NewWikiPageRepository(db).(interfaces.WikiPageRenamer).RenamePage(context.Background(), wikiRenameRequest(page))
		require.NoError(t, err)
		require.Equal(t, page.ID, result.Page.ID)
		var count int64
		require.NoError(t, db.Unscoped().Model(&types.WikiPage{}).Count(&count).Error)
		require.EqualValues(t, 3, count)
	})
}

func TestWikiRenameRollsBackAllWritesOnIssueFailure(t *testing.T) {
	withWikiRenameDB(t, func(t *testing.T, db *gorm.DB) {
		page := makeWikiPage("kb-a", "concept/old", types.WikiPageTypeConcept, types.WikiPageStatusPublished)
		other := makeWikiPage("kb-a", "concept/from", types.WikiPageTypeConcept, types.WikiPageStatusPublished)
		other.Content, other.ParentSlug, other.OutLinks = "[[concept/old]]", "concept/old", types.StringArray{"concept/old"}
		require.NoError(t, db.Create(page).Error)
		require.NoError(t, db.Create(other).Error)
		require.NoError(t, db.Create(makeWikiRevision(page, 1, types.WikiEditSourceUser)).Error)
		before := snapshotWikiRename(t, db)
		injected := errors.New("injected issue write failure")
		writes := 0
		require.NoError(t, db.Callback().Update().Before("gorm:update").Register("test:rename_failure", func(tx *gorm.DB) {
			if tx.Statement.Table == "wiki_pages" {
				writes++
			}
			if tx.Statement.Table == "wiki_page_issues" {
				tx.AddError(injected)
			}
		}))
		result, err := NewWikiPageRepository(db).(interfaces.WikiPageRenamer).RenamePage(context.Background(), wikiRenameRequest(page))
		require.ErrorIs(t, err, injected)
		require.Nil(t, result)
		require.Equal(t, 2, writes, "failure must happen after both page writes")
		require.Equal(t, before, snapshotWikiRename(t, db))
	})
}

func TestWikiRenameCASFencesVersionAndMetadataChanges(t *testing.T) {
	for _, field := range []string{"version", "updated_at"} {
		t.Run(field, func(t *testing.T) {
			withWikiRenameDB(t, func(t *testing.T, db *gorm.DB) {
				page := makeWikiPage("kb-a", "concept/old", types.WikiPageTypeConcept, types.WikiPageStatusPublished)
				require.NoError(t, db.Create(page).Error)
				before := snapshotWikiRename(t, db)
				injected := false
				require.NoError(t, db.Callback().Update().Before("gorm:update").Register("test:rename_cas", func(tx *gorm.DB) {
					if injected || tx.Statement.Table != "wiki_pages" {
						return
					}
					injected = true
					if field == "version" {
						tx.AddError(tx.Exec("UPDATE wiki_pages SET version = version + 1 WHERE id = ?", page.ID).Error)
					} else {
						tx.AddError(tx.Exec("UPDATE wiki_pages SET updated_at = ? WHERE id = ?", page.UpdatedAt.Add(time.Second), page.ID).Error)
					}
				}))
				result, err := NewWikiPageRepository(db).(interfaces.WikiPageRenamer).RenamePage(context.Background(), wikiRenameRequest(page))
				require.ErrorIs(t, err, ErrWikiPageConflict)
				require.Nil(t, result)
				require.Equal(t, before, snapshotWikiRename(t, db))
			})
		})
	}
}

func TestWikiRenameConstraintRaceRollsBack(t *testing.T) {
	withWikiRenameDB(t, func(t *testing.T, db *gorm.DB) {
		page := makeWikiPage("kb-a", "concept/old", types.WikiPageTypeConcept, types.WikiPageStatusPublished)
		require.NoError(t, db.Create(page).Error)
		before := snapshotWikiRename(t, db)
		injected := false
		require.NoError(t, db.Callback().Update().Before("gorm:update").Register("test:rename_collision", func(tx *gorm.DB) {
			if injected || tx.Statement.Table != "wiki_pages" {
				return
			}
			injected = true
			conflict := makeWikiPage("kb-a", "concept/new", types.WikiPageTypeConcept, types.WikiPageStatusPublished)
			tx.AddError(tx.Session(&gorm.Session{NewDB: true}).Create(conflict).Error)
		}))
		result, err := NewWikiPageRepository(db).(interfaces.WikiPageRenamer).RenamePage(context.Background(), wikiRenameRequest(page))
		require.ErrorIs(t, err, ErrWikiPageSlugConflict)
		require.Nil(t, result)
		require.Equal(t, before, snapshotWikiRename(t, db))
	})
}

func TestWikiRenameRejectsStaleIdentityAndInvalidRequests(t *testing.T) {
	db := newWikiRenameDB(t, "sqlite")
	page := makeWikiPage("kb-a", "concept/old", types.WikiPageTypeConcept, types.WikiPageStatusPublished)
	require.NoError(t, db.Create(page).Error)
	before := snapshotWikiRename(t, db)
	requests := []interfaces.WikiPageRenameRequest{
		{KnowledgeBaseID: "kb-a", PageID: "replaced-page", OldSlug: page.Slug, NewSlug: "concept/new"},
		{KnowledgeBaseID: "kb-b", PageID: page.ID, OldSlug: page.Slug, NewSlug: "concept/new"},
		{},
	}
	for _, slug := range []string{"", page.Slug, "CONCEPT/New", "concept//new", "concept/new|label", "concept/new';DROP TABLE wiki_pages;--", strings.Repeat("a", 256)} {
		req := wikiRenameRequest(page)
		req.NewSlug = slug
		requests = append(requests, req)
	}
	for _, req := range requests {
		result, err := NewWikiPageRepository(db).(interfaces.WikiPageRenamer).RenamePage(context.Background(), req)
		require.Error(t, err)
		require.Nil(t, result)
		require.Equal(t, before, snapshotWikiRename(t, db))
	}
}

func TestWikiRenamePostgresWaitsForConcurrentWriter(t *testing.T) {
	db := newWikiRenameDB(t, "postgres")
	page := makeWikiPage("kb-a", "concept/old", types.WikiPageTypeConcept, types.WikiPageStatusPublished)
	require.NoError(t, db.Create(page).Error)
	writer := db.Begin()
	require.NoError(t, writer.Error)
	defer writer.Rollback()
	require.NoError(t, writer.Model(&types.WikiPage{}).Where("id = ?", page.ID).
		Updates(map[string]interface{}{"content": "concurrent edit [[concept/old]]", "version": 2}).Error)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	type outcome struct {
		result *interfaces.WikiPageRenameResult
		err    error
	}
	done := make(chan outcome, 1)
	go func() {
		result, err := NewWikiPageRepository(db).(interfaces.WikiPageRenamer).RenamePage(ctx, wikiRenameRequest(page))
		done <- outcome{result, err}
	}()
	select {
	case got := <-done:
		t.Fatalf("rename did not wait for the locked row: %v", got.err)
	case <-time.After(100 * time.Millisecond):
	}
	require.NoError(t, writer.Commit().Error)
	got := <-done
	require.NoError(t, got.err)
	require.Equal(t, 2, got.result.Page.Version)
	require.Equal(t, "concurrent edit [[concept/new]]", got.result.Page.Content)
}

func TestWikiRenameConcurrentDestinationHasOneWinner(t *testing.T) {
	withWikiRenameDB(t, func(t *testing.T, db *gorm.DB) {
		first := makeWikiPage("kb-a", "concept/first", types.WikiPageTypeConcept, types.WikiPageStatusPublished)
		second := makeWikiPage("kb-a", "concept/second", types.WikiPageTypeConcept, types.WikiPageStatusPublished)
		require.NoError(t, db.Create(first).Error)
		require.NoError(t, db.Create(second).Error)
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		start, done := make(chan struct{}), make(chan error, 2)
		for _, page := range []*types.WikiPage{first, second} {
			go func() {
				<-start
				_, err := NewWikiPageRepository(db).(interfaces.WikiPageRenamer).RenamePage(ctx, wikiRenameRequest(page))
				done <- err
			}()
		}
		close(start)
		errorsSeen := []error{<-done, <-done}
		wins := 0
		for _, err := range errorsSeen {
			if err == nil {
				wins++
			} else {
				require.ErrorIs(t, err, ErrWikiPageSlugConflict)
			}
		}
		require.Equal(t, 1, wins)
		var count int64
		require.NoError(t, db.Model(&types.WikiPage{}).Count(&count).Error)
		require.EqualValues(t, 2, count)
	})
}

func TestWikiRenameChunkSyncIsConditionalAndNeverUpserts(t *testing.T) {
	withWikiRenameDB(t, func(t *testing.T, db *gorm.DB) {
		// The legacy wp- prefix exceeds the production UUID column width;
		// fixtures explicitly admit legacy Wiki projection IDs for this test.
		ddl := `CREATE TABLE chunks (
			id VARCHAR(64) PRIMARY KEY, tenant_id BIGINT, knowledge_base_id VARCHAR(36),
			knowledge_id VARCHAR(36), chunk_type VARCHAR(20), content TEXT, source_content TEXT,
			content_revision INTEGER, metadata TEXT, content_hash TEXT, index_status TEXT,
			is_enabled BOOLEAN, updated_at TIMESTAMP, deleted_at TIMESTAMP)`
		if db.Name() == "postgres" {
			ddl = strings.ReplaceAll(ddl, "TIMESTAMP", "TIMESTAMPTZ")
		}
		require.NoError(t, db.Exec(ddl).Error)
		page := makeWikiPage("kb-a", "concept/new", types.WikiPageTypeConcept, types.WikiPageStatusPublished)
		require.NoError(t, db.Create(page).Error)
		page, err := NewWikiPageRepository(db).GetByID(context.Background(), page.ID)
		require.NoError(t, err)
		chunkID := "wp-" + page.ID
		now := time.Now().UTC().Truncate(time.Microsecond)
		require.NoError(t, db.Exec(`INSERT INTO chunks
			(id, tenant_id, knowledge_base_id, knowledge_id, chunk_type, content, source_content,
			content_revision, metadata, content_hash, index_status, is_enabled, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, chunkID, page.TenantID, page.KnowledgeBaseID, page.ID,
			types.ChunkTypeWikiPage, "before", "source", 3, `{}`, "old-hash", "ready", false, now).Error)
		chunkRepo := NewChunkRepository(db)
		chunk, err := chunkRepo.GetChunkByID(context.Background(), page.TenantID, chunkID)
		require.NoError(t, err)
		expected := chunk.UpdatedAt
		chunk.Content, chunk.Metadata = "after", types.JSON(`{"wiki_slug":"concept/new"}`)
		chunk.SourceContent, chunk.IsEnabled = "must not overwrite", true
		chunk.UpdatedAt = expected.Add(time.Second)
		updater := chunkRepo.(interfaces.WikiPageRenameChunkUpdater)
		require.NoError(t, updater.UpdateRenamedWikiChunk(context.Background(), page, chunk, expected))
		stored, err := chunkRepo.GetChunkByID(context.Background(), page.TenantID, chunkID)
		require.NoError(t, err)
		require.Equal(t, "after", stored.Content)
		require.Equal(t, "source", stored.SourceContent)
		require.False(t, stored.IsEnabled)
		require.Equal(t, 3, stored.ContentRevision)
		require.ErrorIs(t, updater.UpdateRenamedWikiChunk(context.Background(), page, chunk, expected), ErrChunkRevisionConflict)
		require.NoError(t, db.Model(&types.WikiPage{}).Where("id = ?", page.ID).Update("slug", "concept/newer").Error)
		require.ErrorIs(t, updater.UpdateRenamedWikiChunk(context.Background(), page, stored, stored.UpdatedAt), ErrWikiPageConflict)
		page, err = NewWikiPageRepository(db).GetByID(context.Background(), page.ID)
		require.NoError(t, err)
		require.NoError(t, db.Where("id = ?", chunkID).Delete(&types.Chunk{}).Error)
		require.ErrorIs(t, updater.UpdateRenamedWikiChunk(context.Background(), page, stored, stored.UpdatedAt), ErrChunkRevisionConflict)
		var liveCount int64
		require.NoError(t, db.Model(&types.Chunk{}).Where("id = ?", chunkID).Count(&liveCount).Error)
		require.Zero(t, liveCount)
	})
}
