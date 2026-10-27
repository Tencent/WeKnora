package service

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/postgres"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

const (
	backlinkTestKB            = "kb-backlink-regression"
	backlinkTestTenant uint64 = 71
)

type backlinkHarness struct {
	db   *gorm.DB
	repo interfaces.WikiPageRepository
	svc  interfaces.WikiPageService
}

// backlinkBackends 为每个用例创建独立数据库或 schema；从不使用应用配置中的数据库。
func backlinkBackends(t *testing.T, run func(*testing.T, backlinkHarness)) {
	t.Helper()
	for _, backend := range []string{"sqlite", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			var db *gorm.DB
			var err error
			config := &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)}
			if backend == "sqlite" {
				db, err = gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "wiki.db")+"?_busy_timeout=5000"), config)
				require.NoError(t, err)
				sqlDB, openErr := db.DB()
				require.NoError(t, openErr)
				sqlDB.SetMaxOpenConns(1)
				t.Cleanup(func() { _ = sqlDB.Close() })
			} else {
				dsn := os.Getenv("WEKNORA_REPOSITORY_TEST_POSTGRES_DSN")
				if dsn == "" {
					t.Skip("设置 WEKNORA_REPOSITORY_TEST_POSTGRES_DSN 才运行隔离 PostgreSQL 验证")
				}
				admin, openErr := gorm.Open(postgres.Open(dsn), config)
				require.NoError(t, openErr)
				schema := "wiki_backlink_service_" + strings.ReplaceAll(uuid.NewString(), "-", "")
				require.NoError(t, admin.Exec("CREATE SCHEMA "+schema).Error)
				// URL 与 keyword DSN 都只改当前测试连接的 schema，不改外部数据库配置。
				scopedDSN := dsn + " search_path=" + schema
				if strings.HasPrefix(dsn, "postgres://") || strings.HasPrefix(dsn, "postgresql://") {
					parsed, parseErr := url.Parse(dsn)
					require.NoError(t, parseErr)
					query := parsed.Query()
					query.Set("search_path", schema)
					parsed.RawQuery = query.Encode()
					scopedDSN = parsed.String()
				}
				db, err = gorm.Open(postgres.Open(scopedDSN), config)
				require.NoError(t, err)
				sqlDB, openErr := db.DB()
				require.NoError(t, openErr)
				sqlDB.SetMaxOpenConns(12)
				t.Cleanup(func() {
					_ = sqlDB.Close()
					require.NoError(t, admin.Exec("DROP SCHEMA "+schema+" CASCADE").Error)
					adminDB, _ := admin.DB()
					_ = adminDB.Close()
				})
			}
			require.NoError(t, db.AutoMigrate(&types.WikiPage{}, &types.WikiFolder{}, &types.WikiPageRevision{}))
			// 对齐真实迁移的知识库内唯一约束，避免模型 tag 的单列索引阻止跨库同名页。
			require.NoError(t, db.Exec("DROP INDEX IF EXISTS idx_kb_slug").Error)
			require.NoError(t, db.Exec(`
				CREATE UNIQUE INDEX idx_wiki_pages_kb_slug
				ON wiki_pages (knowledge_base_id, slug)
				WHERE deleted_at IS NULL
			`).Error)
			require.NoError(t, db.Exec("CREATE TABLE backlink_write_audit (page_id TEXT NOT NULL)").Error)
			if backend == "sqlite" {
				require.NoError(t, db.Exec(`
					CREATE TRIGGER backlink_write_count AFTER UPDATE ON wiki_pages
					BEGIN
						INSERT INTO backlink_write_audit(page_id) VALUES (NEW.id);
					END
				`).Error)
			} else {
				require.NoError(t, db.Exec(`
					CREATE FUNCTION count_backlink_write() RETURNS trigger LANGUAGE plpgsql AS $$
					BEGIN
						INSERT INTO backlink_write_audit(page_id) VALUES (NEW.id);
						RETURN NEW;
					END
					$$
				`).Error)
				require.NoError(t, db.Exec(`
					CREATE TRIGGER backlink_write_count AFTER UPDATE ON wiki_pages
					FOR EACH ROW EXECUTE FUNCTION count_backlink_write()
				`).Error)
			}
			repo := repository.NewWikiPageRepository(db)
			run(t, backlinkHarness{db: db, repo: repo, svc: NewWikiPageService(repo, nil, nil, nil, nil, nil)})
		})
	}
}

