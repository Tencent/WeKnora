package service

import (
	"context"
	"testing"

	"github.com/Tencent/WeKnora/internal/datasource"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

func TestDataSourceResumeRejectsInvalidSchedule(t *testing.T) {
	f := newSQLiteDataSourceDeleteFixture(t)
	f.scheduler.Remove(f.ds.ID)
	f.ds.Status = types.DataSourceStatusPaused
	f.ds.SyncSchedule = "not-a-cron"
	require.NoError(t, f.dsRepo.Update(context.Background(), f.ds))
	svc := &DataSourceService{dsRepo: f.dsRepo, scheduler: f.scheduler}
	err := svc.ResumeDataSource(context.Background(), f.ds.ID)
	saved, readErr := svc.GetDataSource(context.Background(), f.ds.ID)
	require.NoError(t, readErr)
	t.Logf("resume error=%v; status=%s; entries=%d", err, saved.Status, f.scheduler.EntryCount())
	require.ErrorContains(t, err, "invalid cron expression")
	require.Equal(t, types.DataSourceStatusPaused, saved.Status)
	require.Zero(t, f.scheduler.EntryCount())
}

func TestDataSourceUpdateRejectsInvalidSchedule(t *testing.T) {
	f := newSQLiteDataSourceDeleteFixture(t)
	svc := &DataSourceService{dsRepo: f.dsRepo, scheduler: f.scheduler}
	updated := *f.ds
	updated.SyncSchedule = "not-a-cron"
	_, err := svc.UpdateDataSource(context.Background(), &updated)
	require.ErrorContains(t, err, "invalid cron expression")
	saved, err := svc.GetDataSource(context.Background(), f.ds.ID)
	require.NoError(t, err)
	require.Equal(t, f.ds.SyncSchedule, saved.SyncSchedule)
	require.Equal(t, 1, f.scheduler.EntryCount())
}

func TestDataSourceCreateRejectsInvalidSchedule(t *testing.T) {
	f := newSQLiteDataSourceDeleteFixture(t)
	registry := datasource.NewConnectorRegistry()
	require.NoError(t, registry.Register(deletedItemConnector{}))
	svc := &DataSourceService{
		dsRepo: f.dsRepo, scheduler: f.scheduler, connectorRegistry: registry,
		kbService: &processSyncKBService{kb: &types.KnowledgeBase{ID: f.ds.KnowledgeBaseID, TenantID: f.ds.TenantID}},
	}
	ds := *f.ds
	ds.ID = "invalid-schedule"
	ds.Type = deletedItemConnectorType
	ds.SyncSchedule = "not-a-cron"
	ds.Config = []byte(`{"type":"test"}`)
	_, err := svc.CreateDataSource(context.Background(), &ds)
	require.ErrorContains(t, err, "invalid cron expression")
	_, err = svc.GetDataSource(context.Background(), ds.ID)
	require.Error(t, err)
	require.Equal(t, 1, f.scheduler.EntryCount())
}

func TestDataSourceScheduleLifecycle(t *testing.T) {
	for _, schedule := range []string{"", "0 0 */6 * * *", "@every 6h", "CRON_TZ=UTC 0 0 * * * *"} {
		t.Run(schedule, func(t *testing.T) {
			f := newSQLiteDataSourceDeleteFixture(t)
			svc := &DataSourceService{dsRepo: f.dsRepo, scheduler: f.scheduler}
			updated := *f.ds
			updated.SyncSchedule = schedule
			_, err := svc.UpdateDataSource(context.Background(), &updated)
			require.NoError(t, err)
			wantEntries := 1
			if schedule == "" {
				wantEntries = 0
			}
			require.Equal(t, wantEntries, f.scheduler.EntryCount())
			require.NoError(t, svc.PauseDataSource(context.Background(), f.ds.ID))
			require.Zero(t, f.scheduler.EntryCount())
			// Repeated resume must replace, rather than duplicate, the job.
			for range 2 {
				require.NoError(t, svc.ResumeDataSource(context.Background(), f.ds.ID))
				require.Equal(t, wantEntries, f.scheduler.EntryCount())
			}
			saved, err := svc.GetDataSource(context.Background(), f.ds.ID)
			require.NoError(t, err)
			require.Equal(t, types.DataSourceStatusActive, saved.Status)
			require.Equal(t, schedule, saved.SyncSchedule)
		})
	}
}

func TestDataSourceScheduleWriteFailurePreservesState(t *testing.T) {
	for _, operation := range []string{"update", "resume"} {
		t.Run(operation, func(t *testing.T) {
			f := newSQLiteDataSourceDeleteFixture(t)
			svc := &DataSourceService{dsRepo: f.dsRepo, scheduler: f.scheduler}
			if operation == "resume" {
				require.NoError(t, svc.PauseDataSource(context.Background(), f.ds.ID))
			}
			before, err := svc.GetDataSource(context.Background(), f.ds.ID)
			require.NoError(t, err)
			entries := f.scheduler.EntryCount()
			require.NoError(t, f.db.Exec(`CREATE TRIGGER fail_schedule_write BEFORE UPDATE ON data_sources
				BEGIN SELECT RAISE(FAIL, 'forced schedule write failure'); END;`).Error)
			if operation == "resume" {
				err = svc.ResumeDataSource(context.Background(), f.ds.ID)
			} else {
				updated := *before
				updated.SyncSchedule = "0 30 * * * *"
				_, err = svc.UpdateDataSource(context.Background(), &updated)
			}
			require.ErrorContains(t, err, "forced schedule write failure")
			saved, err := svc.GetDataSource(context.Background(), f.ds.ID)
			require.NoError(t, err)
			require.Equal(t, before.Status, saved.Status)
			require.Equal(t, before.SyncSchedule, saved.SyncSchedule)
			require.Equal(t, entries, f.scheduler.EntryCount())
		})
	}
}

func TestDataSourceScheduleCanceledUpdatePreservesState(t *testing.T) {
	f := newSQLiteDataSourceDeleteFixture(t)
	svc := &DataSourceService{dsRepo: f.dsRepo, scheduler: f.scheduler}
	updated := *f.ds
	updated.SyncSchedule = "0 30 * * * *"
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := svc.UpdateDataSource(ctx, &updated)
	require.ErrorIs(t, err, context.Canceled)
	saved, err := svc.GetDataSource(context.Background(), f.ds.ID)
	require.NoError(t, err)
	require.Equal(t, f.ds.SyncSchedule, saved.SyncSchedule)
	require.Equal(t, 1, f.scheduler.EntryCount())
}
