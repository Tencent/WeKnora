package repository

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// enqueueWikiProgressSelectedDocument 只写合成归属和未领取任务，供指定领取与自动领取共享。
// 同一文档允许多条队列行，避免测试只覆盖单行而遗漏整组领取语义。
func enqueueWikiProgressSelectedDocument(
	t *testing.T, db *gorm.DB, knowledgeID string, count int,
) []*types.TaskPendingOp {
	t.Helper()
	require.NoError(t, db.Exec(
		"INSERT INTO knowledges (id, tenant_id, knowledge_base_id, parse_status, pending_subtasks_count) "+
			"VALUES (?, 1, 'kb', 'finalizing', 2)", knowledgeID,
	).Error)
	rows := make([]*types.TaskPendingOp, 0, count)
	for range count {
		row := &types.TaskPendingOp{
			TenantID: 1, TaskType: types.TypeWikiIngest, Scope: types.TaskScopeKnowledgeBase,
			ScopeID: "kb", DedupKey: knowledgeID, Op: "ingest",
			Payload: json.RawMessage("{\"attempt\":3}"), EnqueuedAt: time.Now(),
		}
		require.NoError(t, db.Create(row).Error)
		rows = append(rows, row)
	}
	return rows
}

// selectedWikiProgressClaim 使用真实领取返回的行构造围栏，不手工假定代次已递增。
func selectedWikiProgressClaim(row *types.TaskPendingOp) types.WikiProgressClaim {
	return types.WikiProgressClaim{
		PendingOpID: row.ID, Epoch: row.ClaimEpoch, TenantID: row.TenantID,
		KnowledgeBaseID: row.ScopeID, KnowledgeID: row.DedupKey, Op: row.Op,
		Attempt: 3, SourceAttempt: 3,
	}
}

func claimSelectedWikiProgressRows(t *testing.T, db *gorm.DB, knowledgeID string) []*types.TaskPendingOp {
	t.Helper()
	claimer, ok := NewTaskPendingOpsRepository(db).(interfaces.TaskPendingOpsKnowledgeClaimer)
	require.True(t, ok)
	rows, err := claimer.ClaimKnowledge(
		context.Background(), types.TypeWikiIngest, types.TaskScopeKnowledgeBase,
		"kb", knowledgeID, time.Now().Add(-time.Hour),
	)
	require.NoError(t, err)
	return rows
}

func TestWikiProgressSelectedClaimKeepsWholeKeyAndEpoch(t *testing.T) {
	forWikiProgressDialects(t, func(t *testing.T, db *gorm.DB) {
		other := enqueueWikiProgressSelectedDocument(t, db, "older-unselected", 1)[0]
		selected := enqueueWikiProgressSelectedDocument(t, db, "selected", 2)
		rows := claimSelectedWikiProgressRows(t, db, "selected")
		require.Len(t, rows, 2)
		require.Equal(t, []int64{selected[0].ID, selected[1].ID}, []int64{rows[0].ID, rows[1].ID})
		for _, row := range rows {
			require.Equal(t, int64(1), row.ClaimEpoch)
			require.NotNil(t, row.ClaimedAt)
			state, err := wikiProgressRepo(t, db).LoadWikiIngestPlan(
				context.Background(), selectedWikiProgressClaim(row),
			)
			require.NoError(t, err, "指定领取必须能进入持久计划路径")
			require.Nil(t, state.Plan)
		}
		require.NoError(t, db.First(other, other.ID).Error)
		require.Nil(t, other.ClaimedAt, "指定文档不能领取优先级更高的其他队列")
		require.Zero(t, other.ClaimEpoch)
	})
}

func TestWikiProgressSelectedFreshClaimBlocksLateSibling(t *testing.T) {
	forWikiProgressDialects(t, func(t *testing.T, db *gorm.DB) {
		enqueueWikiProgressSelectedDocument(t, db, "selected", 1)
		first := claimSelectedWikiProgressRows(t, db, "selected")
		require.Len(t, first, 1)
		late := &types.TaskPendingOp{
			TenantID: 1, TaskType: types.TypeWikiIngest, Scope: types.TaskScopeKnowledgeBase,
			ScopeID: "kb", DedupKey: "selected", Op: "retract",
			Payload: json.RawMessage("{\"attempt\":3}"), EnqueuedAt: time.Now(),
		}
		require.NoError(t, db.Create(late).Error)
		require.Empty(t, claimSelectedWikiProgressRows(t, db, "selected"))
		automatic, err := NewTaskPendingOpsRepository(db).ClaimBatch(
			context.Background(), types.TypeWikiIngest, types.TaskScopeKnowledgeBase,
			"kb", 1, time.Now().Add(-time.Hour),
		)
		require.NoError(t, err)
		require.Empty(t, automatic, "自动领取也不能拆开已有活跃领取的文档")
		require.NoError(t, db.First(late, late.ID).Error)
		require.Nil(t, late.ClaimedAt)
		require.Zero(t, late.ClaimEpoch)

		require.NoError(t, db.Model(&types.TaskPendingOp{}).Where("id = ?", first[0].ID).
			Update("claimed_at", time.Now().Add(-2*time.Hour)).Error)
		reclaimed := claimSelectedWikiProgressRows(t, db, "selected")
		require.Len(t, reclaimed, 2)
		require.Equal(t, first[0].ID, reclaimed[0].ID)
		require.Equal(t, first[0].ClaimEpoch+1, reclaimed[0].ClaimEpoch)
		require.Equal(t, late.ID, reclaimed[1].ID)
		require.Equal(t, int64(1), reclaimed[1].ClaimEpoch)
	})
}

