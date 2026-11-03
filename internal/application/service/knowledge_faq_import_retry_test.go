package service

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/alicebob/miniredis/v2"
	"github.com/hibiken/asynq"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

type faqImportTenantLookup struct {
	interfaces.TenantRepository
	err error
}

func (r *faqImportTenantLookup) GetTenantByID(_ context.Context, id uint64) (*types.Tenant, error) {
	if r.err != nil {
		return nil, r.err
	}
	return &types.Tenant{ID: id}, nil
}

func TestFAQImportRetriesTenantLookupFailure(t *testing.T) {
	for _, dryRun := range []bool{false, true} {
		t.Run(fmt.Sprintf("dry_run=%t", dryRun), func(t *testing.T) {
			f := newFAQWriteFixture(t)
			f.svc.redisClient = redis.NewClient(&redis.Options{Addr: miniredis.RunT(t).Addr()})
			t.Cleanup(func() { _ = f.svc.redisClient.Close() })
			lookupErr := errors.New("database temporarily unavailable")
			tenants := &faqImportTenantLookup{err: lookupErr}
			f.svc.tenantRepo = tenants
			payload := types.FAQImportPayload{
				TenantID: 7, KBID: "kb", KnowledgeID: "faq", TaskID: "task",
				InstanceID: "instance", Mode: types.FAQBatchModeReplace, DryRun: dryRun,
				Entries: []types.FAQEntryPayload{{
					StandardQuestion: "How do I reset my password?",
					Answers:          []string{"Use the reset link."},
				}},
			}
			pending := &types.FAQImportProgress{
				TaskID: payload.TaskID, KBID: payload.KBID, KnowledgeID: payload.KnowledgeID,
				Status: types.FAQImportStatusPending, Total: 1, DryRun: dryRun,
			}
			running := &runningFAQImportInfo{TaskID: payload.TaskID, InstanceID: payload.InstanceID}
			ctx := types.WithExecutionTenant(context.Background(), payload.TenantID)
			require.NoError(t, f.svc.saveFAQImportProgress(ctx, pending))
			require.NoError(t, f.svc.setRunningFAQImportInfo(ctx, payload.KBID, running))
			task := asynq.NewTask(types.TypeFAQImport, mustWriteJSON(t, payload))

			err := f.svc.ProcessFAQImport(ctx, task)
			if err == nil {
				t.Fatal("tenant lookup failure was acknowledged as success")
			}
			require.ErrorIs(t, err, lookupErr)
			require.NotErrorIs(t, err, asynq.SkipRetry)
			progress, err := f.svc.GetFAQImportProgress(ctx, payload.TaskID)
			require.NoError(t, err)
			require.Equal(t, pending, progress)
			lock, err := f.svc.getRunningFAQImportInfo(ctx, payload.KBID)
			require.NoError(t, err)
			require.Equal(t, running, lock)
			require.Zero(t, f.chunks.writes)

			if dryRun {
				// Retry the same task after the dependency recovers. The real
				// validation/finalization path must complete and release its lock.
				tenants.err = nil
				require.NoError(t, f.svc.ProcessFAQImport(ctx, task))
				progress, err = f.svc.GetFAQImportProgress(ctx, payload.TaskID)
				require.NoError(t, err)
				require.Equal(t, types.FAQImportStatusCompleted, progress.Status)
				require.Equal(t, 1, progress.SuccessCount)
				require.Equal(t, 100, progress.Progress)
				lock, err = f.svc.getRunningFAQImportInfo(ctx, payload.KBID)
				require.NoError(t, err)
				require.Nil(t, lock)
			}
		})
	}
}

func TestFAQImportDoesNotRetryDeletedTenant(t *testing.T) {
	f := newFAQWriteFixture(t)
	f.svc.tenantRepo = &faqImportTenantLookup{err: fmt.Errorf("lookup: %w", repository.ErrTenantNotFound)}
	payload := types.FAQImportPayload{TenantID: 7, KBID: "kb", KnowledgeID: "faq", TaskID: "task"}
	err := f.svc.ProcessFAQImport(context.Background(), asynq.NewTask(types.TypeFAQImport, mustWriteJSON(t, payload)))
	require.ErrorIs(t, err, asynq.SkipRetry)
	require.Zero(t, f.chunks.writes)
}
