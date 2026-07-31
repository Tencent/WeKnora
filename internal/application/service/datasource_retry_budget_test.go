package service

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/datasource"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/hibiken/asynq"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type recordedDataSourceTask struct {
	task    *asynq.Task
	options []asynq.Option
}

type recordingDataSourceTaskEnqueuer struct {
	mu      sync.Mutex
	records []recordedDataSourceTask
	err     error
}

type failingPendingReconciliationSyncLogRepo struct {
	interfaces.SyncLogRepository
	log       *types.SyncLog
	findErr   error
	updateErr error
}

func (r *failingPendingReconciliationSyncLogRepo) FindByID(
	context.Context,
	string,
) (*types.SyncLog, error) {
	if r.findErr != nil {
		return nil, r.findErr
	}
	if r.log == nil {
		return nil, nil
	}
	logCopy := *r.log
	return &logCopy, nil
}

func (r *failingPendingReconciliationSyncLogRepo) Update(
	_ context.Context,
	log *types.SyncLog,
) error {
	if r.updateErr != nil {
		return r.updateErr
	}
	r.log = log
	return nil
}

func (e *recordingDataSourceTaskEnqueuer) Enqueue(
	task *asynq.Task,
	options ...asynq.Option,
) (*asynq.TaskInfo, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.records = append(e.records, recordedDataSourceTask{
		task:    task,
		options: append([]asynq.Option(nil), options...),
	})
	if e.err != nil {
		return nil, e.err
	}
	return &asynq.TaskInfo{ID: "recorded-data-source-task"}, nil
}

func (e *recordingDataSourceTaskEnqueuer) recordCount() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return len(e.records)
}

func (e *recordingDataSourceTaskEnqueuer) record(index int) recordedDataSourceTask {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.records[index]
}

func taskOptionValue(options []asynq.Option, optionType asynq.OptionType) (any, bool) {
	for _, option := range options {
		if option.Type() == optionType {
			return option.Value(), true
		}
	}
	return nil, false
}

func requireTaskOption(
	t *testing.T,
	options []asynq.Option,
	optionType asynq.OptionType,
	expected any,
) {
	t.Helper()
	actual, ok := taskOptionValue(options, optionType)
	require.True(t, ok, "missing task option %v", optionType)
	assert.Equal(t, expected, actual)
}

func TestManualSyncKeepsOrdinaryRetryBudgetExplicit(t *testing.T) {
	ds := &types.DataSource{
		ID:              "ds-manual-retry-budget",
		TenantID:        7,
		KnowledgeBaseID: "kb-manual-retry-budget",
		Type:            types.ConnectorTypeDingTalk,
		Status:          types.DataSourceStatusActive,
	}
	enqueuer := &recordingDataSourceTaskEnqueuer{}
	svc := &DataSourceService{
		dsRepo:       newKBDeleteDSRepo(ds.KnowledgeBaseID, ds),
		syncLogRepo:  &processSyncSyncLogRepo{logs: map[string]*types.SyncLog{}},
		taskEnqueuer: enqueuer,
	}

	_, err := svc.ManualSync(context.Background(), ds.ID)

	require.NoError(t, err)
	require.Equal(t, 1, enqueuer.recordCount())
	record := enqueuer.record(0)
	requireTaskOption(t, record.options, asynq.QueueOpt, types.QueueSync)
	requireTaskOption(t, record.options, asynq.MaxRetryOpt, types.DataSourceSyncMaxRetry)
	requireTaskOption(t, record.options, asynq.TimeoutOpt, 2*time.Hour)
}

