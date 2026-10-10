package service

import (
	"context"
	"fmt"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/alicebob/miniredis/v2"
	"github.com/hibiken/asynq"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Each worker repeatedly interrupts a full batch after claiming it, then
// recovers the same rows. Redis is shared; each KB has a separate in-memory
// SQLite database because this tests handler recovery, not PostgreSQL locking.
func TestWikiInterruptedLookupRecoveryStress(t *testing.T) {
	const workers, rounds, batchSize = 16, 10, 5
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	for worker := range workers {
		t.Run(fmt.Sprintf("kb-%d", worker), func(t *testing.T) {
			t.Parallel()
			kbID := fmt.Sprintf("kb-%d", worker)
			svc, db := newWikiLookupRecoveryService(t, kbID, rdb)
			lookup := svc.knowledgeSvc.(*wikiKnowledgeLookupStub)
			for round := range rounds {
				var task *asynq.Task
				for doc := range batchSize {
					id := fmt.Sprintf("k-%d-%d-%d", worker, round, doc)
					task = seedWikiLookupTask(t, svc, db, kbID, id)
				}
				ctx, cancel := context.WithCancel(context.Background())
				lookup.lookup = func(ctx context.Context, id string) (*types.Knowledge, error) {
					cancel()
					return svc.knowledgeRepo.GetKnowledgeByIDOnly(ctx, id)
				}
				err := svc.ProcessWikiIngest(ctx, task)
				cancel()
				require.NoError(t, err)
				var ops []types.TaskPendingOp
				require.NoError(t, db.Find(&ops).Error)
				require.Len(t, ops, batchSize, "every interrupted op must remain retryable")
				for _, op := range ops {
					require.Nil(t, op.ClaimedAt)
					require.Equal(t, 1, op.FailCount)
				}
				require.Zero(t, rdb.ZCard(context.Background(), wikiInflightPrefix+kbID).Val())

				lookup.lookup = svc.knowledgeRepo.GetKnowledgeByIDOnly
				require.NoError(t, svc.ProcessWikiIngest(context.Background(), task))
				count, err := svc.pendingRepo.PendingCount(context.Background(), wikiTaskType, wikiTaskScope, kbID)
				require.NoError(t, err)
				require.Zero(t, count, "healthy follow-up must drain the preserved work")
				require.Zero(t, rdb.ZCard(context.Background(), wikiInflightPrefix+kbID).Val())
			}
			var completed int64
			require.NoError(t, db.Model(&types.Knowledge{}).
				Where("parse_status = ? AND pending_subtasks_count = 0", types.ParseStatusCompleted).
				Count(&completed).Error)
			assert.EqualValues(t, rounds*batchSize, completed)
			assert.Len(t, svc.task.(*wikiGuardTaskQueue).tasks, rounds)
		})
	}
}
