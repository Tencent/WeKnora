package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	apprepo "github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/alicebob/miniredis/v2"
	"github.com/hibiken/asynq"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

type finalizeDeadlineRepo struct {
	interfaces.TaskPendingOpsRepository
	calls    int
	deadline time.Time
}

func (r *finalizeDeadlineRepo) EnqueueIfKnowledgeBaseActive(ctx context.Context, _ *types.TaskPendingOp) (bool, error) {
	r.calls++
	r.deadline, _ = ctx.Deadline()
	return true, ctx.Err()
}

func TestWikiFinalizeEnqueueHasIndependentBudget(t *testing.T) {
	repo := &finalizeDeadlineRepo{}
	queue := &wikiGuardTaskQueue{}
	svc := &wikiIngestService{pendingRepo: repo, task: queue}
	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()
	failed, err := svc.enqueueFinalize(ctx, WikiIngestPayload{TenantID: 7, KnowledgeBaseID: "kb-1"},
		[]string{"entity/a"}, nil, nil, nil)
	require.NoError(t, err)
	require.Zero(t, failed)
	require.Equal(t, 1, repo.calls)
	require.True(t, time.Until(repo.deadline) > wikiIngestCleanupTimeout,
		"finalize persistence needs a fresh budget, independent of an expired cleanup context")
	require.Len(t, queue.tasks, 1)
}

func TestHousekeepingRearmsFinalizeForCompletedDocuments(t *testing.T) {
	db := setupHousekeepingDB(t)
	require.NoError(t, db.AutoMigrate(&types.TaskPendingOp{}))
	require.NoError(t, db.Exec(`CREATE TABLE tenants (id INTEGER PRIMARY KEY, deleted_at DATETIME)`).Error)
	require.NoError(t, db.Exec(`INSERT INTO tenants (id) VALUES (1)`).Error)
	require.NoError(t, db.Exec(`CREATE TABLE knowledge_bases
 (id TEXT PRIMARY KEY, tenant_id INTEGER, deleted_at DATETIME)`).Error)
	require.NoError(t, db.Exec(`INSERT INTO knowledge_bases (id, tenant_id) VALUES ('kb-done',1)`).Error)

	require.NoError(t, db.Exec(`INSERT INTO tenants (id,deleted_at) VALUES (2,CURRENT_TIMESTAMP)`).Error)
	require.NoError(t, db.Exec(`INSERT INTO knowledge_bases (id,tenant_id,deleted_at) VALUES
 ('kb-recent',1,NULL), ('kb-deleted',1,CURRENT_TIMESTAMP), ('kb-tenant-deleted',2,NULL), ('kb-mismatch',2,NULL)`).Error)
	for _, scope := range []struct {
		id     string
		tenant uint64
		recent bool
	}{
		{"kb-done", 1, false},
		{"kb-recent", 1, true},
		{"kb-deleted", 1, false},
		{"kb-tenant-deleted", 2, false},
		{"kb-mismatch", 1, false},
		{"kb-missing", 1, false},
	} {
		enqueuedAt := time.Now().Add(-3 * time.Hour)
		if scope.recent {
			enqueuedAt = time.Now()
		}
		for range 2 { // Multiple rows in the same lane must produce one trigger.
			require.NoError(t, db.Create(&types.TaskPendingOp{
				TenantID: scope.tenant, TaskType: types.TypeWikiFinalize,
				Scope: types.TaskScopeKnowledgeBase, ScopeID: scope.id, Op: wikiFinalizeOpSlug, DedupKey: "entity/a",
				Payload: []byte(`{"slug":"entity/a"}`), EnqueuedAt: enqueuedAt,
			}).Error)
		}
	}

	queue := &wikiGuardTaskQueue{}
	h := NewHousekeepingService(db, nil, nil, queue)
	h.runSweep(context.Background())
	require.Len(t, queue.tasks, 1, "durable finalize work must wake even when there are no stuck documents")
	require.Equal(t, types.TypeWikiFinalize, queue.tasks[0].Type())
	var payload WikiIngestPayload
	require.NoError(t, json.Unmarshal(queue.tasks[0].Payload(), &payload))
	require.Equal(t, "kb-done", payload.KnowledgeBaseID)
	require.EqualValues(t, 1, payload.TenantID)
}

