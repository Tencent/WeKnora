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

func TestProcessSyncTerminalRetryCannotOverlapNewRunningSync(t *testing.T) {
	ds := &types.DataSource{
		ID:              "ds-terminal-retry",
		TenantID:        1,
		KnowledgeBaseID: "kb-1",
		Type:            types.ConnectorTypeDingTalk,
		Status:          types.DataSourceStatusActive,
	}
	dsRepo := newKBDeleteDSRepo(ds.KnowledgeBaseID, ds)
	oldLog := &types.SyncLog{
		ID:           "log-old-failed",
		DataSourceID: ds.ID,
		TenantID:     ds.TenantID,
		Status:       types.SyncLogStatusFailed,
		StartedAt:    time.Now().UTC().Add(-time.Minute),
	}
	newLog := &types.SyncLog{
		ID:           "log-new-running",
		DataSourceID: ds.ID,
		TenantID:     ds.TenantID,
		Status:       types.SyncLogStatusRunning,
		StartedAt:    time.Now().UTC(),
	}
	syncLogRepo := &processSyncSyncLogRepo{logs: map[string]*types.SyncLog{
		oldLog.ID: oldLog,
		newLog.ID: newLog,
	}}
	svc := &DataSourceService{
		dsRepo:      dsRepo,
		syncLogRepo: syncLogRepo,
		// Intentionally leave connectorRegistry nil: a terminal redelivery must
		// return before any connector/fetch work even while a newer log is running.
	}
	payload, err := json.Marshal(types.DataSourceSyncPayload{
		DataSourceID: ds.ID,
		TenantID:     ds.TenantID,
		SyncLogID:    oldLog.ID,
	})
	require.NoError(t, err)

	err = svc.ProcessSync(context.Background(), asynq.NewTask(types.TypeDataSourceSync, payload))

	require.NoError(t, err)
	assert.Equal(t, types.SyncLogStatusFailed, oldLog.Status)
	assert.Equal(t, types.SyncLogStatusRunning, newLog.Status)
}

func TestProcessSyncAutomaticallyReconcilesPendingDingTalkCreate(t *testing.T) {
	configJSON, err := (&types.DataSourceConfig{
		Type:        types.ConnectorTypeDingTalk,
		ResourceIDs: []string{"space-1"},
	}).ToJSON()
	require.NoError(t, err)
	oldCursor := types.JSON(`{"connector_cursor":{"revision":"old"}}`)
	nextCursor := &types.SyncCursor{
		ConnectorCursor: map[string]interface{}{"revision": "new"},
	}
	ds := &types.DataSource{
		ID:              "ds-pending-reconcile",
		TenantID:        1,
		KnowledgeBaseID: "kb-1",
		Name:            "DingTalk",
		Type:            types.ConnectorTypeDingTalk,
		Status:          types.DataSourceStatusActive,
		SyncMode:        types.SyncModeIncremental,
		SyncDeletions:   true,
		Config:          configJSON,
		LastSyncCursor:  oldCursor,
	}
	connector := &runtimeSettingsConnector{
		connectorType: types.ConnectorTypeDingTalk,
		fetchItems: []types.FetchedItem{
			{
				ExternalID: "doc-1",
				Title:      "Document",
				FileName:   "document.md",
				Content:    []byte("# document"),
				Metadata:   map[string]string{"revision": "rev-1"},
			},
			{
				ExternalID: "obsolete-doc",
				Title:      "Obsolete document",
				IsDeleted:  true,
			},
		},
		nextCursor: nextCursor,
	}
	registry := datasource.NewConnectorRegistry()
	require.NoError(t, registry.Register(connector))

	obsoleteMetadata, err := json.Marshal(map[string]string{
		"external_id":   "obsolete-doc",
		"datasource_id": ds.ID,
	})
	require.NoError(t, err)
	obsolete := &types.Knowledge{
		ID:          "knowledge-obsolete",
		ParseStatus: types.ParseStatusCompleted,
		Metadata:    types.JSON(obsoleteMetadata),
	}
	knowledgeRepo := &sweepFakeRepo{findAllReturn: []*types.Knowledge{obsolete}}
	created := &types.Knowledge{
		ID:          "knowledge-pending",
		ParseStatus: types.ParseStatusPending,
	}
	knowledgeService := &sweepFakeKS{repo: knowledgeRepo, created: created}
	dsRepo := newKBDeleteDSRepo(ds.KnowledgeBaseID, ds)
	syncLog := &types.SyncLog{
		ID:           "log-pending-reconcile",
		DataSourceID: ds.ID,
		TenantID:     ds.TenantID,
		Status:       types.SyncLogStatusRunning,
		StartedAt:    time.Now().UTC(),
	}
	syncLogs := &processSyncSyncLogRepo{logs: map[string]*types.SyncLog{
		syncLog.ID: syncLog,
	}}
	enqueuer := &recordingDataSourceTaskEnqueuer{}
	svc := &DataSourceService{
		dsRepo:            dsRepo,
		syncLogRepo:       syncLogs,
		knowledgeService:  knowledgeService,
		kbService:         &processSyncKBService{kb: &types.KnowledgeBase{ID: ds.KnowledgeBaseID}},
		taskEnqueuer:      enqueuer,
		connectorRegistry: registry,
		tenantRepo:        &processSyncTenantRepo{},
		tagService:        &processSyncTagService{},
	}
	payload, err := json.Marshal(types.DataSourceSyncPayload{
		DataSourceID: ds.ID,
		TenantID:     ds.TenantID,
		SyncLogID:    syncLog.ID,
	})
	require.NoError(t, err)
	task := asynq.NewTask(types.TypeDataSourceSync, payload)

	err = svc.ProcessSync(context.Background(), task)

	require.NoError(t, err)
	assert.Equal(t, types.SyncLogStatusRunning, syncLog.Status)
	assert.Nil(t, syncLog.FinishedAt)
	assert.Zero(t, syncLog.ItemsFailed)
	assert.JSONEq(t, string(oldCursor), string(ds.LastSyncCursor))
	var firstResult types.SyncResult
	require.NoError(t, json.Unmarshal(syncLog.Result, &firstResult))
	assert.Equal(t, 1, firstResult.Pending)
	assert.Zero(t, firstResult.Deleted)
	assert.Empty(t, knowledgeService.deleted, "old identity must remain while the replacement is pending")
	assert.Equal(t, []string{"create:document.md"}, knowledgeService.events)
	assert.Equal(t, 1, connector.fetchCalls)
	require.Equal(t, 1, enqueuer.recordCount())
	continuation := enqueuer.record(0).task
	knowledgeRepo.findAllReturn = []*types.Knowledge{created, obsolete}

	// The dedicated continuation alone owns the long retry budget. Repeated
	// pending observations return the sentinel for Asynq without enqueuing
	// another continuation.
	err = svc.ProcessSync(context.Background(), continuation)

	require.ErrorIs(t, err, ErrDataSourceIngestPending)
	require.Equal(t, 1, enqueuer.recordCount())
	assert.Equal(t, types.SyncLogStatusRunning, syncLog.Status)
	assert.Equal(t, 2, connector.fetchCalls)

	// The knowledge worker completes asynchronously. The same Asynq task retry
	// then promotes the row, applies the deferred deletion, and is the only
	// attempt allowed to advance cursor.
	created.ParseStatus = types.ParseStatusCompleted

	err = svc.ProcessSync(context.Background(), continuation)

	require.NoError(t, err)
	assert.Equal(t, types.SyncLogStatusSuccess, syncLog.Status)
	require.NotNil(t, syncLog.FinishedAt)
	persistedCursor, err := ds.ParseSyncCursor()
	require.NoError(t, err)
	require.NotNil(t, persistedCursor)
	assert.Equal(t, "new", persistedCursor.ConnectorCursor["revision"])
	assert.Equal(t, 3, connector.fetchCalls)
	assert.Equal(t, []string{"create:document.md", "delete:knowledge-obsolete"}, knowledgeService.events)
	assert.Equal(t, []string{"knowledge-obsolete"}, knowledgeService.deleted)
	require.Len(t, knowledgeRepo.updated, 1)
	assert.NotEqual(
		t,
		"true",
		knowledgeRepo.updated[0].GetMetadata()[dataSourceIngestPendingMetadataKey],
	)
}

