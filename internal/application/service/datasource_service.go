package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/datasource"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/tracing/langfuse"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	secutils "github.com/Tencent/WeKnora/internal/utils"
	"github.com/hibiken/asynq"
)

// DataSourceService implements the DataSourceService interface
type DataSourceService struct {
	dsRepo            interfaces.DataSourceRepository
	syncLogRepo       interfaces.SyncLogRepository
	kbService         interfaces.KnowledgeBaseService
	taskEnqueuer      interfaces.TaskEnqueuer
	connectorRegistry *datasource.ConnectorRegistry
	scheduler         *datasource.Scheduler
	tagService        interfaces.KnowledgeTagService
	content           *DataSourceContentManager
}

// ErrDataSourceIngestionPending asks the worker to retry finalization later.
var ErrDataSourceIngestionPending = errors.New("data source ingestion is still processing")

// NewDataSourceService creates a new data source service
func NewDataSourceService(
	dsRepo interfaces.DataSourceRepository,
	syncLogRepo interfaces.SyncLogRepository,
	kbService interfaces.KnowledgeBaseService,
	taskEnqueuer interfaces.TaskEnqueuer,
	connectorRegistry *datasource.ConnectorRegistry,
	scheduler *datasource.Scheduler,
	tagService interfaces.KnowledgeTagService,
	content *DataSourceContentManager,
) interfaces.DataSourceService {
	return &DataSourceService{
		dsRepo:            dsRepo,
		syncLogRepo:       syncLogRepo,
		kbService:         kbService,
		taskEnqueuer:      taskEnqueuer,
		connectorRegistry: connectorRegistry,
		scheduler:         scheduler,
		tagService:        tagService,
		content:           content,
	}
}

// CreateDataSource creates a new data source configuration
func (s *DataSourceService) CreateDataSource(ctx context.Context, ds *types.DataSource) (*types.DataSource, error) {
	if ds == nil {
		return nil, datasource.ErrDataSourceInvalid
	}
	// Connection generations are server-owned and never accepted from clients.
	ds.ConnectionVersion = 1

	// Validate knowledge base exists
	kb, err := s.kbService.GetKnowledgeBaseByID(ctx, ds.KnowledgeBaseID)
	if err != nil || kb == nil {
		return nil, datasource.ErrKnowledgeBaseNotFound
	}
	if kb.TenantID != ds.TenantID {
		return nil, datasource.ErrKnowledgeBaseNotFound
	}

	// Validate connector type
	connector, err := s.connectorRegistry.Get(ds.Type)
	if err != nil {
		return nil, err
	}

	// Validate configuration
	if cfg, err := ds.ParseConfig(); err == nil && cfg != nil {
		cfg.StripNonSecretCredentials(ds.Type)
		if blob, err := cfg.ToJSON(); err == nil {
			ds.Config = blob
		}
	}
	if oauthConnector, ok := connector.(datasource.OAuthConnector); ok {
		cfg, parseErr := ds.ParseConfig()
		if parseErr != nil || oauthConnector.ValidateStaticConfig(cfg) != nil {
			return nil, datasource.ErrInvalidConfig
		}
		ds.Status = types.DataSourceStatusPaused
	} else {
		if err := s.validateDataSourceConfig(ctx, ds); err != nil {
			return nil, err
		}
	}

	// Create in database
	if err := s.dsRepo.Create(ctx, ds); err != nil {
		logger.Errorf(ctx, "failed to create data source: %v", err)
		return nil, err
	}

	// Register cron schedule if configured
	if ds.SyncSchedule != "" && ds.Status == types.DataSourceStatusActive {
		if err := s.scheduler.AddOrUpdate(ds); err != nil {
			logger.Warnf(ctx, "failed to register cron for ds=%s: %v", ds.ID, err)
		}
	}

	logger.Infof(ctx, "data source created: id=%s type=%s kb=%s", ds.ID, ds.Type, ds.KnowledgeBaseID)
	return ds, nil
}

// GetDataSource retrieves a data source by ID
func (s *DataSourceService) GetDataSource(ctx context.Context, id string) (*types.DataSource, error) {
	ds, err := s.dsRepo.FindByID(ctx, id)
	if err != nil {
		return nil, err
	}
	return ds, nil
}

// ListDataSources lists all data sources for a knowledge base
func (s *DataSourceService) ListDataSources(ctx context.Context, kbID string) ([]*types.DataSource, error) {
	dataSources, err := s.dsRepo.FindByKnowledgeBase(ctx, kbID)
	if err != nil {
		logger.Errorf(ctx, "failed to list data sources: %v", err)
		return nil, err
	}

	// Attach latest sync log to each data source
	for _, ds := range dataSources {
		log, _ := s.syncLogRepo.FindLatest(ctx, ds.ID)
		if log != nil {
			ds.LatestSyncLog = log
		}
	}

	return dataSources, nil
}

