package datasource

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type retentionCall struct {
	ctx  context.Context
	days int
}

type retentionSyncLogRepo struct {
	*fakeSyncLogRepo
	calls   chan retentionCall
	release <-chan struct{}
}

func (r *retentionSyncLogRepo) CleanupOldLogs(ctx context.Context, days int) error {
	r.calls <- retentionCall{ctx, days}
	select {
	case <-r.release:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func TestSchedulerRunsDailyRetentionAndWaitsForShutdown(t *testing.T) {
	release := make(chan struct{})
	var releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }
	repo := &retentionSyncLogRepo{newFakeSyncLogRepo(), make(chan retentionCall, 1), release}
	scheduler := NewScheduler(newFakeDataSourceRepo(), repo, &fakeTaskEnqueuer{})
	require.NoError(t, scheduler.Start(context.Background()))
	t.Cleanup(scheduler.Stop)
	t.Cleanup(unblock)

	// Retention must be registered even without a source cron entry, so
	// paused/manual-only/deleted sources are also eventually cleaned.
	require.Zero(t, scheduler.EntryCount())
	entries := scheduler.cron.Entries()
	require.Len(t, entries, 1)
	now := time.Now()
	want := time.Date(now.Year(), now.Month(), now.Day()+1, 0, 0, 0, 0, now.Location())
	require.Equal(t, want, entries[0].Schedule.Next(now))

	// Run the registered production job through cron at a shorter test
	// interval; cron's in-flight wait group must include the retention call.
	scheduler.cron.Remove(entries[0].ID)
	_, err := scheduler.cron.AddJob("@every 1s", entries[0].Job)
	require.NoError(t, err)
	select {
	case call := <-repo.calls:
		require.Equal(t, 30, call.days)
		deadline, ok := call.ctx.Deadline()
		require.True(t, ok, "retention database calls must have a timeout")
		require.InDelta(t, 30, time.Until(deadline).Seconds(), 2)
	case <-time.After(3 * time.Second):
		t.Fatal("retention job did not run")
	}
	stopped := make(chan struct{})
	go func() {
		scheduler.Stop()
		close(stopped)
	}()
	select {
	case <-stopped:
		t.Fatal("scheduler stopped before its retention call finished")
	case <-time.After(20 * time.Millisecond):
	}
	unblock()
	select {
	case <-stopped:
	case <-time.After(3 * time.Second):
		t.Fatal("scheduler failed to stop after retention completed")
	}
}
