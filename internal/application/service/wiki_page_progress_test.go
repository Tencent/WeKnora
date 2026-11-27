package service

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// pageProgressTestRepo 隔离页面收尾的读写；只实现这些测试会调用的仓储能力。
type pageProgressTestRepo struct {
	interfaces.WikiPageRepository
	pages        map[string]*types.WikiPage
	getErrors    map[string]error
	writeErrors  map[string]error
	reads        []string
	writes       []string
	writeCommits []*types.WikiPageProgressCommit
	pruneCommits []*types.WikiPageProgressCommit
}

func cloneProgressTestPage(page *types.WikiPage) *types.WikiPage {
	if page == nil {
		return nil
	}
	cloned := *page
	cloned.InLinks = append(types.StringArray(nil), page.InLinks...)
	cloned.OutLinks = append(types.StringArray(nil), page.OutLinks...)
	cloned.SourceRefs = append(types.StringArray(nil), page.SourceRefs...)
	cloned.ChunkRefs = append(types.StringArray(nil), page.ChunkRefs...)
	cloned.Aliases = append(types.StringArray(nil), page.Aliases...)
	cloned.CategoryPath = append(types.StringArray(nil), page.CategoryPath...)
	cloned.PageMetadata = append(types.JSON(nil), page.PageMetadata...)
	return &cloned
}

func progressTestPageKey(kbID, slug string) string { return kbID + "/" + slug }

func (r *pageProgressTestRepo) GetBySlug(ctx context.Context, kbID, slug string) (*types.WikiPage, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	key := progressTestPageKey(kbID, slug)
	r.reads = append(r.reads, key)
	if err := r.getErrors[key]; err != nil {
		return nil, err
	}
	page := r.pages[key]
	if page == nil {
		return nil, repository.ErrWikiPageNotFound
	}
	cloned := cloneProgressTestPage(page)
	snapshot, err := types.WikiPageProgressSnapshot(cloned)
	if err != nil {
		return nil, err
	}
	cloned.ProgressSnapshot = snapshot
	return cloned, nil
}

func (r *pageProgressTestRepo) write(ctx context.Context, page *types.WikiPage) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	key := progressTestPageKey(page.KnowledgeBaseID, page.Slug)
	if err := r.writeErrors[key]; err != nil {
		return err
	}
	snapshot, err := types.WikiPageProgressSnapshot(page)
	if err != nil {
		return err
	}
	page.ProgressSnapshot = snapshot
	r.writes = append(r.writes, key)
	r.writeCommits = append(r.writeCommits, types.WikiPageProgressCommitFromContext(ctx))
	r.pages[key] = cloneProgressTestPage(page)
	return nil
}

func (r *pageProgressTestRepo) Create(ctx context.Context, page *types.WikiPage) error {
	return r.write(ctx, page)
}

func (r *pageProgressTestRepo) UpdateWithRevision(
	ctx context.Context, page *types.WikiPage, _ *types.WikiPageRevision,
) error {
	page.Version++
	return r.write(ctx, page)
}

func (r *pageProgressTestRepo) UpdateMeta(ctx context.Context, page *types.WikiPage) error {
	return r.write(ctx, page)
}

// 收尾夹具从当前页重建单来源反链，不通过整页元数据写入模拟原子更新。
func (r *pageProgressTestRepo) ReconcileWikiPageProgressInLink(
	ctx context.Context, tenantID uint64, kbID, sourceID, sourceSlug, targetID, targetSlug string,
) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	source := r.pages[progressTestPageKey(kbID, sourceSlug)]
	target := r.pages[progressTestPageKey(kbID, targetSlug)]
	if source == nil || target == nil || source.ID != sourceID || target.ID != targetID ||
		source.TenantID != tenantID || target.TenantID != tenantID ||
		source.KnowledgeBaseID != kbID || target.KnowledgeBaseID != kbID {
		return repository.ErrWikiPageNotFound
	}
	want := containsString(source.OutLinks, targetSlug)
	if want == containsString(target.InLinks, sourceSlug) {
		return nil
	}
	if err := r.writeErrors[progressTestPageKey(kbID, targetSlug)]; err != nil {
		return err
	}
	if want {
		target.InLinks = append(target.InLinks, sourceSlug)
	} else {
		target.InLinks = removeString(target.InLinks, sourceSlug)
	}
	r.writes = append(r.writes, progressTestPageKey(kbID, targetSlug))
	r.writeCommits = append(r.writeCommits, types.WikiPageProgressCommitFromContext(ctx))
	return nil
}