// UpdateDataSource updates an existing data source
func (s *DataSourceService) UpdateDataSource(ctx context.Context, ds *types.DataSource) (*types.DataSource, error) {
	if ds == nil || ds.ID == "" {
		return nil, datasource.ErrDataSourceInvalid
	}

	// Verify data source exists
	existing, err := s.dsRepo.FindByID(ctx, ds.ID)
	if err != nil {
		return nil, err
	}
	if ds.Type == "" {
		ds.Type = existing.Type
	}
	if ds.Type != existing.Type {
		return nil, fmt.Errorf("changing data source type is not allowed")
	}
	ds.ConnectionVersion = existing.ConnectionVersion
	ds.LastSyncAt = existing.LastSyncAt
	ds.LastSyncCursor = existing.LastSyncCursor
	ds.LastSyncResult = existing.LastSyncResult
	ds.ErrorMessage = existing.ErrorMessage
	if ds.Status == "" {
		ds.Status = existing.Status
	}

	if ds.KnowledgeBaseID == "" {
		ds.KnowledgeBaseID = existing.KnowledgeBaseID
	}
	if ds.KnowledgeBaseID != existing.KnowledgeBaseID {
		return nil, fmt.Errorf("changing knowledge base is not allowed")
	}

	if ds.TenantID == 0 {
		ds.TenantID = existing.TenantID
	}
	if ds.TenantID != existing.TenantID {
		return nil, datasource.ErrDataSourceInvalid
	}

	// Credentials NEVER flow through this endpoint — they live behind the
	// /credentials subresource. Force-preserve the stored credentials map
	// regardless of what the body says. Log a warning if a stale caller
	// passes one so we can spot them and migrate later. Non-credential
	// fields of Config (Type / ResourceIDs / Settings) flow through.
	var mergedCfg, existingParsedCfg *types.DataSourceConfig
	if len(ds.Config) > 0 {
		incomingCfg, parseIncErr := ds.ParseConfig()
		existingCfg, parseExErr := existing.ParseConfig()
		if parseIncErr == nil && parseExErr == nil && incomingCfg != nil {
			if incomingCfg.HasCredentials() {
				logger.Warnf(ctx,
					"deprecated: credentials in PUT /datasource/%s body are ignored; use PUT /credentials instead",
					secutils.SanitizeForLog(ds.ID))
			}
			merged := *incomingCfg
			if existingCfg != nil {
				merged.Credentials = existingCfg.Credentials
			} else {
				merged.Credentials = nil
			}
			merged.StripNonSecretCredentials(ds.Type)
			if blob, err := merged.ToJSON(); err == nil {
				ds.Config = blob
			}
			mergedCfg = &merged
			existingParsedCfg = existingCfg
		}
	}

	// Validate new configuration if non-credential fields changed. Skip
	// when there are no stored credentials yet (validators would fail with
	// no token to call the live API) and when the parsed config is
	// structurally identical.
	configActuallyChanged := true
	if mergedCfg != nil && existingParsedCfg != nil {
		configActuallyChanged = !reflect.DeepEqual(*mergedCfg, *existingParsedCfg)
	}
	hasCreds := mergedCfg != nil && mergedCfg.HasConfiguredCredentials(ds.Type)
	if hasCreds && (ds.Type != existing.Type || configActuallyChanged) {
		if err := s.validateDataSourceConfig(ctx, ds); err != nil {
			return nil, err
		}
	}
	connector, err := s.connectorRegistry.Get(ds.Type)
	if err != nil {
		return nil, err
	}
	if ready, ok := connector.(datasource.RuntimeConnector); ok && ds.Status == types.DataSourceStatusActive {
		if err := ready.EnsureReady(ctx, ds); err != nil {
			return nil, err
		}
	}

	if err := s.dsRepo.Update(ctx, ds); err != nil {
		logger.Errorf(ctx, "failed to update data source: %v", err)
		return nil, err
	}

	// Update cron schedule
	if err := s.scheduler.AddOrUpdate(ds); err != nil {
		logger.Warnf(ctx, "failed to update cron for ds=%s: %v", ds.ID, err)
	}

	logger.Infof(ctx, "data source updated: id=%s", ds.ID)
	return ds, nil
}

// UpdateDataSourceCredentials replaces the connector credential map. This is
// a single atomic write; the previous credential set is discarded entirely
// (callers cannot patch individual keys because half-configured connector
// auth is meaningless). After persisting, the live connection is validated
// so the caller learns immediately if the new credentials are wrong.
func (s *DataSourceService) UpdateDataSourceCredentials(
	ctx context.Context, id string, credentials map[string]interface{},
) (*types.DataSource, error) {
	if id == "" {
		return nil, datasource.ErrDataSourceInvalid
	}
	existing, err := s.dsRepo.FindByID(ctx, id)
	if err != nil {
		return nil, err
	}
	parsed, err := existing.ParseConfig()
	if err != nil {
		return nil, err
	}
	if parsed == nil {
		parsed = &types.DataSourceConfig{Type: existing.Type}
	}
	parsed.Credentials = credentials
	parsed.StripNonSecretCredentials(existing.Type)
	blob, err := parsed.ToJSON()
	if err != nil {
		return nil, err
	}
	existing.Config = blob

	// Run live validation now that the credentials are in place — surfaces
	// "wrong token" feedback immediately to the user instead of waiting for
	// the next scheduled sync.
	if err := s.validateDataSourceConfig(ctx, existing); err != nil {
		return nil, err
	}
	if err := s.dsRepo.Update(ctx, existing); err != nil {
		return nil, err
	}
	logger.Infof(ctx, "DataSource credentials updated: id=%s", secutils.SanitizeForLog(id))
	return existing, nil
}

// ClearDataSourceCredentials wipes the connector credential map without
// touching any other config field. Idempotent.
func (s *DataSourceService) ClearDataSourceCredentials(ctx context.Context, id string) error {
	if id == "" {
		return datasource.ErrDataSourceInvalid
	}
	existing, err := s.dsRepo.FindByID(ctx, id)
	if err != nil {
		return err
	}
	parsed, err := existing.ParseConfig()
	if err != nil {
		return err
	}
	if parsed == nil {
		return nil
	}
	parsed.StripNonSecretCredentials(existing.Type)
	if !parsed.HasConfiguredCredentials(existing.Type) {
		blob, err := parsed.ToJSON()
		if err != nil {
			return err
		}
		existing.Config = blob
		return s.dsRepo.Update(ctx, existing)
	}
	parsed.Credentials = nil
	blob, err := parsed.ToJSON()
	if err != nil {
		return err
	}
	existing.Config = blob
	if err := s.dsRepo.Update(ctx, existing); err != nil {
		return err
	}
	logger.Infof(ctx, "DataSource credentials cleared by user: id=%s", secutils.SanitizeForLog(id))
	return nil
}

