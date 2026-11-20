package service

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"

	apprepo "github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// wikiSettlementFixture 使用真实队列、页面和进度仓储；模型输出仅使用合成正文。
type wikiSettlementFixture struct {
	svc     *wikiIngestService
	batch   *wikiProgressBatch
	pending map[string][]SlugUpdate
	docs    []*docIngestResult
	c       *wikiDocumentSettlement
}

func newWikiSettlementFixture(t *testing.T, db *gorm.DB, dependencies map[string][]string) *wikiSettlementFixture {
	t.Helper()
	svc := newWikiIngestProgressService(t, db)
	var docs []*docIngestResult
	grouped := make(map[string][]SlugUpdate)
	for id, slugs := range dependencies {
		enqueueWikiIngestProgressDoc(t, svc, db, id)
		doc := &docIngestResult{KnowledgeID: id, DocTitle: id, Summary: "synthetic summary"}
		for _, slug := range slugs {
			grouped[slug] = append(grouped[slug], wikiIngestProgressUpdate(id, slug))
			doc.Pages = append(doc.Pages, wikiIngestPageRef{Slug: slug, Title: slug})
		}
		docs = append(docs, doc)
	}
	ops, _, batch := claimWikiIngestProgress(t, svc, len(dependencies))
	pending, docs, _, err := batch.prepare(context.Background(), svc, ops, nil, docs, grouped, nil,
		&WikiBatchContext{PlannedFolderID: make(map[string]string)})
	require.NoError(t, err)
	f := &wikiSettlementFixture{svc: svc, batch: batch, pending: pending, docs: docs}
	f.c = newWikiDocumentSettlement(batch, svc, wikiSettlementPayload(), docs, pending)
	return f
}

func wikiSettlementPayload() WikiIngestPayload {
	return WikiIngestPayload{TenantID: 7, KnowledgeBaseID: wikiIngestProgressTestKB}
}

func wikiSettlementSlots(t *testing.T, db *gorm.DB, id string) int {
	t.Helper()
	var slots int
	require.NoError(t, db.Table("knowledges").Where("id = ?", id).
		Select("pending_subtasks_count").Scan(&slots).Error)
	return slots
}

func wikiSettlementRows(t *testing.T, db *gorm.DB, taskType, op string) int64 {
	t.Helper()
	var rows int64
	require.NoError(t, db.Model(&types.TaskPendingOp{}).
		Where("task_type = ? AND op = ?", taskType, op).Count(&rows).Error)
	return rows
}

func (f *wikiSettlementFixture) id(knowledgeID string) int64 {
	return f.batch.canonical[knowledgeID].dbID
}

func (f *wikiSettlementFixture) apply(t *testing.T, slug string) {
	t.Helper()
	applyWikiIngestProgress(t, f.svc, f.batch, slug, f.pending[slug])
}

func TestWikiDocumentSettlementDoesNotWaitForUnrelatedPage(t *testing.T) {
	forWikiIngestProgressDBs(t, func(t *testing.T, db *gorm.DB) {
		f := newWikiSettlementFixture(t, db, map[string][]string{
			"fast": {"summary/fast"}, "slow": {"summary/slow"},
		})
		require.NoError(t, db.Table("knowledges").Where("id = ?", "fast").
			Update("pending_subtasks_count", 1).Error)
		releaseSlow := make(chan struct{})
		var releaseOnce sync.Once
		slowDone := make(chan error, 1)
		slowExited := make(chan struct{})
		t.Cleanup(func() {
			releaseOnce.Do(func() { close(releaseSlow) })
			<-slowExited
		})
		go func() {
			defer close(slowExited)
			<-releaseSlow
			slowDone <- f.c.completeSlug(context.Background(), "summary/slow")
		}()
		f.apply(t, "summary/fast")
		require.NoError(t, f.c.completeSlug(context.Background(), "summary/fast"))
		require.True(t, f.c.isSettled(f.id("fast")))
		require.False(t, f.c.isSettled(f.id("slow")))
		require.Zero(t, wikiSettlementSlots(t, db, "fast"))
		require.Equal(t, 2, wikiSettlementSlots(t, db, "slow"))
		var status string
		require.NoError(t, db.Table("knowledges").Where("id = ?", "fast").Select("parse_status").Scan(&status).Error)
		require.Equal(t, types.ParseStatusCompleted, status)
		page, err := f.svc.wikiService.GetPageBySlug(context.Background(), wikiIngestProgressTestKB, "summary/fast")
		require.NoError(t, err)
		require.Equal(t, types.WikiPageStatusPublished, page.Status)
		require.Equal(t, int64(1), wikiSettlementRows(t, db, wikiTaskType, WikiOpIngest))
		// 慢页尚未提交，单纯回调也不能绕过持久回执检查。
		releaseOnce.Do(func() { close(releaseSlow) })
		require.ErrorIs(t, <-slowDone, types.ErrWikiProgressIncomplete)
		f.apply(t, "summary/slow")
		require.NoError(t, f.c.settleReady(context.Background()))
		require.True(t, f.c.isSettled(f.id("slow")))
	})
}