func (r *pageProgressTestRepo) PruneRevisions(ctx context.Context, _ types.WikiRevisionPruneRequest) error {
	r.pruneCommits = append(r.pruneCommits, types.WikiPageProgressCommitFromContext(ctx))
	return nil
}

func newPageProgressTestRepo() *pageProgressTestRepo {
	return &pageProgressTestRepo{
		pages:       map[string]*types.WikiPage{},
		getErrors:   map[string]error{},
		writeErrors: map[string]error{},
	}
}

func progressTestPage(slug string, links ...string) *types.WikiPage {
	return &types.WikiPage{
		ID: "page-" + slug, TenantID: 7, KnowledgeBaseID: "kb-progress",
		Slug: slug, Title: slug, Content: "正文 " + slug,
		PageType: types.WikiPageTypeEntity, Status: types.WikiPageStatusPublished,
		Version: 2, OutLinks: append(types.StringArray(nil), links...),
	}
}

func (r *pageProgressTestRepo) put(page *types.WikiPage) {
	snapshot, err := types.WikiPageProgressSnapshot(page)
	if err != nil {
		panic(err)
	}
	page.ProgressSnapshot = snapshot
	r.pages[progressTestPageKey(page.KnowledgeBaseID, page.Slug)] = cloneProgressTestPage(page)
}

func (r *pageProgressTestRepo) page(slug string) *types.WikiPage {
	return r.pages[progressTestPageKey("kb-progress", slug)]
}

func progressTestContext() context.Context {
	return types.WithWikiPageProgressCommit(context.Background(), &types.WikiPageProgressCommit{
		Slug: "source", Claims: []types.WikiProgressClaim{{
			PendingOpID: 11, Epoch: 1, TenantID: 7,
			KnowledgeBaseID: "kb-progress", KnowledgeID: "knowledge-progress",
		}},
	})
}

func progressTestReceipt() types.WikiIngestPageProgress {
	return types.WikiIngestPageProgress{
		PendingOpID: 11, Slug: "source", Applied: true, Changed: true,
		PageID: "page-source", PageVersion: 2,
		OldOutLinks: types.StringArray{"old"}, NewOutLinks: types.StringArray{"new"},
	}
}

func TestWikiPageProgressDefersEffectsUntilExplicitFinish(t *testing.T) {
	for _, operation := range []string{"create", "update", "metadata"} {
		t.Run(operation, func(t *testing.T) {
			repo := newPageProgressTestRepo()
			repo.put(progressTestPage("old"))
			repo.put(progressTestPage("new"))
			old := progressTestPage("source", "old")
			old.Content = "旧正文 [[old]]"
			old.Version = types.WikiMaxRevisionsHardCap + 1
			if operation != "create" {
				repo.put(old)
			}
			candidate := cloneProgressTestPage(old)
			if operation != "metadata" {
				candidate.Content = "新正文 [[new]]"
			}
			service := &wikiPageService{repo: repo}
			var err error
			if operation == "create" {
				_, err = service.CreatePage(progressTestContext(), candidate)
			} else {
				_, err = service.UpdatePage(progressTestContext(), candidate)
			}
			require.NoError(t, err)
			require.Equal(t, []string{"kb-progress/source"}, repo.writes)
			require.NotNil(t, repo.writeCommits[0], "主页面写入必须携带原子完成记录")
			if operation == "update" {
				require.Len(t, repo.pruneCommits, 1)
			}
			for _, commit := range repo.pruneCommits {
				require.Nil(t, commit, "版本修剪不能继承主页面提交授权")
			}
			require.NotContains(t, repo.reads, "kb-progress/old")
			require.NotContains(t, repo.reads, "kb-progress/new")
		})
	}
}

