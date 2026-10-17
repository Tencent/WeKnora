package service

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Tencent/WeKnora/internal/types"
)

// Regression tests for Tencent/WeKnora#4237 (guard half): a retried or
// duplicated asynq task used to replay its whole sync round through the same
// sync_log_id even when that log had already reached a terminal state
// (canceled by the user, or completed by a previous delivery). failed and
// running must keep falling through — failed is the streaming
// checkpoint-resume path (#2136) and running is the only recovery for a
// worker killed before persisting a terminal status.

func TestProcessSync_SkipsReplayOfTerminalSyncLog(t *testing.T) {
	for _, status := range []string{
		types.SyncLogStatusCanceled,
		types.SyncLogStatusSuccess,
		types.SyncLogStatusPartial,
	} {
		t.Run(status, func(t *testing.T) {
			h := newSyncDeletionHarness(t, true, "ds-4237-guard", "log-4237-guard", nil, nil)
			h.syncLogRepo.logs[h.syncLogID].Status = status

			updated, err := h.run(t)
			require.NoError(t, err)
			assert.Equal(t, status, updated.Status,
				"terminal sync log must not be re-run by a replayed task")
			assert.Nil(t, updated.Result, "skipped replay must not write a sync result")
		})
	}
}

func TestProcessSync_FailedLogStillReplaysForResume(t *testing.T) {
	h := newSyncDeletionHarness(t, true, "ds-4237-guard", "log-4237-guard", nil, nil)
	h.syncLogRepo.logs[h.syncLogID].Status = types.SyncLogStatusFailed

	updated, err := h.run(t)
	require.NoError(t, err)
	assert.Equal(t, types.SyncLogStatusSuccess, updated.Status,
		"a failed log must keep flowing through the normal sync round")
}

func TestProcessSync_RunningLogStillProcesses(t *testing.T) {
	h := newSyncDeletionHarness(t, true, "ds-4237-guard", "log-4237-guard", nil, nil)
	// Default harness log already starts as running — this is the plain
	// first-delivery path and must run end to end.
	updated, err := h.run(t)
	require.NoError(t, err)
	assert.Equal(t, types.SyncLogStatusSuccess, updated.Status)
}