func TestWikiDocumentSettlementSharedPageKeepsOtherDependencies(t *testing.T) {
	forWikiIngestProgressDBs(t, func(t *testing.T, db *gorm.DB) {
		f := newWikiSettlementFixture(t, db, map[string][]string{
			"first": {"summary/shared"}, "second": {"summary/shared", "summary/slow"},
		})
		f.apply(t, "summary/shared")
		require.NoError(t, f.c.completeSlug(context.Background(), "summary/shared"))
		require.True(t, f.c.isSettled(f.id("first")))
		require.False(t, f.c.isSettled(f.id("second")))
		require.Equal(t, 1, wikiSettlementSlots(t, db, "first"))
		require.Equal(t, 2, wikiSettlementSlots(t, db, "second"))
		f.apply(t, "summary/slow")
		require.NoError(t, f.c.completeSlug(context.Background(), "summary/slow"))
		require.True(t, f.c.isSettled(f.id("second")))
	})
}

func TestWikiDocumentSettlementConcurrentLastPagesSettleOnce(t *testing.T) {
	forWikiIngestProgressDBs(t, func(t *testing.T, db *gorm.DB) {
		f := newWikiSettlementFixture(t, db, map[string][]string{
			"doc": {"summary/one", "summary/two"},
		})
		f.apply(t, "summary/one")
		f.apply(t, "summary/two")
		const callbacks = 16
		start := make(chan struct{})
		errs := make(chan error, callbacks)
		var wg sync.WaitGroup
		for i := range callbacks {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				<-start
				slug := "summary/one"
				if i%2 != 0 {
					slug = "summary/two"
				}
				errs <- f.c.completeSlug(context.Background(), slug)
			}(i)
		}
		close(start)
		wg.Wait()
		close(errs)
		for err := range errs {
			require.NoError(t, err)
		}
		require.NoError(t, f.c.settleReady(context.Background()))
		require.True(t, f.c.isSettled(f.id("doc")))
		require.Equal(t, 1, wikiSettlementSlots(t, db, "doc"))
		require.Equal(t, int64(1), wikiSettlementRows(t, db, wikiFinalizeTaskType, wikiFinalizeOpChange))
		state, ok := f.c.settledState(f.id("doc"))
		require.True(t, ok)
		require.Len(t, state.Pages, 2)
		for _, page := range state.Pages {
			require.True(t, page.Applied && page.EffectsDone)
		}
	})
}

func TestWikiDocumentSettlementRestoredReadyPlanNeedsNoReduce(t *testing.T) {
	forWikiIngestProgressDBs(t, func(t *testing.T, db *gorm.DB) {
		f := newWikiSettlementFixture(t, db, map[string][]string{"doc": {"summary/saved"}})
		f.apply(t, "summary/saved")
		require.NoError(t, f.batch.release(context.Background()))
		restarted := newWikiIngestProgressService(t, db)
		ops, _, batch := claimWikiIngestProgress(t, restarted, 1)
		pending, docs, _, err := batch.prepare(context.Background(), restarted, ops, nil, nil, nil, nil,
			&WikiBatchContext{PlannedFolderID: make(map[string]string)})
		require.NoError(t, err)
		require.Empty(t, pending)
		c := newWikiDocumentSettlement(batch, restarted, wikiSettlementPayload(), docs, pending)
		require.NoError(t, c.settleReady(context.Background()))
		require.True(t, c.isSettled(ops[0].dbID))
		require.Empty(t, restarted.wikiService.(*wikiIngestProgressPages).createCalls)
		require.Equal(t, 1, wikiSettlementSlots(t, db, "doc"))
	})
}