func TestWikiPageProgressDoesNotChangeOrdinaryLinkMaintenance(t *testing.T) {
	// 使用真实仓储验证普通写入仍调用主线的原子反链接口。
	forWikiIngestProgressDBs(t, func(t *testing.T, db *gorm.DB) {
		ctx := context.Background()
		repo := repository.NewWikiPageRepository(db)
		require.NoError(t, repo.Create(ctx, progressTestPage("new")))
		service := &wikiPageService{repo: repo}
		page := progressTestPage("source")
		page.Content = "正文 [[new]]"
		_, err := service.CreatePage(ctx, page)
		require.NoError(t, err)
		target, err := repo.GetBySlug(ctx, wikiIngestProgressTestKB, "new")
		require.NoError(t, err)
		require.Equal(t, types.StringArray{"source"}, target.InLinks)
	})
}

// 模拟正文已提交后进程退出：新 service 只补反链，不再生成或写回正文。
func TestWikiPageProgressResumesEffectsAfterServiceRestart(t *testing.T) {
	repo := newPageProgressTestRepo()
	source := progressTestPage("source", "new")
	source.Content = "已提交的新正文"
	repo.put(source)
	old := progressTestPage("old")
	old.InLinks = types.StringArray{"other", "source"}
	repo.put(old)
	fresh := progressTestPage("new")
	fresh.InLinks = types.StringArray{"another"}
	repo.put(fresh)
	service := &wikiPageService{repo: repo}
	receipt := progressTestReceipt()
	require.NoError(t, service.ReconcileAppliedPage(progressTestContext(), 7, "kb-progress", receipt))
	require.Equal(t, types.StringArray{"other"}, repo.page("old").InLinks)
	require.Equal(t, types.StringArray{"another", "source"}, repo.page("new").InLinks)
	require.Equal(t, source.Content, repo.page("source").Content)
	require.Equal(t, source.Version, repo.page("source").Version)
	require.NotContains(t, repo.writes, "kb-progress/source")
	for _, commit := range repo.writeCommits {
		require.Nil(t, commit, "目标页元数据写入不能继承主页面完成记录")
	}
	service = &wikiPageService{repo: repo}
	require.NoError(t, service.ReconcileAppliedPage(context.Background(), 7, "kb-progress", receipt))
	require.Equal(t, types.StringArray{"other"}, repo.page("old").InLinks)
	require.Equal(t, types.StringArray{"another", "source"}, repo.page("new").InLinks)
}

func TestWikiPageProgressRetriesInterruptedEffects(t *testing.T) {
	repo := newPageProgressTestRepo()
	repo.put(progressTestPage("source", "new"))
	old := progressTestPage("old")
	old.InLinks = types.StringArray{"source"}
	repo.put(old)
	repo.put(progressTestPage("new"))
	want := errors.New("write unavailable")
	repo.writeErrors["kb-progress/new"] = want
	service := &wikiPageService{repo: repo}
	require.ErrorIs(t,
		service.ReconcileAppliedPage(context.Background(), 7, "kb-progress", progressTestReceipt()), want)
	require.Empty(t, repo.page("old").InLinks, "允许已经完成的部分在重启后幂等重放")
	delete(repo.writeErrors, "kb-progress/new")
	service = &wikiPageService{repo: repo}
	require.NoError(t, service.ReconcileAppliedPage(context.Background(), 7, "kb-progress", progressTestReceipt()))
	require.Equal(t, types.StringArray{"source"}, repo.page("new").InLinks)
}

func TestWikiPageProgressPropagatesRealReadFailures(t *testing.T) {
	for _, slug := range []string{"source", "old", "new"} {
		t.Run(slug, func(t *testing.T) {
			repo := newPageProgressTestRepo()
			repo.put(progressTestPage("source", "new"))
			repo.put(progressTestPage("old"))
			repo.put(progressTestPage("new"))
			want := errors.New("database unavailable")
			repo.getErrors["kb-progress/"+slug] = fmt.Errorf("read: %w", want)
			service := &wikiPageService{repo: repo}
			require.ErrorIs(t,
				service.ReconcileAppliedPage(context.Background(), 7, "kb-progress", progressTestReceipt()), want)
		})
	}
}

