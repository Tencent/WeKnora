package service

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/alicebob/miniredis/v2"
	"github.com/hibiken/asynq"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// 指定文档可能排在自动队列之后，且同一 dedup key 有多条操作；不能按普通批次限额误领其他文档。
func TestWikiSelectedProgressClaimsWholeTargetWithoutOtherDocuments(t *testing.T) {
	forWikiIngestProgressDBs(t, func(t *testing.T, db *gorm.DB) {
		svc := newWikiIngestProgressService(t, db)
		enqueueWikiIngestProgressDoc(t, svc, db, "ordinary")
		enqueueWikiIngestProgressDoc(t, svc, db, "selected")
		retract := WikiPendingOp{Op: WikiOpRetract, KnowledgeID: "selected", DocTitle: "synthetic retraction"}
		raw, err := json.Marshal(retract)
		require.NoError(t, err)
		require.NoError(t, svc.pendingRepo.Enqueue(context.Background(), &types.TaskPendingOp{
			TenantID: 7, TaskType: wikiTaskType, Scope: wikiTaskScope, ScopeID: wikiIngestProgressTestKB,
			Op: retract.Op, DedupKey: retract.KnowledgeID, Payload: raw, EnqueuedAt: time.Now(),
		}))

		ops, ids, batch, err := svc.claimWikiProgressBatch(context.Background(), selectedWikiProgressPayload(), 1)
		require.NoError(t, err)
		require.Len(t, ops, 1)
		require.Equal(t, "selected", ops[0].KnowledgeID)
		require.Equal(t, WikiOpRetract, ops[0].Op, "同文档操作仍按已有最后一条语义去重")
		require.Len(t, ids, 2, "指定文档的全部可领取行必须属于同一次持久领取")
		require.Len(t, batch.claims, 2)
		for _, claim := range batch.claims {
			require.Equal(t, "selected", claim.KnowledgeID)
			require.Positive(t, claim.Epoch)
		}
		assertSelectedWikiOtherPending(t, db)
		require.NoError(t, batch.release(context.Background()))
	})
}

// 缺失或已有活跃领取的目标都不能退回普通批次，否则一次手工触发会处理用户没有选择的文档。
func TestWikiSelectedProgressUnavailableTargetDoesNotFallBack(t *testing.T) {
	for _, state := range []string{"missing", "claimed"} {
		t.Run(state, func(t *testing.T) {
			forWikiIngestProgressDBs(t, func(t *testing.T, db *gorm.DB) {
				svc := newWikiIngestProgressService(t, db)
				enqueueWikiIngestProgressDoc(t, svc, db, "ordinary")
				var claimedID int64
				if state == "claimed" {
					enqueueWikiIngestProgressDoc(t, svc, db, "selected")
					rows, err := svc.pendingRepo.(interfaces.TaskPendingOpsKnowledgeClaimer).ClaimKnowledge(
						context.Background(), wikiTaskType, wikiTaskScope, wikiIngestProgressTestKB,
						"selected", time.Now().Add(-wikiClaimStaleAfter),
					)
					require.NoError(t, err)
					require.Len(t, rows, 1)
					claimedID = rows[0].ID
				}
				ops, ids, _, err := svc.claimWikiProgressBatch(context.Background(), selectedWikiProgressPayload(), 1)
				require.NoError(t, err)
				require.Empty(t, ops)
				require.Empty(t, ids)
				assertSelectedWikiOtherPending(t, db)
				if claimedID != 0 {
					var row types.TaskPendingOp
					require.NoError(t, db.First(&row, claimedID).Error)
					require.NotNil(t, row.ClaimedAt, "空结果的收尾不能释放其他 worker 的领取")
					require.EqualValues(t, 1, row.ClaimEpoch)
				}
			})
		})
	}
}

// 隐去可选接口以模拟尚未支持指定领取的适配器；明确报错，不能悄悄扩大到普通队列。
type selectedWikiBatchOnlyRepository struct {
	interfaces.TaskPendingOpsRepository
}

func TestWikiSelectedProgressMissingClaimerDoesNotClaimOrdinaryWork(t *testing.T) {
	forWikiIngestProgressDBs(t, func(t *testing.T, db *gorm.DB) {
		svc := newWikiIngestProgressService(t, db)
		enqueueWikiIngestProgressDoc(t, svc, db, "ordinary")
		svc.pendingRepo = selectedWikiBatchOnlyRepository{TaskPendingOpsRepository: svc.pendingRepo}
		_, _, _, err := svc.claimWikiProgressBatch(context.Background(), selectedWikiProgressPayload(), 1)
		require.Error(t, err)
		assertSelectedWikiOtherPending(t, db)
	})
}

type selectedWikiProgressFixture struct {
	pendingID int64
	frozen    []byte
	first     SlugUpdate
	second    SlugUpdate
	saved     *types.WikiPage
}

func selectedWikiProgressPayload() WikiIngestPayload {
	return WikiIngestPayload{TenantID: 7, KnowledgeBaseID: wikiIngestProgressTestKB, KnowledgeID: "selected"}
}

