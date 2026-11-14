package service

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/Tencent/WeKnora/internal/utils"
	"github.com/alicebob/miniredis/v2"
	"github.com/hibiken/asynq"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

type faqAdmissionTenant struct{ interfaces.TenantRepository }

func (faqAdmissionTenant) GetTenantByID(_ context.Context, id uint64) (*types.Tenant, error) {
	return &types.Tenant{ID: id}, nil
}

type faqAdmissionQueue struct{ tasks []*asynq.Task }

func (q *faqAdmissionQueue) Enqueue(task *asynq.Task, _ ...asynq.Option) (*asynq.TaskInfo, error) {
	var payload types.FAQImportPayload
	if err := json.Unmarshal(task.Payload(), &payload); err != nil {
		return nil, err
	}
	q.tasks = append(q.tasks, task)
	return &asynq.TaskInfo{ID: payload.TaskID + ":" + payload.InstanceID, Queue: types.QueueMaintenance}, nil
}

type faqAdmissionFixture struct {
	*faqWriteFixture
	mini  *miniredis.Miniredis
	queue *faqAdmissionQueue
}

func newFAQAdmissionFixture(t *testing.T) *faqAdmissionFixture {
	t.Helper()
	f := newFAQWriteFixture(t)
	sqlDB, err := f.db.DB()
	require.NoError(t, err)
	t.Cleanup(func() { _ = sqlDB.Close() })
	mini := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mini.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	f.svc.redisClient = rdb
	f.svc.tenantRepo = faqAdmissionTenant{}
	queue := &faqAdmissionQueue{}
	f.svc.task = queue
	return &faqAdmissionFixture{faqWriteFixture: f, mini: mini, queue: queue}
}

func (f *faqAdmissionFixture) enqueue(t *testing.T, taskID string, count int) *asynq.Task {
	t.Helper()
	entries := make([]types.FAQEntryPayload, count)
	for i := range entries {
		entries[i] = types.FAQEntryPayload{
			StandardQuestion: fmt.Sprintf("How do I complete step %d?", i+1),
			Answers:          []string{fmt.Sprintf("Follow step %d.", i+1)},
		}
	}
	got, err := f.svc.UpsertFAQEntries(f.ctx, f.kb.ID, &types.FAQBatchUpsertPayload{
		TaskID: taskID, Mode: types.FAQBatchModeAppend, DryRun: true, Entries: entries,
	})
	require.NoError(t, err)
	require.Equal(t, taskID, got)
	require.NotEmpty(t, f.queue.tasks)
	return f.queue.tasks[len(f.queue.tasks)-1]
}

func (f *faqAdmissionFixture) prepareReplacement(t *testing.T, reuseTaskID bool) (*asynq.Task, string) {
	t.Helper()
	taskID := utils.GenerateTaskID("faq_import", f.kb.TenantID, f.kb.ID)
	old := f.enqueue(t, taskID, 1)
	// Keep the original queued payload while its three-hour marker/progress
	// expires. A later Upsert creates a distinct import instance for this KB.
	f.mini.FastForward(faqImportProgressTTL + time.Second)
	if !reuseTaskID {
		taskID = utils.GenerateTaskID("faq_import", f.kb.TenantID, f.kb.ID)
	}
	replacement := f.enqueue(t, taskID, 2)
	var a, b types.FAQImportPayload
	require.NoError(t, json.Unmarshal(old.Payload(), &a))
	require.NoError(t, json.Unmarshal(replacement.Payload(), &b))
	require.NotEmpty(t, a.InstanceID)
	require.NotEmpty(t, b.InstanceID)
	require.NotEqual(t, a.InstanceID, b.InstanceID)
	if reuseTaskID {
		require.Equal(t, a.TaskID, b.TaskID)
	} else {
		require.NotEqual(t, a.TaskID, b.TaskID)
	}
	return old, taskID
}