// DeleteDataSource deletes a data source (soft delete)
func (s *DataSourceService) DeleteDataSource(ctx context.Context, id string) error {
	// Verify data source exists
	ds, err := s.dsRepo.FindByID(ctx, id)
	if err != nil {
		return err
	}
	connector, connectorErr := s.connectorRegistry.Get(ds.Type)
	if connectorErr == nil {
		if lifecycle, ok := connector.(datasource.ConnectionLifecycleConnector); ok {
			if err := lifecycle.Disconnect(ctx, ds); err != nil {
				return fmt.Errorf("disconnect data source before delete: %w", err)
			}
			if _, err := s.content.DeleteByDataSource(ctx, ds); err != nil {
				return fmt.Errorf("delete data source knowledge: %w", err)
			}
		}
	} else {
		logger.Warnf(ctx, "connector unavailable during data source delete: type=%s err=%v", ds.Type, connectorErr)
	}

	if err := s.dsRepo.Delete(ctx, id); err != nil {
		logger.Errorf(ctx, "failed to delete data source: %v", err)
		return err
	}

	// Remove cron schedule
	s.scheduler.Remove(id)

	// Cancel any pending/running sync logs so queued asynq tasks won't retry
	if err := s.syncLogRepo.CancelPendingByDataSource(ctx, id); err != nil {
		logger.Warnf(ctx, "failed to cancel pending sync logs for ds=%s: %v", id, err)
	}

	logger.Infof(ctx, "data source deleted: id=%s", id)
	return nil
}

// ValidateConnection tests the connection to an external data source
func (s *DataSourceService) ValidateConnection(ctx context.Context, dsID string) error {
	ds, err := s.GetDataSource(ctx, dsID)
	if err != nil {
		return err
	}

	// Get connector
	connector, err := s.connectorRegistry.Get(ds.Type)
	if err != nil {
		return err
	}

	// Parse configuration
	config, err := ds.ParseConfig()
	if err != nil {
		return datasource.ErrInvalidConfig
	}
	if err := s.injectRuntime(ctx, ds, config); err != nil {
		return err
	}

	// Validate connection
	if err := connector.Validate(ctx, config); err != nil {
		// Update data source with error
		if errors.Is(err, datasource.ErrOAuthReauthorizationRequired) {
			ds.Status = types.DataSourceStatusReauthorizationRequired
			s.scheduler.Remove(ds.ID)
		} else {
			ds.Status = types.DataSourceStatusError
		}
		ds.ErrorMessage = err.Error()
		_ = s.dsRepo.UpdateSyncState(ctx, ds)
		return err
	}

	// Clear error if it was previously in error state
	if ds.Status == types.DataSourceStatusError {
		ds.Status = types.DataSourceStatusActive
		ds.ErrorMessage = ""
		_ = s.dsRepo.UpdateSyncState(ctx, ds)
	}

	return nil
}

// ListAvailableResources lists resources available for sync in the external system.
// parentID enables lazy (on-demand) loading of hierarchical resources: pass "" to
// list the top level, or a resource's ExternalID to list only its direct children.
func (s *DataSourceService) ListAvailableResources(
	ctx context.Context, dsID string, parentID string,
) ([]types.Resource, error) {
	ds, err := s.GetDataSource(ctx, dsID)
	if err != nil {
		return nil, err
	}

	// Get connector
	connector, err := s.connectorRegistry.Get(ds.Type)
	if err != nil {
		return nil, err
	}

	// Parse configuration
	config, err := ds.ParseConfig()
	if err != nil {
		return nil, datasource.ErrInvalidConfig
	}
	if err := s.injectRuntime(ctx, ds, config); err != nil {
		return nil, err
	}

	// List resources
	resources, err := connector.ListResources(ctx, config, parentID)
	if err != nil {
		logger.Errorf(ctx, "failed to list resources: %v", err)
		return nil, err
	}

	return resources, nil
}

// ResolveResourceAncestors resolves the ancestor ExternalIDs needed to reveal the
// given resources in a lazily-loaded picker (see the connector method for details).
func (s *DataSourceService) ResolveResourceAncestors(
	ctx context.Context, dsID string, resourceIDs []string,
) ([]string, error) {
	if len(resourceIDs) == 0 {
		return []string{}, nil
	}

	ds, err := s.GetDataSource(ctx, dsID)
	if err != nil {
		return nil, err
	}

	connector, err := s.connectorRegistry.Get(ds.Type)
	if err != nil {
		return nil, err
	}

	config, err := ds.ParseConfig()
	if err != nil {
		return nil, datasource.ErrInvalidConfig
	}
	if err := s.injectRuntime(ctx, ds, config); err != nil {
		return nil, err
	}

	ancestors, err := connector.ResolveResourceAncestors(ctx, config, resourceIDs)
	if err != nil {
		logger.Errorf(ctx, "failed to resolve resource ancestors: %v", err)
		return nil, err
	}

	return ancestors, nil
}

// ManualSync triggers an immediate sync for a data source
func (s *DataSourceService) ManualSync(ctx context.Context, dsID string) (*types.SyncLog, error) {
	ds, err := s.GetDataSource(ctx, dsID)
	if err != nil {
		return nil, err
	}

	if ds.Status != types.DataSourceStatusActive &&
		ds.Status != types.DataSourceStatusError &&
		ds.Status != types.DataSourceStatusPaused {
		return nil, datasource.ErrDataSourceNotActive
	}
	connector, err := s.connectorRegistry.Get(ds.Type)
	if err != nil {
		return nil, err
	}
	if ready, ok := connector.(datasource.RuntimeConnector); ok {
		if err := ready.EnsureReady(ctx, ds); err != nil {
			return nil, err
		}
	}

	// Create sync log
	syncLog := &types.SyncLog{
		DataSourceID: dsID,
		TenantID:     ds.TenantID,
		Status:       types.SyncLogStatusRunning,
		StartedAt:    time.Now().UTC(),
	}

	if err := s.syncLogRepo.Create(ctx, syncLog); err != nil {
		logger.Errorf(ctx, "failed to create sync log: %v", err)
		return nil, err
	}

	// Enqueue sync task
	payload := &types.DataSourceSyncPayload{
		DataSourceID:      dsID,
		TenantID:          ds.TenantID,
		ConnectionVersion: ds.ConnectionVersion,
		SyncLogID:         syncLog.ID,
		ForceFull:         false,
	}
	langfuse.InjectTracing(ctx, payload)

	payloadJSON, _ := json.Marshal(payload)
	task := asynq.NewTask(types.TypeDataSourceSync, payloadJSON,
		asynq.Queue(types.QueueSync), asynq.MaxRetry(5), asynq.Timeout(2*time.Hour))

	_, err = s.taskEnqueuer.Enqueue(task)
	if err != nil {
		logger.Errorf(ctx, "failed to enqueue sync task: %v", err)
		syncLog.Status = types.SyncLogStatusFailed
		syncLog.FinishedAt = timePtr(time.Now().UTC())
		syncLog.ErrorMessage = err.Error()
		_ = s.syncLogRepo.Update(ctx, syncLog)
		if ds.Status != types.DataSourceStatusPaused {
			ds.Status = types.DataSourceStatusError
		}
		ds.ErrorMessage = fmt.Sprintf("Failed to enqueue sync: %v", err)
		_ = s.dsRepo.UpdateSyncState(ctx, ds)
		return nil, err
	}

	logger.Infof(ctx, "sync task enqueued: ds=%s syncLog=%s", dsID, syncLog.ID)
	return syncLog, nil
}

