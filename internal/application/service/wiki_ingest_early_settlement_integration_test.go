package service

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/hibiken/asynq"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// earlySettlementGate 用通道固定慢页的交错顺序；超时只用于终止失败用例，不参与成功判定。
type earlySettlementGate struct {
	entered chan struct{}
	release chan struct{}
	once    sync.Once
	unblock sync.Once
}

func newEarlySettlementGate() *earlySettlementGate {
	return &earlySettlementGate{entered: make(chan struct{}), release: make(chan struct{})}
}

func (g *earlySettlementGate) open() { g.unblock.Do(func() { close(g.release) }) }

func (g *earlySettlementGate) wait(ctx context.Context) error {
	g.once.Do(func() { close(g.entered) })
	select {
	case <-g.release:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// earlySettlementPages 只阻塞指定页面的写入，页面与回执仍由真实仓储事务保存。
type earlySettlementPages struct {
	interfaces.WikiPageService
	gates map[string]*earlySettlementGate
}

func (p *earlySettlementPages) CreatePage(ctx context.Context, page *types.WikiPage) (*types.WikiPage, error) {
	if gate := p.gates[page.Slug]; gate != nil {
		if err := gate.wait(ctx); err != nil {
			return nil, err
		}
	}
	return p.WikiPageService.CreatePage(ctx, page)
}

func (p *earlySettlementPages) ReconcileAppliedPage(
	ctx context.Context, tenantID uint64, kbID string, receipt types.WikiIngestPageProgress,
) error {
	return p.WikiPageService.(interfaces.WikiPageProgressFinisher).
		ReconcileAppliedPage(ctx, tenantID, kbID, receipt)
}

// earlySettlementObserver 只在真实结算事务成功后通知测试，不替代数据库检查。
type earlySettlementObserver struct {
	interfaces.WikiIngestProgressRepository
	mu      sync.Mutex
	settled map[string]chan struct{}
	counts  map[string]int
}

func (r *earlySettlementObserver) SettleWikiIngestClaim(
	ctx context.Context, claim types.WikiProgressClaim, finalize, discard bool,
) (bool, error) {
	consumed, err := r.WikiIngestProgressRepository.SettleWikiIngestClaim(ctx, claim, finalize, discard)
	if err == nil && consumed && finalize {
		r.mu.Lock()
		r.counts[claim.KnowledgeID]++
		if r.counts[claim.KnowledgeID] == 1 {
			if signal := r.settled[claim.KnowledgeID]; signal != nil {
				close(signal)
			}
		}
		r.mu.Unlock()
	}
	return consumed, err
}

// earlySettlementTasks 记录真实编排发出的触发器；不会执行后台模型或发送外部请求。
type earlySettlementTasks struct {
	mu          sync.Mutex
	finalize    int
	failTrigger bool
}

func (q *earlySettlementTasks) Enqueue(task *asynq.Task, _ ...asynq.Option) (*asynq.TaskInfo, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if task.Type() == types.TypeWikiFinalize {
		q.finalize++
		if q.failTrigger {
			return nil, errors.New("synthetic finalize trigger failure")
		}
	}
	return &asynq.TaskInfo{ID: "early-settlement", Type: task.Type()}, nil
}

func (q *earlySettlementTasks) finalizeCount() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.finalize
}

// seedEarlySettlementPlans 冻结合成 Map 输出，完整执行真实 Reduce、发布、交接和结算。
// 这样恢复测试不会依赖模型返回格式，也不会把服务重建说成进程强杀验证。
func seedEarlySettlementPlans(t *testing.T, db *gorm.DB, updates []SlugUpdate, appliedSlugs ...string) {
	t.Helper()
	seed := newWikiIngestProgressService(t, db)
	byDoc := make(map[string][]SlugUpdate)
	grouped := make(map[string][]SlugUpdate)
	for _, update := range updates {
		byDoc[update.KnowledgeID] = append(byDoc[update.KnowledgeID], update)
		grouped[update.Slug] = append(grouped[update.Slug], update)
	}
	var docs []*docIngestResult
	for id, contributions := range byDoc {
		enqueueWikiIngestProgressDoc(t, seed, db, id)
		require.NoError(t, db.Exec("UPDATE knowledges SET pending_subtasks_count = 1 WHERE id = ?", id).Error)
		doc := &docIngestResult{KnowledgeID: id, DocTitle: id, Summary: "冻结的合成摘要"}
		for _, update := range contributions {
			doc.Pages = append(doc.Pages, wikiIngestPageRef{Slug: update.Slug, Title: update.DocTitle})
		}
		docs = append(docs, doc)
	}
	ops, _, batch := claimWikiIngestProgress(t, seed, len(byDoc))
	pending, _, _, err := batch.prepare(context.Background(), seed, ops, nil, docs, grouped, nil,
		&WikiBatchContext{PlannedFolderID: make(map[string]string)})
	require.NoError(t, err)
	for _, slug := range appliedSlugs {
		applyWikiIngestProgress(t, seed, batch, slug, pending[slug])
	}
	require.NoError(t, batch.release(context.Background()))
}

func newEarlySettlementRunner(t *testing.T, db *gorm.DB, gates map[string]*earlySettlementGate, ids ...string) (
	*wikiIngestService, *earlySettlementObserver, *earlySettlementTasks, *wikiIngestProgressPages,
) {
	t.Helper()
	runner := newWikiIngestProgressService(t, db)
	runner.kbService.(*wikiLookupKBService).kb.WikiConfig.IngestReduceParallel = 4
	writes := runner.wikiService.(*wikiIngestProgressPages)
	runner.wikiService = &earlySettlementPages{WikiPageService: writes, gates: gates}
	observer := &earlySettlementObserver{
		WikiIngestProgressRepository: runner.progressRepo,
		settled:                      make(map[string]chan struct{}), counts: make(map[string]int),
	}
	for _, id := range ids {
		observer.settled[id] = make(chan struct{})
	}
	runner.progressRepo = observer
	queue := &earlySettlementTasks{}
	runner.task = queue
	return runner, observer, queue, writes
}

func awaitEarlySettlement(t *testing.T, signal <-chan struct{}, description string) {
	t.Helper()
	select {
	case <-signal:
	case <-time.After(10 * time.Second):
		t.Fatalf("等待%s超时；慢页尚未释放，不能依赖批尾结算", description)
	}
}

// startEarlySettlementTask 在断言失败时也先退出工作协程，再让双库夹具关闭连接。
func startEarlySettlementTask(t *testing.T, runner *wikiIngestService, gates ...*earlySettlementGate) (
	context.CancelFunc, <-chan struct{}, *error,
) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	var result error
	go func() {
		result = runProgressPublishTask(ctx, runner)
		close(done)
	}()
	t.Cleanup(func() {
		cancel()
		for _, gate := range gates {
			gate.open()
		}
		awaitEarlySettlement(t, done, "任务退出")
	})
	return cancel, done, &result
}