func TestProcessSyncInjectsTenantIsolationSettingsBeforeFetch(t *testing.T) {
	const connectorType = "capture-runtime-settings"
	stopAfterCapture := errors.New("stop after capturing connector config")
	capture := &runtimeSettingsConnector{
		connectorType: connectorType,
		fetchErr:      stopAfterCapture,
	}
	registry := datasource.NewConnectorRegistry()
	require.NoError(t, registry.Register(capture))

	configJSON, err := (&types.DataSourceConfig{
		Type:        connectorType,
		ResourceIDs: []string{"resource-1"},
	}).ToJSON()
	require.NoError(t, err)
	ds := &types.DataSource{
		ID:              "ds-runtime",
		TenantID:        42,
		KnowledgeBaseID: "kb-runtime",
		Type:            connectorType,
		Config:          configJSON,
		SyncMode:        types.SyncModeIncremental,
		Status:          types.DataSourceStatusActive,
	}
	dsRepo := newKBDeleteDSRepo(ds.KnowledgeBaseID, ds)
	syncLog := &types.SyncLog{
		ID:           "log-runtime",
		DataSourceID: ds.ID,
		TenantID:     ds.TenantID,
		Status:       types.SyncLogStatusRunning,
		StartedAt:    time.Now().UTC(),
	}
	syncLogRepo := &processSyncSyncLogRepo{logs: map[string]*types.SyncLog{syncLog.ID: syncLog}}
	svc := &DataSourceService{
		dsRepo:            dsRepo,
		syncLogRepo:       syncLogRepo,
		kbService:         &processSyncKBService{kb: &types.KnowledgeBase{ID: ds.KnowledgeBaseID}},
		connectorRegistry: registry,
	}

	payload, err := json.Marshal(types.DataSourceSyncPayload{
		DataSourceID: ds.ID,
		TenantID:     ds.TenantID,
		SyncLogID:    syncLog.ID,
	})
	require.NoError(t, err)

	err = svc.ProcessSync(context.Background(), asynq.NewTask(types.TypeDataSourceSync, payload))
	require.ErrorIs(t, err, stopAfterCapture)
	require.NotNil(t, capture.config)
	assert.Equal(t, "42", capture.config.Settings["tenant_id"])
	assert.Equal(t, ds.ID, capture.config.Settings["data_source_id"])
}

func TestProcessSyncUsesCursorAwareFullReconciliation(t *testing.T) {
	const connectorType = "cursor-aware-full"
	stopAfterCapture := errors.New("stop after cursor-aware full fetch")
	base := &runtimeSettingsConnector{connectorType: connectorType}
	capture := &cursorAwareFullTestConnector{
		runtimeSettingsConnector: base,
		fetchErr:                 stopAfterCapture,
	}
	registry := datasource.NewConnectorRegistry()
	require.NoError(t, registry.Register(capture))

	configJSON, err := (&types.DataSourceConfig{
		Type:        connectorType,
		ResourceIDs: []string{"resource-1"},
	}).ToJSON()
	require.NoError(t, err)
	ds := &types.DataSource{
		ID:              "ds-cursor-aware-full",
		TenantID:        42,
		KnowledgeBaseID: "kb-cursor-aware-full",
		Type:            connectorType,
		Config:          configJSON,
		SyncMode:        types.SyncModeFull,
		Status:          types.DataSourceStatusActive,
		LastSyncCursor:  types.JSON(`{"connector_cursor":{"revision":"previous"}}`),
	}
	dsRepo := newKBDeleteDSRepo(ds.KnowledgeBaseID, ds)
	syncLog := &types.SyncLog{
		ID:           "log-cursor-aware-full",
		DataSourceID: ds.ID,
		TenantID:     ds.TenantID,
		Status:       types.SyncLogStatusRunning,
		StartedAt:    time.Now().UTC(),
	}
	syncLogRepo := &processSyncSyncLogRepo{logs: map[string]*types.SyncLog{syncLog.ID: syncLog}}
	svc := &DataSourceService{
		dsRepo:            dsRepo,
		syncLogRepo:       syncLogRepo,
		kbService:         &processSyncKBService{kb: &types.KnowledgeBase{ID: ds.KnowledgeBaseID}},
		connectorRegistry: registry,
	}
	payload, err := json.Marshal(types.DataSourceSyncPayload{
		DataSourceID: ds.ID,
		TenantID:     ds.TenantID,
		SyncLogID:    syncLog.ID,
	})
	require.NoError(t, err)

	err = svc.ProcessSync(context.Background(), asynq.NewTask(types.TypeDataSourceSync, payload))

	require.ErrorIs(t, err, stopAfterCapture)
	assert.Equal(t, 1, capture.calls)
	require.NotNil(t, capture.cursor)
	assert.Equal(t, "previous", capture.cursor.ConnectorCursor["revision"])
	assert.Equal(t, []string{"resource-1"}, capture.resourceIDs)
}