func TestWikiDocumentSettlementEmptyPlanCanComplete(t *testing.T) {
	forWikiIngestProgressDBs(t, func(t *testing.T, db *gorm.DB) {
		f := newWikiSettlementFixture(t, db, map[string][]string{"doc": nil})
		require.NoError(t, f.c.settleReady(context.Background()))
		require.True(t, f.c.isSettled(f.id("doc")))
		require.Equal(t, 1, wikiSettlementSlots(t, db, "doc"))
	})
}

func TestWikiDocumentSettlementMissingPlanIsNotSuccessful(t *testing.T) {
	forWikiIngestProgressDBs(t, func(t *testing.T, db *gorm.DB) {
		svc := newWikiIngestProgressService(t, db)
		enqueueWikiIngestProgressDoc(t, svc, db, "doc")
		ops, _, batch := claimWikiIngestProgress(t, svc, 1)
		c := newWikiDocumentSettlement(batch, svc, wikiSettlementPayload(), nil, nil)
		require.NoError(t, c.settleReady(context.Background()))
		require.False(t, c.isSettled(ops[0].dbID))
		require.Equal(t, 2, wikiSettlementSlots(t, db, "doc"))
	})
}

// wikiSettlementPublishFailure 只替换发布返回值，页面提交和回执查询仍走真实数据库。
type wikiSettlementPublishFailure struct {
	interfaces.WikiPageService
	err error
}

func (s *wikiSettlementPublishFailure) PublishDraftPage(
	ctx context.Context, tenantID uint64, kbID, pageID, slug string,
) error {
	if s.err != nil {
		return s.err
	}
	return s.WikiPageService.PublishDraftPage(ctx, tenantID, kbID, pageID, slug)
}

func TestWikiDocumentSettlementPublicationFailureRetainsClaim(t *testing.T) {
	forWikiIngestProgressDBs(t, func(t *testing.T, db *gorm.DB) {
		f := newWikiSettlementFixture(t, db, map[string][]string{"doc": {"summary/page"}})
		f.apply(t, "summary/page")
		pages := &wikiSettlementPublishFailure{
			WikiPageService: f.svc.wikiService, err: errors.New("publish unavailable"),
		}
		f.svc.wikiService = pages
		require.ErrorContains(t, f.c.completeSlug(context.Background(), "summary/page"), "publish unavailable")
		require.False(t, f.c.isSettled(f.id("doc")))
		require.Zero(t, wikiSettlementRows(t, db, wikiFinalizeTaskType, wikiFinalizeOpSlug))
		require.Equal(t, 2, wikiSettlementSlots(t, db, "doc"))
		pages.err = nil
		require.NoError(t, f.c.settleReady(context.Background()))
		require.True(t, f.c.isSettled(f.id("doc")))
		require.Equal(t, 1, wikiSettlementSlots(t, db, "doc"))
	})
}

type wikiSettlementHandoffFailure struct {
	interfaces.TaskPendingOpsRepository
	fail bool
}

func (r *wikiSettlementHandoffFailure) Enqueue(ctx context.Context, op *types.TaskPendingOp) error {
	if r.fail && op.TaskType == wikiFinalizeTaskType && op.Op == wikiFinalizeOpChange {
		return errors.New("handoff unavailable")
	}
	return r.TaskPendingOpsRepository.Enqueue(ctx, op)
}