func TestEnqueuePendingReconciliationUsesDedicatedLongBudget(t *testing.T) {
	enqueuer := &recordingDataSourceTaskEnqueuer{}
	svc := &DataSourceService{taskEnqueuer: enqueuer}
	payload := types.DataSourceSyncPayload{
		DataSourceID: "ds-pending",
		TenantID:     9,
		SyncLogID:    "sync-log-pending",
	}

	err := svc.enqueuePendingReconciliation(context.Background(), payload)

	require.NoError(t, err)
	require.Equal(t, 1, enqueuer.recordCount())
	record := enqueuer.record(0)
	requireTaskOption(t, record.options, asynq.QueueOpt, types.QueueSync)
	requireTaskOption(t, record.options, asynq.MaxRetryOpt, types.DataSourceIngestPendingMaxRetry)
	requireTaskOption(t, record.options, asynq.TimeoutOpt, 2*time.Hour)
	requireTaskOption(t, record.options, asynq.ProcessInOpt, types.DataSourceIngestPendingRetryDelay)
	requireTaskOption(
		t,
		record.options,
		asynq.TaskIDOpt,
		dataSourcePendingReconciliationTaskIDPrefix+payload.SyncLogID,
	)

	var continuation types.DataSourceSyncPayload
	require.NoError(t, json.Unmarshal(record.task.Payload(), &continuation))
	assert.True(t, continuation.PendingReconciliation)
	assert.Equal(t, payload.SyncLogID, continuation.SyncLogID)
}

func TestEnqueuePendingReconciliationTreatsTaskIDConflictAsSuccess(t *testing.T) {
	enqueuer := &recordingDataSourceTaskEnqueuer{err: asynq.ErrTaskIDConflict}
	svc := &DataSourceService{taskEnqueuer: enqueuer}

	err := svc.enqueuePendingReconciliation(context.Background(), types.DataSourceSyncPayload{
		SyncLogID: "sync-log-conflict",
	})

	require.NoError(t, err)
	require.Equal(t, 1, enqueuer.recordCount())
}

func TestLimitPendingReconciliationErrorStopsOrdinaryErrorsAtNormalBudget(t *testing.T) {
	syncLog := &types.SyncLog{
		ID:     "sync-log-retry-limit",
		Status: types.SyncLogStatusRunning,
	}
	syncLogs := &processSyncSyncLogRepo{logs: map[string]*types.SyncLog{
		syncLog.ID: syncLog,
	}}
	svc := &DataSourceService{syncLogRepo: syncLogs}
	payload := types.DataSourceSyncPayload{
		SyncLogID:             syncLog.ID,
		PendingReconciliation: true,
	}
	ordinaryErr := errors.New("generation lookup failed")

	beforeLimit := svc.limitPendingReconciliationError(
		types.WithTaskRetryMetadata(context.Background(), 4, types.DataSourceIngestPendingMaxRetry),
		payload,
		ordinaryErr,
	)
	require.ErrorIs(t, beforeLimit, ordinaryErr)
	assert.NotErrorIs(t, beforeLimit, asynq.SkipRetry)
	assert.Equal(t, types.SyncLogStatusRunning, syncLog.Status)

	atLimit := svc.limitPendingReconciliationError(
		types.WithTaskRetryMetadata(context.Background(), 5, types.DataSourceIngestPendingMaxRetry),
		payload,
		ordinaryErr,
	)
	require.ErrorIs(t, atLimit, ordinaryErr)
	require.ErrorIs(t, atLimit, asynq.SkipRetry)
	assert.Equal(t, types.SyncLogStatusFailed, syncLog.Status)
	require.NotNil(t, syncLog.FinishedAt)
}

func TestLimitPendingReconciliationErrorPreservesPendingThroughLongBudget(t *testing.T) {
	svc := &DataSourceService{}
	ctx := types.WithTaskRetryMetadata(
		context.Background(),
		types.DataSourceIngestPendingMaxRetry,
		types.DataSourceIngestPendingMaxRetry,
	)

	got := svc.limitPendingReconciliationError(
		ctx,
		types.DataSourceSyncPayload{PendingReconciliation: true},
		ErrDataSourceIngestPending,
	)

	require.ErrorIs(t, got, ErrDataSourceIngestPending)
	assert.NotErrorIs(t, got, asynq.SkipRetry)
	assert.True(t, syncRetryBudgetExhausted(ctx))
}