// 合成冻结计划只准备持久恢复输入；后续执行真实队列、页面仓储、收尾与结算，不调用外部模型。
func seedSelectedWikiProgress(t *testing.T, db *gorm.DB) selectedWikiProgressFixture {
	t.Helper()
	seed := newWikiIngestProgressService(t, db)
	enqueueWikiIngestProgressDoc(t, seed, db, "selected")
	require.NoError(t, db.Exec("UPDATE knowledges SET pending_subtasks_count = 1 WHERE id = ?", "selected").Error)
	ops, ids, batch := claimWikiIngestProgress(t, seed, 1)
	first := wikiIngestProgressUpdate("selected", "summary/selected-success")
	second := wikiIngestProgressUpdate("selected", "summary/selected-pending")
	pending := prepareWikiIngestProgress(t, seed, batch, ops, first, second)
	applyWikiIngestProgress(t, seed, batch, first.Slug, pending[first.Slug])
	saved, err := seed.wikiService.GetPageBySlug(context.Background(), wikiIngestProgressTestKB, first.Slug)
	require.NoError(t, err)
	frozen := append([]byte(nil), batch.states[ops[0].dbID].Plan.Payload...)
	require.NoError(t, batch.release(context.Background()))
	enqueueWikiIngestProgressDoc(t, seed, db, "ordinary")
	return selectedWikiProgressFixture{pendingID: ids[0], frozen: frozen, first: first, second: second, saved: saved}
}

func newSelectedWikiProgressRunner(t *testing.T, db *gorm.DB, mode string) *wikiIngestService {
	t.Helper()
	svc := newWikiIngestProgressService(t, db)
	if mode == "redis" {
		server := miniredis.RunT(t)
		svc.redisClient = redis.NewClient(&redis.Options{Addr: server.Addr()})
		t.Cleanup(func() { require.NoError(t, svc.redisClient.Close()) })
	}
	return svc
}

func runSelectedWikiProgress(ctx context.Context, t *testing.T, svc *wikiIngestService) error {
	t.Helper()
	raw, err := json.Marshal(selectedWikiProgressPayload())
	require.NoError(t, err)
	return svc.ProcessWikiIngest(ctx, asynq.NewTask(types.TypeWikiIngest, raw))
}

func assertSelectedWikiOtherPending(t *testing.T, db *gorm.DB) {
	t.Helper()
	var ordinary types.TaskPendingOp
	require.NoError(t, db.Where("task_type = ? AND dedup_key = ?", wikiTaskType, "ordinary").First(&ordinary).Error)
	require.Nil(t, ordinary.ClaimedAt)
	require.Zero(t, ordinary.ClaimEpoch)
	require.Zero(t, ordinary.FailCount)
}

func assertSelectedWikiSuccessfulPageUnchanged(
	t *testing.T, svc *wikiIngestService, fixture selectedWikiProgressFixture,
) {
	t.Helper()
	writes := svc.wikiService.(*wikiIngestProgressPages)
	require.Zero(t, writes.createCalls[fixture.first.Slug])
	require.Zero(t, writes.updateCalls[fixture.first.Slug])
	require.Zero(t, svc.chunkRepo.(*wikiIngestProgressChunks).calls.Load(), "冻结计划恢复不能再次执行 Map")
	page, err := svc.wikiService.GetPageBySlug(context.Background(), wikiIngestProgressTestKB, fixture.first.Slug)
	require.NoError(t, err)
	require.Equal(t, fixture.saved.ID, page.ID)
	require.Equal(t, fixture.saved.Version, page.Version)
	require.Equal(t, fixture.saved.Content, page.Content)
}

func assertSelectedWikiOrdinaryFollowUp(t *testing.T, svc *wikiIngestService) {
	t.Helper()
	count := 0
	for _, task := range svc.task.(*wikiGuardTaskQueue).tasks {
		if task.Type() != types.TypeWikiIngest {
			continue
		}
		count++
		var payload WikiIngestPayload
		require.NoError(t, json.Unmarshal(task.Payload(), &payload))
		require.Empty(t, payload.KnowledgeID, "后续批次必须回到 KB 队列，不能再次绑定已完成的目标")
	}
	require.Positive(t, count, "仍有普通文档排队时必须实际产生后续任务，避免空循环断言")
}

// Lite 和 Redis 调度都必须使用持久计划，并在成功后把未选择文档留给普通批次。
func TestProcessWikiSelectedProgressResumesOnlyTargetDocument(t *testing.T) {
	for _, mode := range []string{"lite", "redis"} {
		t.Run(mode, func(t *testing.T) {
			forWikiIngestProgressDBs(t, func(t *testing.T, db *gorm.DB) {
				fixture := seedSelectedWikiProgress(t, db)
				runner := newSelectedWikiProgressRunner(t, db, mode)
				require.NoError(t, runSelectedWikiProgress(context.Background(), t, runner))
				assertEarlySettlementDocument(t, db, "selected", true)
				assertSelectedWikiOtherPending(t, db)
				assertSelectedWikiSuccessfulPageUnchanged(t, runner, fixture)
				require.Equal(t, 1, runner.wikiService.(*wikiIngestProgressPages).createCalls[fixture.second.Slug])
				assertSelectedWikiOrdinaryFollowUp(t, runner)
			})
		})
	}
}