// PauseDataSource pauses a data source's scheduled syncs
func (s *DataSourceService) PauseDataSource(ctx context.Context, id string) error {
	ds, err := s.GetDataSource(ctx, id)
	if err != nil {
		return err
	}

	ds.Status = types.DataSourceStatusPaused
	if err := s.dsRepo.Update(ctx, ds); err != nil {
		logger.Errorf(ctx, "failed to pause data source: %v", err)
		return err
	}

	// Remove cron schedule
	s.scheduler.Remove(id)

	logger.Infof(ctx, "data source paused: id=%s", id)
	return nil
}

// ResumeDataSource resumes a paused data source
func (s *DataSourceService) ResumeDataSource(ctx context.Context, id string) error {
	ds, err := s.GetDataSource(ctx, id)
	if err != nil {
		return err
	}
	connector, connectorErr := s.connectorRegistry.Get(ds.Type)
	if connectorErr != nil {
		return connectorErr
	}
	if ready, ok := connector.(datasource.RuntimeConnector); ok {
		if err := ready.EnsureReady(ctx, ds); err != nil {
			return err
		}
	}

	ds.Status = types.DataSourceStatusActive
	if err := s.dsRepo.Update(ctx, ds); err != nil {
		logger.Errorf(ctx, "failed to resume data source: %v", err)
		return err
	}

	// Re-register cron schedule
	if err := s.scheduler.AddOrUpdate(ds); err != nil {
		logger.Warnf(ctx, "failed to re-register cron for ds=%s: %v", ds.ID, err)
	}

	logger.Infof(ctx, "data source resumed: id=%s", id)
	return nil
}

// GetSyncLogs retrieves sync history for a data source
func (s *DataSourceService) GetSyncLogs(ctx context.Context, dsID string, limit int, offset int) ([]*types.SyncLog, error) {
	logs, err := s.syncLogRepo.FindByDataSource(ctx, dsID, limit, offset)
	if err != nil {
		logger.Errorf(ctx, "failed to get sync logs: %v", err)
		return nil, err
	}
	return logs, nil
}

// GetSyncLog retrieves a specific sync log entry
func (s *DataSourceService) GetSyncLog(ctx context.Context, syncLogID string) (*types.SyncLog, error) {
	log, err := s.syncLogRepo.FindByID(ctx, syncLogID)
	if err != nil {
		return nil, err
	}
	return log, nil
}

