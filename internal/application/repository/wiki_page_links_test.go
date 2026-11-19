package repository

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/postgres"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// wikiBacklinkMutator 让测试也能编译于旧基线；旧版缺少接口不算行为回归的失败对照。
type wikiBacklinkMutator interface {
	AddInLink(context.Context, uint64, string, string, string) error
	RemoveInLink(context.Context, uint64, string, string, string) error
}

// wikiInLinkBackends 为每组测试分配独立数据库，PostgreSQL 只操作本测试创建的 schema。
func wikiInLinkBackends(t *testing.T, test func(*testing.T, *gorm.DB, wikiBacklinkMutator)) {
	t.Helper()
	for _, dialect := range []string{"sqlite", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			var db *gorm.DB
			var err error
			config := &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)}
			if dialect == "sqlite" {
				dsn := filepath.Join(t.TempDir(), "wiki-links.db") + "?_busy_timeout=10000&_journal_mode=WAL"
				db, err = gorm.Open(sqlite.Open(dsn), config)
				require.NoError(t, err)
				require.NoError(t, db.Exec(wikiPagesTestDDL).Error)
			} else {
				dsn := os.Getenv("WEKNORA_REPOSITORY_TEST_POSTGRES_DSN")
				if dsn == "" {
					t.Skip("设置 WEKNORA_REPOSITORY_TEST_POSTGRES_DSN 才运行隔离 PostgreSQL 反链测试")
				}
				admin, openErr := gorm.Open(postgres.Open(dsn), config)
				require.NoError(t, openErr)
				adminSQL, sqlErr := admin.DB()
				require.NoError(t, sqlErr)
				schema := "wiki_inlinks_" + strings.ReplaceAll(uuid.NewString(), "-", "")
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
				db, err = gorm.Open(postgres.Open(dsn+separator+"search_path="+schema), config)
				require.NoError(t, err)
				ddl := strings.ReplaceAll(wikiPagesTestDDL, "DATETIME", "TIMESTAMPTZ")
				ddl = strings.ReplaceAll(ddl, "TEXT DEFAULT '[]'", "JSONB DEFAULT '[]'")
				ddl = strings.ReplaceAll(ddl, "TEXT DEFAULT '{}'", "JSONB DEFAULT '{}'")
				require.NoError(t, db.Exec(ddl).Error)
			}
			sqlDB, err := db.DB()
			require.NoError(t, err)
			sqlDB.SetMaxOpenConns(8)
			t.Cleanup(func() { require.NoError(t, sqlDB.Close()) })
			repo, ok := NewWikiPageRepository(db).(wikiBacklinkMutator)
			require.True(t, ok, "仓储必须提供原子反链操作")
			test(t, db, repo)
		})
	}
}

// installWikiInLinkCounter 记录实际落到行上的 UPDATE，不能用最终数组相同替代零写入断言。
func installWikiInLinkCounter(t *testing.T, db *gorm.DB) {
	t.Helper()
	require.NoError(t, db.Exec("CREATE TABLE wiki_link_writes (page_id TEXT NOT NULL)").Error)
	if db.Name() == "sqlite" {
		require.NoError(t, db.Exec(`CREATE TRIGGER count_wiki_link_updates AFTER UPDATE ON wiki_pages
		BEGIN INSERT INTO wiki_link_writes(page_id) VALUES (NEW.id); END`).Error)
	} else {
		require.NoError(t, db.Exec(`CREATE FUNCTION count_wiki_link_updates() RETURNS trigger LANGUAGE plpgsql AS $$
		BEGIN INSERT INTO wiki_link_writes(page_id) VALUES (NEW.id); RETURN NEW; END $$`).Error)
		require.NoError(t, db.Exec(`CREATE TRIGGER count_wiki_link_updates AFTER UPDATE ON wiki_pages
		FOR EACH ROW EXECUTE FUNCTION count_wiki_link_updates()`).Error)
	}
}

