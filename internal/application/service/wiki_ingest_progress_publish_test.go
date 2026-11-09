package service

import (
	"context"
	"encoding/json"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	apprepo "github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/hibiken/asynq"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// progressPublishReadHook 在发布读完页面、真正写入前插入另一份已提交结果。
// 这里只控制交错顺序，页面写入和回执仍使用真实仓储事务。
type progressPublishReadHook struct {
	interfaces.WikiPageService
	slug      string
	afterRead func(context.Context, *types.WikiPage)
}

func (p *progressPublishReadHook) GetPageBySlug(
	ctx context.Context, kbID, slug string,
) (*types.WikiPage, error) {
	page, err := p.WikiPageService.GetPageBySlug(ctx, kbID, slug)
	if err == nil && page != nil && slug == p.slug && p.afterRead != nil {
		hook := p.afterRead
		p.afterRead = nil
		hook(ctx, page)
	}
	return page, err
}

type progressPublishFixture struct {
	service *wikiIngestService
	batch   *wikiProgressBatch
	ids     []int64
	page    *types.WikiPage
}

// newProgressPublishFixture 留下正文、反链都已完成但尚未发布/结算的真实任务。
func newProgressPublishFixture(t *testing.T, db *gorm.DB, docID, slug string) progressPublishFixture {
	t.Helper()
	service := newWikiIngestProgressService(t, db)
	enqueueWikiIngestProgressDoc(t, service, db, docID)
	ops, ids, batch := claimWikiIngestProgress(t, service, 1)
	update := progressBoundaryEntityUpdate(docID, slug)
	pending := prepareWikiIngestProgress(t, service, batch, ops, update)
	page := wikiIngestProgressPage(slug)
	page.PageType = types.WikiPageTypeEntity
	page.SourceRefs = types.StringArray{docID}
	page.ChunkRefs = types.StringArray{docID + "-chunk"}
	_, _, _, _, err := batch.reduce(context.Background(), service, slug, pending[slug],
		func(ctx context.Context, _ []SlugUpdate) (bool, string, bool, bool, error) {
			_, createErr := service.wikiService.CreatePage(ctx, page)
			return true, "ingest", false, false, createErr
		})
	require.NoError(t, err)
	return progressPublishFixture{service: service, batch: batch, ids: ids, page: page}
}

func runProgressPublishTask(ctx context.Context, service *wikiIngestService) error {
	raw, err := json.Marshal(WikiIngestPayload{TenantID: 7, KnowledgeBaseID: wikiIngestProgressTestKB})
	if err != nil {
		return err
	}
	return service.ProcessWikiIngest(ctx, asynq.NewTask(types.TypeWikiIngest, raw))
}

func assertProgressPublishSkippedReduce(t *testing.T, service *wikiIngestService) {
	t.Helper()
	writes := service.wikiService.(*wikiIngestProgressPages)
	require.Empty(t, writes.createCalls, "恢复不能重新创建已完成页")
	require.Empty(t, writes.updateCalls, "恢复不能重新提交已完成贡献")
	require.Zero(t, service.chunkRepo.(*wikiIngestProgressChunks).calls.Load(), "恢复不能重新执行 Map")
}

func TestWikiProgressPublishPreservesAnotherBatchContributionOnResume(t *testing.T) {
	for _, change := range []string{"metadata", "content"} {
		t.Run(change, func(t *testing.T) {
			forWikiIngestProgressDBs(t, func(t *testing.T, db *gorm.DB) {
				ctx := context.Background()
				first := newProgressPublishFixture(t, db, "doc-publish-first", "entity/publish-shared")
				second := newWikiIngestProgressService(t, db)
				enqueueWikiIngestProgressDoc(t, second, db, "doc-publish-second")
				ops, _, batch := claimWikiIngestProgress(t, second, 1)
				update := progressBoundaryEntityUpdate("doc-publish-second", first.page.Slug)
				pending := prepareWikiIngestProgress(t, second, batch, ops, update)
				interleaved := false
				var committed *types.WikiPage
				first.service.wikiService = &progressPublishReadHook{
					WikiPageService: first.service.wikiService,
					slug:            first.page.Slug,
					afterRead: func(readCtx context.Context, _ *types.WikiPage) {
						candidate, err := second.wikiService.GetPageBySlug(
							readCtx, wikiIngestProgressTestKB, first.page.Slug)
						require.NoError(t, err)
						candidate.SourceRefs = append(candidate.SourceRefs, update.KnowledgeID)
						candidate.ChunkRefs = append(candidate.ChunkRefs, update.KnowledgeID+"-chunk")
						if change == "content" {
							candidate.Content += "\n第二份文档已经提交的正文"
						}
						_, _, _, _, err = batch.reduce(readCtx, second, first.page.Slug, pending[first.page.Slug],
							func(writeCtx context.Context, _ []SlugUpdate) (bool, string, bool, bool, error) {
								var writeErr error
								committed, writeErr = second.wikiService.UpdatePage(writeCtx, candidate)
								return true, "ingest", false, false, writeErr
							})
						require.NoError(t, err)
						state := loadWikiIngestProgress(t, second, batch.claims[ops[0].dbID])
						require.True(t, state.Pages[0].Applied)
						require.True(t, state.Pages[0].EffectsDone)
						interleaved = true
					},
				}
				require.NoError(t, first.service.publishDraftPages(ctx, wikiIngestProgressTestKB,
					[]string{first.page.Slug}, map[string]string{first.page.Slug: first.page.ID}))
				require.True(t, interleaved)
				stored, err := second.wikiService.GetPageBySlug(ctx, wikiIngestProgressTestKB, first.page.Slug)
				require.NoError(t, err)
				require.ElementsMatch(t, []string{"doc-publish-first", "doc-publish-second"}, stored.SourceRefs)
				require.ElementsMatch(t,
					[]string{"doc-publish-first-chunk", "doc-publish-second-chunk"}, stored.ChunkRefs)
				require.Equal(t, committed.Content, stored.Content)
				require.Equal(t, committed.Version, stored.Version)
				require.Equal(t, types.WikiPageStatusPublished, stored.Status)

				// 两个独立批次都留下成功回执；重新创建 service 后不能靠重做 B 来修补丢失贡献。
				require.NoError(t, first.batch.release(ctx))
				require.NoError(t, batch.release(ctx))
				restarted := newWikiIngestProgressService(t, db)
				require.NoError(t, runProgressPublishTask(ctx, restarted))
				assertProgressPublishSkippedReduce(t, restarted)
				count, err := restarted.pendingRepo.PendingCount(
					ctx, wikiTaskType, wikiTaskScope, wikiIngestProgressTestKB)
				require.NoError(t, err)
				require.Zero(t, count)
				after, err := restarted.wikiService.GetPageBySlug(ctx, wikiIngestProgressTestKB, first.page.Slug)
				require.NoError(t, err)
				require.Equal(t, stored.SourceRefs, after.SourceRefs)
				require.Equal(t, stored.ChunkRefs, after.ChunkRefs)
				require.Equal(t, stored.Content, after.Content)
				require.Equal(t, stored.Version, after.Version)
			})
		})
	}
}

func TestWikiProgressPublishPreservesInterleavedInboundLink(t *testing.T) {
	forWikiIngestProgressDBs(t, func(t *testing.T, db *gorm.DB) {
		ctx := context.Background()
		fixture := newProgressPublishFixture(t, db, "doc-publish", "entity/publish-target")
		repo := apprepo.NewWikiPageRepository(db)
		source := progressTestPage("another-source", fixture.page.Slug)
		require.NoError(t, repo.Create(ctx, source))
		interleaved := false
		fixture.service.wikiService = &progressPublishReadHook{
			WikiPageService: fixture.service.wikiService,
			slug:            fixture.page.Slug,
			afterRead: func(readCtx context.Context, _ *types.WikiPage) {
				finisher := &wikiPageService{repo: repo}
				require.NoError(t, finisher.ReconcileAppliedPage(readCtx, 7, wikiIngestProgressTestKB,
					progressEffectReceipt(source, fixture.page)))
				interleaved = true
			},
		}
		require.NoError(t, fixture.service.publishDraftPages(ctx, wikiIngestProgressTestKB,
			[]string{fixture.page.Slug}, map[string]string{fixture.page.Slug: fixture.page.ID}))
		require.True(t, interleaved)
		after, err := repo.GetBySlug(ctx, wikiIngestProgressTestKB, fixture.page.Slug)
		require.NoError(t, err)
		require.Contains(t, after.InLinks, source.Slug)
		require.Equal(t, fixture.page.SourceRefs, after.SourceRefs)
		require.Equal(t, types.WikiPageStatusPublished, after.Status)
	})
}

// progressPublishUpdate 只识别发布语句，避免将夹具准备或计数结算算成发布写入。
func progressPublishUpdate(tx *gorm.DB) bool {
	if tx.Statement.Table != (types.WikiPage{}).TableName() {
		return false
	}
	fields, ok := tx.Statement.Dest.(map[string]interface{})
	return ok && fields["status"] == types.WikiPageStatusPublished
}

func TestWikiProgressRepeatedPublishDoesNotWriteAgain(t *testing.T) {
	forWikiIngestProgressDBs(t, func(t *testing.T, db *gorm.DB) {
		ctx := context.Background()
		fixture := newProgressPublishFixture(t, db, "doc-publish", "entity/publish-once")
		var written atomic.Int64
		const callback = "test:progress-publish-count"
		require.NoError(t, db.Callback().Update().After("gorm:update").Register(callback, func(tx *gorm.DB) {
			if tx.Error == nil && progressPublishUpdate(tx) {
				written.Add(tx.RowsAffected)
			}
		}))
		t.Cleanup(func() { require.NoError(t, db.Callback().Update().Remove(callback)) })
		require.NoError(t, fixture.service.publishDraftPages(ctx, wikiIngestProgressTestKB,
			[]string{fixture.page.Slug}, map[string]string{fixture.page.Slug: fixture.page.ID}))
		published, err := fixture.service.wikiService.GetPageBySlug(ctx, wikiIngestProgressTestKB, fixture.page.Slug)
		require.NoError(t, err)
		for range 2 {
			require.NoError(t, fixture.service.publishDraftPages(ctx, wikiIngestProgressTestKB,
				[]string{fixture.page.Slug}, map[string]string{fixture.page.Slug: fixture.page.ID}))
		}
		after, err := fixture.service.wikiService.GetPageBySlug(ctx, wikiIngestProgressTestKB, fixture.page.Slug)
		require.NoError(t, err)
		require.EqualValues(t, 1, written.Load(), "重复发布不能产生额外 UPDATE")
		require.Equal(t, published.UpdatedAt, after.UpdatedAt)
		require.Equal(t, fixture.page.Version, after.Version)
	})
}

func TestWikiProgressPublishFailureKeepsAppliedPageForResume(t *testing.T) {
	for _, failure := range []string{"write-error", "canceled-query"} {
		t.Run(failure, func(t *testing.T) {
			forWikiIngestProgressDBs(t, func(t *testing.T, db *gorm.DB) {
				ctx := context.Background()
				fixture := newProgressPublishFixture(t, db, "doc-publish", "entity/publish-retry")
				require.NoError(t, fixture.batch.release(ctx))
				want := errors.New("synthetic publication write failure")
				if failure == "canceled-query" {
					want = context.Canceled
				}
				var injected atomic.Bool
				const callback = "test:progress-publish-failure"
				require.NoError(t, db.Callback().Update().Before("gorm:update").Register(callback, func(tx *gorm.DB) {
					if !progressPublishUpdate(tx) || !injected.CompareAndSwap(false, true) {
						return
					}
					if failure == "canceled-query" {
						// 仅取消这次实际发布 SQL；编排层的清理上下文仍可保留任务供下一轮恢复。
						canceled, cancel := context.WithCancel(tx.Statement.Context)
						cancel()
						tx.Statement.Context = canceled
					} else {
						_ = tx.AddError(want)
					}
				}))
				t.Cleanup(func() { require.NoError(t, db.Callback().Update().Remove(callback)) })
				runner := newWikiIngestProgressService(t, db)
				require.ErrorIs(t, runProgressPublishTask(ctx, runner), want)
				require.True(t, injected.Load())
				assertProgressPublishSkippedReduce(t, runner)
				var queue types.TaskPendingOp
				require.NoError(t, db.First(&queue, fixture.ids[0]).Error)
				require.Nil(t, queue.ClaimedAt, "发布失败后释放领取，但保留待结算任务")
				var receipt types.WikiIngestPageProgress
				require.NoError(t, db.Where("pending_op_id = ? AND slug = ?",
					fixture.ids[0], fixture.page.Slug).First(&receipt).Error)
				require.True(t, receipt.Applied)
				require.True(t, receipt.EffectsDone)
				var doc types.Knowledge
				require.NoError(t, db.First(&doc, "id = ?", "doc-publish").Error)
				require.Equal(t, 2, doc.PendingSubtasksCount, "发布失败不能提前结算文档")
				before, err := runner.wikiService.GetPageBySlug(ctx, wikiIngestProgressTestKB, fixture.page.Slug)
				require.NoError(t, err)
				require.Equal(t, types.WikiPageStatusDraft, before.Status)

				// 故障仅注入一次；新 service 直接从成功回执恢复发布，无模型或 Reduce 补写。
				restarted := newWikiIngestProgressService(t, db)
				require.NoError(t, runProgressPublishTask(ctx, restarted))
				assertProgressPublishSkippedReduce(t, restarted)
				after, err := restarted.wikiService.GetPageBySlug(ctx, wikiIngestProgressTestKB, fixture.page.Slug)
				require.NoError(t, err)
				require.Equal(t, types.WikiPageStatusPublished, after.Status)
				require.Equal(t, before.Content, after.Content)
				require.Equal(t, before.Version, after.Version)
				require.Equal(t, before.SourceRefs, after.SourceRefs)
				require.Equal(t, before.ChunkRefs, after.ChunkRefs)
				require.NoError(t, db.First(&doc, "id = ?", "doc-publish").Error)
				require.Equal(t, 1, doc.PendingSubtasksCount)
				var remaining int64
				require.NoError(t, db.Model(&types.TaskPendingOp{}).
					Where("id = ?", fixture.ids[0]).Count(&remaining).Error)
				require.Zero(t, remaining)
			})
		})
	}
}

func TestWikiProgressPublishDoesNotUndoConcurrentArchive(t *testing.T) {
	forWikiIngestProgressDBs(t, func(t *testing.T, db *gorm.DB) {
		ctx := context.Background()
		fixture := newProgressPublishFixture(t, db, "doc-publish", "entity/publish-archived")
		interleaved := false
		fixture.service.wikiService = &progressPublishReadHook{
			WikiPageService: fixture.service.wikiService,
			slug:            fixture.page.Slug,
			afterRead: func(readCtx context.Context, page *types.WikiPage) {
				require.NoError(t, db.WithContext(readCtx).Model(&types.WikiPage{}).
					Where("id = ?", page.ID).UpdateColumn("status", types.WikiPageStatusArchived).Error)
				interleaved = true
			},
		}
		require.NoError(t, fixture.service.publishDraftPages(ctx, wikiIngestProgressTestKB,
			[]string{fixture.page.Slug}, map[string]string{fixture.page.Slug: fixture.page.ID}))
		require.True(t, interleaved)
		after, err := fixture.service.wikiService.GetPageBySlug(ctx, wikiIngestProgressTestKB, fixture.page.Slug)
		require.NoError(t, err)
		require.Equal(t, types.WikiPageStatusArchived, after.Status)
		require.Equal(t, fixture.page.SourceRefs, after.SourceRefs)
	})
}

func TestWikiProgressPublishSoftDeleteThenResumeDoesNotRevivePage(t *testing.T) {
	forWikiIngestProgressDBs(t, func(t *testing.T, db *gorm.DB) {
		ctx := context.Background()
		fixture := newProgressPublishFixture(t, db, "doc-publish", "entity/publish-deleted")
		interleaved := false
		fixture.service.wikiService = &progressPublishReadHook{
			WikiPageService: fixture.service.wikiService,
			slug:            fixture.page.Slug,
			afterRead: func(readCtx context.Context, page *types.WikiPage) {
				require.NoError(t, db.WithContext(readCtx).Model(&types.WikiPage{}).
					Where("id = ?", page.ID).UpdateColumn("deleted_at", time.Now()).Error)
				interleaved = true
			},
		}
		require.ErrorIs(t, fixture.service.publishDraftPages(ctx, wikiIngestProgressTestKB,
			[]string{fixture.page.Slug}, map[string]string{fixture.page.Slug: fixture.page.ID}),
			apprepo.ErrWikiPageNotFound)
		require.True(t, interleaved)
		require.NoError(t, fixture.batch.release(ctx))
		restarted := newWikiIngestProgressService(t, db)
		require.NoError(t, runProgressPublishTask(ctx, restarted))
		assertProgressPublishSkippedReduce(t, restarted)
		var deleted types.WikiPage
		require.NoError(t, db.Unscoped().First(&deleted, "id = ?", fixture.page.ID).Error)
		require.True(t, deleted.DeletedAt.Valid)
		require.Equal(t, types.WikiPageStatusDraft, deleted.Status)
		require.Equal(t, fixture.page.Content, deleted.Content)
		var remaining int64
		require.NoError(t, db.Model(&types.TaskPendingOp{}).Where("id = ?", fixture.ids[0]).Count(&remaining).Error)
		require.Zero(t, remaining)
	})
}
