package service

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"

	apprepo "github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// sharedPublicationHook 只控制发布交错，实际页面写入与进度回执仍经过真实仓储。
type sharedPublicationHook struct {
	interfaces.WikiPageService
	before func(context.Context, string) error
	after  func(context.Context, string) error
}

func (p *sharedPublicationHook) PublishDraftPage(
	ctx context.Context, tenantID uint64, kbID, pageID, slug string,
) error {
	if p.before != nil {
		if err := p.before(ctx, slug); err != nil {
			return err
		}
	}
	if err := p.WikiPageService.PublishDraftPage(ctx, tenantID, kbID, pageID, slug); err != nil {
		return err
	}
	if p.after != nil {
		return p.after(ctx, slug)
	}
	return nil
}

func (p *sharedPublicationHook) ReconcileAppliedPage(
	ctx context.Context, tenantID uint64, kbID string, receipt types.WikiIngestPageProgress,
) error {
	return p.WikiPageService.(interfaces.WikiPageProgressFinisher).
		ReconcileAppliedPage(ctx, tenantID, kbID, receipt)
}

// sharedPublicationEditDraft 模拟用户保存下一版草稿，不产生 ingest 完成回执。
func sharedPublicationEditDraft(ctx context.Context, db *gorm.DB, slug string) (*types.WikiPage, error) {
	var page types.WikiPage
	if err := db.WithContext(ctx).Where("tenant_id = ? AND knowledge_base_id = ? AND slug = ?",
		7, wikiIngestProgressTestKB, slug).Take(&page).Error; err != nil {
		return nil, err
	}
	result := db.WithContext(ctx).Model(&types.WikiPage{}).
		Where("id = ? AND version = ?", page.ID, page.Version).
		Updates(map[string]interface{}{
			"status": types.WikiPageStatusDraft, "version": page.Version + 1,
			"content": "用户随后保存、尚未发布的合成正文",
		})
	if result.Error != nil {
		return nil, result.Error
	}
	if result.RowsAffected != 1 {
		return nil, fmt.Errorf("synthetic user edit affected %d rows", result.RowsAffected)
	}
	page.Version++
	page.Status = types.WikiPageStatusDraft
	page.Content = "用户随后保存、尚未发布的合成正文"
	return &page, nil
}

// sharedPublicationResumedFixture 将旧成功回执和同页的新贡献放进同一次领取。
// 旧文档先结算后，新贡献仍须能够发布，不能按 PageID 永久屏蔽本批页面。
func sharedPublicationResumedFixture(t *testing.T, db *gorm.DB, slug string) *wikiSettlementFixture {
	t.Helper()
	const restoredID = "shared-publication-restored"
	const pendingID = "shared-publication-pending"
	seed := newWikiSettlementFixture(t, db, map[string][]string{restoredID: {slug}})
	seed.apply(t, slug)
	require.NoError(t, seed.batch.release(context.Background()))
	enqueueWikiIngestProgressDoc(t, seed.svc, db, pendingID)
	ops, _, batch := claimWikiIngestProgress(t, seed.svc, 2)
	update := wikiIngestProgressUpdate(pendingID, slug)
	doc := &docIngestResult{
		KnowledgeID: pendingID, DocTitle: pendingID, Summary: "后续贡献的合成摘要",
		Pages: []wikiIngestPageRef{{Slug: slug, Title: slug}},
	}
	pending, docs, _, err := batch.prepare(context.Background(), seed.svc, ops, nil,
		[]*docIngestResult{doc}, map[string][]SlugUpdate{slug: {update}}, nil,
		&WikiBatchContext{PlannedFolderID: make(map[string]string)})
	require.NoError(t, err)
	require.Len(t, pending[slug], 1)
	require.Equal(t, pendingID, pending[slug][0].KnowledgeID)
	f := &wikiSettlementFixture{svc: seed.svc, batch: batch, pending: pending, docs: docs}
	f.c = newWikiDocumentSettlement(batch, f.svc, wikiSettlementPayload(), docs, pending)
	batch.settlement = f.c
	return f
}