func TestWikiPageProgressSkipsMissingTargets(t *testing.T) {
	repo := newPageProgressTestRepo()
	repo.put(progressTestPage("source", "new"))
	service := &wikiPageService{repo: repo}
	require.NoError(t, service.ReconcileAppliedPage(context.Background(), 7, "kb-progress", progressTestReceipt()))
	require.Empty(t, repo.writes)
}

func TestWikiPageProgressDoesNotReviveDeletedOrReplacedSource(t *testing.T) {
	for _, replacement := range []string{"deleted", "replaced", "other-tenant", "other-kb"} {
		t.Run(replacement, func(t *testing.T) {
			repo := newPageProgressTestRepo()
			if replacement != "deleted" {
				source := progressTestPage("source", "new")
				switch replacement {
				case "replaced":
					source.ID = "new-page-id"
				case "other-tenant":
					source.TenantID++
				case "other-kb":
					source.KnowledgeBaseID = "another-kb"
				}
				// 模拟仓储返回意外归属，收尾仍必须自行核对。
				repo.pages["kb-progress/source"] = source
			}
			repo.put(progressTestPage("old"))
			repo.put(progressTestPage("new"))
			service := &wikiPageService{repo: repo}
			require.NoError(t,
				service.ReconcileAppliedPage(context.Background(), 7, "kb-progress", progressTestReceipt()))
			require.Empty(t, repo.writes)
		})
	}
}

func TestWikiPageProgressPreservesNewerContentAndLinks(t *testing.T) {
	repo := newPageProgressTestRepo()
	source := progressTestPage("source", "old", "later")
	source.Content, source.Version = "用户后来编辑的正文", 9
	repo.put(source)
	old := progressTestPage("old")
	repo.put(old)
	removed := progressTestPage("new")
	removed.InLinks = types.StringArray{"source", "other"}
	repo.put(removed)
	later := progressTestPage("later")
	later.InLinks = types.StringArray{"source"}
	repo.put(later)
	service := &wikiPageService{repo: repo}
	require.NoError(t, service.ReconcileAppliedPage(context.Background(), 7, "kb-progress", progressTestReceipt()))
	require.Equal(t, types.StringArray{"source"}, repo.page("old").InLinks)
	require.Equal(t, types.StringArray{"other"}, repo.page("new").InLinks)
	require.NotContains(t, repo.reads, "kb-progress/later", "本次回执之外的新目标由后来更新负责")
	require.Equal(t, source.Content, repo.page("source").Content)
	require.Equal(t, 9, repo.page("source").Version)
}

// ordinaryProgressInLinkFailureRepo 只注入普通反链写入失败，主页面继续使用真实仓储。
type ordinaryProgressInLinkFailureRepo struct {
	interfaces.WikiPageRepository
	calls int
}

func (r *ordinaryProgressInLinkFailureRepo) AddInLink(
	_ context.Context, _ uint64, _, _, _ string,
) error {
	r.calls++
	return errors.New("synthetic target write failure")
}

func TestWikiPageProgressPreservesOrdinaryBestEffortFailure(t *testing.T) {
	forWikiIngestProgressDBs(t, func(t *testing.T, db *gorm.DB) {
		ctx := context.Background()
		repo := repository.NewWikiPageRepository(db)
		require.NoError(t, repo.Create(ctx, progressTestPage("new")))
		failing := &ordinaryProgressInLinkFailureRepo{WikiPageRepository: repo}
		service := &wikiPageService{repo: failing}
		page := progressTestPage("source")
		page.Content = "正文 [[new]]"
		_, err := service.CreatePage(ctx, page)
		require.NoError(t, err, "普通调用保留原有的 best-effort 行为")
		require.Equal(t, 1, failing.calls, "必须实际经过失败的原子反链接口")
		saved, err := repo.GetBySlug(ctx, wikiIngestProgressTestKB, "source")
		require.NoError(t, err)
		require.Equal(t, page.Content, saved.Content, "反链失败不能撤销已经保存的主页面")
		require.Equal(t, types.StringArray{"new"}, saved.OutLinks)
		target, err := repo.GetBySlug(ctx, wikiIngestProgressTestKB, "new")
		require.NoError(t, err)
		require.Empty(t, target.InLinks, "失败注入不能写入目标反链")
	})
}