// forbidWikiInLinkWideWrites 连相同值的无关列赋值也拒绝，保护大引用字段不被反链操作带入 UPDATE。
func forbidWikiInLinkWideWrites(t *testing.T, db *gorm.DB) {
	t.Helper()
	const columns = "source_refs, chunk_refs, page_metadata, content, summary, title, out_links, aliases, status, " +
		"parent_slug, folder_id, category_path, wiki_path, depth, sort_order, version"
	if db.Name() == "sqlite" {
		require.NoError(t, db.Exec(`CREATE TRIGGER reject_wide_link_update BEFORE UPDATE OF `+columns+` ON wiki_pages
		BEGIN SELECT RAISE(ABORT, 'backlink operation attempted an unrelated column update'); END`).Error)
	} else {
		require.NoError(t, db.Exec(`CREATE FUNCTION reject_wide_link_update() RETURNS trigger LANGUAGE plpgsql AS $$
		BEGIN RAISE EXCEPTION 'backlink operation attempted an unrelated column update'; END $$`).Error)
		require.NoError(t, db.Exec(`CREATE TRIGGER reject_wide_link_update BEFORE UPDATE OF `+columns+` ON wiki_pages
		FOR EACH ROW EXECUTE FUNCTION reject_wide_link_update()`).Error)
	}
}

func wikiInLinkWriteCount(t *testing.T, db *gorm.DB) int64 {
	t.Helper()
	var count int64
	require.NoError(t, db.Table("wiki_link_writes").Count(&count).Error)
	return count
}

func readWikiInLinkPage(t *testing.T, db *gorm.DB, id string) *types.WikiPage {
	t.Helper()
	var page types.WikiPage
	require.NoError(t, db.Unscoped().Where("id = ?", id).First(&page).Error)
	return &page
}

func TestWikiInLinksIdempotentAndNarrowWrites(t *testing.T) {
	wikiInLinkBackends(t, func(t *testing.T, db *gorm.DB, repo wikiBacklinkMutator) {
		page := makeWikiPage("kb", "entity/target", types.WikiPageTypeEntity, types.WikiPageStatusPublished)
		page.InLinks = types.StringArray{"entity/source", "entity/other"}
		page.OutLinks = types.StringArray{"entity/outgoing"}
		page.Aliases = types.StringArray{"别名"}
		page.ChunkRefs = types.StringArray{"chunk-before"}
		for i := range 256 {
			page.SourceRefs = append(page.SourceRefs, fmt.Sprintf("source-%03d|%s", i, strings.Repeat("合成引用", 16)))
		}
		page.UpdatedAt = time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)
		require.NoError(t, db.Create(page).Error)
		before := readWikiInLinkPage(t, db, page.ID)
		installWikiInLinkCounter(t, db)
		forbidWikiInLinkWideWrites(t, db)
		ctx := context.Background()
		require.NoError(t, repo.AddInLink(ctx, page.TenantID, "kb", page.Slug, "entity/source"))
		require.NoError(t, repo.RemoveInLink(ctx, page.TenantID, "kb", page.Slug, "entity/missing"))
		require.Zero(t, wikiInLinkWriteCount(t, db))
		require.True(t, before.UpdatedAt.Equal(readWikiInLinkPage(t, db, page.ID).UpdatedAt))

		require.NoError(t, repo.AddInLink(ctx, page.TenantID, "kb", page.Slug, "entity/new"))
		require.NoError(t, repo.AddInLink(ctx, page.TenantID, "kb", page.Slug, "entity/new"))
		require.EqualValues(t, 1, wikiInLinkWriteCount(t, db))
		require.NoError(t, repo.RemoveInLink(ctx, page.TenantID, "kb", page.Slug, "entity/source"))
		require.NoError(t, repo.RemoveInLink(ctx, page.TenantID, "kb", page.Slug, "entity/source"))
		require.EqualValues(t, 2, wikiInLinkWriteCount(t, db))
		after := readWikiInLinkPage(t, db, page.ID)
		require.Equal(t, types.StringArray{"entity/other", "entity/new"}, after.InLinks)
		require.True(t, after.UpdatedAt.After(before.UpdatedAt))
		// 除反链和更新时间外，完整页面必须保持原样。
		after.InLinks, after.UpdatedAt = before.InLinks, before.UpdatedAt
		require.Equal(t, before, after)
	})
}