// ProcessSync handles the actual sync operation (called by asynq task)
func (s *DataSourceService) ProcessSync(ctx context.Context, task *asynq.Task) error {
	var payload types.DataSourceSyncPayload
	if err := json.Unmarshal(task.Payload(), &payload); err != nil {
		logger.Errorf(ctx, "failed to unmarshal sync payload: %v", err)
		return err
	}

	logger.Infof(ctx, "processing data source sync: ds=%s syncLog=%s", payload.DataSourceID, payload.SyncLogID)

	// Get data source
	ds, err := s.GetDataSource(ctx, payload.DataSourceID)
	if err != nil {
		logger.Warnf(ctx, "data source not found (likely deleted), cancelling sync: ds=%s err=%v", payload.DataSourceID, err)
		if syncLog, slErr := s.syncLogRepo.FindByID(ctx, payload.SyncLogID); slErr == nil && syncLog != nil {
			syncLog.Status = types.SyncLogStatusCanceled
			syncLog.FinishedAt = timePtr(time.Now().UTC())
			syncLog.ErrorMessage = "data source has been deleted"
			_ = s.syncLogRepo.UpdateResult(ctx, syncLog)
		}
		return nil
	}
	staleConnection := payload.ConnectionVersion != 0 && payload.ConnectionVersion != ds.ConnectionVersion
	if payload.TenantID != ds.TenantID || staleConnection {
		logger.Warnf(ctx, "discarding stale data source sync task: ds=%s", payload.DataSourceID)
		if syncLog, logErr := s.syncLogRepo.FindByID(ctx, payload.SyncLogID); logErr == nil && syncLog != nil {
			syncLog.Status = types.SyncLogStatusCanceled
			syncLog.FinishedAt = timePtr(time.Now().UTC())
			syncLog.ErrorMessage = "data source connection changed"
			syncLog.Checkpoint = nil
			_ = s.syncLogRepo.UpdateResult(ctx, syncLog)
		}
		return nil
	}

	// Get sync log
	syncLog, err := s.syncLogRepo.FindByID(ctx, payload.SyncLogID)
	if err != nil {
		logger.Errorf(ctx, "failed to get sync log: %v", err)
		return nil
	}

	if _, err := s.kbService.GetKnowledgeBaseByID(ctx, ds.KnowledgeBaseID); err != nil {
		logger.Warnf(ctx, "knowledge base not found (likely deleted), cancelling sync: kb=%s ds=%s err=%v",
			ds.KnowledgeBaseID, payload.DataSourceID, err)
		syncLog.Status = types.SyncLogStatusCanceled
		syncLog.FinishedAt = timePtr(time.Now().UTC())
		syncLog.ErrorMessage = "knowledge base has been deleted"
		_ = s.syncLogRepo.Update(ctx, syncLog)
		return nil
	}

	wasPaused := ds.Status == types.DataSourceStatusPaused

	// Get connector
	connector, err := s.connectorRegistry.Get(ds.Type)
	if err != nil {
		logger.Errorf(ctx, "connector not found: type=%s", ds.Type)
		syncLog.Status = types.SyncLogStatusFailed
		syncLog.FinishedAt = timePtr(time.Now().UTC())
		syncLog.ErrorMessage = fmt.Sprintf("Connector not found: %s", ds.Type)
		_ = s.syncLogRepo.Update(ctx, syncLog)
		if !wasPaused {
			ds.Status = types.DataSourceStatusError
		}
		ds.ErrorMessage = syncLog.ErrorMessage
		_ = s.dsRepo.UpdateSyncState(ctx, ds)
		return err
	}
	syncLifecycle, hasSyncLifecycle := connector.(datasource.SyncLifecycleConnector)
	runIsCurrent := func() bool {
		if !hasSyncLifecycle {
			return true
		}
		current, currentErr := syncLifecycle.IsRunCurrent(ctx, ds)
		return currentErr == nil && current
	}

	// Parse configuration
	config, err := ds.ParseConfig()
	if err != nil {
		logger.Errorf(ctx, "failed to parse config: %v", err)
		syncLog.Status = types.SyncLogStatusFailed
		syncLog.FinishedAt = timePtr(time.Now().UTC())
		syncLog.ErrorMessage = fmt.Sprintf("Invalid configuration: %v", err)
		_ = s.syncLogRepo.Update(ctx, syncLog)
		if !wasPaused {
			ds.Status = types.DataSourceStatusError
		}
		ds.ErrorMessage = syncLog.ErrorMessage
		_ = s.dsRepo.UpdateSyncState(ctx, ds)
		return err
	}
	if err := s.injectRuntime(ctx, ds, config); err != nil {
		syncLog.Status = types.SyncLogStatusFailed
		syncLog.FinishedAt = timePtr(time.Now().UTC())
		syncLog.ErrorMessage = err.Error()
		_ = s.syncLogRepo.Update(ctx, syncLog)
		if errors.Is(err, datasource.ErrOAuthReauthorizationRequired) {
			ds.Status = types.DataSourceStatusReauthorizationRequired
			s.scheduler.Remove(ds.ID)
		} else if !wasPaused {
			ds.Status = types.DataSourceStatusError
		}
		ds.ErrorMessage = err.Error()
		_ = s.dsRepo.UpdateSyncState(ctx, ds)
		return err
	}

	// Fetch items based on sync mode
	var items []types.FetchedItem
	var nextCursor *types.SyncCursor
	var fetchErr error
	var fetchWarnings []string

	if reliable, ok := connector.(datasource.FetchResultConnector); ok {
		var fetched *types.FetchResult
		if payload.ForceFull || ds.SyncMode == types.SyncModeFull {
			fetched, fetchErr = reliable.FetchAllResult(ctx, config, config.ResourceIDs)
		} else {
			cursor, _ := ds.ParseSyncCursor()
			fetched, fetchErr = reliable.FetchIncrementalResult(ctx, config, cursor)
		}
		if fetched != nil {
			items = fetched.Items
			nextCursor = fetched.NextCursor
			for _, warning := range fetched.Warnings {
				message := warning.Code
				if warning.ExternalID != "" {
					message += " [" + warning.ExternalID + "]"
				}
				fetchWarnings = append(fetchWarnings, message+": "+warning.Message)
			}
		}
	} else if payload.ForceFull || ds.SyncMode == types.SyncModeFull {
		items, fetchErr = connector.FetchAll(ctx, config, config.ResourceIDs)
	} else {
		cursor, _ := ds.ParseSyncCursor()
		items, nextCursor, fetchErr = connector.FetchIncremental(ctx, config, cursor)
	}
	isFull := payload.ForceFull || ds.SyncMode == types.SyncModeFull
	logger.Infof(ctx, "data source fetch completed: items=%d full=%t", len(items), isFull)

	var partialFetch *datasource.PartialFetchError
	if errors.As(fetchErr, &partialFetch) {
		fetchWarnings = partialFetch.Details
		fetchErr = nil
	}

	if fetchErr != nil {
		logger.Errorf(ctx, "fetch operation failed: %v", fetchErr)
		syncLog.Status = types.SyncLogStatusFailed
		syncLog.FinishedAt = timePtr(time.Now().UTC())
		syncLog.ErrorMessage = fmt.Sprintf("Fetch failed: %v", fetchErr)
		_ = s.syncLogRepo.Update(ctx, syncLog)
		if errors.Is(fetchErr, datasource.ErrOAuthReauthorizationRequired) {
			ds.Status = types.DataSourceStatusReauthorizationRequired
			s.scheduler.Remove(ds.ID)
		} else if !wasPaused {
			ds.Status = types.DataSourceStatusError
		}
		ds.ErrorMessage = syncLog.ErrorMessage
		_ = s.dsRepo.UpdateSyncState(ctx, ds)
		return fetchErr
	}
	if hasSyncLifecycle {
		items, err = syncLifecycle.ReconcileItems(ctx, ds, items)
		if err != nil {
			syncLog.Status = types.SyncLogStatusFailed
			syncLog.FinishedAt = timePtr(time.Now().UTC())
			syncLog.ErrorMessage = "failed to reconcile connector sync state"
			_ = s.syncLogRepo.Update(ctx, syncLog)
			return err
		}
	}

	// Process fetched items and write to knowledge base
	result := &types.SyncResult{
		Total: len(items),
	}
	cancelForConnectionChange := func() {
		syncLog.ItemsTotal = result.Total
		syncLog.ItemsCreated = result.Created
		syncLog.ItemsUpdated = result.Updated
		syncLog.ItemsDeleted = result.Deleted
		syncLog.ItemsSkipped = result.Skipped
		syncLog.ItemsFailed = result.Failed
		syncLog.Status = types.SyncLogStatusCanceled
		syncLog.FinishedAt = timePtr(time.Now().UTC())
		syncLog.ErrorMessage = "data source connection changed"
		_ = s.syncLogRepo.UpdateResult(ctx, syncLog)
	}

	// Set tenant context so KnowledgeService can resolve tenant info correctly
	ctx, err = s.content.WithTenant(ctx, ds.TenantID)
	if err != nil {
		logger.Errorf(ctx, "failed to get tenant info: %v", err)
		syncLog.Status = types.SyncLogStatusFailed
		syncLog.FinishedAt = timePtr(time.Now().UTC())
		syncLog.ErrorMessage = fmt.Sprintf("Failed to get tenant info: %v", err)
		_ = s.syncLogRepo.Update(ctx, syncLog)
		if !wasPaused {
			ds.Status = types.DataSourceStatusError
		}
		ds.ErrorMessage = syncLog.ErrorMessage
		_ = s.dsRepo.UpdateSyncState(ctx, ds)
		return err
	}
	// Auto-tag: find or create a tag for this data source so synced items are easily identifiable
	autoTagIDs := []string{}
	autoTagName := ds.Name
	if autoTag, tagErr := s.tagService.FindOrCreateTagByName(ctx, ds.KnowledgeBaseID, autoTagName); tagErr != nil {
		logger.Warnf(ctx, "failed to find/create auto-tag %q: %v (proceeding without tag)", autoTagName, tagErr)
	} else if autoTag != nil {
		autoTagIDs = append(autoTagIDs, autoTag.ID)
		logger.Infof(ctx, "using auto-tag %q (id=%s) for data source sync", autoTagName, autoTag.ID)
	}
	pendingKnowledgeItems := make([]types.DataSourcePendingKnowledge, 0, len(items))

	for _, item := range items {
		if !runIsCurrent() {
			cancelForConnectionChange()
			return nil
		}
		if item.IsDeleted {
			if ds.SyncDeletions {
				if err := s.content.DeleteItem(ctx, ds, &item); err != nil {
					result.Failed++
					result.Errors = append(result.Errors, fmt.Sprintf("delete %s: %v", item.ExternalID, err))
				} else if hasSyncLifecycle {
					if err := syncLifecycle.MarkItemDeleted(ctx, ds, &item); err != nil {
						result.Failed++
						result.Errors = append(result.Errors, fmt.Sprintf("record deletion %s: %v", item.ExternalID, err))
					} else {
						result.Deleted++
					}
				} else {
					result.Deleted++
				}
			} else if hasSyncLifecycle {
				if err := syncLifecycle.MarkItemDeleted(ctx, ds, &item); err != nil {
					result.Failed++
					result.Errors = append(result.Errors, fmt.Sprintf("record retained deletion %s: %v", item.ExternalID, err))
				} else {
					result.Skipped++
				}
			} else {
				result.Skipped++
			}
			continue
		}

		if len(item.Content) == 0 && item.URL == "" {
			// Check if this is an error item from the connector (failed to fetch content)
			if errMsg, hasErr := item.Metadata["error"]; hasErr {
				logger.Warnf(ctx, "item %q (external_id=%s) fetch failed: %s", item.Title, item.ExternalID, errMsg)
				result.Failed++
				result.Errors = append(result.Errors, fmt.Sprintf("%s: %s", item.Title, errMsg))
			} else {
				logger.Infof(ctx, "skipping item %q (external_id=%s): no content or URL", item.Title, item.ExternalID)
				result.Skipped++
			}
			continue
		}

		isUpdate, knowledgeID, err := s.content.Ingest(ctx, ds, &item, autoTagIDs)
		if err == nil && !runIsCurrent() {
			if knowledgeID != "" {
				_ = s.content.DeleteKnowledge(ctx, knowledgeID)
			}
			cancelForConnectionChange()
			return nil
		}
		if err != nil {
			// Duplicate file/URL is not a failure — count as skipped
			var dupErr *types.DuplicateKnowledgeError
			if errors.As(err, &dupErr) {
				logger.Infof(ctx, "item %q (external_id=%s) already exists, skipping", item.Title, item.ExternalID)
				result.Skipped++
			} else {
				logger.Warnf(ctx, "failed to ingest item %q (external_id=%s): %v", item.Title, item.ExternalID, err)
				result.Failed++
				result.Errors = append(result.Errors, fmt.Sprintf("%s: %v", item.Title, err))
			}
		} else if isUpdate {
			result.Updated++
		} else {
			result.Created++
		}
		if err == nil {
			if deferred, ok := connector.(datasource.DeferredCommitConnector); ok &&
				deferred.DeferCursorUntilIngestionCompletes() && knowledgeID != "" {
				pendingKnowledgeItems = append(pendingKnowledgeItems, types.DataSourcePendingKnowledge{
					KnowledgeID: knowledgeID, Title: item.Title, IsUpdate: isUpdate,
				})
			}
			if hasSyncLifecycle {
				if markErr := syncLifecycle.MarkItemIngested(ctx, ds, &item); markErr != nil {
					result.Failed++
					result.Errors = append(result.Errors, fmt.Sprintf("record ingestion %s: %v", item.ExternalID, markErr))
				}
			}
		}
	}

	if len(fetchWarnings) > 0 {
		result.Errors = append(result.Errors, fetchWarnings...)
	}
	resultJSON, _ := result.ToJSON()
	if err := allFetchedItemsFailedError(result); err != nil {
		logger.Errorf(ctx, "data source sync failed while processing fetched items: %v", err)
		s.updateSyncRunResult(ctx, ds, syncLog, result, resultJSON, types.SyncLogStatusFailed, err.Error(), wasPaused)
		return err
	}

	if len(pendingKnowledgeItems) > 0 {
		deadline := time.Now().UTC().Add(90 * time.Minute)
		checkpointJSON, _ := json.Marshal(&types.DataSourceSyncCheckpoint{
			Result: *result, NextCursor: nextCursor,
			PendingKnowledge: pendingKnowledgeItems, FinalizeDeadline: deadline,
		})
		syncLog.ItemsTotal = result.Total
		syncLog.ItemsCreated = result.Created
		syncLog.ItemsUpdated = result.Updated
		syncLog.ItemsDeleted = result.Deleted
		syncLog.ItemsSkipped = result.Skipped
		syncLog.ItemsFailed = result.Failed
		syncLog.Status = types.SyncLogStatusRunning
		syncLog.Result = resultJSON
		syncLog.Checkpoint = checkpointJSON
		if err := s.syncLogRepo.UpdateResult(ctx, syncLog); err != nil {
			return err
		}
		finalizePayload := &types.DataSourceFinalizePayload{
			DataSourceID: ds.ID, TenantID: ds.TenantID,
			ConnectionVersion: ds.ConnectionVersion, SyncLogID: syncLog.ID,
			WasPaused: wasPaused,
		}
		langfuse.InjectTracing(ctx, finalizePayload)
		payloadJSON, _ := json.Marshal(finalizePayload)
		finalizeTask := asynq.NewTask(types.TypeDataSourceFinalize, payloadJSON,
			asynq.Queue(types.QueueSync), asynq.ProcessIn(5*time.Second),
			asynq.MaxRetry(1080), asynq.Timeout(30*time.Second))
		if _, err := s.taskEnqueuer.Enqueue(finalizeTask); err != nil {
			syncLog.Checkpoint = nil
			resultJSON, _ = result.ToJSON()
			s.updateSyncRunResult(ctx, ds, syncLog, result, resultJSON,
				types.SyncLogStatusFailed, "failed to enqueue ingestion finalizer", wasPaused)
			return err
		}
		logger.Infof(ctx, "data source sync awaiting asynchronous ingestion: ds=%s items=%d",
			ds.ID, len(pendingKnowledgeItems))
		return nil
	}

	// Update cursor for next incremental sync
	if nextCursor != nil && result.Failed == 0 {
		if !runIsCurrent() {
			cancelForConnectionChange()
			return nil
		}
		cursorJSON, _ := nextCursor.ToJSON()
		ds.LastSyncCursor = cursorJSON
	}

	ds.LastSyncAt = timePtr(time.Now().UTC())
	syncStatus := types.SyncLogStatusSuccess
	syncErrorMessage := ""
	if result.Failed > 0 {
		syncStatus = types.SyncLogStatusPartial
		syncErrorMessage = fmt.Sprintf("%d item(s) failed; cursor was not advanced", result.Failed)
	}
	if len(fetchWarnings) > 0 {
		syncStatus = types.SyncLogStatusPartial
		warningMessage := fmt.Sprintf("Some items were skipped: %s", strings.Join(fetchWarnings, "; "))
		if syncErrorMessage == "" {
			syncErrorMessage = warningMessage
		} else {
			syncErrorMessage += "; " + warningMessage
		}
	}
	s.updateSyncRunResult(ctx, ds, syncLog, result, resultJSON, syncStatus, syncErrorMessage, wasPaused)

	logger.Infof(ctx, "data source sync completed: ds=%s created=%d updated=%d deleted=%d",
		payload.DataSourceID, syncLog.ItemsCreated, syncLog.ItemsUpdated, syncLog.ItemsDeleted)

	return nil
}

