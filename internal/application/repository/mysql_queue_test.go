//go:build mysqlintegration

package repository

import (
	"context"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/testutil"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

func TestMySQLQueueAtomicCountersClaimsAndDrain(t *testing.T) {
	db := testutil.MySQL(t)
	r := &taskPendingOpsRepository{db: db}
	ctx := context.Background()
	op := &types.TaskPendingOp{TenantID: 1, TaskType: "wiki", Scope: "knowledge_base", ScopeID: "kb", Op: "ingest", DedupKey: "doc"}
	require.NoError(t, r.Enqueue(ctx, op))
	const workers = 6
	start := make(chan struct{})
	counts := make(chan int, workers)
	errors := make(chan error, workers)
	var group sync.WaitGroup
	for range workers {
		group.Add(1)
		go func() {
			defer group.Done()
			<-start
			count, err := r.IncrFailCount(ctx, op.ID)
			counts <- count
			errors <- err
		}()
	}
	close(start)
	group.Wait()
	close(counts)
	close(errors)
	for err := range errors {
		require.NoError(t, err)
	}
	var values []int
	for count := range counts {
		values = append(values, count)
	}
	sort.Ints(values)
	require.Equal(t, []int{1, 2, 3, 4, 5, 6}, values)
	missing, err := r.IncrFailCount(ctx, -1)
	require.NoError(t, err)
	require.Zero(t, missing)
	require.NoError(t, r.DeleteByIDs(ctx, []int64{op.ID}))
	for _, key := range []string{"a", "a", "b", "b", "c", "c"} {
		require.NoError(t, r.Enqueue(ctx, &types.TaskPendingOp{TenantID: 1, TaskType: "wiki", Scope: "knowledge_base", ScopeID: "kb", Op: "ingest", DedupKey: key}))
	}
	start = make(chan struct{})
	batches := make(chan []*types.TaskPendingOp, 3)
	errors = make(chan error, 3)
	for range 3 {
		group.Add(1)
		go func() {
			defer group.Done()
			<-start
			batch, err := r.ClaimBatch(ctx, "wiki", "knowledge_base", "kb", 1, time.Now().Add(-time.Minute))
			batches <- batch
			errors <- err
		}()
	}
	close(start)
	group.Wait()
	close(batches)
	close(errors)
	for err := range errors {
		require.NoError(t, err)
	}
	owners := map[string]bool{}
	for batch := range batches {
		require.Len(t, batch, 2)
		key := batch[0].DedupKey
		require.False(t, owners[key], "same key claimed twice")
		require.Equal(t, key, batch[1].DedupKey)
		owners[key] = true
	}
	require.Len(t, owners, 3)
	// A new sibling must wait for the already running key's lease.
	require.NoError(t, r.Enqueue(ctx, &types.TaskPendingOp{TenantID: 1, TaskType: "wiki", Scope: "knowledge_base", ScopeID: "kb", Op: "retract", DedupKey: "a"}))
	batch, err := r.ClaimBatch(ctx, "wiki", "knowledge_base", "kb", 10, time.Now().Add(-time.Minute))
	require.NoError(t, err)
	require.Empty(t, batch)
	keys, err := r.DrainUnclaimedAndRelease(ctx, "wiki", "knowledge_base", "kb", "retract", time.Now().Add(-time.Minute))
	require.NoError(t, err)
	require.Empty(t, keys, "fresh sibling claims protect the whole document")
	require.NoError(t, db.Exec("UPDATE task_pending_ops SET claimed_at=NULL").Error)
	keys, err = r.DrainUnclaimedAndRelease(ctx, "wiki", "knowledge_base", "kb", "retract", time.Now().Add(-time.Minute))
	require.NoError(t, err)
	require.Equal(t, []string{"a"}, keys)
}

func TestMySQLQueueDrainRollsBackAndClaimsExpiredLease(t *testing.T) {
	db := testutil.MySQL(t)
	r := &taskPendingOpsRepository{db: db}
	ctx := context.Background()
	require.NoError(t, db.Exec("INSERT INTO knowledges (id,tenant_id,knowledge_base_id,type,title,source,parse_status,pending_subtasks_count) VALUES ('doc',1,'kb','file','doc','','finalizing',1)").Error)
	op := &types.TaskPendingOp{TenantID: 1, TaskType: "wiki", Scope: "knowledge_base", ScopeID: "kb", Op: "retract", DedupKey: "doc"}
	require.NoError(t, r.Enqueue(ctx, op))
	require.NoError(t, db.Exec("CREATE TRIGGER test_finalize_failure BEFORE UPDATE ON knowledges FOR EACH ROW SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='injected finalize failure'").Error)
	_, err := r.DrainUnclaimedAndRelease(ctx, "wiki", "knowledge_base", "kb", "retract", time.Now().Add(-time.Minute))
	require.ErrorContains(t, err, "injected finalize failure")
	var count int64
	require.NoError(t, db.Model(&types.TaskPendingOp{}).Where("id=?", op.ID).Count(&count).Error)
	require.EqualValues(t, 1, count, "delete must roll back when release fails")
	require.NoError(t, db.Exec("DROP TRIGGER test_finalize_failure").Error)
	require.NoError(t, db.Exec("UPDATE task_pending_ops SET claimed_at=? WHERE id=?", time.Now().Add(-time.Hour), op.ID).Error)
	batch, err := r.ClaimBatch(ctx, "wiki", "knowledge_base", "kb", 1, time.Now().Add(-time.Minute))
	require.NoError(t, err)
	require.Len(t, batch, 1)
	keys, err := r.DrainUnclaimedAndRelease(ctx, "wiki", "knowledge_base", "kb", "retract", time.Now().Add(-time.Minute))
	require.NoError(t, err)
	require.Empty(t, keys)
	require.NoError(t, r.ReleaseByIDs(ctx, []int64{op.ID}))
	keys, err = r.DrainUnclaimedAndRelease(ctx, "wiki", "knowledge_base", "kb", "retract", time.Now().Add(-time.Minute))
	require.NoError(t, err)
	require.Equal(t, []string{"doc"}, keys)
	var status string
	require.NoError(t, db.Table("knowledges").Select("parse_status").Where("id='doc'").Scan(&status).Error)
	require.Equal(t, string(types.ParseStatusCompleted), status)
}
