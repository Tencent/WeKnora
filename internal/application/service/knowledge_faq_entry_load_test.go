package service

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"testing"
	"testing/iotest"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/alicebob/miniredis/v2"
	"github.com/hibiken/asynq"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

type faqEntryLoadFiles struct {
	interfaces.FileService
	downloadErr error
	readErr     error
	cancel      context.CancelFunc
}

func (s *faqEntryLoadFiles) GetFile(_ context.Context, path string) (io.ReadCloser, error) {
	if s.cancel != nil {
		s.cancel()
	}
	if s.downloadErr != nil {
		return nil, s.downloadErr
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	if s.readErr != nil {
		return struct {
			io.Reader
			io.Closer
		}{iotest.ErrReader(s.readErr), file}, nil
	}
	return file, nil
}

func (s *faqEntryLoadFiles) DeleteFile(ctx context.Context, path string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return os.Remove(path)
}

func TestFAQImportEntryLoadFailure(t *testing.T) {
	for _, stage := range []string{"download", "read", "decode"} {
		for _, maxRetry := range []int{0, 1} {
			t.Run(fmt.Sprintf("%s/max_retry=%d", stage, maxRetry), func(t *testing.T) {
				testFAQImportEntryLoadFailure(t, stage, maxRetry, false, false)
			})
		}
	}
	t.Run("new_instance_keeps_its_state", func(t *testing.T) {
		testFAQImportEntryLoadFailure(t, "download", 0, true, false)
	})
	t.Run("cancelled_download_still_finalizes", func(t *testing.T) {
		testFAQImportEntryLoadFailure(t, "download", 0, false, true)
	})
}

func testFAQImportEntryLoadFailure(t *testing.T, stage string, maxRetry int, replaced, cancelled bool) {
	t.Helper()
	f := newFAQWriteFixture(t)
	sqlDB, err := f.db.DB()
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, sqlDB.Close()) })
	f.svc.tenantRepo = &processSyncTenantRepo{tenant: &types.Tenant{ID: 7}}
	files := &faqEntryLoadFiles{}
	f.svc.fileSvc = files
	loadErr := errors.New("object storage unavailable")
	switch stage {
	case "download":
		files.downloadErr = loadErr
	case "read":
		files.readErr = loadErr
	}
	if cancelled {
		files.downloadErr = context.Canceled
	}
	entriesPath := filepath.Join(t.TempDir(), "entries.json")
	require.NoError(t, os.WriteFile(entriesPath, []byte("{"), 0o600))
	payload := types.FAQImportPayload{
		TenantID: 7, KBID: "kb", KnowledgeID: "faq", TaskID: "entry-load",
		InstanceID: "original", EnqueuedAt: 100, EntriesURL: entriesPath, DryRun: true,
	}
	pending := &types.FAQImportProgress{
		TaskID: payload.TaskID, KBID: payload.KBID, KnowledgeID: payload.KnowledgeID,
		Status: types.FAQImportStatusPending, Total: 201, DryRun: true,
	}
	running := &runningFAQImportInfo{
		TaskID: payload.TaskID, InstanceID: payload.InstanceID, EnqueuedAt: payload.EnqueuedAt,
	}
	if replaced {
		running.InstanceID = "replacement"
		pending.Message = "replacement import is pending"
	}
	redisClient := redis.NewClient(&redis.Options{Addr: miniredis.RunT(t).Addr()})
	t.Cleanup(func() { require.NoError(t, redisClient.Close()) })
	f.svc.redisClient = redisClient
	require.NoError(t, f.svc.saveFAQImportProgress(f.ctx, pending))
	require.NoError(t, f.svc.setRunningFAQImportInfo(f.ctx, payload.KBID, running))

	// A real worker supplies retry metadata; a plain context would make every attempt final.
	worker := asynq.NewServerFromRedisClient(redisClient, asynq.Config{
		Concurrency: 1, TaskCheckInterval: 10 * time.Millisecond,
		DelayedTaskCheckInterval: time.Hour, ShutdownTimeout: time.Second, LogLevel: asynq.FatalLevel,
		RetryDelayFunc: func(int, error, *asynq.Task) time.Duration { return time.Hour },
	})
	result := make(chan error, 1)
	require.NoError(t, worker.Start(asynq.HandlerFunc(func(ctx context.Context, task *asynq.Task) error {
		ctx, cancel := context.WithCancel(ctx)
		defer cancel()
		if cancelled {
			files.cancel = cancel
		}
		processErr := f.svc.ProcessFAQImport(ctx, task)
		result <- processErr
		return processErr
	})))
	t.Cleanup(worker.Shutdown)
	client := asynq.NewClientFromRedisClient(redisClient)
	_, err = client.Enqueue(asynq.NewTask(types.TypeFAQImport, mustWriteJSON(t, payload)), asynq.MaxRetry(maxRetry))
	require.NoError(t, err)
	select {
	case processErr := <-result:
		require.Error(t, processErr)
		if cancelled {
			require.ErrorIs(t, processErr, context.Canceled)
		} else if stage != "decode" {
			require.ErrorIs(t, processErr, loadErr)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("FAQ import worker did not return")
	}
	progress, err := f.svc.GetFAQImportProgress(f.ctx, payload.TaskID)
	require.NoError(t, err)
	lock, err := f.svc.getRunningFAQImportInfo(f.ctx, payload.KBID)
	require.NoError(t, err)
	_, statErr := os.Stat(entriesPath)
	if replaced {
		require.Equal(t, pending, progress, "an old attempt must not fail the replacement import")
		require.Equal(t, running, lock)
		require.NoError(t, statErr, "the replacement may still need the same entries object")
		return
	}
	if maxRetry == 0 {
		require.Equal(t, types.FAQImportStatusFailed, progress.Status)
		require.NotEmpty(t, progress.Error)
		require.Equal(t, 201, progress.Total)
		require.ErrorIs(t, statErr, os.ErrNotExist)
		require.Nil(t, lock)
	} else {
		require.Equal(t, pending, progress)
		require.Equal(t, running, lock)
		require.NoError(t, statErr, "retryable attempts still need their entries file")
	}
}