func TestWikiDocumentSettlementPartialHandoffFailureCanRecover(t *testing.T) {
	forWikiIngestProgressDBs(t, func(t *testing.T, db *gorm.DB) {
		f := newWikiSettlementFixture(t, db, map[string][]string{"doc": {"summary/page"}})
		f.apply(t, "summary/page")
		queue := &wikiSettlementHandoffFailure{TaskPendingOpsRepository: f.svc.pendingRepo, fail: true}
		f.svc.pendingRepo = queue
		require.ErrorContains(t, f.c.completeSlug(context.Background(), "summary/page"), "handoff unavailable")
		require.Equal(t, int64(1), wikiSettlementRows(t, db, wikiFinalizeTaskType, wikiFinalizeOpSlug),
			"已持久化的部分页面交接仍需要批尾调度")
		require.False(t, f.c.isHandedOff(f.id("doc")))
		require.False(t, f.c.isSettled(f.id("doc")))
		require.Equal(t, 2, wikiSettlementSlots(t, db, "doc"))
		queue.fail = false
		require.NoError(t, f.c.settleReady(context.Background()))
		require.True(t, f.c.isSettled(f.id("doc")))
		require.Equal(t, int64(1), wikiSettlementRows(t, db, wikiFinalizeTaskType, wikiFinalizeOpChange))
	})
}

type wikiSettlementFailure struct {
	interfaces.WikiIngestProgressRepository
	failID int64
	calls  []int64
}

func (r *wikiSettlementFailure) SettleWikiIngestClaim(
	ctx context.Context, claim types.WikiProgressClaim, finalize, discard bool,
) (bool, error) {
	r.calls = append(r.calls, claim.PendingOpID)
	if r.failID == claim.PendingOpID {
		return false, fmt.Errorf("settlement unavailable for %d", claim.PendingOpID)
	}
	return r.WikiIngestProgressRepository.SettleWikiIngestClaim(ctx, claim, finalize, discard)
}

func TestWikiDocumentSettlementDiscardsOldRowsBeforeCanonical(t *testing.T) {
	forWikiIngestProgressDBs(t, func(t *testing.T, db *gorm.DB) {
		svc := newWikiIngestProgressService(t, db)
		enqueueWikiIngestProgressDoc(t, svc, db, "doc")
		enqueueWikiIngestProgressDoc(t, svc, db, "doc")
		ops, ids, batch, err := svc.claimWikiProgressBatch(context.Background(), wikiSettlementPayload(), 1)
		require.NoError(t, err)
		require.Len(t, ids, 2)
		require.Len(t, ops, 1)
		pending := prepareWikiIngestProgress(t, svc, batch, ops, wikiIngestProgressUpdate("doc", "summary/page"))
		docs := []*docIngestResult{{KnowledgeID: "doc", DocTitle: "synthetic"}}
		c := newWikiDocumentSettlement(batch, svc, wikiSettlementPayload(), docs, pending)
		applyWikiIngestProgress(t, svc, batch, "summary/page", pending["summary/page"])
		repo := &wikiSettlementFailure{WikiIngestProgressRepository: batch.repo, failID: ids[0]}
		batch.repo = repo
		require.ErrorContains(t, c.completeSlug(context.Background(), "summary/page"), "settlement unavailable")
		require.Equal(t, []int64{ids[0]}, repo.calls)
		require.Equal(t, 2, wikiSettlementSlots(t, db, "doc"))
		require.Equal(t, int64(2), wikiSettlementRows(t, db, wikiTaskType, WikiOpIngest))
		require.True(t, c.isHandedOff(ops[0].dbID))

		// 旧行成功、canonical 失败后再次尝试，不能重复消费旧行或重复交接 change。
		repo.failID = ops[0].dbID
		require.ErrorContains(t, c.settleReady(context.Background()), "settlement unavailable")
		require.Equal(t, int64(1), wikiSettlementRows(t, db, wikiTaskType, WikiOpIngest))
		require.Equal(t, 2, wikiSettlementSlots(t, db, "doc"))
		repo.failID = 0
		require.NoError(t, c.settleReady(context.Background()))
		for _, id := range ids {
			require.True(t, c.isSettled(id))
		}
		require.Equal(t, 1, wikiSettlementSlots(t, db, "doc"))
		require.Equal(t, int64(1), wikiSettlementRows(t, db, wikiFinalizeTaskType, wikiFinalizeOpChange))
	})
}