func TestWikiPageProgressRejectsForeignTarget(t *testing.T) {
	repo := newPageProgressTestRepo()
	repo.put(progressTestPage("source", "new"))
	foreign := progressTestPage("new")
	foreign.TenantID = 8
	repo.put(foreign)
	service := &wikiPageService{repo: repo}
	require.Error(t, service.ReconcileAppliedPage(context.Background(), 7, "kb-progress", progressTestReceipt()))
	require.Empty(t, repo.writes)
}

func TestWikiPageProgressRequiresAppliedReceipt(t *testing.T) {
	repo := newPageProgressTestRepo()
	service := &wikiPageService{repo: repo}
	receipt := progressTestReceipt()
	receipt.Applied = false
	require.ErrorIs(t,
		service.ReconcileAppliedPage(context.Background(), 7, "kb-progress", receipt),
		types.ErrWikiProgressIncomplete)
	require.Empty(t, repo.reads)
}

func TestWikiPageProgressSkipsNoopAndCompletedEffects(t *testing.T) {
	for _, state := range []string{"noop", "completed"} {
		t.Run(state, func(t *testing.T) {
			repo := newPageProgressTestRepo()
			service := &wikiPageService{repo: repo}
			receipt := progressTestReceipt()
			if state == "noop" {
				receipt.Changed = false
			} else {
				receipt.EffectsDone = true
			}
			require.NoError(t, service.ReconcileAppliedPage(context.Background(), 7, "kb-progress", receipt))
			require.Empty(t, repo.reads)
		})
	}
}

func TestWikiPageProgressHonorsCancellation(t *testing.T) {
	repo := newPageProgressTestRepo()
	repo.put(progressTestPage("source", "new"))
	service := &wikiPageService{repo: repo}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	require.ErrorIs(t,
		service.ReconcileAppliedPage(ctx, 7, "kb-progress", progressTestReceipt()), context.Canceled)
	require.Empty(t, repo.writes)
}

func TestWikiPageProgressPropagatesEveryEffectWriteFailure(t *testing.T) {
	for _, phase := range []string{"remove-old", "remove-new", "add-new"} {
		t.Run(phase, func(t *testing.T) {
			repo := newPageProgressTestRepo()
			repo.put(progressTestPage("source", "new"))
			target := progressTestPage("new")
			if phase == "remove-new" {
				// 新版本已移除本次回执中的新链接，收尾必须执行真实删除并传播失败。
				repo.put(progressTestPage("source"))
			}
			if phase == "remove-old" {
				target = progressTestPage("old")
			}
			if phase != "add-new" {
				target.InLinks = types.StringArray{"source"}
			}
			repo.put(target)
			want := errors.New("effect write failed")
			repo.writeErrors[progressTestPageKey(target.KnowledgeBaseID, target.Slug)] = want
			service := &wikiPageService{repo: repo}
			require.ErrorIs(t,
				service.ReconcileAppliedPage(context.Background(), 7, "kb-progress", progressTestReceipt()), want)
		})
	}
}

func TestWikiPageProgressRejectsIncompletePageIdentity(t *testing.T) {
	for _, missing := range []string{"page-id", "slug"} {
		t.Run(missing, func(t *testing.T) {
			repo := newPageProgressTestRepo()
			service := &wikiPageService{repo: repo}
			receipt := progressTestReceipt()
			if missing == "page-id" {
				receipt.PageID = ""
			} else {
				receipt.Slug = ""
			}
			require.Error(t, service.ReconcileAppliedPage(context.Background(), 7, "kb-progress", receipt))
			require.Empty(t, repo.reads)
		})
	}
}

