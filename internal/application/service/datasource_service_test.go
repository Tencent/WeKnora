package service

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	apprepo "github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/datasource"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/hibiken/asynq"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestProcessSyncCancelsWhenKnowledgeBaseDeleted(t *testing.T) {
	ds := &types.DataSource{
		ID:              "ds-1",
		TenantID:        1,
		KnowledgeBaseID: "kb-deleted",
		Type:            types.ConnectorTypeRSS,
		Status:          types.DataSourceStatusActive,
	}
	dsRepo := newKBDeleteDSRepo("kb-deleted", ds)
	syncLog := &types.SyncLog{
		ID:           "log-1",
		DataSourceID: ds.ID,
		TenantID:     ds.TenantID,
		Status:       types.SyncLogStatusRunning,
		StartedAt:    time.Now().UTC(),
	}
	syncLogRepo := &processSyncSyncLogRepo{logs: map[string]*types.SyncLog{syncLog.ID: syncLog}}

	svc := &DataSourceService{
		dsRepo:      dsRepo,
		syncLogRepo: syncLogRepo,
		kbService:   &processSyncKBService{getErr: apprepo.ErrKnowledgeBaseNotFound},
	}

	payload, err := json.Marshal(types.DataSourceSyncPayload{
		DataSourceID: ds.ID,
		TenantID:     ds.TenantID,
		SyncLogID:    syncLog.ID,
	})
	require.NoError(t, err)

	err = svc.ProcessSync(context.Background(), asynq.NewTask(types.TypeDataSourceSync, payload))
	require.NoError(t, err)

	updated := syncLogRepo.logs[syncLog.ID]
	require.NotNil(t, updated)
	assert.Equal(t, types.SyncLogStatusCanceled, updated.Status)
	assert.Equal(t, "knowledge base has been deleted", updated.ErrorMessage)
	require.NotNil(t, updated.FinishedAt)
}

type processSyncKBService struct {
	getErr error
}

func (s *processSyncKBService) CreateKnowledgeBase(context.Context, *types.KnowledgeBase) (*types.KnowledgeBase, error) {
	return nil, nil
}

func (s *processSyncKBService) GetKnowledgeBaseByID(context.Context, string) (*types.KnowledgeBase, error) {
	return nil, s.getErr
}

func (s *processSyncKBService) GetKnowledgeBaseByIDOnly(context.Context, string) (*types.KnowledgeBase, error) {
	return nil, s.getErr
}

func (s *processSyncKBService) GetKnowledgeBasesByIDsOnly(context.Context, []string) ([]*types.KnowledgeBase, error) {
	return nil, nil
}

func (s *processSyncKBService) FillKnowledgeBaseCounts(context.Context, *types.KnowledgeBase) error {
	return nil
}

func (s *processSyncKBService) ListKnowledgeBases(context.Context) ([]*types.KnowledgeBase, error) {
	return nil, nil
}

func (s *processSyncKBService) ListKnowledgeBasesByTenantID(context.Context, uint64) ([]*types.KnowledgeBase, error) {
	return nil, nil
}

func (s *processSyncKBService) UpdateKnowledgeBase(
	context.Context, string, string, string, *types.KnowledgeBaseConfig,
) (*types.KnowledgeBase, error) {
	return nil, nil
}
func (s *processSyncKBService) DeleteKnowledgeBase(context.Context, string) error { return nil }
func (s *processSyncKBService) TogglePinKnowledgeBase(context.Context, string) (*types.KnowledgeBase, error) {
	return nil, nil
}

func (s *processSyncKBService) HybridSearch(context.Context, string, types.SearchParams) ([]*types.SearchResult, error) {
	return nil, nil
}

func (s *processSyncKBService) GetQueryEmbedding(context.Context, string, string) ([]float32, error) {
	return nil, nil
}

func (s *processSyncKBService) ResolveEmbeddingModelKeys(context.Context, []*types.KnowledgeBase) map[string]string {
	return nil
}

func (s *processSyncKBService) CopyKnowledgeBase(context.Context, string, string) (*types.KnowledgeBase, *types.KnowledgeBase, error) {
	return nil, nil, nil
}