func TestWikiDocumentSettlementDoesNotConsumeNewQueueRow(t *testing.T) {
	forWikiIngestProgressDBs(t, func(t *testing.T, db *gorm.DB) {
		f := newWikiSettlementFixture(t, db, map[string][]string{"doc": {"summary/page"}})
		f.apply(t, "summary/page")
		require.NoError(t, f.c.completeSlug(context.Background(), "summary/page"))
		enqueueWikiIngestProgressDoc(t, f.svc, db, "doc")
		ops, _, fresh := claimWikiIngestProgress(t, f.svc, 1)
		require.NotEqual(t, f.id("doc"), ops[0].dbID)
		require.NoError(t, f.c.completeSlug(context.Background(), "summary/page"))
		require.NoError(t, f.c.settleReady(context.Background()))
		require.False(t, f.c.isSettled(ops[0].dbID))
		require.Equal(t, int64(1), wikiSettlementRows(t, db, wikiTaskType, WikiOpIngest))
		require.Equal(t, 1, wikiSettlementSlots(t, db, "doc"))
		_, err := fresh.repo.LoadWikiIngestPlan(context.Background(), fresh.claims[ops[0].dbID])
		require.NoError(t, err)
	})
}

func TestWikiDocumentSettlementLostClaimCannotPublishOrConsume(t *testing.T) {
	forWikiIngestProgressDBs(t, func(t *testing.T, db *gorm.DB) {
		f := newWikiSettlementFixture(t, db, map[string][]string{"doc": {"summary/page"}})
		f.apply(t, "summary/page")
		require.NoError(t, db.Model(&types.TaskPendingOp{}).Where("id = ?", f.id("doc")).
			Update("claim_epoch", gorm.Expr("claim_epoch + 1")).Error)
		require.ErrorIs(t, f.c.completeSlug(context.Background(), "summary/page"), types.ErrWikiProgressClaimLost)
		require.False(t, f.c.isSettled(f.id("doc")))
		require.Equal(t, 2, wikiSettlementSlots(t, db, "doc"))
		page, err := f.svc.wikiService.GetPageBySlug(context.Background(), wikiIngestProgressTestKB, "summary/page")
		require.NoError(t, err)
		require.Equal(t, types.WikiPageStatusDraft, page.Status)
	})
}

func TestWikiDocumentSettlementCancellationKeepsProgressRecoverable(t *testing.T) {
	forWikiIngestProgressDBs(t, func(t *testing.T, db *gorm.DB) {
		f := newWikiSettlementFixture(t, db, map[string][]string{"doc": {"summary/page"}})
		f.apply(t, "summary/page")
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		require.ErrorIs(t, f.c.completeSlug(ctx, "summary/page"), context.Canceled)
		require.False(t, f.c.isSettled(f.id("doc")))
		require.Equal(t, 2, wikiSettlementSlots(t, db, "doc"))
		require.NoError(t, f.c.settleReady(context.Background()))
		require.True(t, f.c.isSettled(f.id("doc")))
	})
}

func TestWikiDocumentSettlementDoesNotPublishReplacement(t *testing.T) {
	forWikiIngestProgressDBs(t, func(t *testing.T, db *gorm.DB) {
		f := newWikiSettlementFixture(t, db, map[string][]string{"doc": {"summary/page"}})
		f.apply(t, "summary/page")
		old, err := f.svc.wikiService.GetPageBySlug(context.Background(), wikiIngestProgressTestKB, "summary/page")
		require.NoError(t, err)
		require.NoError(t, db.Unscoped().Delete(&types.WikiPage{}, "id = ?", old.ID).Error)
		replacement := wikiIngestProgressPage("summary/page")
		require.NoError(t, apprepo.NewWikiPageRepository(db).Create(context.Background(), replacement))
		require.NoError(t, f.c.completeSlug(context.Background(), "summary/page"))
		current, err := f.svc.wikiService.GetPageBySlug(context.Background(), wikiIngestProgressTestKB, "summary/page")
		require.NoError(t, err)
		require.Equal(t, replacement.ID, current.ID)
		require.Equal(t, types.WikiPageStatusDraft, current.Status)
		require.Zero(t, wikiSettlementRows(t, db, wikiFinalizeTaskType, wikiFinalizeOpSlug))
	})
}