func assertEarlySettlementDocument(t *testing.T, db *gorm.DB, id string, completed bool) {
	t.Helper()
	var doc types.Knowledge
	require.NoError(t, db.First(&doc, "id = ?", id).Error)
	var pending int64
	require.NoError(t, db.Model(&types.TaskPendingOp{}).
		Where("task_type = ? AND dedup_key = ?", wikiTaskType, id).Count(&pending).Error)
	if completed {
		require.Zero(t, pending, "成功文档应消费自己的队列行")
		require.Zero(t, doc.PendingSubtasksCount, "成功文档应及时释放自己的子任务计数")
		require.Equal(t, "completed", doc.ParseStatus)
	} else {
		require.EqualValues(t, 1, pending)
		require.Equal(t, 1, doc.PendingSubtasksCount)
		require.Equal(t, "finalizing", doc.ParseStatus)
	}
}

func assertEarlySettlementHandoff(t *testing.T, db *gorm.DB, id string) {
	t.Helper()
	var rows []types.TaskPendingOp
	require.NoError(t, db.Where("task_type = ? AND op = ?", wikiFinalizeTaskType, wikiFinalizeOpChange).
		Find(&rows).Error)
	found := false
	for _, row := range rows {
		var change wikiFinalizeRow
		require.NoError(t, json.Unmarshal(row.Payload, &change))
		if change.Change != nil && change.Change.DocTitle == id {
			found = true
		}
	}
	require.True(t, found, "删除文档计划之前必须持久交接对应的 finalize 变更")
}