func TestWikiSharedPublicationAllowsLaterAppliedVersionInSameBatch(t *testing.T) {
	for _, replacement := range []bool{false, true} {
		t.Run(fmt.Sprintf("replacement=%t", replacement), func(t *testing.T) {
			forWikiIngestProgressDBs(t, func(t *testing.T, db *gorm.DB) {
				ctx := context.Background()
				const slug = "summary/shared-later-version"
				f := sharedPublicationResumedFixture(t, db, slug)
				require.NoError(t, f.c.settleReady(ctx))
				require.True(t, f.c.isSettled(f.id("shared-publication-restored")))
				require.False(t, f.c.isSettled(f.id("shared-publication-pending")))
				page, err := f.svc.wikiService.GetPageBySlug(ctx, wikiIngestProgressTestKB, slug)
				require.NoError(t, err)
				require.Equal(t, types.WikiPageStatusPublished, page.Status)
				firstID, firstVersion := page.ID, page.Version
				if replacement {
					require.NoError(t, db.Unscoped().Delete(&types.WikiPage{}, "id = ?", page.ID).Error)
					page = wikiIngestProgressPage(slug)
					require.NoError(t, apprepo.NewWikiPageRepository(db).Create(ctx, page))
					require.NoError(t, f.c.settleReady(ctx))
					current, loadErr := f.svc.wikiService.GetPageBySlug(ctx, wikiIngestProgressTestKB, slug)
					require.NoError(t, loadErr)
					require.Equal(t, types.WikiPageStatusDraft, current.Status, "旧回执不能发布同路径替代页")
				}
				candidate, err := f.svc.wikiService.GetPageBySlug(ctx, wikiIngestProgressTestKB, slug)
				require.NoError(t, err)
				candidate.Content += "\n同批后续文档合法提交的正文"
				candidate.Status = types.WikiPageStatusDraft
				candidate.SourceRefs = append(candidate.SourceRefs, "shared-publication-pending")
				var committed *types.WikiPage
				_, _, failed, deferred, err := f.batch.reduce(ctx, f.svc, slug, f.pending[slug],
					func(writeCtx context.Context, _ []SlugUpdate) (bool, string, bool, bool, error) {
						var writeErr error
						committed, writeErr = f.svc.wikiService.UpdatePage(writeCtx, candidate)
						return true, "ingest", false, false, writeErr
					})
				require.NoError(t, err)
				require.False(t, failed)
				require.False(t, deferred)
				require.NotNil(t, committed)
				state := loadWikiIngestProgress(t, f.svc, f.batch.claims[f.id("shared-publication-pending")])
				require.Len(t, state.Pages, 1)
				require.True(t, state.Pages[0].Applied && state.Pages[0].EffectsDone)
				require.Equal(t, committed.ID, state.Pages[0].PageID)
				require.Equal(t, committed.Version, state.Pages[0].PageVersion)
				if replacement {
					require.NotEqual(t, firstID, committed.ID)
				} else {
					require.Equal(t, firstID, committed.ID)
					require.Greater(t, committed.Version, firstVersion)
				}
				require.NoError(t, f.c.completeSlug(ctx, slug))
				require.True(t, f.c.isSettled(f.id("shared-publication-pending")))
				current, err := f.svc.wikiService.GetPageBySlug(ctx, wikiIngestProgressTestKB, slug)
				require.NoError(t, err)
				require.Equal(t, types.WikiPageStatusPublished, current.Status, "新的有效贡献不能被旧发布记录屏蔽")
				require.Equal(t, committed.Version, current.Version)
				require.Equal(t, committed.Content, current.Content)
				require.Contains(t, current.SourceRefs, "shared-publication-pending")
				require.Zero(t, wikiSettlementRows(t, db, wikiTaskType, WikiOpIngest))
			})
		})
	}
}