func (s *DataSourceService) updateSyncRunResult(
	ctx context.Context,
	ds *types.DataSource,
	syncLog *types.SyncLog,
	result *types.SyncResult,
	resultJSON types.JSON,
	status string,
	errorMessage string,
	wasPaused bool,
) {
	syncLog.ItemsTotal = result.Total
	syncLog.ItemsCreated = result.Created
	syncLog.ItemsUpdated = result.Updated
	syncLog.ItemsDeleted = result.Deleted
	syncLog.ItemsSkipped = result.Skipped
	syncLog.ItemsFailed = result.Failed
	syncLog.Status = status
	syncLog.FinishedAt = timePtr(time.Now().UTC())
	syncLog.ErrorMessage = errorMessage
	syncLog.Result = resultJSON
	if err := s.syncLogRepo.UpdateResult(ctx, syncLog); err != nil {
		logger.Errorf(ctx, "failed to update sync log: %v", err)
	}

	if status == types.SyncLogStatusFailed {
		if !wasPaused {
			ds.Status = types.DataSourceStatusError
		}
	} else if wasPaused {
		ds.Status = types.DataSourceStatusPaused
	} else {
		ds.Status = types.DataSourceStatusActive
	}
	ds.ErrorMessage = errorMessage
	ds.LastSyncResult = resultJSON
	if err := s.dsRepo.UpdateSyncState(ctx, ds); err != nil {
		logger.Errorf(ctx, "failed to update data source: %v", err)
		current, currentErr := s.GetDataSource(ctx, ds.ID)
		if currentErr == nil && current.ConnectionVersion != ds.ConnectionVersion {
			syncLog.Status = types.SyncLogStatusCanceled
			syncLog.ErrorMessage = "data source connection changed before cursor commit"
			_ = s.syncLogRepo.UpdateResult(ctx, syncLog)
		}
	}
}