func TestProcessWikiIngestSettlesFastDocumentBeforeUnrelatedSlowPage(t *testing.T) {
	forWikiIngestProgressDBs(t, func(t *testing.T, db *gorm.DB) {
		fast := wikiIngestProgressUpdate("doc-fast", "summary/fast")
		slow := wikiIngestProgressUpdate("doc-slow", "summary/slow")
		seedEarlySettlementPlans(t, db, []SlugUpdate{fast, slow})
		gate := newEarlySettlementGate()
		runner, observer, queue, _ := newEarlySettlementRunner(t, db,
			map[string]*earlySettlementGate{slow.Slug: gate}, fast.KnowledgeID, slow.KnowledgeID)
		_, done, result := startEarlySettlementTask(t, runner, gate)
		awaitEarlySettlement(t, gate.entered, "慢页进入写入")
		awaitEarlySettlement(t, observer.settled[fast.KnowledgeID], "快文档结算")
		assertEarlySettlementDocument(t, db, fast.KnowledgeID, true)
		assertEarlySettlementDocument(t, db, slow.KnowledgeID, false)
		assertEarlySettlementHandoff(t, db, fast.KnowledgeID)
		page, err := runner.wikiService.GetPageBySlug(context.Background(), wikiIngestProgressTestKB, fast.Slug)
		require.NoError(t, err)
		require.Equal(t, types.WikiPageStatusPublished, page.Status)
		require.Zero(t, queue.finalizeCount(), "逐文档交接不应在本批尚未结束时触发全局收尾")
		gate.open()
		awaitEarlySettlement(t, done, "完整批次结束")
		require.NoError(t, *result)
		assertEarlySettlementDocument(t, db, slow.KnowledgeID, true)
		require.Equal(t, 1, queue.finalizeCount())
		require.Equal(t, map[string]int{"doc-fast": 1, "doc-slow": 1}, observer.counts)
	})
}

func TestProcessWikiIngestSharedPageDoesNotWaitForOtherDocumentRemainingPage(t *testing.T) {
	forWikiIngestProgressDBs(t, func(t *testing.T, db *gorm.DB) {
		shared := "entity/shared-early"
		slow := wikiIngestProgressUpdate("doc-multiple", "summary/multiple-slow")
		seedEarlySettlementPlans(t, db, []SlugUpdate{
			progressBoundaryEntityUpdate("doc-single", shared),
			progressBoundaryEntityUpdate("doc-multiple", shared), slow,
		})
		gate := newEarlySettlementGate()
		runner, observer, _, _ := newEarlySettlementRunner(t, db,
			map[string]*earlySettlementGate{slow.Slug: gate}, "doc-single", "doc-multiple")
		runner.modelService = progressBoundaryModelService{model: &templateCaptureChatModel{
			response: "SUMMARY: 合成共享页\n# 共享实体\n两份合成文档的共同内容。",
		}}
		_, done, result := startEarlySettlementTask(t, runner, gate)
		awaitEarlySettlement(t, gate.entered, "多页文档的剩余页面")
		awaitEarlySettlement(t, observer.settled["doc-single"], "只依赖共享页的文档")
		assertEarlySettlementDocument(t, db, "doc-single", true)
		assertEarlySettlementDocument(t, db, "doc-multiple", false)
		page, err := runner.wikiService.GetPageBySlug(context.Background(), wikiIngestProgressTestKB, shared)
		require.NoError(t, err)
		require.Contains(t, page.SourceRefs, "doc-single")
		require.Contains(t, page.SourceRefs, "doc-multiple")
		gate.open()
		awaitEarlySettlement(t, done, "共享页面批次结束")
		require.NoError(t, *result)
		assertEarlySettlementDocument(t, db, "doc-multiple", true)
	})
}