func TestWikiIngestRetainsSourceAfterFinalizePersistenceFailure(t *testing.T) {
	for _, redisMode := range []bool{false, true} {
		t.Run(map[bool]string{false: "lite", true: "redis"}[redisMode], func(t *testing.T) {
			var rdb *redis.Client
			if redisMode {
				mr := miniredis.RunT(t)
				rdb = redis.NewClient(&redis.Options{Addr: mr.Addr()})
				t.Cleanup(func() { _ = rdb.Close() })
			}
			svc, db := newWikiLookupRecoveryService(t, "kb-1", rdb)
			require.NoError(t, db.Exec(`CREATE TABLE knowledge_bases
 (id TEXT PRIMARY KEY, tenant_id INTEGER, deleted_at DATETIME)`).Error)
			require.NoError(t, db.Exec(`INSERT INTO knowledge_bases(id,tenant_id) VALUES ('kb-1',7)`).Error)
			// A retract with no remaining pages still needs a durable index change.
			b, err := json.Marshal(WikiPendingOp{Op: WikiOpRetract, DocTitle: "Removed document"})
			require.NoError(t, err)
			source := &types.TaskPendingOp{
				TenantID: 7, TaskType: wikiTaskType, Scope: wikiTaskScope, ScopeID: "kb-1",
				Op: WikiOpRetract, Payload: b,
			}
			require.NoError(t, svc.pendingRepo.Enqueue(context.Background(), source))
			require.NoError(t, db.Exec(`CREATE TRIGGER fail_finalize BEFORE INSERT ON task_pending_ops
    WHEN NEW.task_type = 'wiki:finalize' BEGIN SELECT RAISE(ABORT, 'injected finalize failure'); END`).Error)
			b, err = json.Marshal(WikiIngestPayload{TenantID: 7, KnowledgeBaseID: "kb-1"})
			require.NoError(t, err)
			task := asynq.NewTask(wikiTaskType, b)
			err = svc.ProcessWikiIngest(context.Background(), task)
			require.ErrorContains(t, err, "injected finalize failure")
			var retained types.TaskPendingOp
			require.NoError(t, db.First(&retained, source.ID).Error)
			require.Nil(t, retained.ClaimedAt, "failed batches must release their claim for retry")
			require.Empty(t, svc.task.(*wikiGuardTaskQueue).tasks, "never schedule finalize before its rows commit")
			require.NoError(t, db.Exec("DROP TRIGGER fail_finalize").Error)
			require.NoError(t, svc.ProcessWikiIngest(context.Background(), task))
			count, err := svc.pendingRepo.PendingCount(context.Background(), wikiTaskType, wikiTaskScope, "kb-1")
			require.NoError(t, err)
			require.Zero(t, count, "only a successful handoff may trim source work")
			rows, err := apprepo.NewTaskPendingOpsRepository(db).PeekBatch(
				context.Background(), wikiFinalizeTaskType, wikiTaskScope, "kb-1", 10)
			require.NoError(t, err)
			require.Len(t, rows, 1)
			require.Equal(t, wikiFinalizeOpChange, rows[0].Op)
		})
	}
}

type finalizeBatchRepo struct {
	interfaces.TaskPendingOpsRepository
	accepted bool
	err      error
	calls    int
	rows     []*types.TaskPendingOp
	budget   time.Duration
}

func (r *finalizeBatchRepo) EnqueueBatchIfKnowledgeBaseActive(
	ctx context.Context, rows []*types.TaskPendingOp,
) (bool, error) {
	r.calls++
	r.rows = rows
	deadline, _ := ctx.Deadline()
	r.budget = time.Until(deadline)
	return r.accepted, r.err
}

func TestWikiFinalizeLargeBatchUsesOneGuardedHandoff(t *testing.T) {
	for _, mode := range []string{"accepted", "deleted", "failed"} {
		t.Run(mode, func(t *testing.T) {
			repo := &finalizeBatchRepo{accepted: mode == "accepted"}
			if mode == "failed" {
				repo.err = errors.New("database unavailable")
			}
			queue := &wikiGuardTaskQueue{}
			svc := &wikiIngestService{pendingRepo: repo, task: queue}
			slugs := make([]string, 1250)
			for i := range slugs {
				slugs[i] = fmt.Sprintf("entity/%d", i)
			}
			failed, err := svc.enqueueFinalize(
				context.Background(), WikiIngestPayload{TenantID: 7, KnowledgeBaseID: "kb-1"},
				slugs, nil, []wikiFinalizeChange{{Action: wikiFinalizeAdded}}, []string{"folder-1", "folder-1"})
			require.Equal(t, 1, repo.calls)
			require.Len(t, repo.rows, 1252)
			require.Greater(t, repo.budget, time.Minute)
			require.LessOrEqual(t, repo.budget, 5*time.Minute)
			var prune wikiFinalizeRow
			require.NoError(t, json.Unmarshal(repo.rows[1251].Payload, &prune))
			require.Equal(t, []string{"folder-1"}, prune.FolderIDs)
			if repo.err != nil {
				require.ErrorIs(t, err, repo.err)
				require.Equal(t, 1252, failed)
			} else {
				require.NoError(t, err)
				require.Zero(t, failed)
			}
			if repo.accepted {
				require.Len(t, queue.tasks, 1)
			} else {
				require.Empty(t, queue.tasks)
			}
		})
	}
}