func TestWikiInLinksNullEmptyAndEscapedSlug(t *testing.T) {
	for _, emptyValue := range []string{"sql_null", "json_null", "empty_array"} {
		t.Run(emptyValue, func(t *testing.T) {
			wikiInLinkBackends(t, func(t *testing.T, db *gorm.DB, repo wikiBacklinkMutator) {
				page := makeWikiPage("kb", "entity/target", types.WikiPageTypeEntity, types.WikiPageStatusPublished)
				require.NoError(t, db.Create(page).Error)
				var raw any
				switch emptyValue {
				case "json_null":
					raw = []byte("null")
				case "empty_array":
					raw = []byte("[]")
				}
				require.NoError(t, db.Exec("UPDATE wiki_pages SET in_links = ? WHERE id = ?", raw, page.ID).Error)
				installWikiInLinkCounter(t, db)
				ctx := context.Background()
				const source = `entity/引号'"; DROP TABLE wiki_pages; --`
				require.NoError(t, repo.RemoveInLink(ctx, page.TenantID, "kb", page.Slug, source))
				require.Zero(t, wikiInLinkWriteCount(t, db))
				require.NoError(t, repo.AddInLink(ctx, page.TenantID, "kb", page.Slug, source))
				require.NoError(t, repo.AddInLink(ctx, page.TenantID, "kb", page.Slug, source))
				require.Equal(t, types.StringArray{source}, readWikiInLinkPage(t, db, page.ID).InLinks)
				require.EqualValues(t, 1, wikiInLinkWriteCount(t, db))
				require.NoError(t, repo.RemoveInLink(ctx, page.TenantID, "kb", page.Slug, source))
				require.Empty(t, readWikiInLinkPage(t, db, page.ID).InLinks)
				require.EqualValues(t, 2, wikiInLinkWriteCount(t, db))
			})
		})
	}
}

func TestWikiInLinksRemovalClearsDuplicateEdges(t *testing.T) {
	wikiInLinkBackends(t, func(t *testing.T, db *gorm.DB, repo wikiBacklinkMutator) {
		page := makeWikiPage("kb", "entity/target", types.WikiPageTypeEntity, types.WikiPageStatusPublished)
		page.InLinks = types.StringArray{"entity/source", "entity/keep", "entity/source"}
		require.NoError(t, db.Create(page).Error)
		installWikiInLinkCounter(t, db)
		require.NoError(t, repo.AddInLink(context.Background(), page.TenantID, "kb", page.Slug, "entity/source"))
		require.Zero(t, wikiInLinkWriteCount(t, db))
		require.NoError(t, repo.RemoveInLink(context.Background(), page.TenantID, "kb", page.Slug, "entity/source"))
		require.Equal(t, types.StringArray{"entity/keep"}, readWikiInLinkPage(t, db, page.ID).InLinks)
		require.EqualValues(t, 1, wikiInLinkWriteCount(t, db))
	})
}

func TestWikiInLinksScopeAndMissingTargets(t *testing.T) {
	wikiInLinkBackends(t, func(t *testing.T, db *gorm.DB, repo wikiBacklinkMutator) {
		page := makeWikiPage("kb", "entity/target", types.WikiPageTypeEntity, types.WikiPageStatusPublished)
		page.InLinks = types.StringArray{"entity/source"}
		other := makeWikiPage("other-kb", page.Slug, types.WikiPageTypeEntity, types.WikiPageStatusPublished)
		other.TenantID = 2
		other.InLinks = types.StringArray{"entity/source"}
		deleted := makeWikiPage("kb", "entity/deleted", types.WikiPageTypeEntity, types.WikiPageStatusPublished)
		deleted.InLinks = types.StringArray{"entity/source"}
		for _, item := range []*types.WikiPage{page, other, deleted} {
			require.NoError(t, db.Create(item).Error)
		}
		require.NoError(t, db.Delete(deleted).Error)
		installWikiInLinkCounter(t, db)
		for _, target := range []struct {
			tenant   uint64
			kb, slug string
		}{
			{2, "kb", page.Slug},
			{1, "other-kb", page.Slug},
			{1, "missing-kb", page.Slug},
			{1, "kb", "entity/missing"},
			{1, "kb", deleted.Slug},
		} {
			require.NoError(t,
				repo.AddInLink(context.Background(), target.tenant, target.kb, target.slug, "entity/new"))
			require.NoError(t,
				repo.RemoveInLink(context.Background(), target.tenant, target.kb, target.slug, "entity/source"))
		}
		require.Zero(t, wikiInLinkWriteCount(t, db))
		for _, item := range []*types.WikiPage{page, other, deleted} {
			require.Equal(t, types.StringArray{"entity/source"}, readWikiInLinkPage(t, db, item.ID).InLinks)
		}
	})
}

