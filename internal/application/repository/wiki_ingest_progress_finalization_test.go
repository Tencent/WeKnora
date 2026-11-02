package repository

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// prepareWikiFinalSlot 保存已完成的页面回执，计数用于区分最后一个子任务和并行子任务。
func prepareWikiFinalSlot(t *testing.T, db *gorm.DB, count int) types.WikiProgressClaim {
	t.Helper()
	claim := seedWikiProgressClaim(t, db, "final-slot-doc")
	require.NoError(t, db.Table("knowledges").Where("id = ?", claim.KnowledgeID).
		Update("pending_subtasks_count", count).Error)
	saveWikiProgressPlan(t, db, claim, "entity/final-slot")
	require.NoError(t, wikiProgressRepo(t, db).CompleteWikiPageNoop(
		context.Background(), wikiProgressCommit(claim, "entity/final-slot"),
	))
	return claim
}

func assertWikiFinalSlot(
	t *testing.T, db *gorm.DB, claim types.WikiProgressClaim, count int, status string, queued int64,
) {
	t.Helper()
	var state struct {
		ParseStatus          string
		PendingSubtasksCount int
	}
	require.NoError(t, db.Table("knowledges").Where("id = ?", claim.KnowledgeID).Take(&state).Error)
	require.Equal(t, count, state.PendingSubtasksCount)
	require.Equal(t, status, state.ParseStatus)
	var rows int64
	require.NoError(t, db.Model(&types.TaskPendingOp{}).Where("id = ?", claim.PendingOpID).Count(&rows).Error)
	require.Equal(t, queued, rows)
}

// TestWikiProgressSettlementPromotesFinalSlot 同时检查计数和终态，不能把队列清空视为文档成功。
func TestWikiProgressSettlementPromotesFinalSlot(t *testing.T) {
	for _, discard := range []bool{false, true} {
		name := "completed_pages"
		if discard {
			name = "terminal_discard"
		}
		t.Run(name, func(t *testing.T) {
			forWikiProgressDialects(t, func(t *testing.T, db *gorm.DB) {
				claim := prepareWikiFinalSlot(t, db, 1)
				settled, err := wikiProgressRepo(t, db).SettleWikiIngestClaim(
					context.Background(), claim, true, discard,
				)
				require.NoError(t, err)
				require.True(t, settled)
				assertWikiFinalSlot(t, db, claim, 0, types.ParseStatusCompleted, 0)
			})
		})
	}
}

func TestWikiProgressSettlementPreservesOtherSubtasks(t *testing.T) {
	forWikiProgressDialects(t, func(t *testing.T, db *gorm.DB) {
		claim := prepareWikiFinalSlot(t, db, 2)
		settled, err := wikiProgressRepo(t, db).SettleWikiIngestClaim(context.Background(), claim, true, false)
		require.NoError(t, err)
		require.True(t, settled)
		assertWikiFinalSlot(t, db, claim, 1, types.ParseStatusFinalizing, 0)
	})
}

func TestWikiProgressSettlementConcurrentFinalSlotOnce(t *testing.T) {
	forWikiProgressDialects(t, func(t *testing.T, db *gorm.DB) {
		claim := prepareWikiFinalSlot(t, db, 1)
		repo := wikiProgressRepo(t, db)
		type result struct {
			settled bool
			err     error
		}
		const workers = 8
		results := make(chan result, workers)
		start := make(chan struct{})
		var wg sync.WaitGroup
		for range workers {
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-start
				settled, err := repo.SettleWikiIngestClaim(context.Background(), claim, true, false)
				results <- result{settled: settled, err: err}
			}()
		}
		close(start)
		wg.Wait()
		close(results)
		winners := 0
		for got := range results {
			require.NoError(t, got.err)
			if got.settled {
				winners++
			}
		}
		require.Equal(t, 1, winners)
		assertWikiFinalSlot(t, db, claim, 0, types.ParseStatusCompleted, 0)
		settled, err := repo.SettleWikiIngestClaim(context.Background(), claim, true, false)
		require.NoError(t, err)
		require.False(t, settled)
		assertWikiFinalSlot(t, db, claim, 0, types.ParseStatusCompleted, 0)
	})
}

// TestWikiProgressSettlementPromotionFailureRollsBack 在第二条 UPDATE 注入故障，验证扣减与消费一起回滚。
func TestWikiProgressSettlementPromotionFailureRollsBack(t *testing.T) {
	forWikiProgressDialects(t, func(t *testing.T, db *gorm.DB) {
		claim := prepareWikiFinalSlot(t, db, 1)
		repo := wikiProgressRepo(t, db)
		const callback = "wiki_final_slot_promotion_failure"
		require.NoError(t, db.Callback().Update().Before("gorm:update").Register(callback, func(tx *gorm.DB) {
			values, ok := tx.Statement.Dest.(map[string]interface{})
			if ok && values["parse_status"] == types.ParseStatusCompleted {
				_ = tx.AddError(errors.New("synthetic promotion failure"))
			}
		}))
		t.Cleanup(func() { _ = db.Callback().Update().Remove(callback) })
		settled, err := repo.SettleWikiIngestClaim(context.Background(), claim, true, false)
		require.ErrorContains(t, err, "synthetic promotion failure")
		require.False(t, settled)
		assertWikiFinalSlot(t, db, claim, 1, types.ParseStatusFinalizing, 1)
		state, err := repo.LoadWikiIngestPlan(context.Background(), claim)
		require.NoError(t, err)
		require.Len(t, state.Pages, 1)
		require.True(t, state.Pages[0].Applied && state.Pages[0].EffectsDone)

		require.NoError(t, db.Callback().Update().Remove(callback))
		settled, err = repo.SettleWikiIngestClaim(context.Background(), claim, true, false)
		require.NoError(t, err)
		require.True(t, settled)
		assertWikiFinalSlot(t, db, claim, 0, types.ParseStatusCompleted, 0)
	})
}