// seedBacklinkPage 直接落合成页面，避免初始化通过待测逻辑悄悄补好缺失反链。
func seedBacklinkPage(t *testing.T, h backlinkHarness, slug, content string, in, out []string) *types.WikiPage {
	t.Helper()
	stamp := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	page := &types.WikiPage{
		ID: uuid.NewString(), TenantID: backlinkTestTenant, KnowledgeBaseID: backlinkTestKB,
		Slug: slug, Title: slug, PageType: types.WikiPageTypeEntity, Status: types.WikiPageStatusPublished,
		Content: content, Summary: "保留摘要", Version: 3,
		SourceRefs: types.StringArray{"keep-source"}, ChunkRefs: types.StringArray{"keep-chunk"},
		InLinks: types.StringArray(in), OutLinks: types.StringArray(out), CreatedAt: stamp, UpdatedAt: stamp,
	}
	require.NoError(t, h.repo.Create(context.Background(), page))
	return page
}

func backlinkWriteCount(t *testing.T, h backlinkHarness, id string) int64 {
	t.Helper()
	var count int64
	require.NoError(t, h.db.Table("backlink_write_audit").Where("page_id = ?", id).Count(&count).Error)
	return count
}

func TestWikiBacklinksIncremental(t *testing.T) {
	cases := []struct {
		name               string
		old, next, initial []string
		wantWrites         int64
		wantIn             []string
	}{
		{
			"unchanged",
			[]string{"entity/target"},
			[]string{"entity/target"},
			[]string{"entity/source"},
			0,
			[]string{"entity/source"},
		},
		{"missing_backlink", []string{"entity/target"}, []string{"entity/target"}, nil, 1, []string{"entity/source"}},
		{"add", nil, []string{"entity/target"}, []string{"entity/other"}, 1, []string{"entity/other", "entity/source"}},
		{
			"remove",
			[]string{"entity/target"},
			nil,
			[]string{"entity/other", "entity/source"},
			1,
			[]string{"entity/other"},
		},
		{
			"remove_already_missing",
			[]string{"entity/target"},
			nil,
			[]string{"entity/other"},
			0,
			[]string{"entity/other"},
		},
		{
			"duplicate_link",
			[]string{"entity/target"},
			[]string{"entity/target", "entity/target"},
			[]string{"entity/source"},
			0,
			[]string{"entity/source"},
		},
	}
	for _, entry := range []string{"content", "autolink"} {
		for _, tc := range cases {
			t.Run(entry+"/"+tc.name, func(t *testing.T) {
				backlinkBackends(t, func(t *testing.T, h backlinkHarness) {
					target := seedBacklinkPage(t, h, "entity/target", "目标正文", tc.initial, nil)
					source := seedBacklinkPage(t, h, "entity/source", backlinkBody(tc.old), nil, tc.old)
					source.Content = "修改后的正文 " + backlinkBody(tc.next)
					if entry == "content" {
						_, err := h.svc.UpdatePage(context.Background(), source)
						require.NoError(t, err)
					} else {
						require.NoError(t, h.svc.UpdateAutoLinkedContent(context.Background(), source))
					}
					current, err := h.repo.GetByID(context.Background(), target.ID)
					require.NoError(t, err)
					require.ElementsMatch(t, tc.wantIn, current.InLinks)
					require.Equal(t, tc.wantWrites, backlinkWriteCount(t, h, target.ID), "统计实际数据库 UPDATE，而非仅统计最终边集合")
					require.Equal(t, target.SourceRefs, current.SourceRefs)
					require.Equal(t, target.ChunkRefs, current.ChunkRefs)
					require.Equal(t, target.Content, current.Content)
					require.Equal(t, target.Version, current.Version)
					if tc.wantWrites == 0 {
						require.True(t, target.UpdatedAt.Equal(current.UpdatedAt))
					}
				})
			})
		}
	}
}