func TestWikiInLinksCanceledContextDoesNotWrite(t *testing.T) {
	wikiInLinkBackends(t, func(t *testing.T, db *gorm.DB, repo wikiBacklinkMutator) {
		page := makeWikiPage("kb", "entity/target", types.WikiPageTypeEntity, types.WikiPageStatusPublished)
		page.InLinks = types.StringArray{"entity/source"}
		require.NoError(t, db.Create(page).Error)
		installWikiInLinkCounter(t, db)
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		require.ErrorIs(t, repo.AddInLink(ctx, page.TenantID, "kb", page.Slug, "entity/new"), context.Canceled)
		require.ErrorIs(t, repo.RemoveInLink(ctx, page.TenantID, "kb", page.Slug, "entity/source"), context.Canceled)
		require.Zero(t, wikiInLinkWriteCount(t, db))
		require.Equal(t, page.InLinks, readWikiInLinkPage(t, db, page.ID).InLinks)
	})
}

// runWikiInLinkWriters 同时释放多连接写入，goroutine 只回传错误，不调用 require.FailNow。
func runWikiInLinkWriters(t *testing.T, operations []func() error) {
	t.Helper()
	start := make(chan struct{})
	results := make(chan error, len(operations))
	var wait sync.WaitGroup
	for _, operation := range operations {
		wait.Add(1)
		go func() {
			defer wait.Done()
			<-start
			results <- operation()
		}()
	}
	close(start)
	wait.Wait()
	close(results)
	for err := range results {
		require.NoError(t, err)
	}
}

func TestWikiInLinksConcurrentAdditionsAreNotLost(t *testing.T) {
	wikiInLinkBackends(t, func(t *testing.T, db *gorm.DB, repo wikiBacklinkMutator) {
		page := makeWikiPage("kb", "entity/target", types.WikiPageTypeEntity, types.WikiPageStatusPublished)
		page.InLinks = types.StringArray{"entity/keep"}
		require.NoError(t, db.Create(page).Error)
		installWikiInLinkCounter(t, db)
		const sources = 16
		var operations []func() error
		want := types.StringArray{"entity/keep"}
		for i := range sources {
			source := fmt.Sprintf("entity/source-%02d", i)
			want = append(want, source)
			// 同一个来源重复提交，只有第一次有效添加允许真正更新目标页。
			for range 2 {
				operations = append(operations, func() error {
					return repo.AddInLink(context.Background(), page.TenantID, "kb", page.Slug, source)
				})
			}
		}
		runWikiInLinkWriters(t, operations)
		require.ElementsMatch(t, want, readWikiInLinkPage(t, db, page.ID).InLinks)
		require.EqualValues(t, sources, wikiInLinkWriteCount(t, db))
	})
}

func TestWikiInLinksConcurrentAddAndRemovePreserveOtherSources(t *testing.T) {
	wikiInLinkBackends(t, func(t *testing.T, db *gorm.DB, repo wikiBacklinkMutator) {
		page := makeWikiPage("kb", "entity/target", types.WikiPageTypeEntity, types.WikiPageStatusPublished)
		page.InLinks = types.StringArray{"entity/keep"}
		const sources = 16
		for i := range sources {
			page.InLinks = append(page.InLinks, fmt.Sprintf("entity/old-%02d", i))
		}
		require.NoError(t, db.Create(page).Error)
		installWikiInLinkCounter(t, db)
		var operations []func() error
		want := types.StringArray{"entity/keep"}
		for i := range sources {
			oldSource := fmt.Sprintf("entity/old-%02d", i)
			newSource := fmt.Sprintf("entity/new-%02d", i)
			want = append(want, newSource)
			operations = append(operations,
				func() error {
					return repo.RemoveInLink(context.Background(), page.TenantID, "kb", page.Slug, oldSource)
				},
				func() error { return repo.AddInLink(context.Background(), page.TenantID, "kb", page.Slug, newSource) },
			)
		}
		runWikiInLinkWriters(t, operations)
		require.ElementsMatch(t, want, readWikiInLinkPage(t, db, page.ID).InLinks)
		require.EqualValues(t, 2*sources, wikiInLinkWriteCount(t, db))
	})
}