func TestProcessWikiIngestSettlesRestoredDocumentWithoutReexecutingCompletedPages(t *testing.T) {
	forWikiIngestProgressDBs(t, func(t *testing.T, db *gorm.DB) {
		donePage := wikiIngestProgressUpdate("doc-restored", "summary/restored")
		slow := wikiIngestProgressUpdate("doc-slow", "summary/restore-slow")
		seedEarlySettlementPlans(t, db, []SlugUpdate{donePage, slow}, donePage.Slug)
		gate := newEarlySettlementGate()
		runner, observer, queue, writes := newEarlySettlementRunner(t, db,
			map[string]*earlySettlementGate{slow.Slug: gate}, donePage.KnowledgeID)
		_, done, result := startEarlySettlementTask(t, runner, gate)
		awaitEarlySettlement(t, observer.settled[donePage.KnowledgeID], "恢复文档提前结算")
		awaitEarlySettlement(t, gate.entered, "尚未完成页面")
		assertEarlySettlementDocument(t, db, donePage.KnowledgeID, true)
		assertEarlySettlementDocument(t, db, slow.KnowledgeID, false)
		writes.mu.Lock()
		created, updated := writes.createCalls[donePage.Slug], writes.updateCalls[donePage.Slug]
		writes.mu.Unlock()
		require.Zero(t, created)
		require.Zero(t, updated)
		require.Zero(t, runner.chunkRepo.(*wikiIngestProgressChunks).calls.Load(), "恢复不能重新读取分块执行 Map")
		require.Zero(t, queue.finalizeCount())
		gate.open()
		awaitEarlySettlement(t, done, "恢复批次结束")
		require.NoError(t, *result)
	})
}

func TestProcessWikiIngestCancellationKeepsUnfinishedDocumentAndSchedulesHandoff(t *testing.T) {
	forWikiIngestProgressDBs(t, func(t *testing.T, db *gorm.DB) {
		fast := wikiIngestProgressUpdate("doc-fast", "summary/cancel-fast")
		slow := wikiIngestProgressUpdate("doc-slow", "summary/cancel-slow")
		seedEarlySettlementPlans(t, db, []SlugUpdate{fast, slow})
		gate := newEarlySettlementGate()
		runner, observer, queue, _ := newEarlySettlementRunner(t, db,
			map[string]*earlySettlementGate{slow.Slug: gate}, fast.KnowledgeID)
		cancel, done, result := startEarlySettlementTask(t, runner, gate)
		awaitEarlySettlement(t, gate.entered, "取消前慢页阻塞")
		awaitEarlySettlement(t, observer.settled[fast.KnowledgeID], "取消前快文档完成")
		cancel()
		awaitEarlySettlement(t, done, "取消后退出")
		if *result != nil {
			require.ErrorIs(t, *result, context.Canceled)
		}
		assertEarlySettlementDocument(t, db, fast.KnowledgeID, true)
		assertEarlySettlementDocument(t, db, slow.KnowledgeID, false)
		assertEarlySettlementHandoff(t, db, fast.KnowledgeID)
		require.Equal(t, 1, queue.finalizeCount(), "取消不能留下无触发器的已持久交接")
		resumed := newWikiIngestProgressService(t, db)
		require.NoError(t, runProgressPublishTask(context.Background(), resumed))
		assertEarlySettlementDocument(t, db, slow.KnowledgeID, true)
		writes := resumed.wikiService.(*wikiIngestProgressPages)
		require.Zero(t, writes.createCalls[fast.Slug])
		require.Zero(t, writes.updateCalls[fast.Slug])
	})
}