func backlinkBody(slugs []string) string {
	var body strings.Builder
	body.WriteString("合成正文")
	for _, slug := range slugs {
		fmt.Fprintf(&body, " [[%s]]", slug)
	}
	return body.String()
}

func TestWikiBacklinksBookkeepingAndReorderingAreNoOps(t *testing.T) {
	backlinkBackends(t, func(t *testing.T, h backlinkHarness) {
		a := seedBacklinkPage(t, h, "entity/a", "A", []string{"entity/source"}, nil)
		b := seedBacklinkPage(t, h, "entity/b", "B", []string{"entity/source"}, nil)
		source := seedBacklinkPage(t, h, "entity/source",
			backlinkBody([]string{"entity/a", "entity/b"}), nil, []string{"entity/a", "entity/b"})
		_, err := h.svc.UpdatePage(context.Background(), source)
		require.NoError(t, err)
		source.Content = backlinkBody([]string{"entity/b", "entity/a"})
		require.NoError(t, h.svc.UpdateAutoLinkedContent(context.Background(), source))
		require.Zero(t, backlinkWriteCount(t, h, a.ID))
		require.Zero(t, backlinkWriteCount(t, h, b.ID))
	})
}

func TestWikiBacklinksMixedDelta(t *testing.T) {
	backlinkBackends(t, func(t *testing.T, h backlinkHarness) {
		a := seedBacklinkPage(t, h, "entity/a", "A", []string{"entity/source"}, nil)
		b := seedBacklinkPage(t, h, "entity/b", "B", []string{"entity/source"}, nil)
		c := seedBacklinkPage(t, h, "entity/c", "C", nil, nil)
		source := seedBacklinkPage(t, h, "entity/source",
			backlinkBody([]string{"entity/a", "entity/b"}), nil, []string{"entity/a", "entity/b"})
		source.Content = backlinkBody([]string{"entity/b", "entity/c"})
		_, err := h.svc.UpdatePage(context.Background(), source)
		require.NoError(t, err)
		for _, tc := range []struct {
			page   *types.WikiPage
			writes int64
			in     []string
		}{{a, 1, nil}, {b, 0, []string{"entity/source"}}, {c, 1, []string{"entity/source"}}} {
			actual, readErr := h.repo.GetByID(context.Background(), tc.page.ID)
			require.NoError(t, readErr)
			require.ElementsMatch(t, tc.in, actual.InLinks)
			require.Equal(t, tc.writes, backlinkWriteCount(t, h, tc.page.ID))
		}
	})
}

func TestWikiBacklinksCreateDeletePreserveOtherSources(t *testing.T) {
	backlinkBackends(t, func(t *testing.T, h backlinkHarness) {
		target := seedBacklinkPage(t, h, "entity/target", "目标", []string{"entity/other"}, nil)
		page, err := h.svc.CreatePage(context.Background(), &types.WikiPage{
			TenantID: backlinkTestTenant, KnowledgeBaseID: backlinkTestKB,
			Slug: "entity/source", Title: "来源", PageType: types.WikiPageTypeEntity,
			Content: backlinkBody([]string{"entity/target", "entity/target"}),
		})
		require.NoError(t, err)
		require.EqualValues(t, 1, backlinkWriteCount(t, h, target.ID))
		require.NoError(t, h.svc.DeletePage(context.Background(), backlinkTestKB, page.Slug))
		current, err := h.repo.GetByID(context.Background(), target.ID)
		require.NoError(t, err)
		require.Equal(t, types.StringArray{"entity/other"}, current.InLinks)
		require.EqualValues(t, 2, backlinkWriteCount(t, h, target.ID))
	})
}