// 指定文档的失败页保留原计划和回执；新的 service 只续跑未完成页，不触碰其他排队文档。
func TestProcessWikiSelectedProgressFailedWriteResumesFrozenPlan(t *testing.T) {
	for _, mode := range []string{"lite", "redis"} {
		t.Run(mode, func(t *testing.T) {
			forWikiIngestProgressDBs(t, func(t *testing.T, db *gorm.DB) {
				fixture := seedSelectedWikiProgress(t, db)
				runner := newSelectedWikiProgressRunner(t, db, mode)
				runner.wikiService.(*wikiIngestProgressPages).failWriteSlug = fixture.second.Slug
				require.NoError(t, runSelectedWikiProgress(context.Background(), t, runner))
				var row types.TaskPendingOp
				require.NoError(t, db.First(&row, fixture.pendingID).Error)
				require.Nil(t, row.ClaimedAt)
				require.Equal(t, 1, row.FailCount)
				require.Positive(t, row.ClaimEpoch)
				var plan types.WikiIngestPlan
				require.NoError(t, db.First(&plan, "pending_op_id = ?", fixture.pendingID).Error)
				require.Equal(t, fixture.frozen, []byte(plan.Payload))
				var receipts []types.WikiIngestPageProgress
				require.NoError(t, db.Where("pending_op_id = ?", fixture.pendingID).Find(&receipts).Error)
				require.Len(t, receipts, 2)
				for _, receipt := range receipts {
					require.Equal(t, receipt.Slug == fixture.first.Slug, receipt.Applied)
					require.Equal(t, receipt.Slug == fixture.first.Slug, receipt.EffectsDone)
				}
				assertSelectedWikiOtherPending(t, db)
				assertSelectedWikiSuccessfulPageUnchanged(t, runner, fixture)

				restarted := newSelectedWikiProgressRunner(t, db, mode)
				require.NoError(t, runSelectedWikiProgress(context.Background(), t, restarted))
				assertEarlySettlementDocument(t, db, "selected", true)
				assertSelectedWikiOtherPending(t, db)
				assertSelectedWikiSuccessfulPageUnchanged(t, restarted, fixture)
				require.Equal(t, 1, restarted.wikiService.(*wikiIngestProgressPages).createCalls[fixture.second.Slug])
				assertSelectedWikiOrdinaryFollowUp(t, restarted)
			})
		})
	}
}

// 加载阶段注入旧 worker 取消；领取与再领取仍走真实仓储，以验证失败清理的代次隔离。
type selectedWikiProgressLoadInterceptor struct {
	interfaces.WikiIngestProgressRepository
	load func(context.Context, types.WikiProgressClaim) (*types.WikiIngestPlanState, error)
}

func (r *selectedWikiProgressLoadInterceptor) LoadWikiIngestPlan(
	ctx context.Context, claim types.WikiProgressClaim,
) (*types.WikiIngestPlanState, error) {
	return r.load(ctx, claim)
}

func TestWikiSelectedProgressCanceledLoadKeepsNewerClaim(t *testing.T) {
	forWikiIngestProgressDBs(t, func(t *testing.T, db *gorm.DB) {
		svc := newWikiIngestProgressService(t, db)
		enqueueWikiIngestProgressDoc(t, svc, db, "ordinary")
		enqueueWikiIngestProgressDoc(t, svc, db, "selected")
		actual := svc.progressRepo
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		var newer *types.TaskPendingOp
		svc.progressRepo = &selectedWikiProgressLoadInterceptor{
			WikiIngestProgressRepository: actual,
			load: func(_ context.Context, claim types.WikiProgressClaim) (*types.WikiIngestPlanState, error) {
				require.Equal(t, "selected", claim.KnowledgeID)
				require.NoError(t, actual.ReleaseWikiIngestClaims(
					context.Background(), []types.WikiProgressClaim{claim},
				))
				rows, err := svc.pendingRepo.(interfaces.TaskPendingOpsKnowledgeClaimer).ClaimKnowledge(
					context.Background(), wikiTaskType, wikiTaskScope, wikiIngestProgressTestKB,
					"selected", time.Now().Add(-wikiClaimStaleAfter),
				)
				require.NoError(t, err)
				require.Len(t, rows, 1)
				newer = rows[0]
				require.Greater(t, newer.ClaimEpoch, claim.Epoch)
				cancel()
				return nil, context.Canceled
			},
		}
		_, _, _, err := svc.claimWikiProgressBatch(ctx, selectedWikiProgressPayload(), 1)
		require.ErrorIs(t, err, context.Canceled)
		require.NotNil(t, newer)
		var row types.TaskPendingOp
		require.NoError(t, db.First(&row, newer.ID).Error)
		require.Equal(t, newer.ClaimEpoch, row.ClaimEpoch)
		require.NotNil(t, row.ClaimedAt, "旧领取失败清理不得释放新的领取")
		assertSelectedWikiOtherPending(t, db)
	})
}