// ProcessSyncFinalize is a short-lived checkpoint task. It observes the
// asynchronous knowledge pipeline and commits the connector cursor only after
// every required item has completed. Returning ErrDataSourceIngestionPending
// asks Asynq to retry later without occupying a worker while documents parse.
func (s *DataSourceService) ProcessSyncFinalize(ctx context.Context, task *asynq.Task) error {
	var payload types.DataSourceFinalizePayload
	if err := json.Unmarshal(task.Payload(), &payload); err != nil {
		return err
	}
	ds, err := s.GetDataSource(ctx, payload.DataSourceID)
	if err != nil || ds == nil || ds.TenantID != payload.TenantID ||
		ds.ConnectionVersion != payload.ConnectionVersion {
		if syncLog, logErr := s.syncLogRepo.FindByID(ctx, payload.SyncLogID); logErr == nil && syncLog != nil {
			syncLog.Status = types.SyncLogStatusCanceled
			syncLog.FinishedAt = timePtr(time.Now().UTC())
			syncLog.ErrorMessage = "data source connection changed"
			syncLog.Checkpoint = nil
			_ = s.syncLogRepo.UpdateResult(ctx, syncLog)
		}
		return nil
	}
	syncLog, err := s.syncLogRepo.FindByID(ctx, payload.SyncLogID)
	if err != nil {
		return err
	}
	if syncLog.Status != types.SyncLogStatusRunning {
		return nil
	}
	var checkpoint types.DataSourceSyncCheckpoint
	if err := json.Unmarshal(syncLog.Checkpoint, &checkpoint); err != nil {
		return fmt.Errorf("decode data source finalization checkpoint: %w", err)
	}
	result := checkpoint.Result

	deadlineReached := time.Now().UTC().After(checkpoint.FinalizeDeadline)
	remaining := make([]types.DataSourcePendingKnowledge, 0, len(checkpoint.PendingKnowledge))
	for _, pending := range checkpoint.PendingKnowledge {
		status, message, statusErr := s.content.Status(ctx, ds.TenantID, pending.KnowledgeID)
		if statusErr != nil {
			if !deadlineReached {
				remaining = append(remaining, pending)
				continue
			}
			message = "cannot read knowledge processing status before the finalization deadline"
		}
		switch {
		case statusErr != nil:
			// The deadline converts persistent repository/read failures into a
			// terminal item failure so the sync log cannot remain running forever.
		case status == types.ParseStatusCompleted || status == types.ParseStatusFinalizing:
			continue
		case status == types.ParseStatusFailed ||
			status == types.ParseStatusCancelled ||
			status == types.ParseStatusDeleting:
			if strings.TrimSpace(message) == "" {
				message = "knowledge processing ended with status " + status
			}
		case status == "":
			message = "cannot read knowledge processing status"
		default:
			if !deadlineReached {
				remaining = append(remaining, pending)
				continue
			}
			message = "knowledge processing did not finish within 90 minutes"
		}
		if pending.IsUpdate && result.Updated > 0 {
			result.Updated--
		} else if !pending.IsUpdate && result.Created > 0 {
			result.Created--
		}
		result.Failed++
		result.Errors = append(result.Errors, fmt.Sprintf("%s: %s", pending.Title, message))
	}

	if len(remaining) > 0 {
		checkpoint.Result = result
		checkpoint.PendingKnowledge = remaining
		checkpointJSON, _ := json.Marshal(&checkpoint)
		syncLog.Checkpoint = checkpointJSON
		if err := s.syncLogRepo.UpdateResult(ctx, syncLog); err != nil {
			return err
		}
		return ErrDataSourceIngestionPending
	}

	connector, err := s.connectorRegistry.Get(ds.Type)
	if err != nil {
		return err
	}
	if lifecycle, ok := connector.(datasource.SyncLifecycleConnector); ok {
		current, currentErr := lifecycle.IsRunCurrent(ctx, ds)
		if currentErr != nil {
			return currentErr
		}
		if !current {
			syncLog.Status = types.SyncLogStatusCanceled
			syncLog.FinishedAt = timePtr(time.Now().UTC())
			syncLog.ErrorMessage = "data source connection changed before cursor commit"
			syncLog.Checkpoint = nil
			_ = s.syncLogRepo.UpdateResult(ctx, syncLog)
			return nil
		}
	}
	if result.Failed == 0 && checkpoint.NextCursor != nil {
		cursorJSON, cursorErr := checkpoint.NextCursor.ToJSON()
		if cursorErr != nil {
			return cursorErr
		}
		ds.LastSyncCursor = cursorJSON
	}
	syncLog.Checkpoint = nil
	ds.LastSyncAt = timePtr(time.Now().UTC())
	status := types.SyncLogStatusSuccess
	errorMessage := ""
	if result.Failed > 0 {
		status = types.SyncLogStatusPartial
		errorMessage = fmt.Sprintf("%d item(s) failed; cursor was not advanced", result.Failed)
	} else if len(result.Errors) > 0 {
		status = types.SyncLogStatusPartial
		errorMessage = "some items were skipped; see sync result"
	}
	resultJSON, _ := result.ToJSON()
	s.updateSyncRunResult(ctx, ds, syncLog, &result, resultJSON, status, errorMessage, payload.WasPaused)
	return nil
}