// staleBacklinkMetaRepo 在旧快照读出后插入另一来源，确定性复现元数据回写覆盖反链。
type staleBacklinkMetaRepo struct {
	interfaces.WikiPageRepository
	before func()
}

func (r *staleBacklinkMetaRepo) UpdateMeta(ctx context.Context, page *types.WikiPage) error {
	if r.before != nil {
		fn := r.before
		r.before = nil
		fn()
	}
	return r.WikiPageRepository.UpdateMeta(ctx, page)
}

func TestWikiBacklinksMetadataPreservesConcurrentLink(t *testing.T) {
	backlinkBackends(t, func(t *testing.T, h backlinkHarness) {
		page := seedBacklinkPage(t, h, "entity/target", "保持正文", []string{"entity/old"}, nil)
		wrapped := &staleBacklinkMetaRepo{WikiPageRepository: h.repo, before: func() {
			require.NoError(t, h.db.Model(&types.WikiPage{}).Where("id = ?", page.ID).
				Update("in_links", types.StringArray{"entity/old", "entity/concurrent"}).Error)
		}}
		svc := NewWikiPageService(wrapped, nil, nil, nil, nil, nil)
		_, err := svc.UpdatePage(context.Background(), page)
		require.NoError(t, err)
		actual, err := h.repo.GetByID(context.Background(), page.ID)
		require.NoError(t, err)
		require.ElementsMatch(t, []string{"entity/old", "entity/concurrent"}, actual.InLinks)
	})
}

// changedAfterListRepo 在重建取得整批快照后插入变化，确保旧重建不能覆盖新链接。
type changedAfterListRepo struct {
	interfaces.WikiPageRepository
	after func()
}

func (r *changedAfterListRepo) ListAll(ctx context.Context, kbID string) ([]*types.WikiPage, error) {
	pages, err := r.WikiPageRepository.ListAll(ctx, kbID)
	if err == nil && r.after != nil {
		fn := r.after
		r.after = nil
		fn()
	}
	return pages, err
}

func TestWikiBacklinksRebuildSkipsUnchangedPages(t *testing.T) {
	backlinkBackends(t, func(t *testing.T, h backlinkHarness) {
		source := seedBacklinkPage(t, h, "entity/source",
			backlinkBody([]string{"entity/target"}), nil, []string{"entity/target"})
		target := seedBacklinkPage(t, h, "entity/target", "目标", []string{"entity/source"}, nil)
		require.NoError(t, h.svc.RebuildLinks(context.Background(), backlinkTestKB))
		require.Zero(t, backlinkWriteCount(t, h, source.ID))
		require.Zero(t, backlinkWriteCount(t, h, target.ID))
		require.NoError(t, h.db.Model(&types.WikiPage{}).Where("id = ?", target.ID).
			Update("in_links", types.StringArray{}).Error)
		require.NoError(t, h.db.Exec("DELETE FROM backlink_write_audit").Error)
		require.NoError(t, h.svc.RebuildLinks(context.Background(), backlinkTestKB))
		require.Zero(t, backlinkWriteCount(t, h, source.ID))
		require.EqualValues(t, 1, backlinkWriteCount(t, h, target.ID))
	})
}