func TestProcessSyncCancelsTaskQueuedForOlderConfiguration(t *testing.T) {
	oldConfig, err := (&types.DataSourceConfig{
		Type:     "stale-generation",
		Settings: map[string]interface{}{"identity": "old"},
	}).ToJSON()
	require.NoError(t, err)
	newConfig, err := (&types.DataSourceConfig{
		Type:     "stale-generation",
		Settings: map[string]interface{}{"identity": "new"},
	}).ToJSON()
	require.NoError(t, err)
	oldDS := &types.DataSource{
		ID:              "ds-stale-queued",
		TenantID:        43,
		KnowledgeBaseID: "kb-stale-queued",
		Type:            "stale-generation",
		Config:          oldConfig,
		SyncMode:        types.SyncModeIncremental,
	}
	currentDS := *oldDS
	currentDS.Config = newConfig
	repo := newKBDeleteDSRepo(currentDS.KnowledgeBaseID, &currentDS)
	syncLog := &types.SyncLog{
		ID:           "log-stale-queued",
		DataSourceID: currentDS.ID,
		Status:       types.SyncLogStatusRunning,
	}
	syncLogs := &processSyncSyncLogRepo{logs: map[string]*types.SyncLog{syncLog.ID: syncLog}}
	svc := &DataSourceService{dsRepo: repo, syncLogRepo: syncLogs}
	payload, err := json.Marshal(types.DataSourceSyncPayload{
		DataSourceID:      currentDS.ID,
		SyncLogID:         syncLog.ID,
		ConfigFingerprint: oldDS.SyncConfigFingerprint(),
	})
	require.NoError(t, err)

	err = svc.ProcessSync(context.Background(), asynq.NewTask(types.TypeDataSourceSync, payload))
	require.NoError(t, err)
	assert.Equal(t, types.SyncLogStatusCanceled, syncLog.Status)
	assert.Contains(t, syncLog.ErrorMessage, "configuration changed")
}

func TestProcessSyncRechecksConfigurationAfterFetchBeforeIngestion(t *testing.T) {
	const connectorType = "generation-change-during-fetch"
	oldConfig, err := (&types.DataSourceConfig{
		Type:        connectorType,
		ResourceIDs: []string{"resource-1"},
		Settings:    map[string]interface{}{"identity": "old"},
	}).ToJSON()
	require.NoError(t, err)
	newConfig, err := (&types.DataSourceConfig{
		Type:        connectorType,
		ResourceIDs: []string{"resource-2"},
		Settings:    map[string]interface{}{"identity": "new"},
	}).ToJSON()
	require.NoError(t, err)
	ds := &types.DataSource{
		ID:              "ds-change-during-fetch",
		TenantID:        44,
		KnowledgeBaseID: "kb-change-during-fetch",
		Type:            connectorType,
		Config:          oldConfig,
		SyncMode:        types.SyncModeIncremental,
		Status:          types.DataSourceStatusActive,
	}
	expected := ds.SyncConfigFingerprint()
	capture := &runtimeSettingsConnector{
		connectorType: connectorType,
		fetchHook: func() {
			ds.Config = newConfig
		},
		fetchItems: []types.FetchedItem{{
			ExternalID: "must-not-be-ingested",
			Title:      "Stale item",
			Content:    []byte("stale"),
		}},
	}
	registry := datasource.NewConnectorRegistry()
	require.NoError(t, registry.Register(capture))
	repo := newKBDeleteDSRepo(ds.KnowledgeBaseID, ds)
	syncLog := &types.SyncLog{
		ID:           "log-change-during-fetch",
		DataSourceID: ds.ID,
		Status:       types.SyncLogStatusRunning,
	}
	syncLogs := &processSyncSyncLogRepo{logs: map[string]*types.SyncLog{syncLog.ID: syncLog}}
	svc := &DataSourceService{
		dsRepo:            repo,
		syncLogRepo:       syncLogs,
		kbService:         &processSyncKBService{kb: &types.KnowledgeBase{ID: ds.KnowledgeBaseID}},
		connectorRegistry: registry,
	}
	payload, err := json.Marshal(types.DataSourceSyncPayload{
		DataSourceID:      ds.ID,
		SyncLogID:         syncLog.ID,
		ConfigFingerprint: expected,
	})
	require.NoError(t, err)

	err = svc.ProcessSync(context.Background(), asynq.NewTask(types.TypeDataSourceSync, payload))
	require.NoError(t, err)
	assert.Equal(t, 1, capture.fetchCalls)
	assert.Equal(t, types.SyncLogStatusCanceled, syncLog.Status)
	assert.Contains(t, syncLog.ErrorMessage, "configuration changed")
}

func TestValidateCredentialsInjectsTenantIsolationSettingsFromContext(t *testing.T) {
	const connectorType = "capture-validation-settings"
	capture := &runtimeSettingsConnector{connectorType: connectorType}
	registry := datasource.NewConnectorRegistry()
	require.NoError(t, registry.Register(capture))
	svc := &DataSourceService{connectorRegistry: registry}
	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(42))

	require.NoError(t, svc.ValidateCredentials(
		ctx,
		connectorType,
		map[string]interface{}{"secret": "fixture-secret"},
	))
	require.NotNil(t, capture.config)
	assert.Equal(t, "42", capture.config.Settings["tenant_id"])
	assert.Equal(t, "credential-validation", capture.config.Settings["data_source_id"])
}

func TestStoredConfigValidationInjectsDataSourceIdentity(t *testing.T) {
	const connectorType = "capture-stored-validation-settings"
	capture := &runtimeSettingsConnector{connectorType: connectorType}
	registry := datasource.NewConnectorRegistry()
	require.NoError(t, registry.Register(capture))
	configJSON, err := (&types.DataSourceConfig{
		Type:        connectorType,
		Credentials: map[string]interface{}{"secret": "fixture-secret"},
	}).ToJSON()
	require.NoError(t, err)
	ds := &types.DataSource{
		ID:       "ds-validation",
		TenantID: 77,
		Type:     connectorType,
		Config:   configJSON,
	}
	svc := &DataSourceService{connectorRegistry: registry}

	require.NoError(t, svc.validateDataSourceConfig(context.Background(), ds))
	require.NotNil(t, capture.config)
	assert.Equal(t, "77", capture.config.Settings["tenant_id"])
	assert.Equal(t, ds.ID, capture.config.Settings["data_source_id"])
}