func (s *processSyncKBService) DuplicateKnowledgeBase(context.Context, string) (*types.KnowledgeBase, error) {
	return nil, nil
}
func (s *processSyncKBService) GetRepository() interfaces.KnowledgeBaseRepository { return nil }
func (s *processSyncKBService) ProcessKBDelete(context.Context, *asynq.Task) error {
	return nil
}

var _ interfaces.KnowledgeBaseService = (*processSyncKBService)(nil)

type processSyncSyncLogRepo struct {
	logs map[string]*types.SyncLog
}

func (r *processSyncSyncLogRepo) Create(_ context.Context, log *types.SyncLog) error {
	r.logs[log.ID] = log
	return nil
}

func (r *processSyncSyncLogRepo) FindByID(_ context.Context, id string) (*types.SyncLog, error) {
	log, ok := r.logs[id]
	if !ok {
		return nil, errors.New("sync log not found")
	}
	return log, nil
}

func (r *processSyncSyncLogRepo) FindByDataSource(context.Context, string, int, int) ([]*types.SyncLog, error) {
	return nil, nil
}

func (r *processSyncSyncLogRepo) FindLatest(context.Context, string) (*types.SyncLog, error) {
	return nil, nil
}

func (r *processSyncSyncLogRepo) HasRunningSync(context.Context, string) (bool, error) {
	return false, nil
}

func (r *processSyncSyncLogRepo) Update(_ context.Context, log *types.SyncLog) error {
	r.logs[log.ID] = log
	return nil
}

func (r *processSyncSyncLogRepo) UpdateResult(_ context.Context, log *types.SyncLog) error {
	return r.Update(context.Background(), log)
}

func (r *processSyncSyncLogRepo) CancelPendingByDataSource(context.Context, string) error {
	return nil
}
func (r *processSyncSyncLogRepo) CleanupOldLogs(context.Context, int) error { return nil }

