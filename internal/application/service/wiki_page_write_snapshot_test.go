package service

import (
	"context"
	"path/filepath"
	"testing"

	apprepo "github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

// 每次测试使用独立文件并关闭连接，允许 race 重复运行而不共享历史数据。
func newWikiSnapshotService(t *testing.T) (*wikiPageService, *types.WikiPage, *gorm.DB) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "wiki.db")), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, sqlDB.Close()) })
	require.NoError(t, db.AutoMigrate(&types.WikiFolder{}, &types.WikiPage{}, &types.WikiPageRevision{}))
	svc := NewWikiPageService(apprepo.NewWikiPageRepository(db), nil, nil, nil, nil, nil).(*wikiPageService)
	page, err := svc.CreatePage(context.Background(), &types.WikiPage{
		TenantID: 7, KnowledgeBaseID: "kb-snapshot", Slug: "entity/example", Title: "Example",
		PageType: types.WikiPageTypeEntity, Content: "Original body",
		SourceRefs: types.StringArray{"original|Original"},
	})
	require.NoError(t, err)
	page, err = svc.GetPageBySlug(context.Background(), page.KnowledgeBaseID, page.Slug)
	require.NoError(t, err)
	return svc, page, db
}

// 模型读取后发生的正文、同版本装饰或来源更新，都不能被旧结果覆盖。
func TestWikiPageSnapshotServiceRejectsStaleModelOutput(t *testing.T) {
	for _, mutation := range []string{"revision", "auto_link", "source_refs"} {
		t.Run(mutation, func(t *testing.T) {
			svc, page, db := newWikiSnapshotService(t)
			ctx := types.WithWikiPageWriteSnapshot(context.Background(), page)
			other := *page
			switch mutation {
			case "revision":
				other.Content = "Concurrent body"
				_, err := svc.UpdatePage(context.Background(), &other)
				require.NoError(t, err)
			case "auto_link":
				other.Content = "Concurrent linked body"
				require.NoError(t, svc.UpdateAutoLinkedContent(context.Background(), &other))
			case "source_refs":
				other.SourceRefs = append(types.StringArray(nil), page.SourceRefs...)
				other.SourceRefs = append(other.SourceRefs, "other|Other")
				require.NoError(t, svc.UpdatePageMeta(context.Background(), &other))
			}
			current, err := svc.GetPageBySlug(context.Background(), page.KnowledgeBaseID, page.Slug)
			require.NoError(t, err)
			var countBefore int64
			require.NoError(t, db.Model(&types.WikiPageRevision{}).Count(&countBefore).Error)
			page.Content = "Stale generated body"
			got, err := svc.UpdatePage(ctx, page)
			require.ErrorIs(t, err, apprepo.ErrWikiPageConflict)
			require.Nil(t, got)
			after, err := svc.GetPageBySlug(context.Background(), page.KnowledgeBaseID, page.Slug)
			require.NoError(t, err)
			require.Equal(t, current, after, "拒绝过期输出不能改变当前页面")
			var countAfter int64
			require.NoError(t, db.Model(&types.WikiPageRevision{}).Count(&countAfter).Error)
			require.Equal(t, countBefore, countAfter, "失败尝试不能留下修订记录")
		})
	}
}

// 在 service 早检之后、仓储真正落库之前插入并发写，验证第二次检查不是摆设。
type wikiSnapshotGapRepo struct {
	interfaces.WikiPageRepository
	beforeWrite func()
}

func (r *wikiSnapshotGapRepo) UpdateWithRevision(
	ctx context.Context, page *types.WikiPage, revision *types.WikiPageRevision,
) error {
	r.beforeWrite()
	return r.WikiPageRepository.UpdateWithRevision(ctx, page, revision)
}

func (r *wikiSnapshotGapRepo) UpdateMeta(ctx context.Context, page *types.WikiPage) error {
	r.beforeWrite()
	return r.WikiPageRepository.UpdateMeta(ctx, page)
}

func TestWikiPageSnapshotServiceChecksAgainAtCommit(t *testing.T) {
	for _, changeBody := range []bool{false, true} {
		name := "metadata"
		if changeBody {
			name = "content"
		}
		t.Run(name, func(t *testing.T) {
			svc, page, db := newWikiSnapshotService(t)
			ctx := types.WithWikiPageWriteSnapshot(context.Background(), page)
			originalRepo := svc.repo
			svc.repo = &wikiSnapshotGapRepo{WikiPageRepository: originalRepo, beforeWrite: func() {
				concurrent, err := originalRepo.GetBySlug(context.Background(), page.KnowledgeBaseID, page.Slug)
				require.NoError(t, err)
				concurrent.SourceRefs = append(concurrent.SourceRefs, "other|Other")
				require.NoError(t, originalRepo.UpdateMeta(context.Background(), concurrent))
			}}
			page.SourceRefs = append(page.SourceRefs, "mine|Mine")
			if changeBody {
				page.Content = "My new body"
			}
			_, err := svc.UpdatePage(ctx, page)
			require.ErrorIs(t, err, apprepo.ErrWikiPageConflict)
			stored, err := originalRepo.GetBySlug(context.Background(), page.KnowledgeBaseID, page.Slug)
			require.NoError(t, err)
			require.Equal(t, types.StringArray{"original|Original", "other|Other"}, stored.SourceRefs)
			require.Equal(t, "Original body", stored.Content)
			var revisions int64
			require.NoError(t, db.Model(&types.WikiPageRevision{}).Count(&revisions).Error)
			require.Zero(t, revisions)
		})
	}
}