func TestWikiBacklinksRebuildRejectsConcurrentChange(t *testing.T) {
	backlinkBackends(t, func(t *testing.T, h backlinkHarness) {
		seedBacklinkPage(t, h, "entity/source", backlinkBody([]string{"entity/target"}), nil, []string{"entity/target"})
		target := seedBacklinkPage(t, h, "entity/target", "目标", nil, nil)
		wrapped := &changedAfterListRepo{WikiPageRepository: h.repo, after: func() {
			require.NoError(t, h.db.Model(&types.WikiPage{}).Where("id = ?", target.ID).
				Update("in_links", types.StringArray{"entity/concurrent"}).Error)
		}}
		svc := NewWikiPageService(wrapped, nil, nil, nil, nil, nil)
		err := svc.RebuildLinks(context.Background(), backlinkTestKB)
		require.ErrorIs(t, err, repository.ErrWikiPageConflict)
		actual, readErr := h.repo.GetByID(context.Background(), target.ID)
		require.NoError(t, readErr)
		require.Equal(t, types.StringArray{"entity/concurrent"}, actual.InLinks)
	})
}

func TestWikiBacklinksConcurrentSources(t *testing.T) {
	backlinkBackends(t, func(t *testing.T, h backlinkHarness) {
		const writers = 12
		target := seedBacklinkPage(t, h, "entity/target", "目标", nil, nil)
		pages := make([]*types.WikiPage, writers)
		want := make([]string, writers)
		for i := range pages {
			want[i] = fmt.Sprintf("entity/source-%d", i)
			pages[i] = seedBacklinkPage(t, h, want[i], "原正文", nil, nil)
			pages[i].Content = backlinkBody([]string{"entity/target"})
		}
		start := make(chan struct{})
		errors := make(chan error, writers)
		var wg sync.WaitGroup
		for _, page := range pages {
			wg.Add(1)
			go func(p *types.WikiPage) {
				defer wg.Done()
				<-start
				errors <- h.svc.UpdateAutoLinkedContent(context.Background(), p)
			}(page)
		}
		close(start)
		wg.Wait()
		close(errors)
		for err := range errors {
			require.NoError(t, err)
		}
		actual, err := h.repo.GetByID(context.Background(), target.ID)
		require.NoError(t, err)
		require.ElementsMatch(t, want, actual.InLinks)
		require.EqualValues(t, writers, backlinkWriteCount(t, h, target.ID))
	})
}

// failingBacklinkSourceRepo 仅在源页持久化时返回错误，验证失败前不会更改目标反链。
type failingBacklinkSourceRepo struct {
	interfaces.WikiPageRepository
	failure error
	called  string
}

func (r *failingBacklinkSourceRepo) UpdateWithRevision(
	_ context.Context, _ *types.WikiPage, _ *types.WikiPageRevision,
) error {
	r.called = "content"
	return r.failure
}

func (r *failingBacklinkSourceRepo) UpdateMeta(_ context.Context, _ *types.WikiPage) error {
	r.called = "metadata"
	return r.failure
}

func (r *failingBacklinkSourceRepo) UpdateAutoLinkedContent(_ context.Context, _ *types.WikiPage) error {
	r.called = "autolink"
	return r.failure
}

