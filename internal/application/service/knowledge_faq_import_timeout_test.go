package service

import (
	"context"
	"io"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/alicebob/miniredis/v2"
	"github.com/hibiken/asynq"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

// hangingFAQFileService blocks in GetFile until ctx is done, simulating a
// worker stuck downloading entries from object storage.
type hangingFAQFileService struct {
	interfaces.FileService
	deleted []string
}

func (hangingFAQFileService) GetFile(ctx context.Context, _ string) (io.ReadCloser, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}

func (f *hangingFAQFileService) DeleteFile(_ context.Context, path string) error {
	f.deleted = append(f.deleted, path)
	return nil
}

func TestFAQImportTimeoutReleasesLockAndFailsTerminally(t *testing.T) {
	f := newFAQWriteFixture(t)
	f.svc.redisClient = redis.NewClient(&redis.Options{Addr: miniredis.RunT(t).Addr()})
	t.Cleanup(func() { _ = f.svc.redisClient.Close() })
	f.svc.tenantRepo = &faqImportTenantLookup{}
	files := &hangingFAQFileService{}
	f.svc.fileSvc = files
	oldTimeout := faqImportExecTimeout
	faqImportExecTimeout = 100 * time.Millisecond
	t.Cleanup(func() { faqImportExecTimeout = oldTimeout })

	payload := types.FAQImportPayload{
		TenantID: 7, KBID: "kb", KnowledgeID: "faq", TaskID: "task",
		InstanceID: "instance", Mode: types.FAQBatchModeReplace,
		EntriesURL: "faq_import_entries_task_0.json", EntryCount: 1,
	}
	pending := &types.FAQImportProgress{
		TaskID: payload.TaskID, KBID: payload.KBID, KnowledgeID: payload.KnowledgeID,
		Status: types.FAQImportStatusPending, Total: 1,
	}
	running := &runningFAQImportInfo{TaskID: payload.TaskID, InstanceID: payload.InstanceID}
	ctx := types.WithExecutionTenant(context.Background(), payload.TenantID)
	require.NoError(t, f.svc.saveFAQImportProgress(ctx, pending))
	require.NoError(t, f.svc.setRunningFAQImportInfo(ctx, payload.KBID, running))

	start := time.Now()
	err := f.svc.ProcessFAQImport(ctx, asynq.NewTask(types.TypeFAQImport, mustWriteJSON(t, payload)))
	elapsed := time.Since(start)
	require.ErrorIs(t, err, asynq.SkipRetry)
	require.ErrorContains(t, err, "timed out")
	// The stuck download must unblock on the execution timeout, not hang
	// until the 3h progress TTL expires.
	require.Less(t, elapsed, 30*time.Second)

	progress, err := f.svc.GetFAQImportProgress(ctx, payload.TaskID)
	require.NoError(t, err)
	require.Equal(t, types.FAQImportStatusFailed, progress.Status)
	lock, err := f.svc.getRunningFAQImportInfo(ctx, payload.KBID)
	require.NoError(t, err)
	require.Nil(t, lock)
	require.Equal(t, []string{payload.EntriesURL}, files.deleted)
}
