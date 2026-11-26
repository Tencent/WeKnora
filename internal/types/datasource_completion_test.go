package types

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestPendingLogResultMatchesOnlyCompletedRun(t *testing.T) {
	started := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	finished := started.Add(time.Second)
	log := SyncLog{ID: "run", DataSourceID: "source", TenantID: 1, StartedAt: started,
		FinishedAt: &finished, Status: SyncLogStatusSuccess}
	ds := DataSource{ID: log.DataSourceID, TenantID: log.TenantID, LastSyncAt: &finished}
	result := &SyncResult{Total: 3, Created: 3}
	var err error
	ds.LastSyncResult, err = result.WithPendingLog(&log, "")
	require.NoError(t, err)
	// The persisted log may still reflect a previous failed attempt of this run.
	log.Status = SyncLogStatusFailed
	pending, recovered := ds.PendingLogResult(&log)
	require.NotNil(t, pending)
	require.Equal(t, SyncLogStatusSuccess, pending.Status)
	require.Equal(t, result, recovered)

	for _, tc := range []struct {
		name string
		edit func(*SyncLog)
	}{
		{"other run", func(l *SyncLog) { l.ID = "older-run" }},
		{"other source", func(l *SyncLog) { l.DataSourceID = "other-source" }},
		{"other tenant", func(l *SyncLog) { l.TenantID++ }},
		{"later start", func(l *SyncLog) { l.StartedAt = finished.Add(time.Second) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			other := log
			tc.edit(&other)
			pending, recovered := ds.PendingLogResult(&other)
			require.Nil(t, pending)
			require.Nil(t, recovered)
		})
	}

	ds.LastSyncResult, err = result.WithPendingLog(&log, "")
	require.NoError(t, err)
	pending, recovered = ds.PendingLogResult(&log)
	require.Nil(t, pending, "a failed completion cannot bypass fetching")
	require.Nil(t, recovered)
}