func TestAllFetchedItemsFailedError(t *testing.T) {
	err := allFetchedItemsFailedError(&types.SyncResult{
		Total:  2,
		Failed: 2,
		Errors: []string{"doc one: export failed"},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "all fetched items failed during sync (2/2)")
	assert.Contains(t, err.Error(), "doc one: export failed")
}

func TestAllFetchedItemsFailedErrorIgnoresPartialFailure(t *testing.T) {
	err := allFetchedItemsFailedError(&types.SyncResult{
		Total:   3,
		Created: 1,
		Failed:  2,
	})
	require.NoError(t, err)
}

func TestAllFetchedItemsFailedErrorIgnoresSkippedItems(t *testing.T) {
	err := allFetchedItemsFailedError(&types.SyncResult{
		Total:   3,
		Skipped: 3,
	})
	require.NoError(t, err)
}

func TestAllFetchedItemsFailedErrorTruncatesLongDetail(t *testing.T) {
	err := allFetchedItemsFailedError(&types.SyncResult{
		Total:  1,
		Failed: 1,
		Errors: []string{strings.Repeat("x", 600)},
	})
	require.Error(t, err)
	assert.LessOrEqual(t, len(err.Error()), 560)
	assert.Contains(t, err.Error(), "...")
}

type finalizeDataSourceRepo struct {
	interfaces.DataSourceRepository
	dataSource *types.DataSource
}

func (r *finalizeDataSourceRepo) FindByID(context.Context, string) (*types.DataSource, error) {
	return r.dataSource, nil
}

func (r *finalizeDataSourceRepo) UpdateSyncState(_ context.Context, dataSource *types.DataSource) error {
	*r.dataSource = *dataSource
	return nil
}

type finalizeKnowledgeRepo struct {
	interfaces.KnowledgeRepository
	knowledge *types.Knowledge
	err       error
}

func (r *finalizeKnowledgeRepo) GetKnowledgeByID(context.Context, uint64, string) (*types.Knowledge, error) {
	return r.knowledge, r.err
}

type finalizeKnowledgeService struct {
	interfaces.KnowledgeService
	repository interfaces.KnowledgeRepository
}

func (s *finalizeKnowledgeService) GetRepository() interfaces.KnowledgeRepository {
	return s.repository
}

type finalizeConnector struct{ datasource.Connector }

func (finalizeConnector) Type() string { return "finalize-test" }

func TestProcessSyncFinalizeDefersAndThenCommitsCursor(t *testing.T) {
	dataSource := &types.DataSource{
		ID: "ds-finalize", TenantID: 7, Type: "finalize-test",
		ConnectionVersion: 3, Status: types.DataSourceStatusActive,
	}
	deadline := time.Now().UTC().Add(time.Hour)
	resultJSON, err := json.Marshal(&types.DataSourceSyncCheckpoint{
		Result:           types.SyncResult{Total: 1, Created: 1},
		NextCursor:       &types.SyncCursor{ConnectorCursor: map[string]interface{}{"delta": "next"}},
		PendingKnowledge: []types.DataSourcePendingKnowledge{{KnowledgeID: "knowledge-1", Title: "doc"}},
		FinalizeDeadline: deadline,
	})
	require.NoError(t, err)
	syncLog := &types.SyncLog{
		ID: "log-finalize", DataSourceID: dataSource.ID, TenantID: dataSource.TenantID,
		Status: types.SyncLogStatusRunning, Checkpoint: resultJSON,
	}
	syncLogs := &processSyncSyncLogRepo{logs: map[string]*types.SyncLog{syncLog.ID: syncLog}}
	knowledge := &types.Knowledge{
		ID: "knowledge-1", TenantID: dataSource.TenantID, ParseStatus: types.ParseStatusProcessing,
	}
	knowledgeRepo := &finalizeKnowledgeRepo{knowledge: knowledge}
	registry := datasource.NewConnectorRegistry()
	require.NoError(t, registry.Register(finalizeConnector{}))
	service := &DataSourceService{
		dsRepo: &finalizeDataSourceRepo{dataSource: dataSource}, syncLogRepo: syncLogs,
		connectorRegistry: registry,
		content:           NewDataSourceContentManager(&finalizeKnowledgeService{repository: knowledgeRepo}, nil),
	}
	payload, err := json.Marshal(types.DataSourceFinalizePayload{
		DataSourceID: dataSource.ID, TenantID: dataSource.TenantID,
		ConnectionVersion: dataSource.ConnectionVersion, SyncLogID: syncLog.ID,
	})
	require.NoError(t, err)
	task := asynq.NewTask(types.TypeDataSourceFinalize, payload)

	err = service.ProcessSyncFinalize(context.Background(), task)
	require.ErrorIs(t, err, ErrDataSourceIngestionPending)
	assert.Empty(t, dataSource.LastSyncCursor)
	assert.Equal(t, types.SyncLogStatusRunning, syncLog.Status)

	knowledge.ParseStatus = types.ParseStatusCompleted
	require.NoError(t, service.ProcessSyncFinalize(context.Background(), task))
	assert.NotEmpty(t, dataSource.LastSyncCursor)
	assert.Equal(t, types.SyncLogStatusSuccess, syncLog.Status)
	assert.Empty(t, syncLog.Checkpoint)
	require.NotNil(t, syncLog.FinishedAt)
}

func TestProcessSyncFinalizeDoesNotCommitCursorAfterParseFailure(t *testing.T) {
	dataSource := &types.DataSource{
		ID: "ds-failed", TenantID: 9, Type: "finalize-test",
		ConnectionVersion: 1, Status: types.DataSourceStatusActive,
	}
	deadline := time.Now().UTC().Add(time.Hour)
	resultJSON, err := json.Marshal(&types.DataSourceSyncCheckpoint{
		Result:           types.SyncResult{Total: 1, Created: 1},
		NextCursor:       &types.SyncCursor{ConnectorCursor: map[string]interface{}{"delta": "unsafe"}},
		PendingKnowledge: []types.DataSourcePendingKnowledge{{KnowledgeID: "knowledge-failed", Title: "broken"}},
		FinalizeDeadline: deadline,
	})
	require.NoError(t, err)
	syncLog := &types.SyncLog{ID: "log-failed", Status: types.SyncLogStatusRunning, Checkpoint: resultJSON}
	syncLogs := &processSyncSyncLogRepo{logs: map[string]*types.SyncLog{syncLog.ID: syncLog}}
	knowledgeRepo := &finalizeKnowledgeRepo{knowledge: &types.Knowledge{
		ID: "knowledge-failed", TenantID: dataSource.TenantID,
		ParseStatus: types.ParseStatusFailed, ErrorMessage: "parser failed",
	}}
	registry := datasource.NewConnectorRegistry()
	require.NoError(t, registry.Register(finalizeConnector{}))
	service := &DataSourceService{
		dsRepo: &finalizeDataSourceRepo{dataSource: dataSource}, syncLogRepo: syncLogs,
		connectorRegistry: registry,
		content:           NewDataSourceContentManager(&finalizeKnowledgeService{repository: knowledgeRepo}, nil),
	}
	payload, err := json.Marshal(types.DataSourceFinalizePayload{
		DataSourceID: dataSource.ID, TenantID: dataSource.TenantID,
		ConnectionVersion: dataSource.ConnectionVersion, SyncLogID: syncLog.ID,
	})
	require.NoError(t, err)

	require.NoError(t, service.ProcessSyncFinalize(
		context.Background(), asynq.NewTask(types.TypeDataSourceFinalize, payload),
	))
	assert.Empty(t, dataSource.LastSyncCursor)
	assert.Equal(t, types.SyncLogStatusPartial, syncLog.Status)
	assert.Equal(t, 1, syncLog.ItemsFailed)
	assert.Empty(t, syncLog.Checkpoint)
	assert.Contains(t, syncLog.ErrorMessage, "cursor was not advanced")
}

func TestProcessSyncFinalizeCancelsStaleConnectionVersion(t *testing.T) {
	dataSource := &types.DataSource{
		ID: "ds-new-connection", TenantID: 12, Type: "finalize-test",
		ConnectionVersion: 4, Status: types.DataSourceStatusActive,
	}
	syncLog := &types.SyncLog{
		ID: "log-old-connection", DataSourceID: dataSource.ID, TenantID: dataSource.TenantID,
		Status: types.SyncLogStatusRunning, Checkpoint: types.JSON(`{"next_cursor":{"connector_cursor":{"delta":"old"}}}`),
	}
	syncLogs := &processSyncSyncLogRepo{logs: map[string]*types.SyncLog{syncLog.ID: syncLog}}
	service := &DataSourceService{
		dsRepo: &finalizeDataSourceRepo{dataSource: dataSource}, syncLogRepo: syncLogs,
	}
	payload, err := json.Marshal(types.DataSourceFinalizePayload{
		DataSourceID: dataSource.ID, TenantID: dataSource.TenantID,
		ConnectionVersion: 3, SyncLogID: syncLog.ID,
	})
	require.NoError(t, err)

	require.NoError(t, service.ProcessSyncFinalize(
		context.Background(), asynq.NewTask(types.TypeDataSourceFinalize, payload),
	))
	assert.Empty(t, dataSource.LastSyncCursor)
	assert.Equal(t, types.SyncLogStatusCanceled, syncLog.Status)
	assert.Empty(t, syncLog.Checkpoint)
	assert.Contains(t, syncLog.ErrorMessage, "connection changed")
}

func TestProcessSyncFinalizeTimesOutWithoutAdvancingCursor(t *testing.T) {
	dataSource := &types.DataSource{
		ID: "ds-timeout", TenantID: 13, Type: "finalize-test",
		ConnectionVersion: 2, Status: types.DataSourceStatusActive,
	}
	checkpoint, err := json.Marshal(&types.DataSourceSyncCheckpoint{
		Result:           types.SyncResult{Total: 1, Created: 1},
		NextCursor:       &types.SyncCursor{ConnectorCursor: map[string]interface{}{"delta": "unsafe"}},
		PendingKnowledge: []types.DataSourcePendingKnowledge{{KnowledgeID: "knowledge-slow", Title: "slow.md"}},
		FinalizeDeadline: time.Now().UTC().Add(-time.Second),
	})
	require.NoError(t, err)
	syncLog := &types.SyncLog{
		ID: "log-timeout", DataSourceID: dataSource.ID, TenantID: dataSource.TenantID,
		Status: types.SyncLogStatusRunning, Checkpoint: checkpoint,
	}
	syncLogs := &processSyncSyncLogRepo{logs: map[string]*types.SyncLog{syncLog.ID: syncLog}}
	registry := datasource.NewConnectorRegistry()
	require.NoError(t, registry.Register(finalizeConnector{}))
	service := &DataSourceService{
		dsRepo: &finalizeDataSourceRepo{dataSource: dataSource}, syncLogRepo: syncLogs,
		connectorRegistry: registry,
		content: NewDataSourceContentManager(&finalizeKnowledgeService{repository: &finalizeKnowledgeRepo{
			knowledge: &types.Knowledge{
				ID: "knowledge-slow", TenantID: dataSource.TenantID,
				ParseStatus: types.ParseStatusProcessing,
			},
		}}, nil),
	}
	payload, err := json.Marshal(types.DataSourceFinalizePayload{
		DataSourceID: dataSource.ID, TenantID: dataSource.TenantID,
		ConnectionVersion: dataSource.ConnectionVersion, SyncLogID: syncLog.ID,
	})
	require.NoError(t, err)

	require.NoError(t, service.ProcessSyncFinalize(
		context.Background(), asynq.NewTask(types.TypeDataSourceFinalize, payload),
	))
	assert.Empty(t, dataSource.LastSyncCursor)
	assert.Equal(t, types.SyncLogStatusPartial, syncLog.Status)
	assert.Equal(t, 1, syncLog.ItemsFailed)
	assert.Empty(t, syncLog.Checkpoint)
	assert.Contains(t, syncLog.ErrorMessage, "cursor was not advanced")
}

func TestProcessSyncFinalizeClosesExpiredCheckpointAfterStatusReadFailure(t *testing.T) {
	dataSource := &types.DataSource{
		ID: "ds-status-error", TenantID: 14, Type: "finalize-test",
		ConnectionVersion: 2, Status: types.DataSourceStatusActive,
	}
	checkpoint, err := json.Marshal(&types.DataSourceSyncCheckpoint{
		Result:           types.SyncResult{Total: 1, Created: 1},
		NextCursor:       &types.SyncCursor{ConnectorCursor: map[string]interface{}{"delta": "unsafe"}},
		PendingKnowledge: []types.DataSourcePendingKnowledge{{KnowledgeID: "missing", Title: "missing.md"}},
		FinalizeDeadline: time.Now().UTC().Add(-time.Second),
	})
	require.NoError(t, err)
	syncLog := &types.SyncLog{
		ID: "log-status-error", DataSourceID: dataSource.ID, TenantID: dataSource.TenantID,
		Status: types.SyncLogStatusRunning, Checkpoint: checkpoint,
	}
	syncLogs := &processSyncSyncLogRepo{logs: map[string]*types.SyncLog{syncLog.ID: syncLog}}
	registry := datasource.NewConnectorRegistry()
	require.NoError(t, registry.Register(finalizeConnector{}))
	service := &DataSourceService{
		dsRepo: &finalizeDataSourceRepo{dataSource: dataSource}, syncLogRepo: syncLogs,
		connectorRegistry: registry,
		content: NewDataSourceContentManager(&finalizeKnowledgeService{repository: &finalizeKnowledgeRepo{
			err: errors.New("database unavailable"),
		}}, nil),
	}
	payload, err := json.Marshal(types.DataSourceFinalizePayload{
		DataSourceID: dataSource.ID, TenantID: dataSource.TenantID,
		ConnectionVersion: dataSource.ConnectionVersion, SyncLogID: syncLog.ID,
	})
	require.NoError(t, err)

	require.NoError(t, service.ProcessSyncFinalize(
		context.Background(), asynq.NewTask(types.TypeDataSourceFinalize, payload),
	))
	assert.Empty(t, dataSource.LastSyncCursor)
	assert.Equal(t, types.SyncLogStatusPartial, syncLog.Status)
	assert.Equal(t, 1, syncLog.ItemsFailed)
	assert.Empty(t, syncLog.Checkpoint)
	assert.Contains(t, syncLog.ErrorMessage, "cursor was not advanced")
}