func TestWikiInLinksUpdateMetaDoesNotRestoreStaleBacklinks(t *testing.T) {
	wikiInLinkBackends(t, func(t *testing.T, db *gorm.DB, repo wikiBacklinkMutator) {
		page := makeWikiPage("kb", "entity/target", types.WikiPageTypeEntity, types.WikiPageStatusPublished)
		page.InLinks = types.StringArray{"entity/old"}
		require.NoError(t, db.Create(page).Error)
		stale := readWikiInLinkPage(t, db, page.ID)
		require.NoError(t, repo.AddInLink(context.Background(), page.TenantID, "kb", page.Slug, "entity/new"))
		stale.SourceRefs = types.StringArray{"new-source-ref"}
		stale.UpdatedAt = time.Now()
		require.NoError(t, NewWikiPageRepository(db).UpdateMeta(context.Background(), stale))
		actual := readWikiInLinkPage(t, db, page.ID)
		require.ElementsMatch(t, []string{"entity/old", "entity/new"}, actual.InLinks)
		require.Equal(t, stale.SourceRefs, actual.SourceRefs)
	})
}

// wikiLinkSnapshotUpdater 只用于重建整组链接，写入必须验证读到的页面快照仍然有效。
type wikiLinkSnapshotUpdater interface {
	UpdateLinks(context.Context, *types.WikiPage, *types.WikiPage) error
}

func wikiSnapshotUpdater(t *testing.T, db *gorm.DB) wikiLinkSnapshotUpdater {
	t.Helper()
	repo, ok := NewWikiPageRepository(db).(wikiLinkSnapshotUpdater)
	require.True(t, ok, "仓储必须提供带快照校验的链接更新")
	return repo
}

func TestWikiUpdateLinksOnlyPersistsLinkFields(t *testing.T) {
	wikiInLinkBackends(t, func(t *testing.T, db *gorm.DB, _ wikiBacklinkMutator) {
		page := makeWikiPage("kb", "entity/target", types.WikiPageTypeEntity, types.WikiPageStatusPublished)
		page.InLinks = types.StringArray{"entity/old"}
		page.OutLinks = types.StringArray{"entity/out-old"}
		page.SourceRefs = types.StringArray{"source-ref"}
		page.ChunkRefs = types.StringArray{"chunk-ref"}
		require.NoError(t, db.Create(page).Error)
		before := readWikiInLinkPage(t, db, page.ID)
		next := *before
		next.InLinks = types.StringArray{"entity/new"}
		next.OutLinks = types.StringArray{"entity/out-new"}
		next.UpdatedAt = time.Now()
		// 传入对象中的无关字段即使变化，也不能被链接重建写回。
		next.SourceRefs = types.StringArray{"must-not-save"}
		next.Content = "must-not-save"
		next.Version++
		installWikiInLinkCounter(t, db)
		require.NoError(t, wikiSnapshotUpdater(t, db).UpdateLinks(context.Background(), before, &next))
		after := readWikiInLinkPage(t, db, page.ID)
		require.Equal(t, next.InLinks, after.InLinks)
		require.Equal(t, next.OutLinks, after.OutLinks)
		require.EqualValues(t, 1, wikiInLinkWriteCount(t, db))
		after.InLinks, after.OutLinks, after.UpdatedAt = before.InLinks, before.OutLinks, before.UpdatedAt
		require.Equal(t, before, after)
	})
}