func TestWikiBacklinksSourceWriteFailureLeavesTargetsUntouched(t *testing.T) {
	for _, entry := range []string{"content", "metadata", "autolink"} {
		t.Run(entry, func(t *testing.T) {
			backlinkBackends(t, func(t *testing.T, h backlinkHarness) {
				ctx := context.Background()
				oldTarget := seedBacklinkPage(t, h, "entity/old-target", "旧目标",
					[]string{"entity/other", "entity/source"}, nil)
				newTarget := seedBacklinkPage(t, h, "entity/new-target", "新目标", []string{"entity/other"}, nil)
				oldLinks := []string{oldTarget.Slug}
				if entry == "metadata" {
					// 同正文分支仍可能补缺失反链；源页保存失败时也必须禁止这次修补。
					oldLinks = append(oldLinks, newTarget.Slug)
				}
				source := seedBacklinkPage(t, h, "entity/source", backlinkBody(oldLinks), nil, oldLinks)
				before := make([]*types.WikiPage, 0, 3)
				for _, page := range []*types.WikiPage{source, oldTarget, newTarget} {
					snapshot, err := h.repo.GetByID(ctx, page.ID)
					require.NoError(t, err)
					before = append(before, snapshot)
				}
				if entry == "metadata" {
					source.SourceRefs = append(source.SourceRefs, "new-source-ref")
				} else {
					source.Content = "修改后的正文 " + backlinkBody([]string{newTarget.Slug})
				}
				failure := errors.New("synthetic source write failure")
				wrapped := &failingBacklinkSourceRepo{WikiPageRepository: h.repo, failure: failure}
				svc := NewWikiPageService(wrapped, nil, nil, nil, nil, nil)
				var err error
				if entry == "autolink" {
					err = svc.UpdateAutoLinkedContent(ctx, source)
				} else {
					_, err = svc.UpdatePage(ctx, source)
				}
				require.ErrorIs(t, err, failure)
				require.Equal(t, entry, wrapped.called, "确认命中了预期的源页写入分支")
				for _, snapshot := range before {
					actual, readErr := h.repo.GetByID(ctx, snapshot.ID)
					require.NoError(t, readErr)
					require.Equal(t, snapshot, actual, "失败后页面字段及反链必须保持原值")
					require.Zero(t, backlinkWriteCount(t, h, snapshot.ID), "失败前不能产生目标 UPDATE")
				}
			})
		})
	}
}

func TestWikiBacklinksUpdatesOnlyTargetKnowledgeBase(t *testing.T) {
	for _, entry := range []string{"content", "autolink"} {
		for _, operation := range []string{"add", "remove"} {
			for _, scope := range []struct {
				name   string
				tenant uint64
			}{
				{"same_tenant", backlinkTestTenant},
				{"different_tenant", backlinkTestTenant + 1},
			} {
				t.Run(entry+"/"+operation+"/"+scope.name, func(t *testing.T) {
					backlinkBackends(t, func(t *testing.T, h backlinkHarness) {
						ctx := context.Background()
						initial := []string{"entity/other"}
						var oldLinks []string
						nextLinks := []string{"entity/target"}
						want := []string{"entity/other", "entity/source"}
						if operation == "remove" {
							initial = append(initial, "entity/source")
							oldLinks = nextLinks
							nextLinks = nil
							want = []string{"entity/other"}
						}
						target := seedBacklinkPage(t, h, "entity/target", "正确知识库目标", initial, nil)
						// 不同知识库允许同名页面，错误范围的增删会直接改变这个对照目标。
						other := *target
						other.ID = uuid.NewString()
						other.TenantID = scope.tenant
						other.KnowledgeBaseID = "kb-backlink-other"
						require.NoError(t, h.repo.Create(ctx, &other))
						otherBefore, err := h.repo.GetByID(ctx, other.ID)
						require.NoError(t, err)
						source := seedBacklinkPage(t, h, "entity/source", backlinkBody(oldLinks), nil, oldLinks)
						source.Content = "修改后的正文 " + backlinkBody(nextLinks)
						if entry == "content" {
							_, err = h.svc.UpdatePage(ctx, source)
						} else {
							err = h.svc.UpdateAutoLinkedContent(ctx, source)
						}
						require.NoError(t, err)
						actual, err := h.repo.GetByID(ctx, target.ID)
						require.NoError(t, err)
						require.ElementsMatch(t, want, actual.InLinks)
						require.Equal(t, target.TenantID, actual.TenantID)
						require.Equal(t, target.KnowledgeBaseID, actual.KnowledgeBaseID)
						require.EqualValues(t, 1, backlinkWriteCount(t, h, target.ID))
						otherAfter, err := h.repo.GetByID(ctx, other.ID)
						require.NoError(t, err)
						require.Equal(t, otherBefore, otherAfter, "其他知识库的反链和更新时间均不得变化")
						require.Zero(t, backlinkWriteCount(t, h, other.ID))
					})
				})
			}
		}
	}
}