func TestProcessWikiIngestEmptyRetryReschedulesDurableFinalizeAfterTriggerFailure(t *testing.T) {
	forWikiIngestProgressDBs(t, func(t *testing.T, db *gorm.DB) {
		update := wikiIngestProgressUpdate("doc-trigger", "summary/trigger")
		seedEarlySettlementPlans(t, db, []SlugUpdate{update}, update.Slug)
		runner, _, queue, _ := newEarlySettlementRunner(t, db, nil, update.KnowledgeID)
		queue.failTrigger = true
		err := runProgressPublishTask(context.Background(), runner)
		require.ErrorContains(t, err, "synthetic finalize trigger failure")
		assertEarlySettlementDocument(t, db, update.KnowledgeID, true)
		assertEarlySettlementHandoff(t, db, update.KnowledgeID)
		require.Equal(t, 1, queue.finalizeCount())
		queue.failTrigger = false
		require.NoError(t, runProgressPublishTask(context.Background(), runner))
		require.Equal(t, 2, queue.finalizeCount(), "即使 ingest 队列已空，重试仍必须补回 finalize 触发器")
		assertEarlySettlementDocument(t, db, update.KnowledgeID, true)
	})
}

func TestProcessWikiIngestFailedPageDoesNotUndoAlreadySettledSibling(t *testing.T) {
	forWikiIngestProgressDBs(t, func(t *testing.T, db *gorm.DB) {
		fast := wikiIngestProgressUpdate("doc-fast", "summary/failure-fast")
		failed := wikiIngestProgressUpdate("doc-failed", "summary/failure-pending")
		seedEarlySettlementPlans(t, db, []SlugUpdate{fast, failed})
		gate := newEarlySettlementGate()
		runner, observer, queue, writes := newEarlySettlementRunner(t, db,
			map[string]*earlySettlementGate{failed.Slug: gate}, fast.KnowledgeID)
		writes.failWriteSlug = failed.Slug
		_, done, result := startEarlySettlementTask(t, runner, gate)
		awaitEarlySettlement(t, gate.entered, "失败页进入受控写入")
		awaitEarlySettlement(t, observer.settled[fast.KnowledgeID], "失败返回前快文档完成")
		gate.open()
		awaitEarlySettlement(t, done, "失败页回到文档重试")
		require.NoError(t, *result)
		assertEarlySettlementDocument(t, db, fast.KnowledgeID, true)
		assertEarlySettlementDocument(t, db, failed.KnowledgeID, false)
		var row types.TaskPendingOp
		require.NoError(t, db.Where("task_type = ? AND dedup_key = ?", wikiTaskType, failed.KnowledgeID).
			First(&row).Error)
		require.Equal(t, 1, row.FailCount, "失败预算只记在尚未成功的文档")
		require.Nil(t, row.ClaimedAt)
		require.Equal(t, 1, queue.finalizeCount())
		resumed := newWikiIngestProgressService(t, db)
		require.NoError(t, runProgressPublishTask(context.Background(), resumed))
		assertEarlySettlementDocument(t, db, failed.KnowledgeID, true)
		resumedWrites := resumed.wikiService.(*wikiIngestProgressPages)
		require.Zero(t, resumedWrites.createCalls[fast.Slug])
		require.Zero(t, resumedWrites.updateCalls[fast.Slug])
		require.Equal(t, 1, resumedWrites.createCalls[failed.Slug])
	})
}