func allFetchedItemsFailedError(result *types.SyncResult) error {
	if result == nil || result.Total == 0 {
		return nil
	}
	if result.Failed != result.Total || result.Created != 0 || result.Updated != 0 ||
		result.Deleted != 0 || result.Skipped != 0 {
		return nil
	}

	detail := ""
	if len(result.Errors) > 0 {
		detail = result.Errors[0]
		const maxDetailLen = 500
		if len(detail) > maxDetailLen {
			detail = detail[:maxDetailLen] + "..."
		}
	}
	if detail == "" {
		return fmt.Errorf("all fetched items failed during sync (%d/%d)", result.Failed, result.Total)
	}
	return fmt.Errorf("all fetched items failed during sync (%d/%d): %s", result.Failed, result.Total, detail)
}

// ValidateCredentials tests connectivity using raw credentials without persisting anything.
func (s *DataSourceService) ValidateCredentials(ctx context.Context, connectorType string, credentials map[string]interface{}) error {
	connector, err := s.connectorRegistry.Get(connectorType)
	if err != nil {
		return err
	}

	config := &types.DataSourceConfig{
		Type:        connectorType,
		Credentials: credentials,
	}

	if err := connector.Validate(ctx, config); err != nil {
		return err
	}

	return nil
}

// Helper functions

func (s *DataSourceService) validateDataSourceConfig(ctx context.Context, ds *types.DataSource) error {
	connector, err := s.connectorRegistry.Get(ds.Type)
	if err != nil {
		return err
	}

	config, err := ds.ParseConfig()
	if err != nil {
		return datasource.ErrInvalidConfig
	}
	if err := s.injectRuntime(ctx, ds, config); err != nil {
		return err
	}

	return connector.Validate(ctx, config)
}

func (s *DataSourceService) injectRuntime(
	ctx context.Context, ds *types.DataSource, config *types.DataSourceConfig,
) error {
	if ds == nil || config == nil {
		return datasource.ErrInvalidConfig
	}
	connector, err := s.connectorRegistry.Get(ds.Type)
	if err != nil {
		return err
	}
	runtimeConnector, ok := connector.(datasource.RuntimeConnector)
	if !ok {
		return nil
	}
	return runtimeConnector.PrepareRuntime(ctx, ds, config)
}

func timePtr(t time.Time) *time.Time {
	utc := t.UTC()
	return &utc
}