func TestValidateConnectionFailureKeepsPausedSourcePaused(t *testing.T) {
	const connectorType = "paused-validation"
	validationErr := errors.New("credentials rejected")
	capture := &runtimeSettingsConnector{
		connectorType: connectorType,
		validateErr:   validationErr,
	}
	registry := datasource.NewConnectorRegistry()
	require.NoError(t, registry.Register(capture))
	configJSON, err := (&types.DataSourceConfig{Type: connectorType}).ToJSON()
	require.NoError(t, err)
	repo := &validationStateDataSourceRepo{current: &types.DataSource{
		ID:       "ds-paused-validation",
		TenantID: 1,
		Type:     connectorType,
		Status:   types.DataSourceStatusPaused,
		Config:   configJSON,
	}}
	svc := &DataSourceService{connectorRegistry: registry, dsRepo: repo}

	err = svc.ValidateConnection(context.Background(), repo.current.ID)

	require.ErrorIs(t, err, validationErr)
	assert.Equal(t, types.DataSourceStatusPaused, repo.current.Status)
	assert.Equal(t, validationErr.Error(), repo.current.ErrorMessage)
	assert.Equal(t, 1, repo.validationUpdates)
}

func TestValidateConnectionCannotUndoPauseThatRacesValidation(t *testing.T) {
	const connectorType = "concurrent-pause-validation"
	validationErr := errors.New("credentials rejected")
	configJSON, err := (&types.DataSourceConfig{Type: connectorType}).ToJSON()
	require.NoError(t, err)
	repo := &validationStateDataSourceRepo{current: &types.DataSource{
		ID:       "ds-concurrent-pause-validation",
		TenantID: 1,
		Type:     connectorType,
		Status:   types.DataSourceStatusActive,
		Config:   configJSON,
	}}
	capture := &runtimeSettingsConnector{
		connectorType: connectorType,
		validateErr:   validationErr,
		validateHook: func() {
			repo.current.Status = types.DataSourceStatusPaused
		},
	}
	registry := datasource.NewConnectorRegistry()
	require.NoError(t, registry.Register(capture))
	svc := &DataSourceService{connectorRegistry: registry, dsRepo: repo}

	err = svc.ValidateConnection(context.Background(), repo.current.ID)

	require.ErrorIs(t, err, validationErr)
	assert.Equal(t, types.DataSourceStatusPaused, repo.current.Status)
	assert.Empty(t, repo.current.ErrorMessage)
	assert.Equal(t, 1, repo.validationUpdates)
	assert.Equal(t, 1, repo.validationCASMisses)
}

func TestPreviewResourcesUsesCandidateCredentialsAndTenantIsolation(t *testing.T) {
	const connectorType = "capture-preview-settings"
	capture := &runtimeSettingsConnector{
		connectorType: connectorType,
		resources: []types.Resource{{
			ExternalID: "candidate-resource",
			Name:       "Candidate resource",
		}},
	}
	registry := datasource.NewConnectorRegistry()
	require.NoError(t, registry.Register(capture))
	svc := &DataSourceService{connectorRegistry: registry}
	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(42))

	resources, err := svc.PreviewResources(
		ctx,
		connectorType,
		"",
		map[string]interface{}{"secret": "candidate-secret"},
		map[string]interface{}{"region": "candidate-region"},
		"candidate-parent",
		false,
	)
	require.NoError(t, err)
	require.Len(t, resources, 1)
	assert.Equal(t, "candidate-resource", resources[0].ExternalID)
	require.NotNil(t, capture.config)
	assert.Equal(t, "candidate-secret", capture.config.Credentials["secret"])
	assert.Equal(t, "candidate-region", capture.config.Settings["region"])
	assert.Equal(t, "42", capture.config.Settings["tenant_id"])
	assert.Equal(t, credentialPreviewDataSourceID, capture.config.Settings["data_source_id"])
	assert.Equal(t, "candidate-parent", capture.parentID)
}

func TestPreviewResourcesMergesOwnedStoredCredentialsWithCandidateSettings(t *testing.T) {
	const connectorType = "capture-stored-preview"
	capture := &runtimeSettingsConnector{
		connectorType: connectorType,
		resources:     []types.Resource{{ExternalID: "new-setting-resource"}},
	}
	registry := datasource.NewConnectorRegistry()
	require.NoError(t, registry.Register(capture))

	configJSON, err := (&types.DataSourceConfig{
		Type:        connectorType,
		Credentials: map[string]interface{}{"secret": "stored-secret"},
		Settings:    map[string]interface{}{"feed": "old-setting"},
	}).ToJSON()
	require.NoError(t, err)
	existing := &types.DataSource{
		ID:              "ds-stored-preview",
		TenantID:        51,
		KnowledgeBaseID: "kb-stored-preview",
		Type:            connectorType,
		Config:          configJSON,
	}
	repo := &recordingDataSourceRepo{existing: existing}
	svc := &DataSourceService{
		dsRepo:            repo,
		connectorRegistry: registry,
	}
	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(51))

	resources, err := svc.PreviewResources(
		ctx,
		connectorType,
		existing.ID,
		nil,
		map[string]interface{}{"feed": "candidate-setting"},
		"",
		false,
	)
	require.NoError(t, err)
	require.Len(t, resources, 1)
	assert.Equal(t, "stored-secret", capture.config.Credentials["secret"])
	assert.Equal(t, "candidate-setting", capture.config.Settings["feed"])
	assert.Equal(t, "51", capture.config.Settings["tenant_id"])
	assert.Equal(t, existing.ID, capture.config.Settings["data_source_id"])
}

func TestPreviewResourcesValidateOnlyDoesNotListOrPersist(t *testing.T) {
	const connectorType = "capture-preview-validation"
	capture := &runtimeSettingsConnector{connectorType: connectorType}
	registry := datasource.NewConnectorRegistry()
	require.NoError(t, registry.Register(capture))
	svc := &DataSourceService{connectorRegistry: registry}
	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(52))

	resources, err := svc.PreviewResources(
		ctx,
		connectorType,
		"",
		map[string]interface{}{"secret": "candidate-secret"},
		map[string]interface{}{"feed": "candidate-setting"},
		"",
		true,
	)
	require.NoError(t, err)
	assert.Empty(t, resources)
	require.NotNil(t, capture.config)
	assert.Zero(t, capture.listCalls)
}