// wikiSettlementBeforeSettle 只在最终消费前插入外部变化，回执、计数与 claim 校验仍由真实仓储执行。
type wikiSettlementBeforeSettle struct {
	interfaces.WikiIngestProgressRepository
	before func()
}

func (r *wikiSettlementBeforeSettle) SettleWikiIngestClaim(
	ctx context.Context, claim types.WikiProgressClaim, finalize, discard bool,
) (bool, error) {
	if r.before != nil {
		r.before()
		r.before = nil
	}
	return r.WikiIngestProgressRepository.SettleWikiIngestClaim(ctx, claim, finalize, discard)
}

func TestWikiDocumentSettlementClaimLostAfterHandoffDoesNotDecrement(t *testing.T) {
	forWikiIngestProgressDBs(t, func(t *testing.T, db *gorm.DB) {
		f := newWikiSettlementFixture(t, db, map[string][]string{"doc": {"summary/page"}})
		f.apply(t, "summary/page")
		f.batch.repo = &wikiSettlementBeforeSettle{
			WikiIngestProgressRepository: f.batch.repo,
			before: func() {
				require.NoError(t, db.Model(&types.TaskPendingOp{}).Where("id = ?", f.id("doc")).
					Update("claim_epoch", gorm.Expr("claim_epoch + 1")).Error)
			},
		}
		require.ErrorIs(t, f.c.completeSlug(context.Background(), "summary/page"), types.ErrWikiProgressClaimLost)
		require.True(t, f.c.isHandedOff(f.id("doc")))
		require.False(t, f.c.isSettled(f.id("doc")))
		require.Equal(t, 2, wikiSettlementSlots(t, db, "doc"))
		require.Equal(t, int64(1), wikiSettlementRows(t, db, wikiTaskType, WikiOpIngest))
	})
}

func TestWikiDocumentSettlementEmptyPlanWithoutDocumentCanComplete(t *testing.T) {
	forWikiIngestProgressDBs(t, func(t *testing.T, db *gorm.DB) {
		svc := newWikiIngestProgressService(t, db)
		enqueueWikiIngestProgressDoc(t, svc, db, "doc")
		ops, _, batch := claimWikiIngestProgress(t, svc, 1)
		pending := prepareWikiIngestProgress(t, svc, batch, ops)
		c := newWikiDocumentSettlement(batch, svc, wikiSettlementPayload(), nil, pending)
		require.NoError(t, c.settleReady(context.Background()))
		require.True(t, c.isSettled(ops[0].dbID))
		require.Equal(t, 1, wikiSettlementSlots(t, db, "doc"))
		require.Zero(t, wikiSettlementRows(t, db, wikiFinalizeTaskType, wikiFinalizeOpChange))
	})
}

func TestWikiDocumentSettlementSupersededOrDeletedOwnerCannotFinish(t *testing.T) {
	for _, change := range []string{"rebuild", "cancel", "delete"} {
		t.Run(change, func(t *testing.T) {
			forWikiIngestProgressDBs(t, func(t *testing.T, db *gorm.DB) {
				f := newWikiSettlementFixture(t, db, map[string][]string{"doc": {"summary/page"}})
				f.apply(t, "summary/page")
				switch change {
				case "rebuild":
					require.NoError(t, db.Exec(
						"INSERT INTO knowledge_processing_spans (id, knowledge_id, attempt) "+
							"VALUES ('new-attempt', 'doc', 4)",
					).Error)
				case "cancel":
					require.NoError(t, db.Table("knowledges").Where("id = ?", "doc").
						Update("parse_status", types.ParseStatusCancelled).Error)
				case "delete":
					require.NoError(t, db.Table("knowledges").Where("id = ?", "doc").
						Update("deleted_at", gorm.Expr("CURRENT_TIMESTAMP")).Error)
				}
				require.ErrorIs(t, f.c.completeSlug(context.Background(), "summary/page"),
					types.ErrWikiProgressOwnerGone)
				require.False(t, f.c.isSettled(f.id("doc")))
				require.Equal(t, 2, wikiSettlementSlots(t, db, "doc"))
				require.Zero(t, wikiSettlementRows(t, db, wikiFinalizeTaskType, wikiFinalizeOpChange))
			})
		})
	}
}