func TestLimitPendingReconciliationErrorKeepsRetryingWhenFinalizationFails(t *testing.T) {
	ordinaryErr := errors.New("generation lookup failed")
	ctx := types.WithTaskRetryMetadata(
		context.Background(),
		types.DataSourceSyncMaxRetry,
		types.DataSourceIngestPendingMaxRetry,
	)
	tests := []struct {
		name string
		repo *failingPendingReconciliationSyncLogRepo
	}{
		{
			name: "load failure",
			repo: &failingPendingReconciliationSyncLogRepo{
				findErr: errors.New("database unavailable"),
			},
		},
		{
			name: "update failure",
			repo: &failingPendingReconciliationSyncLogRepo{
				log: &types.SyncLog{
					ID:     "sync-log-finalization-failure",
					Status: types.SyncLogStatusRunning,
				},
				updateErr: errors.New("database unavailable"),
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			svc := &DataSourceService{syncLogRepo: test.repo}

			got := svc.limitPendingReconciliationError(
				ctx,
				types.DataSourceSyncPayload{
					SyncLogID:             "sync-log-finalization-failure",
					PendingReconciliation: true,
				},
				ordinaryErr,
			)

			require.ErrorIs(t, got, ordinaryErr)
			assert.NotErrorIs(t, got, asynq.SkipRetry)
			if test.repo.log != nil {
				assert.Equal(t, types.SyncLogStatusRunning, test.repo.log.Status)
			}
		})
	}
}

func TestPendingReconciliationRetriesTransientSyncLogLookupFailure(t *testing.T) {
	ds := &types.DataSource{
		ID:              "ds-transient-log-lookup",
		TenantID:        11,
		KnowledgeBaseID: "kb-transient-log-lookup",
		Type:            types.ConnectorTypeDingTalk,
		Status:          types.DataSourceStatusActive,
	}
	lookupErr := errors.New("database temporarily unavailable")
	svc := &DataSourceService{
		dsRepo: newKBDeleteDSRepo(ds.KnowledgeBaseID, ds),
		syncLogRepo: &failingPendingReconciliationSyncLogRepo{
			findErr: lookupErr,
		},
	}
	payload, err := json.Marshal(types.DataSourceSyncPayload{
		DataSourceID:          ds.ID,
		TenantID:              ds.TenantID,
		SyncLogID:             "sync-log-transient-lookup",
		PendingReconciliation: true,
	})
	require.NoError(t, err)
	ctx := types.WithTaskRetryMetadata(
		context.Background(),
		0,
		types.DataSourceIngestPendingMaxRetry,
	)

	err = svc.ProcessSync(ctx, asynq.NewTask(types.TypeDataSourceSync, payload))

	require.ErrorIs(t, err, lookupErr)
	assert.NotErrorIs(t, err, asynq.SkipRetry)
}

func TestPendingReconciliationTreatsRemovedSyncLogAsIdempotent(t *testing.T) {
	ds := &types.DataSource{
		ID:              "ds-removed-log",
		TenantID:        12,
		KnowledgeBaseID: "kb-removed-log",
		Type:            types.ConnectorTypeDingTalk,
		Status:          types.DataSourceStatusActive,
	}
	svc := &DataSourceService{
		dsRepo: newKBDeleteDSRepo(ds.KnowledgeBaseID, ds),
		syncLogRepo: &failingPendingReconciliationSyncLogRepo{
			findErr: datasource.ErrSyncLogNotFound,
		},
	}
	payload, err := json.Marshal(types.DataSourceSyncPayload{
		DataSourceID:          ds.ID,
		TenantID:              ds.TenantID,
		SyncLogID:             "sync-log-removed",
		PendingReconciliation: true,
	})
	require.NoError(t, err)

	err = svc.ProcessSync(context.Background(), asynq.NewTask(types.TypeDataSourceSync, payload))

	require.NoError(t, err)
}