func TestReconfigureDataSourceValidatesAndPersistsCandidateAtomically(t *testing.T) {
	const connectorType = "capture-atomic-reconfigure"
	capture := &runtimeSettingsConnector{connectorType: connectorType}
	registry := datasource.NewConnectorRegistry()
	require.NoError(t, registry.Register(capture))

	existingConfig, err := (&types.DataSourceConfig{
		Type:        connectorType,
		Credentials: map[string]interface{}{"secret": "old-secret"},
		ResourceIDs: []string{"old-resource"},
	}).ToJSON()
	require.NoError(t, err)
	existing := &types.DataSource{
		ID:              "ds-reconfigure",
		TenantID:        77,
		KnowledgeBaseID: "kb-reconfigure",
		Type:            connectorType,
		Config:          existingConfig,
	}
	repo := &recordingDataSourceRepo{existing: existing}
	svc := &DataSourceService{
		dsRepo:            repo,
		connectorRegistry: registry,
	}

	candidateConfig, err := (&types.DataSourceConfig{
		Type:        connectorType,
		ResourceIDs: []string{"candidate-resource"},
		Settings:    map[string]interface{}{"region": "candidate-region"},
	}).ToJSON()
	require.NoError(t, err)
	candidate := &types.DataSource{
		ID:              existing.ID,
		TenantID:        existing.TenantID,
		KnowledgeBaseID: existing.KnowledgeBaseID,
		Name:            "Reconfigured source",
		Type:            connectorType,
		Config:          candidateConfig,
	}

	updated, err := svc.ReconfigureDataSource(
		context.Background(),
		candidate,
		map[string]interface{}{"secret": "candidate-secret"},
	)
	require.NoError(t, err)
	require.Equal(t, candidate, updated)
	require.Len(t, repo.updates, 1)
	assert.JSONEq(t, `{}`, string(repo.updates[0].LastSyncCursor))

	require.NotNil(t, capture.config)
	assert.Equal(t, "candidate-secret", capture.config.Credentials["secret"])
	assert.Equal(t, []string{"candidate-resource"}, capture.config.ResourceIDs)
	assert.Equal(t, "77", capture.config.Settings["tenant_id"])
	assert.Equal(t, existing.ID, capture.config.Settings["data_source_id"])

	persisted, err := repo.updates[0].ParseConfig()
	require.NoError(t, err)
	require.NotNil(t, persisted)
	assert.Equal(t, "candidate-secret", persisted.Credentials["secret"])
	assert.Equal(t, []string{"candidate-resource"}, persisted.ResourceIDs)
	assert.Equal(t, "candidate-region", persisted.Settings["region"])
	assert.NotContains(t, persisted.Settings, "tenant_id")
	assert.NotContains(t, persisted.Settings, "data_source_id")
}

func TestReconfigureDataSourceValidationFailureLeavesRepositoryUntouched(t *testing.T) {
	const connectorType = "reject-atomic-reconfigure"
	validationErr := errors.New("candidate rejected")
	capture := &runtimeSettingsConnector{
		connectorType: connectorType,
		validateErr:   validationErr,
	}
	registry := datasource.NewConnectorRegistry()
	require.NoError(t, registry.Register(capture))

	existingConfig, err := (&types.DataSourceConfig{
		Type:        connectorType,
		Credentials: map[string]interface{}{"secret": "old-secret"},
		ResourceIDs: []string{"old-resource"},
	}).ToJSON()
	require.NoError(t, err)
	existing := &types.DataSource{
		ID:              "ds-reconfigure-failure",
		TenantID:        88,
		KnowledgeBaseID: "kb-reconfigure",
		Type:            connectorType,
		Config:          existingConfig,
	}
	repo := &recordingDataSourceRepo{existing: existing}
	svc := &DataSourceService{
		dsRepo:            repo,
		connectorRegistry: registry,
	}
	candidateConfig, err := (&types.DataSourceConfig{
		Type:        connectorType,
		ResourceIDs: []string{"candidate-resource"},
	}).ToJSON()
	require.NoError(t, err)

	_, err = svc.ReconfigureDataSource(
		context.Background(),
		&types.DataSource{
			ID:              existing.ID,
			TenantID:        existing.TenantID,
			KnowledgeBaseID: existing.KnowledgeBaseID,
			Type:            connectorType,
			Config:          candidateConfig,
		},
		map[string]interface{}{"secret": "bad-candidate-secret"},
	)
	require.ErrorIs(t, err, validationErr)
	assert.Empty(t, repo.updates)
	assert.Empty(t, existing.LastSyncCursor)

	stillStored, err := existing.ParseConfig()
	require.NoError(t, err)
	assert.Equal(t, "old-secret", stillStored.Credentials["secret"])
	assert.Equal(t, []string{"old-resource"}, stillStored.ResourceIDs)
}

func TestUpdateDataSourceCredentialsResetsIncrementalCursorAtomically(t *testing.T) {
	const connectorType = "capture-credential-reset"
	capture := &runtimeSettingsConnector{connectorType: connectorType}
	registry := datasource.NewConnectorRegistry()
	require.NoError(t, registry.Register(capture))

	configJSON, err := (&types.DataSourceConfig{
		Type:        connectorType,
		Credentials: map[string]interface{}{"secret": "old-secret"},
		ResourceIDs: []string{"resource-1"},
	}).ToJSON()
	require.NoError(t, err)
	existing := &types.DataSource{
		ID:              "ds-credential-reset",
		TenantID:        99,
		KnowledgeBaseID: "kb-credential-reset",
		Type:            connectorType,
		Config:          configJSON,
		LastSyncCursor:  types.JSON(`{"connector_cursor":{"doc_revisions":{"n1":"1000"}}}`),
	}
	repo := &recordingDataSourceRepo{existing: existing}
	svc := &DataSourceService{
		dsRepo:            repo,
		connectorRegistry: registry,
	}

	_, err = svc.UpdateDataSourceCredentials(
		context.Background(),
		existing.ID,
		map[string]interface{}{"secret": "candidate-secret"},
	)
	require.NoError(t, err)
	require.Len(t, repo.updates, 1)
	assert.JSONEq(t, `{}`, string(repo.updates[0].LastSyncCursor))

	persisted, err := repo.updates[0].ParseConfig()
	require.NoError(t, err)
	assert.Equal(t, "candidate-secret", persisted.Credentials["secret"])
}

func TestCredentialMutationCursorPreservesDingTalkCleanupEvidenceOnly(t *testing.T) {
	previous := types.JSON(`{
		"connector_cursor":{
			"identity_fingerprint":"old",
			"doc_revisions":{"old-doc":"1000"}
		}
	}`)
	dingTalk := credentialMutationCursor(types.ConnectorTypeDingTalk, previous)
	assert.JSONEq(t, string(previous), string(dingTalk))
	dingTalk[0] = '['
	assert.NotEqual(t, string(previous), string(dingTalk), "cursor must be cloned")

	other := credentialMutationCursor(types.ConnectorTypeFeishu, previous)
	assert.JSONEq(t, `{}`, string(other))
}