// 已完成文档在慢页等待期间可能开始新一轮编辑；旧批尾不得再次发布同身份的新草稿。
func TestProcessWikiIngestTailDoesNotPublishNewDraftOfAlreadySettledDocument(t *testing.T) {
	forWikiIngestProgressDBs(t, func(t *testing.T, db *gorm.DB) {
		fast := wikiIngestProgressUpdate("doc-fast", "summary/republish-fast")
		slow := wikiIngestProgressUpdate("doc-slow", "summary/republish-slow")
		seedEarlySettlementPlans(t, db, []SlugUpdate{fast, slow})
		gate := newEarlySettlementGate()
		runner, observer, _, _ := newEarlySettlementRunner(t, db,
			map[string]*earlySettlementGate{slow.Slug: gate}, fast.KnowledgeID)
		_, done, result := startEarlySettlementTask(t, runner, gate)
		awaitEarlySettlement(t, gate.entered, "另一文档的慢页")
		awaitEarlySettlement(t, observer.settled[fast.KnowledgeID], "旧文档完成结算")
		page, err := runner.wikiService.GetPageBySlug(context.Background(), wikiIngestProgressTestKB, fast.Slug)
		require.NoError(t, err)
		require.Equal(t, types.WikiPageStatusPublished, page.Status)
		const newerBody = "新一轮尚未发布的合成内容"
		resultUpdate := db.Model(&types.WikiPage{}).Where("id = ?", page.ID).Updates(map[string]interface{}{
			"status": types.WikiPageStatusDraft, "content": newerBody, "version": page.Version + 1,
		})
		require.NoError(t, resultUpdate.Error)
		require.EqualValues(t, 1, resultUpdate.RowsAffected)
		gate.open()
		awaitEarlySettlement(t, done, "旧批次尾部退出")
		require.NoError(t, *result)
		newer, err := runner.wikiService.GetPageBySlug(context.Background(), wikiIngestProgressTestKB, fast.Slug)
		require.NoError(t, err)
		require.Equal(t, page.ID, newer.ID)
		require.Equal(t, page.Version+1, newer.Version)
		require.Equal(t, newerBody, newer.Content)
		require.Equal(t, types.WikiPageStatusDraft, newer.Status, "旧批尾不能再次发布已结算文档的新草稿")
		assertEarlySettlementDocument(t, db, fast.KnowledgeID, true)
		assertEarlySettlementDocument(t, db, slow.KnowledgeID, true)
	})
}

// 模型配置移除后仍可完成纯文本 finalize，恢复触发器不能依赖再次获取生成模型。
func TestProcessWikiIngestEmptyRetryReschedulesFinalizeAfterModelRemoved(t *testing.T) {
	forWikiIngestProgressDBs(t, func(t *testing.T, db *gorm.DB) {
		update := wikiIngestProgressUpdate("doc-removed-model", "summary/removed-model")
		seedEarlySettlementPlans(t, db, []SlugUpdate{update}, update.Slug)
		runner, _, queue, _ := newEarlySettlementRunner(t, db, nil, update.KnowledgeID)
		queue.failTrigger = true
		err := runProgressPublishTask(context.Background(), runner)
		require.ErrorContains(t, err, "synthetic finalize trigger failure")
		assertEarlySettlementDocument(t, db, update.KnowledgeID, true)
		assertEarlySettlementHandoff(t, db, update.KnowledgeID)
		require.Equal(t, 1, queue.finalizeCount())

		kb := runner.kbService.(*wikiLookupKBService).kb
		kb.SummaryModelID = ""
		kb.WikiConfig.SynthesisModelID = ""
		queue.failTrigger = false
		require.NoError(t, runProgressPublishTask(context.Background(), runner))
		require.Equal(t, 2, queue.finalizeCount(), "空 ingest 的恢复不得因模型配置已移除而漏调度")
		assertEarlySettlementDocument(t, db, update.KnowledgeID, true)
	})
}