func TestWikiProgressSelectedReclaimPreservesReceiptsAndRejectsOldWriter(t *testing.T) {
	forWikiProgressDialects(t, func(t *testing.T, db *gorm.DB) {
		enqueueWikiProgressSelectedDocument(t, db, "selected", 1)
		first := claimSelectedWikiProgressRows(t, db, "selected")
		require.Len(t, first, 1)
		oldClaim := selectedWikiProgressClaim(first[0])
		saveWikiProgressPlan(t, db, oldClaim, "entity/completed", "entity/pending")
		progress := wikiProgressRepo(t, db)
		require.NoError(t, progress.CompleteWikiPageNoop(
			context.Background(), wikiProgressCommit(oldClaim, "entity/completed"),
		))
		require.NoError(t, db.Model(&types.TaskPendingOp{}).Where("id = ?", oldClaim.PendingOpID).
			Update("claimed_at", time.Now().Add(-2*time.Hour)).Error)
		second := claimSelectedWikiProgressRows(t, db, "selected")
		require.Len(t, second, 1)
		newClaim := selectedWikiProgressClaim(second[0])
		require.Equal(t, oldClaim.Epoch+1, newClaim.Epoch)

		state, err := progress.LoadWikiIngestPlan(context.Background(), newClaim)
		require.NoError(t, err)
		require.NotNil(t, state.Plan)
		require.JSONEq(t, "{\"version\":1,\"summary\":\"synthetic\"}", string(state.Plan.Payload))
		require.Len(t, state.Pages, 2)
		require.Equal(t, "entity/completed", state.Pages[0].Slug)
		require.True(t, state.Pages[0].Applied)
		require.True(t, state.Pages[0].EffectsDone)
		require.False(t, state.Pages[1].Applied)

		// 旧 worker 晚返回不能落页、补成功回执或释放新 worker 的领取。
		oldCommit := wikiProgressCommit(oldClaim, "entity/pending")
		page := wikiProgressPage("entity/pending")
		err = NewWikiPageRepository(db).Create(types.WithWikiPageProgressCommit(
			context.Background(), &oldCommit,
		), page)
		require.ErrorIs(t, err, types.ErrWikiProgressClaimLost)
		var count int64
		require.NoError(t, db.Model(&types.WikiPage{}).Where("id = ?", page.ID).Count(&count).Error)
		require.Zero(t, count)
		require.ErrorIs(t, progress.CompleteWikiPageNoop(
			context.Background(), oldCommit,
		), types.ErrWikiProgressClaimLost)
		_, err = progress.IncrWikiIngestFailCount(context.Background(), oldClaim)
		require.ErrorIs(t, err, types.ErrWikiProgressClaimLost)
		require.NoError(t, progress.ReleaseWikiIngestClaims(context.Background(), []types.WikiProgressClaim{oldClaim}))
		var current types.TaskPendingOp
		require.NoError(t, db.First(&current, oldClaim.PendingOpID).Error)
		require.Equal(t, newClaim.Epoch, current.ClaimEpoch)
		require.NotNil(t, current.ClaimedAt)
		require.Zero(t, current.FailCount)

		require.NoError(t, progress.CompleteWikiPageNoop(
			context.Background(), wikiProgressCommit(newClaim, "entity/pending"),
		))
		state, err = progress.LoadWikiIngestPlan(context.Background(), newClaim)
		require.NoError(t, err)
		require.True(t, state.Pages[0].Applied)
		require.True(t, state.Pages[1].Applied)
	})
}

func TestWikiProgressSelectedAndAutomaticClaimShareFence(t *testing.T) {
	forWikiProgressDialects(t, func(t *testing.T, db *gorm.DB) {
		enqueueWikiProgressSelectedDocument(t, db, "selected", 2)
		queue := NewTaskPendingOpsRepository(db)
		claimer, ok := queue.(interfaces.TaskPendingOpsKnowledgeClaimer)
		require.True(t, ok)
		type result struct {
			rows []*types.TaskPendingOp
			err  error
		}
		results := make(chan result, 2)
		start := make(chan struct{})
		var workers sync.WaitGroup
		workers.Add(2)
		go func() {
			defer workers.Done()
			<-start
			rows, err := claimer.ClaimKnowledge(
				context.Background(), types.TypeWikiIngest, types.TaskScopeKnowledgeBase,
				"kb", "selected", time.Now().Add(-time.Hour),
			)
			results <- result{rows: rows, err: err}
		}()
		go func() {
			defer workers.Done()
			<-start
			rows, err := queue.ClaimBatch(
				context.Background(), types.TypeWikiIngest, types.TaskScopeKnowledgeBase,
				"kb", 1, time.Now().Add(-time.Hour),
			)
			results <- result{rows: rows, err: err}
		}()
		close(start)
		workers.Wait()
		close(results)
		var sizes []int
		for attempt := range results {
			require.NoError(t, attempt.err)
			sizes = append(sizes, len(attempt.rows))
			for _, row := range attempt.rows {
				require.Equal(t, int64(1), row.ClaimEpoch, "并发入口只能有一个取得本轮领取")
				require.NotNil(t, row.ClaimedAt)
			}
		}
		require.ElementsMatch(t, []int{0, 2}, sizes, "整份文档只能由一个入口领取，不能拆分或重复")
	})
}