func TestFAQImportRejectsSupersededInstance(t *testing.T) {
	for _, tc := range []struct {
		name  string
		reuse bool
	}{
		{name: "same_task_id", reuse: true},
		{name: "different_task_id"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFAQAdmissionFixture(t)
			old, taskID := f.prepareReplacement(t, tc.reuse)
			before, err := f.svc.GetFAQImportProgress(f.ctx, taskID)
			require.NoError(t, err)
			require.Equal(t, types.FAQImportStatusPending, before.Status)
			require.Equal(t, 2, before.Total)
			markerBefore, err := f.svc.getRunningFAQImportInfo(f.ctx, f.kb.ID)
			require.NoError(t, err)
			require.NotNil(t, markerBefore)

			processErr := f.svc.ProcessFAQImport(f.ctx, old)

			after, err := f.svc.GetFAQImportProgress(f.ctx, taskID)
			require.NoError(t, err)
			require.Equal(t, before, after)
			markerAfter, err := f.svc.getRunningFAQImportInfo(f.ctx, f.kb.ID)
			require.NoError(t, err)
			require.Equal(t, markerBefore, markerAfter)
			require.ErrorIs(t, processErr, asynq.SkipRetry)
			require.Zero(t, f.chunks.writes)
		})
	}
}

func TestFAQImportAdmissionAllowsRecoveryAndLegacy(t *testing.T) {
	for _, scenario := range []string{"matching", "missing", "legacy_raw_task_id", "legacy_same_timestamp"} {
		t.Run(scenario, func(t *testing.T) {
			f := newFAQAdmissionFixture(t)
			taskID := utils.GenerateTaskID("faq_import", f.kb.TenantID, f.kb.ID)
			task := f.enqueue(t, taskID, 1)
			var payload types.FAQImportPayload
			require.NoError(t, json.Unmarshal(task.Payload(), &payload))
			switch scenario {
			case "missing":
				f.mini.FastForward(faqImportProgressTTL + time.Second)
			case "legacy_raw_task_id":
				payload.InstanceID = ""
				payload.EnqueuedAt = 0
				require.NoError(t, f.mini.Set(getFAQImportRunningKey(f.kb.ID), taskID))
			case "legacy_same_timestamp":
				payload.InstanceID = ""
				require.NoError(t, f.svc.setRunningFAQImportInfo(f.ctx, f.kb.ID, &runningFAQImportInfo{
					TaskID: taskID, EnqueuedAt: payload.EnqueuedAt,
				}))
			}
			task = asynq.NewTask(types.TypeFAQImport, mustWriteJSON(t, payload))
			require.NoError(t, f.svc.ProcessFAQImport(f.ctx, task))
			progress, err := f.svc.GetFAQImportProgress(f.ctx, taskID)
			require.NoError(t, err)
			require.Equal(t, types.FAQImportStatusCompleted, progress.Status)
			require.Equal(t, 1, progress.Total)
			require.Equal(t, 1, progress.SuccessCount)
			marker, err := f.svc.getRunningFAQImportInfo(f.ctx, f.kb.ID)
			require.NoError(t, err)
			require.Nil(t, marker)
		})
	}
}

func TestFAQImportAdmissionRejectsUnreadableMarker(t *testing.T) {
	for _, scenario := range []string{"redis_read_error", "malformed", "empty_identity"} {
		t.Run(scenario, func(t *testing.T) {
			f := newFAQAdmissionFixture(t)
			taskID := utils.GenerateTaskID("faq_import", f.kb.TenantID, f.kb.ID)
			task := f.enqueue(t, taskID, 1)
			markerKey := getFAQImportRunningKey(f.kb.ID)
			switch scenario {
			case "malformed":
				require.NoError(t, f.mini.Set(markerKey, "{invalid-json"))
			case "empty_identity":
				require.NoError(t, f.mini.Set(markerKey, "{}"))
			}
			before, err := f.mini.Get(getFAQImportProgressKey(taskID))
			require.NoError(t, err)
			markerBefore, err := f.mini.Get(markerKey)
			require.NoError(t, err)
			if scenario == "redis_read_error" {
				f.mini.SetError("ERR marker store temporarily unavailable")
				t.Cleanup(func() { f.mini.SetError("") })
			}

			processErr := f.svc.ProcessFAQImport(f.ctx, task)

			require.Error(t, processErr)
			require.NotErrorIs(t, processErr, asynq.SkipRetry)
			f.mini.SetError("")
			after, err := f.mini.Get(getFAQImportProgressKey(taskID))
			require.NoError(t, err)
			require.Equal(t, before, after)
			markerAfter, err := f.mini.Get(markerKey)
			require.NoError(t, err)
			require.Equal(t, markerBefore, markerAfter)
			require.Zero(t, f.chunks.writes)
		})
	}
}