// 一次发布失败必须留到下一任务恢复；同批无关文档仍可成功，不应被失败结算取消。
func TestProcessWikiIngestPreservesSettlementErrorWithoutBlockingOtherDocument(t *testing.T) {
	for _, restored := range []bool{true, false} {
		name := "fresh-reduce"
		if restored {
			name = "restored-ready"
		}
		t.Run(name, func(t *testing.T) {
			forWikiIngestProgressDBs(t, func(t *testing.T, db *gorm.DB) {
				failed := wikiIngestProgressUpdate("doc-publish-failed", "summary/publish-failed")
				success := wikiIngestProgressUpdate("doc-unrelated-success", "summary/unrelated-success")
				var applied []string
				if restored {
					applied = append(applied, failed.Slug)
				}
				seedEarlySettlementPlans(t, db, []SlugUpdate{failed, success}, applied...)

				want := errors.New("synthetic single publication failure")
				injected := make(chan struct{})
				var once sync.Once
				const callback = "test:early-settlement-publication-error"
				require.NoError(t, db.Callback().Update().Before("gorm:update").Register(callback, func(tx *gorm.DB) {
					if !progressPublishUpdate(tx) {
						return
					}
					// 无关文档在写入门闩中等待，因此第一次发布必定属于待注入故障的文档。
					once.Do(func() {
						_ = tx.AddError(want)
						close(injected)
					})
				}))
				t.Cleanup(func() { require.NoError(t, db.Callback().Update().Remove(callback)) })
				gate := newEarlySettlementGate()
				runner, observer, queue, writes := newEarlySettlementRunner(t, db,
					map[string]*earlySettlementGate{success.Slug: gate}, success.KnowledgeID)
				_, done, result := startEarlySettlementTask(t, runner, gate)
				awaitEarlySettlement(t, gate.entered, "无关文档进入写入门闩")
				awaitEarlySettlement(t, injected, "待恢复文档发布失败")
				gate.open()
				awaitEarlySettlement(t, observer.settled[success.KnowledgeID], "无关文档仍然完成")
				awaitEarlySettlement(t, done, "保留原发布错误后退出本批次")
				require.ErrorIs(t, *result, want)
				assertEarlySettlementDocument(t, db, failed.KnowledgeID, false)
				assertEarlySettlementDocument(t, db, success.KnowledgeID, true)
				assertEarlySettlementHandoff(t, db, success.KnowledgeID)
				require.Equal(t, 1, queue.finalizeCount(), "返回发布错误也必须调度成功文档的持久交接")

				var pending types.TaskPendingOp
				require.NoError(t, db.Where("task_type = ? AND dedup_key = ?", wikiTaskType, failed.KnowledgeID).
					First(&pending).Error)
				require.Nil(t, pending.ClaimedAt, "失败文档必须释放领取供下一任务恢复")
				var receipt types.WikiIngestPageProgress
				require.NoError(t, db.Where("pending_op_id = ? AND slug = ?", pending.ID, failed.Slug).
					First(&receipt).Error)
				require.True(t, receipt.Applied)
				require.True(t, receipt.EffectsDone)
				before, err := runner.wikiService.GetPageBySlug(
					context.Background(), wikiIngestProgressTestKB, failed.Slug,
				)
				require.NoError(t, err)
				require.Equal(t, types.WikiPageStatusDraft, before.Status)
				writes.mu.Lock()
				created, updated := writes.createCalls[failed.Slug], writes.updateCalls[failed.Slug]
				writes.mu.Unlock()
				require.Zero(t, updated)
				if restored {
					require.Zero(t, created, "已完成页面的初始恢复不应重写正文")
				} else {
					require.Equal(t, 1, created, "新页面只应生成一次")
				}

				// 故障只出现一次；新服务直接重试发布，成功文档和已应用页面均不得重新执行。
				restarted := newWikiIngestProgressService(t, db)
				require.NoError(t, runProgressPublishTask(context.Background(), restarted))
				assertProgressPublishSkippedReduce(t, restarted)
				assertEarlySettlementDocument(t, db, failed.KnowledgeID, true)
				assertEarlySettlementDocument(t, db, success.KnowledgeID, true)
				after, err := restarted.wikiService.GetPageBySlug(
					context.Background(), wikiIngestProgressTestKB, failed.Slug,
				)
				require.NoError(t, err)
				require.Equal(t, types.WikiPageStatusPublished, after.Status)
				require.Equal(t, before.ID, after.ID)
				require.Equal(t, before.Version, after.Version)
				require.Equal(t, before.Content, after.Content)
				require.Equal(t, before.SourceRefs, after.SourceRefs)
				require.Equal(t, before.ChunkRefs, after.ChunkRefs)
			})
		})
	}
}