func TestCredentialMutationsRejectRunningSyncWithoutRepositoryWrites(t *testing.T) {
	const connectorType = "capture-running-credential-mutation"
	newService := func(t *testing.T) (*DataSourceService, *recordingDataSourceRepo, *types.DataSource) {
		t.Helper()
		registry := datasource.NewConnectorRegistry()
		require.NoError(t, registry.Register(&runtimeSettingsConnector{connectorType: connectorType}))
		configJSON, err := (&types.DataSourceConfig{
			Type:        connectorType,
			Credentials: map[string]interface{}{"secret": "old-secret"},
			ResourceIDs: []string{"old-resource"},
		}).ToJSON()
		require.NoError(t, err)
		existing := &types.DataSource{
			ID:              "ds-running-mutation",
			TenantID:        101,
			KnowledgeBaseID: "kb-running-mutation",
			Type:            connectorType,
			Config:          configJSON,
			LastSyncCursor:  types.JSON(`{"connector_cursor":{"revision":"old"}}`),
		}
		repo := &recordingDataSourceRepo{existing: existing}
		return &DataSourceService{
			dsRepo:            repo,
			syncLogRepo:       &runningSyncLogRepo{results: []bool{true}},
			connectorRegistry: registry,
		}, repo, existing
	}
	assertUntouched := func(t *testing.T, repo *recordingDataSourceRepo, existing *types.DataSource) {
		t.Helper()
		assert.Empty(t, repo.updates)
		assert.JSONEq(t, `{"connector_cursor":{"revision":"old"}}`, string(existing.LastSyncCursor))
		stored, err := existing.ParseConfig()
		require.NoError(t, err)
		assert.Equal(t, "old-secret", stored.Credentials["secret"])
		assert.Equal(t, []string{"old-resource"}, stored.ResourceIDs)
	}

	t.Run("atomic reconfigure", func(t *testing.T) {
		svc, repo, existing := newService(t)
		candidateConfig, err := (&types.DataSourceConfig{
			Type:        connectorType,
			ResourceIDs: []string{"candidate-resource"},
		}).ToJSON()
		require.NoError(t, err)
		_, err = svc.ReconfigureDataSource(context.Background(), &types.DataSource{
			ID:              existing.ID,
			TenantID:        existing.TenantID,
			KnowledgeBaseID: existing.KnowledgeBaseID,
			Type:            connectorType,
			Config:          candidateConfig,
		}, map[string]interface{}{"secret": "candidate-secret"})
		require.ErrorIs(t, err, datasource.ErrSyncInProgress)
		assertUntouched(t, repo, existing)
	})

	t.Run("resource and settings update", func(t *testing.T) {
		svc, repo, existing := newService(t)
		candidateConfig, err := (&types.DataSourceConfig{
			Type:        connectorType,
			ResourceIDs: []string{"candidate-resource"},
			Settings:    map[string]interface{}{"region": "candidate"},
		}).ToJSON()
		require.NoError(t, err)
		_, err = svc.UpdateDataSource(context.Background(), &types.DataSource{
			ID:              existing.ID,
			TenantID:        existing.TenantID,
			KnowledgeBaseID: existing.KnowledgeBaseID,
			Type:            connectorType,
			Config:          candidateConfig,
		})
		require.ErrorIs(t, err, datasource.ErrSyncInProgress)
		assertUntouched(t, repo, existing)
	})

	t.Run("direct credential update", func(t *testing.T) {
		svc, repo, existing := newService(t)
		_, err := svc.UpdateDataSourceCredentials(
			context.Background(),
			existing.ID,
			map[string]interface{}{"secret": "candidate-secret"},
		)
		require.ErrorIs(t, err, datasource.ErrSyncInProgress)
		assertUntouched(t, repo, existing)
	})

	t.Run("credential clear", func(t *testing.T) {
		svc, repo, existing := newService(t)
		err := svc.ClearDataSourceCredentials(context.Background(), existing.ID)
		require.ErrorIs(t, err, datasource.ErrSyncInProgress)
		assertUntouched(t, repo, existing)
	})
}

func TestUpdateDataSourcePreservesRawConfigForLogicalNoop(t *testing.T) {
	rawConfig := types.JSON(`{
		"settings":{"base_url":"https://api.dingtalk.com"},
		"resource_ids":["workspace/document"],
		"credentials":{},
		"type":"dingtalk"
	}`)
	existing := &types.DataSource{
		ID:                   "ds-metadata-only",
		TenantID:             101,
		KnowledgeBaseID:      "kb-metadata-only",
		Name:                 "Before",
		Type:                 types.ConnectorTypeDingTalk,
		Config:               append(types.JSON(nil), rawConfig...),
		SyncMode:             types.SyncModeIncremental,
		ConflictStrategy:     types.ConflictStrategyOverwrite,
		Status:               types.DataSourceStatusActive,
		SyncLogRetentionDays: 30,
	}
	beforeFingerprint := existing.SyncConfigFingerprint()
	repo := &recordingDataSourceRepo{existing: existing}
	svc := &DataSourceService{dsRepo: repo}

	updated, err := svc.UpdateDataSource(context.Background(), &types.DataSource{
		ID:   existing.ID,
		Name: "After",
	})

	require.NoError(t, err)
	require.Len(t, repo.updates, 1)
	assert.Equal(t, "After", updated.Name)
	assert.Equal(t, string(rawConfig), string(repo.updates[0].Config))
	assert.Equal(t, beforeFingerprint, repo.updates[0].SyncConfigFingerprint())
}

func TestUpdateDataSourceRejectsMalformedConfigWithoutRepositoryWrite(t *testing.T) {
	storedConfig, err := (&types.DataSourceConfig{
		Type:        types.ConnectorTypeDingTalk,
		Credentials: map[string]interface{}{},
	}).ToJSON()
	require.NoError(t, err)
	existing := &types.DataSource{
		ID:                   "ds-malformed-update",
		TenantID:             101,
		KnowledgeBaseID:      "kb-malformed-update",
		Type:                 types.ConnectorTypeDingTalk,
		Config:               storedConfig,
		SyncMode:             types.SyncModeIncremental,
		ConflictStrategy:     types.ConflictStrategyOverwrite,
		Status:               types.DataSourceStatusActive,
		SyncLogRetentionDays: 30,
	}
	repo := &recordingDataSourceRepo{existing: existing}
	svc := &DataSourceService{dsRepo: repo}

	_, err = svc.UpdateDataSource(context.Background(), &types.DataSource{
		ID:     existing.ID,
		Config: types.JSON(`[]`),
	})

	require.ErrorIs(t, err, datasource.ErrInvalidConfig)
	assert.Empty(t, repo.updates)
	assert.Equal(t, string(storedConfig), string(existing.Config))
}

func TestApplySyncOutcomeStatusDerivesRunProposal(t *testing.T) {
	tests := []struct {
		name             string
		failed           bool
		runStartedPaused bool
		want             string
	}{
		{
			name:   "active failed run proposes error",
			failed: true,
			want:   types.DataSourceStatusError,
		},
		{
			name:             "paused run proposes paused",
			failed:           true,
			runStartedPaused: true,
			want:             types.DataSourceStatusPaused,
		},
		{
			name: "active successful run proposes active",
			want: types.DataSourceStatusActive,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			runningCopy := &types.DataSource{ID: "ds-status-proposal"}
			applySyncOutcomeStatus(runningCopy, tt.failed, tt.runStartedPaused)
			assert.Equal(t, tt.want, runningCopy.Status)
		})
	}
}