func TestWikiUpdateLinksRejectsConcurrentChanges(t *testing.T) {
	for _, changed := range []string{"in_links", "out_links", "content", "version"} {
		t.Run(changed, func(t *testing.T) {
			wikiInLinkBackends(t, func(t *testing.T, db *gorm.DB, repo wikiBacklinkMutator) {
				page := makeWikiPage("kb", "entity/target", types.WikiPageTypeEntity, types.WikiPageStatusPublished)
				page.InLinks = types.StringArray{"entity/before"}
				page.OutLinks = types.StringArray{"entity/out-before"}
				require.NoError(t, db.Create(page).Error)
				before := readWikiInLinkPage(t, db, page.ID)
				next := *before
				next.InLinks = types.StringArray{"entity/rebuilt"}
				next.OutLinks = types.StringArray{"entity/out-rebuilt"}
				next.UpdatedAt = time.Now()
				installWikiInLinkCounter(t, db)
				switch changed {
				case "in_links":
					require.NoError(t,
						repo.AddInLink(context.Background(), page.TenantID, "kb", page.Slug, "entity/concurrent"))
				case "out_links":
					require.NoError(t, db.Model(page).
						UpdateColumn("out_links", types.StringArray{"entity/out-concurrent"}).Error)
				case "content":
					// 不改变 version/updated_at，确保正文比较本身能识别并发改动。
					require.NoError(t, db.Model(page).UpdateColumn("content", "concurrent edit").Error)
				case "version":
					require.NoError(t, db.Model(page).UpdateColumn("version", page.Version+1).Error)
				}
				concurrent := readWikiInLinkPage(t, db, page.ID)
				err := wikiSnapshotUpdater(t, db).UpdateLinks(context.Background(), before, &next)
				require.ErrorIs(t, err, ErrWikiPageConflict)
				require.EqualValues(t, 1, wikiInLinkWriteCount(t, db))
				require.Equal(t, concurrent, readWikiInLinkPage(t, db, page.ID))
			})
		})
	}
}

func TestWikiUpdateLinksRejectsIdentityChanges(t *testing.T) {
	wikiInLinkBackends(t, func(t *testing.T, db *gorm.DB, _ wikiBacklinkMutator) {
		page := makeWikiPage("kb", "entity/target", types.WikiPageTypeEntity, types.WikiPageStatusPublished)
		require.NoError(t, db.Create(page).Error)
		before := readWikiInLinkPage(t, db, page.ID)
		installWikiInLinkCounter(t, db)
		for _, field := range []string{"id", "tenant", "kb", "slug"} {
			next := *before
			next.InLinks = types.StringArray{"entity/rebuilt"}
			switch field {
			case "id":
				next.ID = uuid.NewString()
			case "tenant":
				next.TenantID++
			case "kb":
				next.KnowledgeBaseID = "other-kb"
			case "slug":
				next.Slug = "entity/other"
			}
			require.ErrorIs(t,
				wikiSnapshotUpdater(t, db).UpdateLinks(context.Background(), before, &next), ErrWikiPageConflict)
		}
		require.Zero(t, wikiInLinkWriteCount(t, db))
		require.Equal(t, before, readWikiInLinkPage(t, db, page.ID))
	})
}

func TestWikiUpdateLinksAcceptsNullLinkSnapshots(t *testing.T) {
	for _, nullValue := range []string{"sql_null", "json_null"} {
		t.Run(nullValue, func(t *testing.T) {
			wikiInLinkBackends(t, func(t *testing.T, db *gorm.DB, _ wikiBacklinkMutator) {
				page := makeWikiPage("kb", "entity/target", types.WikiPageTypeEntity, types.WikiPageStatusPublished)
				require.NoError(t, db.Create(page).Error)
				var raw any
				if nullValue == "json_null" {
					raw = []byte("null")
				}
				require.NoError(t, db.Exec("UPDATE wiki_pages SET in_links = ?, out_links = ? WHERE id = ?",
					raw, raw, page.ID).Error)
				before := readWikiInLinkPage(t, db, page.ID)
				next := *before
				next.InLinks = types.StringArray{"entity/repaired"}
				next.OutLinks = types.StringArray{"entity/out-repaired"}
				next.UpdatedAt = time.Now()
				installWikiInLinkCounter(t, db)
				require.NoError(t, wikiSnapshotUpdater(t, db).UpdateLinks(context.Background(), before, &next))
				after := readWikiInLinkPage(t, db, page.ID)
				require.Equal(t, next.InLinks, after.InLinks)
				require.Equal(t, next.OutLinks, after.OutLinks)
				require.EqualValues(t, 1, wikiInLinkWriteCount(t, db))
			})
		})
	}
}