func TestWikiPageSnapshotServiceDoesNotGuardBacklinkTargets(t *testing.T) {
	svc, source, _ := newWikiSnapshotService(t)
	target, err := svc.CreatePage(context.Background(), &types.WikiPage{
		TenantID: source.TenantID, KnowledgeBaseID: source.KnowledgeBaseID,
		Slug: "entity/target", Title: "Target", PageType: types.WikiPageTypeEntity, Content: "Target body",
	})
	require.NoError(t, err)
	ctx := types.WithWikiPageWriteSnapshot(context.Background(), source)
	source.Content = "Original body with [[entity/target]]"
	_, err = svc.UpdatePage(ctx, source)
	require.NoError(t, err)
	stored, err := svc.GetPageBySlug(context.Background(), target.KnowledgeBaseID, target.Slug)
	require.NoError(t, err)
	require.Equal(t, types.StringArray{source.Slug}, stored.InLinks, "源页快照不得阻断目标页反链维护")
	require.Equal(t, target.Content, stored.Content)
}

// wikiSnapshotLinkRecorder 记录真正调用的原子接口，检查源页快照没有传给目标页。
type wikiSnapshotLinkRecorder struct {
	interfaces.WikiPageRepository
	added, removed map[string]int
	guardedCalls   int
}

func (r *wikiSnapshotLinkRecorder) AddInLink(
	ctx context.Context, tenantID uint64, kbID, target, source string,
) error {
	r.added[target]++
	if _, guarded := types.WikiPageWriteSnapshotFromContext(ctx); guarded {
		r.guardedCalls++
	}
	return r.WikiPageRepository.AddInLink(ctx, tenantID, kbID, target, source)
}

func (r *wikiSnapshotLinkRecorder) RemoveInLink(
	ctx context.Context, tenantID uint64, kbID, target, source string,
) error {
	r.removed[target]++
	if _, guarded := types.WikiPageWriteSnapshotFromContext(ctx); guarded {
		r.guardedCalls++
	}
	return r.WikiPageRepository.RemoveInLink(ctx, tenantID, kbID, target, source)
}

// 带快照的保存仍按主线差集删边、添加新边，并补齐保留边缺失的反链。
func TestWikiPageSnapshotServiceKeepsIncrementalBacklinkMaintenance(t *testing.T) {
	svc, source, _ := newWikiSnapshotService(t)
	ctx := context.Background()
	for _, slug := range []string{"entity/old", "entity/kept", "entity/new"} {
		_, err := svc.CreatePage(ctx, &types.WikiPage{
			TenantID: source.TenantID, KnowledgeBaseID: source.KnowledgeBaseID,
			Slug: slug, Title: slug, PageType: types.WikiPageTypeEntity, Content: "Target body",
		})
		require.NoError(t, err)
	}
	source.Content = "Original body [[entity/old]] [[entity/kept]]"
	var err error
	source, err = svc.UpdatePage(ctx, source)
	require.NoError(t, err)
	repo := svc.repo
	require.NoError(t, repo.RemoveInLink(ctx, source.TenantID, source.KnowledgeBaseID, "entity/kept", source.Slug))
	for _, slug := range []string{"entity/old", "entity/kept", "entity/new"} {
		require.NoError(t, repo.AddInLink(ctx, source.TenantID, source.KnowledgeBaseID, slug, "another-source"))
	}
	recorder := &wikiSnapshotLinkRecorder{
		WikiPageRepository: repo, added: map[string]int{}, removed: map[string]int{},
	}
	svc.repo = recorder
	guarded := types.WithWikiPageWriteSnapshot(ctx, source)
	source.Content = "Updated body [[entity/kept]] [[entity/new]]"
	_, err = svc.UpdatePage(guarded, source)
	require.NoError(t, err)
	require.Equal(t, map[string]int{"entity/old": 1}, recorder.removed)
	require.Equal(t, map[string]int{"entity/kept": 1, "entity/new": 1}, recorder.added)
	require.Zero(t, recorder.guardedCalls, "目标页原子反链不能继承源页面快照")
	for _, slug := range []string{"entity/old", "entity/kept", "entity/new"} {
		target, err := repo.GetBySlug(ctx, source.KnowledgeBaseID, slug)
		require.NoError(t, err)
		want := []string{"another-source"}
		if slug != "entity/old" {
			want = append(want, source.Slug)
		}
		require.ElementsMatch(t, want, []string(target.InLinks), slug)
		require.Equal(t, "Target body", target.Content)
		require.Equal(t, 1, target.Version, "原子反链不制造内容版本")
	}
}