// wikiSettlementNewDraft 模拟同一页面后续编辑，保留身份并推进版本，不改变旧文档的完成回执。
func wikiSettlementNewDraft(t *testing.T, f *wikiSettlementFixture, db *gorm.DB, slug string) *types.WikiPage {
	t.Helper()
	page, err := f.svc.wikiService.GetPageBySlug(context.Background(), wikiIngestProgressTestKB, slug)
	require.NoError(t, err)
	require.Equal(t, types.WikiPageStatusPublished, page.Status)
	const newBody = "后续编辑尚未发布的合成正文"
	changed := db.Model(&types.WikiPage{}).Where("id = ?", page.ID).Updates(map[string]interface{}{
		"status": types.WikiPageStatusDraft, "content": newBody, "version": page.Version + 1,
	})
	require.NoError(t, changed.Error)
	require.EqualValues(t, 1, changed.RowsAffected)
	page.Version++
	page.Content = newBody
	page.Status = types.WikiPageStatusDraft
	return page
}

func assertWikiSettlementDraftPreserved(t *testing.T, f *wikiSettlementFixture, expected *types.WikiPage) {
	t.Helper()
	current, err := f.svc.wikiService.GetPageBySlug(context.Background(), wikiIngestProgressTestKB, expected.Slug)
	require.NoError(t, err)
	require.Equal(t, expected.ID, current.ID)
	require.Equal(t, expected.Version, current.Version)
	require.Equal(t, expected.Content, current.Content)
	require.Equal(t, types.WikiPageStatusDraft, current.Status, "结算重试不能再次发布后续编辑")
}

// 已发布并交接的页面只需重试队列消费，不能再次发布同身份的新草稿。
func TestWikiDocumentSettlementRetryPreservesDraftAfterHandoff(t *testing.T) {
	forWikiIngestProgressDBs(t, func(t *testing.T, db *gorm.DB) {
		const slug = "summary/retry-after-handoff"
		f := newWikiSettlementFixture(t, db, map[string][]string{"doc": {slug}})
		f.apply(t, slug)
		repo := &wikiSettlementFailure{WikiIngestProgressRepository: f.batch.repo, failID: f.id("doc")}
		f.batch.repo = repo
		require.ErrorContains(t, f.c.completeSlug(context.Background(), slug), "settlement unavailable")
		require.True(t, f.c.isHandedOff(f.id("doc")))
		require.False(t, f.c.isSettled(f.id("doc")))
		require.Equal(t, 2, wikiSettlementSlots(t, db, "doc"))
		draft := wikiSettlementNewDraft(t, f, db, slug)
		repo.failID = 0
		require.NoError(t, f.c.settleReady(context.Background()))
		require.True(t, f.c.isSettled(f.id("doc")))
		require.Zero(t, wikiSettlementRows(t, db, wikiTaskType, WikiOpIngest))
		require.Equal(t, 1, wikiSettlementSlots(t, db, "doc"))
		require.Equal(t, int64(1), wikiSettlementRows(t, db, wikiFinalizeTaskType, wikiFinalizeOpChange))
		assertWikiSettlementDraftPreserved(t, f, draft)
	})
}