func TestWikiSharedPublicationRestoredReceiptPreservesNewDraft(t *testing.T) {
	forWikiIngestProgressDBs(t, func(t *testing.T, db *gorm.DB) {
		const slug = "summary/shared-restored-draft"
		f := newWikiSettlementFixture(t, db, map[string][]string{"doc-restored-draft": {slug}})
		f.apply(t, slug)
		draft, err := sharedPublicationEditDraft(context.Background(), db, slug)
		require.NoError(t, err)
		require.NoError(t, f.batch.release(context.Background()))
		restarted := newWikiIngestProgressService(t, db)
		require.NoError(t, runProgressPublishTask(context.Background(), restarted))
		assertProgressPublishSkippedReduce(t, restarted)
		assertWikiSettlementDraftPreserved(t, f, draft)
		require.Zero(t, wikiSettlementRows(t, db, wikiTaskType, WikiOpIngest))
	})
}

func TestWikiSharedPublicationProtectsDraftChangedBeforeAtomicUpdate(t *testing.T) {
	forWikiIngestProgressDBs(t, func(t *testing.T, db *gorm.DB) {
		const slug = "summary/shared-before-update"
		f := newWikiSettlementFixture(t, db, map[string][]string{"doc-before-update": {slug}})
		f.batch.settlement = f.c
		f.apply(t, slug)
		var draft *types.WikiPage
		var once sync.Once
		f.svc.wikiService = &sharedPublicationHook{
			WikiPageService: f.svc.wikiService,
			before: func(ctx context.Context, got string) error {
				var editErr error
				if got == slug {
					once.Do(func() { draft, editErr = sharedPublicationEditDraft(ctx, db, slug) })
				}
				return editErr
			},
		}
		require.NoError(t, f.c.completeSlug(context.Background(), slug))
		require.NotNil(t, draft, "交错必须发生在实际发布调用之前")
		assertWikiSettlementDraftPreserved(t, f, draft)
		require.True(t, f.c.isSettled(f.id("doc-before-update")))
	})
}

func TestWikiSharedPublicationConcurrentDocumentsPreserveNewDraft(t *testing.T) {
	forWikiIngestProgressDBs(t, func(t *testing.T, db *gorm.DB) {
		const shared = "summary/shared-concurrent"
		const first = "summary/shared-concurrent-a"
		const second = "summary/shared-concurrent-b"
		f := newWikiSettlementFixture(t, db, map[string][]string{
			"doc-concurrent-a": {shared, first}, "doc-concurrent-b": {shared, second},
		})
		f.batch.settlement = f.c
		for _, slug := range []string{shared, first, second} {
			f.apply(t, slug)
		}
		require.NoError(t, f.c.completeSlug(context.Background(), shared))
		published, release := make(chan struct{}), make(chan struct{})
		var enterOnce, releaseOnce sync.Once
		f.svc.wikiService = &sharedPublicationHook{
			WikiPageService: f.svc.wikiService,
			after: func(ctx context.Context, slug string) error {
				if slug != shared {
					return nil
				}
				var waitErr error
				enterOnce.Do(func() {
					close(published)
					select {
					case <-release:
					case <-ctx.Done():
						waitErr = ctx.Err()
					}
				})
				return waitErr
			},
		}
		start := func(slug string) <-chan error {
			ctx, cancel := context.WithCancel(context.Background())
			result := make(chan error, 1)
			done := make(chan struct{})
			t.Cleanup(func() {
				releaseOnce.Do(func() { close(release) })
				cancel()
				<-done
			})
			go func() {
				defer close(done)
				result <- f.c.completeSlug(ctx, slug)
			}()
			return result
		}
		firstResult := start(first)
		awaitEarlySettlement(t, published, "共享页已经落库、首次发布尚未返回")
		draft, err := sharedPublicationEditDraft(context.Background(), db, shared)
		require.NoError(t, err)
		secondResult := start(second)
		releaseOnce.Do(func() { close(release) })
		require.NoError(t, <-firstResult)
		require.NoError(t, <-secondResult)
		assertWikiSettlementDraftPreserved(t, f, draft)
		require.True(t, f.c.isSettled(f.id("doc-concurrent-a")))
		require.True(t, f.c.isSettled(f.id("doc-concurrent-b")))
		require.Zero(t, wikiSettlementRows(t, db, wikiTaskType, WikiOpIngest))
	})
}