// progressEffectInterleavingRepo 在目标读取后插入已经提交的修改，稳定复现恢复中的陈旧快照窗口。
// 内嵌仓储仍执行真实 SQLite/PG 操作，窄反链方法也由真实仓储实现。
type progressEffectInterleavingRepo struct {
	interfaces.WikiPageRepository
	targetSlug string
	afterRead  func(context.Context)
	metaWrites int
}

func (r *progressEffectInterleavingRepo) GetBySlug(
	ctx context.Context, kbID, slug string,
) (*types.WikiPage, error) {
	page, err := r.WikiPageRepository.GetBySlug(ctx, kbID, slug)
	if err == nil && slug == r.targetSlug && r.afterRead != nil {
		hook := r.afterRead
		r.afterRead = nil
		hook(ctx)
	}
	return page, err
}

func (r *progressEffectInterleavingRepo) UpdateMeta(ctx context.Context, page *types.WikiPage) error {
	r.metaWrites++
	return r.WikiPageRepository.UpdateMeta(ctx, page)
}

func progressEffectReceipt(source, target *types.WikiPage) types.WikiIngestPageProgress {
	return types.WikiIngestPageProgress{
		PendingOpID: 1, Applied: true, Changed: true,
		Slug: source.Slug, PageID: source.ID, PageVersion: source.Version,
		OldOutLinks: types.StringArray{target.Slug}, NewOutLinks: types.StringArray{target.Slug},
	}
}

func TestWikiPageProgressReconcilePreservesInterleavedTargetMetadata(t *testing.T) {
	forWikiIngestProgressDBs(t, func(t *testing.T, db *gorm.DB) {
		ctx := context.Background()
		repo := repository.NewWikiPageRepository(db)
		source := progressTestPage("source", "target")
		target := progressTestPage("target")
		target.SourceRefs = types.StringArray{"original-document"}
		require.NoError(t, repo.Create(ctx, source))
		require.NoError(t, repo.Create(ctx, target))
		interleaved := false
		decorated := &progressEffectInterleavingRepo{
			WikiPageRepository: repo,
			targetSlug:         target.Slug,
			afterRead: func(ctx context.Context) {
				newer, err := repo.GetBySlug(ctx, wikiIngestProgressTestKB, target.Slug)
				require.NoError(t, err)
				newer.SourceRefs = append(newer.SourceRefs, "concurrent-document")
				newer.OutLinks = types.StringArray{"later-target"}
				newer.PageMetadata = types.JSON(`{"concurrent":true}`)
				require.NoError(t, repo.UpdateMeta(ctx, newer))
				// 反链由独立的原子接口维护，不能通过整页元数据写入模拟并发。
				require.NoError(t, repo.AddInLink(
					ctx, newer.TenantID, newer.KnowledgeBaseID, newer.Slug, "another-source",
				))
				interleaved = true
			},
		}
		service := &wikiPageService{repo: decorated}
		require.NoError(t, service.ReconcileAppliedPage(ctx, 7, wikiIngestProgressTestKB,
			progressEffectReceipt(source, target)))
		require.True(t, interleaved, "实际提交另一写入后才执行恢复")
		require.Zero(t, decorated.metaWrites, "恢复不能回退为整页元数据覆盖")
		after, err := repo.GetBySlug(ctx, wikiIngestProgressTestKB, target.Slug)
		require.NoError(t, err)
		require.ElementsMatch(t, []string{"original-document", "concurrent-document"}, after.SourceRefs)
		require.Equal(t, types.StringArray{"later-target"}, after.OutLinks)
		require.ElementsMatch(t, []string{"another-source", source.Slug}, after.InLinks)
		require.JSONEq(t, `{"concurrent":true}`, string(after.PageMetadata))
		require.Equal(t, target.Version, after.Version)
		require.Equal(t, target.Content, after.Content)
	})
}

