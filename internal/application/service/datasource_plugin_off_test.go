package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/hibiken/asynq"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	plugintenancy "github.com/Tencent/WeKnora/internal/plugin/tenancy"
	"github.com/Tencent/WeKnora/internal/types"
)

// A sync its plugin cannot serve (switched off in the workspace, out of its
// audience, removed) fails with the reason without retries, and leaves the
// data source active, so its schedule syncs again once the plugin is back.
func TestSyncSkippedForPluginKeepsTheSchedule(t *testing.T) {
	ds := &types.DataSource{ID: "ds-1", TenantID: 1, Status: types.DataSourceStatusActive}
	syncLog := &types.SyncLog{
		ID: "log-1", DataSourceID: ds.ID, Status: types.SyncLogStatusRunning, StartedAt: time.Now(),
	}
	logs := &processSyncSyncLogRepo{logs: map[string]*types.SyncLog{syncLog.ID: syncLog}}
	svc := &DataSourceService{dsRepo: newKBDeleteDSRepo("kb", ds), syncLogRepo: logs}

	cause := errors.Join(plugintenancy.ErrPluginOff)
	err := svc.skipSyncForPlugin(context.Background(), ds, syncLog, "Fetch failed: plugin acme.x is off", cause)
	require.ErrorIs(t, err, asynq.SkipRetry)
	require.ErrorIs(t, err, plugintenancy.ErrPluginOff)
	assert.Equal(t, types.DataSourceStatusActive, ds.Status)
	assert.Equal(t, "Fetch failed: plugin acme.x is off", ds.ErrorMessage)
	assert.Equal(t, types.SyncLogStatusFailed, logs.logs[syncLog.ID].Status)
	require.NotNil(t, logs.logs[syncLog.ID].FinishedAt)
}