func TestWikiSharedPublicationPartialFailureKeepsCompletedSharedVersion(t *testing.T) {
	forWikiIngestProgressDBs(t, func(t *testing.T, db *gorm.DB) {
		const shared = "summary/a-shared-partial"
		const failed = "summary/z-shared-failed"
		const slow = "summary/z-shared-slow"
		f := newWikiSettlementFixture(t, db, map[string][]string{
			"doc-partial-a": {shared, failed}, "doc-partial-b": {shared, slow},
		})
		f.batch.settlement = f.c
		f.apply(t, shared)
		f.apply(t, failed)
		var fail atomic.Bool
		fail.Store(true)
		f.svc.wikiService = &sharedPublicationHook{
			WikiPageService: f.svc.wikiService,
			before: func(_ context.Context, slug string) error {
				if slug == failed && fail.Load() {
					return errors.New("synthetic shared publication unavailable")
				}
				return nil
			},
		}
		require.NoError(t, f.c.completeSlug(context.Background(), shared))
		require.ErrorContains(t, f.c.completeSlug(context.Background(), failed), "publication unavailable")
		require.False(t, f.c.isSettled(f.id("doc-partial-a")))
		require.Equal(t, 2, wikiSettlementSlots(t, db, "doc-partial-a"))
		draft := wikiSettlementNewDraft(t, f, db, shared)
		f.apply(t, slow)
		require.NoError(t, f.c.completeSlug(context.Background(), slow))
		assertWikiSettlementDraftPreserved(t, f, draft)
		fail.Store(false)
		require.NoError(t, f.c.settleReady(context.Background()))
		assertWikiSettlementDraftPreserved(t, f, draft)
		page, err := f.svc.wikiService.GetPageBySlug(context.Background(), wikiIngestProgressTestKB, failed)
		require.NoError(t, err)
		require.Equal(t, types.WikiPageStatusPublished, page.Status, "失败页必须保留重试资格")
		require.True(t, f.c.isSettled(f.id("doc-partial-a")))
		require.True(t, f.c.isSettled(f.id("doc-partial-b")))
		require.Zero(t, wikiSettlementRows(t, db, wikiTaskType, WikiOpIngest))
	})
}

func TestWikiSharedPublicationCanceledAttemptCanRetry(t *testing.T) {
	forWikiIngestProgressDBs(t, func(t *testing.T, db *gorm.DB) {
		const slug = "summary/shared-cancelled-publication"
		f := newWikiSettlementFixture(t, db, map[string][]string{
			"doc-cancel-a": {slug}, "doc-cancel-b": {slug},
		})
		f.batch.settlement = f.c
		f.apply(t, slug)
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		var canceled atomic.Bool
		f.svc.wikiService = &sharedPublicationHook{
			WikiPageService: f.svc.wikiService,
			before: func(_ context.Context, _ string) error {
				if canceled.CompareAndSwap(false, true) {
					cancel()
					return context.Canceled
				}
				return nil
			},
		}
		require.ErrorIs(t, f.c.completeSlug(ctx, slug), context.Canceled)
		require.True(t, canceled.Load(), "取消必须发生在页面发布阶段")
		require.Equal(t, int64(2), wikiSettlementRows(t, db, wikiTaskType, WikiOpIngest))
		page, err := f.svc.wikiService.GetPageBySlug(context.Background(), wikiIngestProgressTestKB, slug)
		require.NoError(t, err)
		require.Equal(t, types.WikiPageStatusDraft, page.Status)
		require.NoError(t, f.c.settleReady(context.Background()))
		page, err = f.svc.wikiService.GetPageBySlug(context.Background(), wikiIngestProgressTestKB, slug)
		require.NoError(t, err)
		require.Equal(t, types.WikiPageStatusPublished, page.Status)
		require.True(t, f.c.isSettled(f.id("doc-cancel-a")))
		require.True(t, f.c.isSettled(f.id("doc-cancel-b")))
		require.Zero(t, wikiSettlementRows(t, db, wikiTaskType, WikiOpIngest))
	})
}