func TestWikiPageProgressReconcileUsesSourceLinksChangedAfterRead(t *testing.T) {
	for _, operation := range []string{"added", "removed"} {
		t.Run(operation, func(t *testing.T) {
			forWikiIngestProgressDBs(t, func(t *testing.T, db *gorm.DB) {
				ctx := context.Background()
				repo := repository.NewWikiPageRepository(db)
				source := progressTestPage("source")
				target := progressTestPage("target")
				target.InLinks = types.StringArray{"another-source"}
				if operation == "removed" {
					source.OutLinks = types.StringArray{target.Slug}
					target.InLinks = append(target.InLinks, source.Slug)
				}
				require.NoError(t, repo.Create(ctx, source))
				require.NoError(t, repo.Create(ctx, target))
				interleaved := false
				decorated := &progressEffectInterleavingRepo{
					WikiPageRepository: repo,
					targetSlug:         target.Slug,
					afterRead: func(ctx context.Context) {
						newer, err := repo.GetBySlug(ctx, wikiIngestProgressTestKB, source.Slug)
						require.NoError(t, err)
						newer.OutLinks = nil
						if operation == "added" {
							newer.OutLinks = types.StringArray{target.Slug}
						}
						require.NoError(t, repo.UpdateMeta(ctx, newer))
						interleaved = true
					},
				}
				service := &wikiPageService{repo: decorated}
				require.NoError(t, service.ReconcileAppliedPage(ctx, 7, wikiIngestProgressTestKB,
					progressEffectReceipt(source, target)))
				require.True(t, interleaved)
				require.Zero(t, decorated.metaWrites)
				after, err := repo.GetBySlug(ctx, wikiIngestProgressTestKB, target.Slug)
				require.NoError(t, err)
				require.Contains(t, after.InLinks, "another-source")
				require.Equal(t, operation == "added", containsString(after.InLinks, source.Slug),
					"目标反链须依据事务内当前源出链，不能使用恢复前的源快照")
			})
		})
	}
}

func TestWikiPageProgressReconcileDoesNotTouchReplacementAfterRead(t *testing.T) {
	for _, changed := range []string{"source-deleted", "source-replaced", "target-deleted", "target-replaced"} {
		t.Run(changed, func(t *testing.T) {
			forWikiIngestProgressDBs(t, func(t *testing.T, db *gorm.DB) {
				ctx := context.Background()
				repo := repository.NewWikiPageRepository(db)
				source := progressTestPage("source", "target")
				target := progressTestPage("target")
				target.InLinks = types.StringArray{"another-source"}
				require.NoError(t, repo.Create(ctx, source))
				require.NoError(t, repo.Create(ctx, target))
				interleaved := false
				decorated := &progressEffectInterleavingRepo{
					WikiPageRepository: repo,
					targetSlug:         target.Slug,
					afterRead: func(ctx context.Context) {
						old := source
						if changed == "target-deleted" || changed == "target-replaced" {
							old = target
						}
						require.NoError(t, db.Unscoped().Where("id = ?", old.ID).Delete(&types.WikiPage{}).Error)
						if changed == "source-replaced" || changed == "target-replaced" {
							replacement := cloneProgressTestPage(old)
							replacement.ID = "replacement-" + old.ID
							replacement.Content = "后来新建的正文"
							require.NoError(t, repo.Create(ctx, replacement))
						}
						interleaved = true
					},
				}
				service := &wikiPageService{repo: decorated}
				require.NoError(t, service.ReconcileAppliedPage(ctx, 7, wikiIngestProgressTestKB,
					progressEffectReceipt(source, target)))
				require.True(t, interleaved)
				require.Zero(t, decorated.metaWrites)
				after, err := repo.GetBySlug(ctx, wikiIngestProgressTestKB, target.Slug)
				if changed == "target-deleted" {
					require.ErrorIs(t, err, repository.ErrWikiPageNotFound)
				} else {
					require.NoError(t, err)
					require.Equal(t, types.StringArray{"another-source"}, after.InLinks,
						"旧身份失效后，不能修改仍存在的目标或同 slug 替代页")
					if changed == "target-replaced" {
						require.Equal(t, "replacement-"+target.ID, after.ID)
						require.Equal(t, "后来新建的正文", after.Content)
					}
				}
			})
		})
	}
}