// 发布已成功而交接部分失败时，也应记住本进程已经完成的发布，不能只靠 handedOff 标记。
func TestWikiDocumentSettlementRetryPreservesDraftAfterHandoffFailure(t *testing.T) {
	forWikiIngestProgressDBs(t, func(t *testing.T, db *gorm.DB) {
		const slug = "summary/retry-handoff-failure"
		f := newWikiSettlementFixture(t, db, map[string][]string{"doc": {slug}})
		f.apply(t, slug)
		queue := &wikiSettlementHandoffFailure{TaskPendingOpsRepository: f.svc.pendingRepo, fail: true}
		f.svc.pendingRepo = queue
		require.ErrorContains(t, f.c.completeSlug(context.Background(), slug), "handoff unavailable")
		require.False(t, f.c.isHandedOff(f.id("doc")))
		require.False(t, f.c.isSettled(f.id("doc")))
		require.Equal(t, 2, wikiSettlementSlots(t, db, "doc"))
		draft := wikiSettlementNewDraft(t, f, db, slug)
		queue.fail = false
		require.NoError(t, f.c.settleReady(context.Background()))
		require.True(t, f.c.isHandedOff(f.id("doc")))
		require.True(t, f.c.isSettled(f.id("doc")))
		require.Zero(t, wikiSettlementRows(t, db, wikiTaskType, WikiOpIngest))
		require.Equal(t, 1, wikiSettlementSlots(t, db, "doc"))
		require.Equal(t, int64(1), wikiSettlementRows(t, db, wikiFinalizeTaskType, wikiFinalizeOpChange))
		assertWikiSettlementDraftPreserved(t, f, draft)
	})
}

// wikiSettlementSelectedPublishFailure 只让指定页面发布失败，其他页面及回执仍使用真实数据库。
type wikiSettlementSelectedPublishFailure struct {
	interfaces.WikiPageService
	failSlug string
	calls    map[string]int
}

func (s *wikiSettlementSelectedPublishFailure) PublishDraftPage(
	ctx context.Context, tenantID uint64, kbID, pageID, slug string,
) error {
	s.calls[slug]++
	if slug == s.failSlug {
		return errors.New("selected page publication unavailable")
	}
	return s.WikiPageService.PublishDraftPage(ctx, tenantID, kbID, pageID, slug)
}

// 多页发布部分成功时按页面保留成功标记，失败页继续重试，不能整体重置发布进度。
func TestWikiDocumentSettlementRetryPreservesDraftAfterPartialPublication(t *testing.T) {
	forWikiIngestProgressDBs(t, func(t *testing.T, db *gorm.DB) {
		const successSlug = "summary/a-published"
		const failedSlug = "summary/z-unpublished"
		f := newWikiSettlementFixture(t, db, map[string][]string{"doc": {successSlug, failedSlug}})
		f.apply(t, successSlug)
		f.apply(t, failedSlug)
		pages := &wikiSettlementSelectedPublishFailure{
			WikiPageService: f.svc.wikiService, failSlug: failedSlug, calls: make(map[string]int),
		}
		f.svc.wikiService = pages
		require.NoError(t, f.c.completeSlug(context.Background(), successSlug))
		require.ErrorContains(t, f.c.completeSlug(context.Background(), failedSlug), "publication unavailable")
		require.False(t, f.c.isHandedOff(f.id("doc")))
		require.False(t, f.c.isSettled(f.id("doc")))
		require.Zero(t, wikiSettlementRows(t, db, wikiFinalizeTaskType, wikiFinalizeOpSlug))
		require.Equal(t, 2, wikiSettlementSlots(t, db, "doc"))
		draft := wikiSettlementNewDraft(t, f, db, successSlug)
		failed, err := f.svc.wikiService.GetPageBySlug(context.Background(), wikiIngestProgressTestKB, failedSlug)
		require.NoError(t, err)
		require.Equal(t, types.WikiPageStatusDraft, failed.Status)
		pages.failSlug = ""
		require.NoError(t, f.c.settleReady(context.Background()))
		require.True(t, f.c.isSettled(f.id("doc")))
		require.Zero(t, wikiSettlementRows(t, db, wikiTaskType, WikiOpIngest))
		require.Equal(t, 1, wikiSettlementSlots(t, db, "doc"))
		assertWikiSettlementDraftPreserved(t, f, draft)
		failed, err = f.svc.wikiService.GetPageBySlug(context.Background(), wikiIngestProgressTestKB, failedSlug)
		require.NoError(t, err)
		require.Equal(t, types.WikiPageStatusPublished, failed.Status, "失败页必须继续发布")
		require.Equal(t, 1, pages.calls[successSlug], "已成功页面不能重复发布")
		require.Equal(t, 2, pages.calls[failedSlug], "失败页面应有一次重试")
	})
}