type abaSyncOutcomeRepo struct {
	interfaces.DataSourceRepository
	currentStatus   string
	staleReadStatus string
	findCalls       int
	proposedStatus  string
}

func (r *abaSyncOutcomeRepo) FindByID(_ context.Context, id string) (*types.DataSource, error) {
	r.findCalls++
	return &types.DataSource{ID: id, Status: r.staleReadStatus}, nil
}

func (r *abaSyncOutcomeRepo) UpdateSyncStateIfStatusUnchanged(
	_ context.Context,
	ds *types.DataSource,
	expectedStatus string,
) error {
	r.proposedStatus = ds.Status
	if r.currentStatus == expectedStatus {
		r.currentStatus = ds.Status
	}
	return nil
}

func TestSyncOutcomeDoesNotUndoResumeAfterStalePauseRead(t *testing.T) {
	// Model active -> paused -> active: the database is active again, while a
	// stale read could still report paused. The proposal phase must not read it;
	// the repository's atomic expected-status CASE is the sole race arbiter.
	repo := &abaSyncOutcomeRepo{
		currentStatus:   types.DataSourceStatusActive,
		staleReadStatus: types.DataSourceStatusPaused,
	}
	svc := &DataSourceService{dsRepo: repo}
	run := &types.DataSource{ID: "ds-aba", Status: types.DataSourceStatusActive}

	applySyncOutcomeStatus(run, false, false)
	err := svc.persistSyncOutcomeState(context.Background(), run, types.DataSourceStatusActive)

	require.NoError(t, err)
	assert.Zero(t, repo.findCalls, "outcome proposal must not perform a stale pre-read")
	assert.Equal(t, types.DataSourceStatusActive, repo.proposedStatus)
	assert.Equal(t, types.DataSourceStatusActive, repo.currentStatus)
}

func TestReconfigureDataSourceRechecksSyncAfterCandidateValidation(t *testing.T) {
	const connectorType = "capture-reconfigure-sync-race"
	capture := &runtimeSettingsConnector{connectorType: connectorType}
	registry := datasource.NewConnectorRegistry()
	require.NoError(t, registry.Register(capture))
	existingConfig, err := (&types.DataSourceConfig{
		Type:        connectorType,
		Credentials: map[string]interface{}{"secret": "old-secret"},
	}).ToJSON()
	require.NoError(t, err)
	existing := &types.DataSource{
		ID:              "ds-reconfigure-sync-race",
		TenantID:        102,
		KnowledgeBaseID: "kb-reconfigure-sync-race",
		Type:            connectorType,
		Config:          existingConfig,
	}
	repo := &recordingDataSourceRepo{existing: existing}
	syncLogs := &runningSyncLogRepo{results: []bool{false, true}}
	svc := &DataSourceService{
		dsRepo:            repo,
		syncLogRepo:       syncLogs,
		connectorRegistry: registry,
	}
	candidateConfig, err := (&types.DataSourceConfig{Type: connectorType}).ToJSON()
	require.NoError(t, err)

	_, err = svc.ReconfigureDataSource(context.Background(), &types.DataSource{
		ID:              existing.ID,
		TenantID:        existing.TenantID,
		KnowledgeBaseID: existing.KnowledgeBaseID,
		Type:            connectorType,
		Config:          candidateConfig,
	}, map[string]interface{}{"secret": "candidate-secret"})

	require.ErrorIs(t, err, datasource.ErrSyncInProgress)
	assert.Equal(t, 2, syncLogs.calls)
	require.NotNil(t, capture.config, "candidate validation should occur between the two guards")
	assert.Empty(t, repo.updates)
}

type processSyncKBService struct {
	getErr error
	kb     *types.KnowledgeBase
}

type processSyncTenantRepo struct {
	interfaces.TenantRepository
}

func (r *processSyncTenantRepo) GetTenantByID(
	_ context.Context,
	id uint64,
) (*types.Tenant, error) {
	return &types.Tenant{ID: id}, nil
}

type processSyncTagService struct {
	interfaces.KnowledgeTagService
}

func (s *processSyncTagService) FindOrCreateTagByName(
	context.Context,
	string,
	string,
) (*types.KnowledgeTag, error) {
	return nil, nil
}

func (s *processSyncKBService) CreateKnowledgeBase(context.Context, *types.KnowledgeBase) (*types.KnowledgeBase, error) {
	return nil, nil
}
func (s *processSyncKBService) GetKnowledgeBaseByID(context.Context, string) (*types.KnowledgeBase, error) {
	return s.kb, s.getErr
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

type runtimeSettingsConnector struct {
	connectorType string
	config        *types.DataSourceConfig
	fetchErr      error
	validateErr   error
	validateHook  func()
	listErr       error
	resources     []types.Resource
	parentID      string
	listCalls     int
	fetchHook     func()
	fetchItems    []types.FetchedItem
	nextCursor    *types.SyncCursor
	fetchCalls    int
}

type cursorAwareFullTestConnector struct {
	*runtimeSettingsConnector
	cursor      *types.SyncCursor
	resourceIDs []string
	fetchErr    error
	calls       int
}

func (c *cursorAwareFullTestConnector) FetchAllWithCursor(
	_ context.Context,
	config *types.DataSourceConfig,
	resourceIDs []string,
	cursor *types.SyncCursor,
) ([]types.FetchedItem, *types.SyncCursor, error) {
	c.config = config
	c.cursor = cursor
	c.resourceIDs = append([]string(nil), resourceIDs...)
	c.calls++
	return nil, nil, c.fetchErr
}

func (c *runtimeSettingsConnector) Type() string { return c.connectorType }
func (c *runtimeSettingsConnector) Validate(_ context.Context, config *types.DataSourceConfig) error {
	c.config = config
	if c.validateHook != nil {
		c.validateHook()
	}
	return c.validateErr
}
func (c *runtimeSettingsConnector) ListResources(
	_ context.Context, config *types.DataSourceConfig, parentID string,
) ([]types.Resource, error) {
	c.config = config
	c.parentID = parentID
	c.listCalls++
	return c.resources, c.listErr
}
func (c *runtimeSettingsConnector) ResolveResourceAncestors(
	context.Context, *types.DataSourceConfig, []string,
) ([]string, error) {
	return nil, nil
}
func (c *runtimeSettingsConnector) FetchAll(
	_ context.Context, config *types.DataSourceConfig, _ []string,
) ([]types.FetchedItem, error) {
	c.config = config
	c.fetchCalls++
	if c.fetchHook != nil {
		c.fetchHook()
	}
	return c.fetchItems, c.fetchErr
}
func (c *runtimeSettingsConnector) FetchIncremental(
	_ context.Context, config *types.DataSourceConfig, _ *types.SyncCursor,
) ([]types.FetchedItem, *types.SyncCursor, error) {
	c.config = config
	c.fetchCalls++
	if c.fetchHook != nil {
		c.fetchHook()
	}
	return c.fetchItems, c.nextCursor, c.fetchErr
}

type recordingDataSourceRepo struct {
	interfaces.DataSourceRepository
	existing *types.DataSource
	updates  []*types.DataSource
}

type validationStateDataSourceRepo struct {
	interfaces.DataSourceRepository
	current             *types.DataSource
	validationUpdates   int
	validationCASMisses int
}

func (r *validationStateDataSourceRepo) FindByID(
	_ context.Context,
	id string,
) (*types.DataSource, error) {
	if r.current == nil || r.current.ID != id {
		return nil, errors.New("data source not found")
	}
	copyOfDS := *r.current
	copyOfDS.Config = append(types.JSON(nil), r.current.Config...)
	return &copyOfDS, nil
}

func (r *validationStateDataSourceRepo) UpdateValidationStateIfConfigUnchanged(
	_ context.Context,
	id string,
	expectedConfig types.JSON,
	expectedStatus string,
	status string,
	errorMessage string,
) (bool, error) {
	r.validationUpdates++
	if r.current == nil || r.current.ID != id ||
		r.current.Config.ToString() != expectedConfig.ToString() ||
		r.current.Status != expectedStatus {
		r.validationCASMisses++
		return false, nil
	}
	r.current.Status = status
	r.current.ErrorMessage = errorMessage
	return true, nil
}

func (r *recordingDataSourceRepo) FindByID(_ context.Context, id string) (*types.DataSource, error) {
	if r.existing == nil || r.existing.ID != id {
		return nil, errors.New("data source not found")
	}
	return r.existing, nil
}

func (r *recordingDataSourceRepo) Update(_ context.Context, ds *types.DataSource) error {
	copyOfDS := *ds
	copyOfDS.Config = append(types.JSON(nil), ds.Config...)
	r.updates = append(r.updates, &copyOfDS)
	// Mirror the production repository's read-after-write behavior. Update and
	// Reconfigure reload the authoritative row so scheduler reconciliation uses
	// the persisted state rather than the caller's possibly stale snapshot.
	r.existing = &copyOfDS
	return nil
}

type runningSyncLogRepo struct {
	interfaces.SyncLogRepository
	results []bool
	err     error
	calls   int
}

func (r *runningSyncLogRepo) HasRunningSync(context.Context, string) (bool, error) {
	r.calls++
	if r.err != nil {
		return false, r.err
	}
	if len(r.results) == 0 {
		return false, nil
	}
	index := r.calls - 1
	if index >= len(r.results) {
		index = len(r.results) - 1
	}
	return r.results[index], nil
}

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
		return nil, datasource.ErrSyncLogNotFound
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
		Errors: []types.SyncItemError{{Message: "doc one: export failed"}},
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
		Errors: []types.SyncItemError{{Message: strings.Repeat("x", 600)}},
	})
	require.Error(t, err)
	assert.LessOrEqual(t, len(err.Error()), 560)
	assert.Contains(t, err.Error(), "...")
}

func TestFinalizeBatchSyncCursorDoesNotAdvanceAfterPartialIngestion(t *testing.T) {
	ds := &types.DataSource{
		LastSyncCursor: types.JSON(`{"connector_cursor":{"revision":"old"}}`),
	}
	next := &types.SyncCursor{
		ConnectorCursor: map[string]interface{}{"revision": "new"},
	}

	status, message := finalizeBatchSyncCursor(ds, next, &types.SyncResult{
		Total:   2,
		Created: 1,
		Failed:  1,
	})

	assert.Equal(t, types.SyncLogStatusPartial, status)
	assert.Contains(t, message, "will be retried")
	assert.JSONEq(
		t,
		`{"connector_cursor":{"revision":"old"}}`,
		string(ds.LastSyncCursor),
	)
}

func TestFinalizeBatchSyncCursorDoesNotConsumeSuppressedDeletions(t *testing.T) {
	ds := &types.DataSource{
		LastSyncCursor: types.JSON(`{"connector_cursor":{"revision":"old-with-deleted-row"}}`),
	}
	next := &types.SyncCursor{
		ConnectorCursor: map[string]interface{}{"revision": "new-without-deleted-row"},
	}

	status, message := finalizeBatchSyncCursor(ds, next, &types.SyncResult{
		Total:               1,
		Skipped:             1,
		SuppressedDeletions: 1,
	})

	assert.Equal(t, types.SyncLogStatusSuccess, status)
	assert.Empty(t, message)
	assert.JSONEq(
		t,
		`{"connector_cursor":{"revision":"old-with-deleted-row"}}`,
		string(ds.LastSyncCursor),
	)
}

func TestFinalizeBatchSyncCursorFailureWinsOverSuppressedDeletion(t *testing.T) {
	ds := &types.DataSource{
		LastSyncCursor: types.JSON(`{"connector_cursor":{"revision":"old"}}`),
	}
	next := &types.SyncCursor{
		ConnectorCursor: map[string]interface{}{"revision": "new"},
	}

	status, message := finalizeBatchSyncCursor(ds, next, &types.SyncResult{
		Total:               2,
		Failed:              1,
		Skipped:             1,
		SuppressedDeletions: 1,
	})

	assert.Equal(t, types.SyncLogStatusPartial, status)
	assert.Contains(t, message, "will be retried")
	assert.JSONEq(t, `{"connector_cursor":{"revision":"old"}}`, string(ds.LastSyncCursor))
}

func TestFinalizeBatchSyncCursorAdvancesAfterSuccessfulIngestion(t *testing.T) {
	ds := &types.DataSource{
		LastSyncCursor: types.JSON(`{"connector_cursor":{"revision":"old"}}`),
	}
	next := &types.SyncCursor{
		ConnectorCursor: map[string]interface{}{"revision": "new"},
	}

	status, message := finalizeBatchSyncCursor(ds, next, &types.SyncResult{
		Total:   1,
		Created: 1,
	})

	assert.Equal(t, types.SyncLogStatusSuccess, status)
	assert.Empty(t, message)
	persisted, err := ds.ParseSyncCursor()
	require.NoError(t, err)
	require.NotNil(t, persisted)
	assert.Equal(t, "new", persisted.ConnectorCursor["revision"])
}
