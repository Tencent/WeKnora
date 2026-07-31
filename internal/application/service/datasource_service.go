package service

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"mime/multipart"
	"net/textproto"
	"reflect"
	"slices"
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
	knowledgeService  interfaces.KnowledgeService
	kbService         interfaces.KnowledgeBaseService
	taskEnqueuer      interfaces.TaskEnqueuer
	connectorRegistry *datasource.ConnectorRegistry
	scheduler         *datasource.Scheduler
	tenantRepo        interfaces.TenantRepository
	tagService        interfaces.KnowledgeTagService
	audit             interfaces.AuditLogService
}

// NewDataSourceService creates a new data source service
func NewDataSourceService(
	dsRepo interfaces.DataSourceRepository,
	syncLogRepo interfaces.SyncLogRepository,
	knowledgeService interfaces.KnowledgeService,
	kbService interfaces.KnowledgeBaseService,
	taskEnqueuer interfaces.TaskEnqueuer,
	connectorRegistry *datasource.ConnectorRegistry,
	scheduler *datasource.Scheduler,
	tenantRepo interfaces.TenantRepository,
	tagService interfaces.KnowledgeTagService,
	audit interfaces.AuditLogService,
) interfaces.DataSourceService {
	return &DataSourceService{
		dsRepo:            dsRepo,
		syncLogRepo:       syncLogRepo,
		knowledgeService:  knowledgeService,
		kbService:         kbService,
		taskEnqueuer:      taskEnqueuer,
		connectorRegistry: connectorRegistry,
		scheduler:         scheduler,
		tenantRepo:        tenantRepo,
		tagService:        tagService,
		audit:             audit,
	}
}

// CreateDataSource creates a new data source configuration
func (s *DataSourceService) CreateDataSource(ctx context.Context, ds *types.DataSource) (*types.DataSource, error) {
	if ds == nil {
		return nil, datasource.ErrDataSourceInvalid
	}

	// Validate knowledge base exists
	kb, err := s.kbService.GetKnowledgeBaseByID(ctx, ds.KnowledgeBaseID)
	if err != nil || kb == nil {
		return nil, datasource.ErrKnowledgeBaseNotFound
	}
	if kb.TenantID != ds.TenantID {
		return nil, datasource.ErrKnowledgeBaseNotFound
	}

	// Validate connector type
	_, err = s.connectorRegistry.Get(ds.Type)
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
	if err := s.validateDataSourceConfig(ctx, ds); err != nil {
		return nil, err
	}

	// Create in database
	if err := s.dsRepo.Create(ctx, ds); err != nil {
		logger.Errorf(ctx, "failed to create data source: %v", err)
		return nil, err
	}

	// Register cron schedule from the authoritative persisted state.
	if s.scheduler != nil {
		if err := s.scheduler.Refresh(ctx, ds.ID); err != nil {
			logger.Warnf(ctx, "failed to register cron for ds=%s: %v", ds.ID, err)
		}
	}

	logger.Infof(ctx, "data source created: id=%s type=%s kb=%s", ds.ID, ds.Type, ds.KnowledgeBaseID)
	recordKBActivity(ctx, s.audit, ds.TenantID, ds.KnowledgeBaseID, types.AuditActionDataSourceCreated,
		"data_source", ds.ID, types.AuditOutcomeSuccess,
		map[string]any{"name": ds.Name, "type": ds.Type})
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
	if ds.Type == "" {
		ds.Type = existing.Type
	}
	if len(ds.Config) == 0 {
		ds.Config = append(types.JSON(nil), existing.Config...)
	}
	if ds.SyncMode == "" {
		ds.SyncMode = existing.SyncMode
	}
	if ds.ConflictStrategy == "" {
		ds.ConflictStrategy = existing.ConflictStrategy
	}
	// Operational state is owned by pause/resume/validation/sync endpoints.
	// Generic editor payloads often carry a stale status snapshot and must not
	// overwrite a concurrent operator action.
	ds.Status = existing.Status
	ds.ErrorMessage = existing.ErrorMessage
	if ds.SyncLogRetentionDays == 0 {
		ds.SyncLogRetentionDays = existing.SyncLogRetentionDays
	}
	// Generic edits never own sync-produced state. Preserve the current cursor;
	// credential replacement explicitly resets it through ReconfigureDataSource.
	ds.LastSyncCursor = append(types.JSON(nil), existing.LastSyncCursor...)

	// Credentials NEVER flow through this endpoint — they live behind the
	// /credentials subresource. Force-preserve the stored credentials map
	// regardless of what the body says. Log a warning if a stale caller
	// passes one so we can spot them and migrate later. Non-credential
	// fields of Config (Type / ResourceIDs / Settings) flow through.
	var mergedCfg, existingParsedCfg *types.DataSourceConfig
	if len(ds.Config) > 0 {
		incomingCfg, parseIncErr := ds.ParseConfig()
		existingCfg, parseExErr := existing.ParseConfig()
		if parseIncErr != nil {
			return nil, fmt.Errorf("%w: malformed candidate data source config", datasource.ErrInvalidConfig)
		}
		if parseExErr != nil {
			return nil, fmt.Errorf("%w: malformed stored data source config", datasource.ErrInvalidConfig)
		}
		if incomingCfg != nil {
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
			blob, err := merged.ToJSON()
			if err != nil {
				return nil, fmt.Errorf("%w: encode candidate data source config", datasource.ErrInvalidConfig)
			}
			ds.Config = blob
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
	if !configActuallyChanged {
		// ToJSON re-encrypts credentials with a fresh AES-GCM nonce. Preserve the
		// exact stored blob for logically identical config so metadata-only edits
		// do not manufacture a new sync generation and cancel queued/running work.
		ds.Config = append(types.JSON(nil), existing.Config...)
	}
	syncConfigChanged := ds.Type != existing.Type ||
		ds.SyncMode != existing.SyncMode ||
		ds.ConflictStrategy != existing.ConflictStrategy ||
		ds.SyncDeletions != existing.SyncDeletions ||
		(mergedCfg != nil && configActuallyChanged)
	if syncConfigChanged {
		if err := s.rejectCredentialMutationDuringSync(ctx, ds.ID); err != nil {
			return nil, err
		}
	}
	hasCreds := mergedCfg != nil && mergedCfg.HasConfiguredCredentials(ds.Type)
	if hasCreds && (ds.Type != existing.Type || configActuallyChanged) {
		if err := s.validateDataSourceConfig(ctx, ds); err != nil {
			return nil, err
		}
	}

	if syncConfigChanged {
		err = s.persistSyncConfigMutation(ctx, ds)
	} else {
		err = s.dsRepo.Update(ctx, ds)
	}
	if err != nil {
		logger.Errorf(ctx, "failed to update data source: %v", err)
		return nil, err
	}
	if latest, loadErr := s.dsRepo.FindByID(ctx, ds.ID); loadErr == nil && latest != nil {
		ds = latest
	}

	// Update cron schedule
	if s.scheduler != nil {
		if err := s.scheduler.Refresh(ctx, ds.ID); err != nil {
			logger.Warnf(ctx, "failed to update cron for ds=%s: %v", ds.ID, err)
		}
	}

	logger.Infof(ctx, "data source updated: id=%s", ds.ID)
	recordKBActivity(ctx, s.audit, ds.TenantID, ds.KnowledgeBaseID, types.AuditActionDataSourceUpdated,
		"data_source", ds.ID, types.AuditOutcomeSuccess,
		map[string]any{"name": ds.Name, "type": ds.Type, "changed_fields": []string{"settings"}})
	return ds, nil
}

// ReconfigureDataSource validates candidate credentials and non-secret
// configuration together, then persists them with one repository update. This
// prevents the edit flow from leaving new credentials paired with old resource
// IDs when a second request fails.
func (s *DataSourceService) ReconfigureDataSource(
	ctx context.Context,
	ds *types.DataSource,
	credentials map[string]interface{},
) (*types.DataSource, error) {
	if ds == nil || ds.ID == "" {
		return nil, datasource.ErrDataSourceInvalid
	}
	existing, err := s.dsRepo.FindByID(ctx, ds.ID)
	if err != nil {
		return nil, err
	}
	if err := s.rejectCredentialMutationDuringSync(ctx, ds.ID); err != nil {
		return nil, err
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
	if ds.Type == "" {
		ds.Type = existing.Type
	}
	if ds.SyncMode == "" {
		ds.SyncMode = existing.SyncMode
	}
	if ds.ConflictStrategy == "" {
		ds.ConflictStrategy = existing.ConflictStrategy
	}
	// Reconfiguration owns connector settings, never operational lifecycle.
	ds.Status = existing.Status
	ds.ErrorMessage = existing.ErrorMessage
	if ds.SyncLogRetentionDays == 0 {
		ds.SyncLogRetentionDays = existing.SyncLogRetentionDays
	}
	if ds.Type != existing.Type {
		return nil, datasource.ErrDataSourceInvalid
	}
	if _, err := s.connectorRegistry.Get(ds.Type); err != nil {
		return nil, err
	}

	config, err := ds.ParseConfig()
	if err != nil || config == nil {
		return nil, datasource.ErrInvalidConfig
	}
	config.Type = ds.Type
	config.Credentials = cloneInterfaceMap(credentials)
	config.StripNonSecretCredentials(ds.Type)
	blob, err := config.ToJSON()
	if err != nil {
		return nil, err
	}
	ds.Config = blob
	// Credentials define the external identity namespace. DingTalk retains the
	// prior snapshot only as cleanup evidence; its connector fingerprint prevents
	// those revisions from skipping fetches in the candidate tenant. Other
	// connectors receive an explicit empty cursor.
	ds.LastSyncCursor = credentialMutationCursor(ds.Type, existing.LastSyncCursor)

	if err := s.validateDataSourceConfig(ctx, ds); err != nil {
		return nil, err
	}
	// Validation can involve network I/O. Check again immediately before the
	// atomic write so a sync that started while validation was in flight also
	// blocks the identity change.
	if err := s.persistSyncConfigMutation(ctx, ds); err != nil {
		logger.Errorf(ctx, "failed to atomically reconfigure data source: %v", err)
		return nil, err
	}
	if latest, loadErr := s.dsRepo.FindByID(ctx, ds.ID); loadErr == nil && latest != nil {
		ds = latest
	}
	if s.scheduler != nil {
		if err := s.scheduler.Refresh(ctx, ds.ID); err != nil {
			logger.Warnf(ctx, "failed to update cron for ds=%s: %v", ds.ID, err)
		}
	}

	logger.Infof(ctx, "data source reconfigured: id=%s", secutils.SanitizeForLog(ds.ID))
	recordKBActivity(ctx, s.audit, ds.TenantID, ds.KnowledgeBaseID, types.AuditActionDataSourceUpdated,
		"data_source", ds.ID, types.AuditOutcomeSuccess,
		map[string]any{
			"name":           ds.Name,
			"type":           ds.Type,
			"changed_fields": []string{"credentials", "settings", "resources"},
		})
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
	if err := s.rejectCredentialMutationDuringSync(ctx, id); err != nil {
		return nil, err
	}
	candidate := cloneDataSourceForMutation(existing)
	parsed, err := candidate.ParseConfig()
	if err != nil {
		return nil, err
	}
	if parsed == nil {
		parsed = &types.DataSourceConfig{Type: candidate.Type}
	}
	parsed.Credentials = credentials
	parsed.StripNonSecretCredentials(candidate.Type)
	blob, err := parsed.ToJSON()
	if err != nil {
		return nil, err
	}
	candidate.Config = blob
	candidate.LastSyncCursor = credentialMutationCursor(candidate.Type, existing.LastSyncCursor)

	// Run live validation now that the credentials are in place — surfaces
	// "wrong token" feedback immediately to the user instead of waiting for
	// the next scheduled sync.
	if err := s.validateDataSourceConfig(ctx, candidate); err != nil {
		return nil, err
	}
	if err := s.persistSyncConfigMutation(ctx, candidate); err != nil {
		return nil, err
	}
	logger.Infof(ctx, "DataSource credentials updated: id=%s", secutils.SanitizeForLog(id))
	recordKBActivity(ctx, s.audit, candidate.TenantID, candidate.KnowledgeBaseID, types.AuditActionDataSourceUpdated,
		"data_source", candidate.ID, types.AuditOutcomeSuccess,
		map[string]any{"name": candidate.Name, "type": candidate.Type, "changed_fields": []string{"credentials"}})
	return candidate, nil
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
	if err := s.rejectCredentialMutationDuringSync(ctx, id); err != nil {
		return err
	}
	candidate := cloneDataSourceForMutation(existing)
	parsed, err := candidate.ParseConfig()
	if err != nil {
		return err
	}
	if parsed == nil {
		return nil
	}
	parsed.StripNonSecretCredentials(candidate.Type)
	if !parsed.HasConfiguredCredentials(candidate.Type) {
		blob, err := parsed.ToJSON()
		if err != nil {
			return err
		}
		candidate.Config = blob
		candidate.LastSyncCursor = credentialMutationCursor(candidate.Type, existing.LastSyncCursor)
		return s.persistSyncConfigMutation(ctx, candidate)
	}
	parsed.Credentials = nil
	blob, err := parsed.ToJSON()
	if err != nil {
		return err
	}
	candidate.Config = blob
	candidate.LastSyncCursor = credentialMutationCursor(candidate.Type, existing.LastSyncCursor)
	if err := s.persistSyncConfigMutation(ctx, candidate); err != nil {
		return err
	}
	logger.Infof(ctx, "DataSource credentials cleared by user: id=%s", secutils.SanitizeForLog(id))
	recordKBActivity(ctx, s.audit, candidate.TenantID, candidate.KnowledgeBaseID, types.AuditActionDataSourceUpdated,
		"data_source", candidate.ID, types.AuditOutcomeSuccess,
		map[string]any{"name": candidate.Name, "type": candidate.Type, "changed_fields": []string{"credentials"}})
	return nil
}

// DeleteDataSource deletes a data source (soft delete)
func (s *DataSourceService) DeleteDataSource(ctx context.Context, id string) error {
	// Verify data source exists
	existing, err := s.dsRepo.FindByID(ctx, id)
	if err != nil {
		return err
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
	recordKBActivity(ctx, s.audit, existing.TenantID, existing.KnowledgeBaseID, types.AuditActionDataSourceDeleted,
		"data_source", existing.ID, types.AuditOutcomeSuccess,
		map[string]any{"name": existing.Name, "type": existing.Type})
	return nil
}

// ValidateConnection tests the connection to an external data source
func (s *DataSourceService) ValidateConnection(ctx context.Context, dsID string) error {
	ds, err := s.GetDataSource(ctx, dsID)
	if err != nil {
		return err
	}
	validationStartStatus := ds.Status

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

	injectDataSourceRuntimeSettings(config, ds)

	// Validate connection
	if err := connector.Validate(ctx, config); err != nil {
		// A paused source remains paused when an explicit connection test fails.
		// For other states, retain the established behavior of surfacing a
		// validation failure through the lifecycle status.
		if validationStartStatus != types.DataSourceStatusPaused {
			ds.Status = types.DataSourceStatusError
		}
		ds.ErrorMessage = err.Error()
		if updateErr := s.persistValidationState(ctx, ds, validationStartStatus); updateErr != nil {
			logger.Warnf(ctx, "failed to persist data source validation error: %v", updateErr)
		}
		return err
	}

	// Clear a previous validation error without changing a paused source back to
	// active. The conditional repository write also prevents an in-flight
	// validation from racing a pause/resume request.
	if validationStartStatus == types.DataSourceStatusError || ds.ErrorMessage != "" {
		if validationStartStatus == types.DataSourceStatusError {
			ds.Status = types.DataSourceStatusActive
		}
		ds.ErrorMessage = ""
		if updateErr := s.persistValidationState(ctx, ds, validationStartStatus); updateErr != nil {
			logger.Warnf(ctx, "failed to clear data source validation error: %v", updateErr)
		}
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

	injectDataSourceRuntimeSettings(config, ds)

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

	injectDataSourceRuntimeSettings(config, ds)

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
	runStartStatus := ds.Status
	wasPaused := runStartStatus == types.DataSourceStatusPaused

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
		SyncLogID:         syncLog.ID,
		ConfigFingerprint: ds.SyncConfigFingerprint(),
		ForceFull:         false,
		Initiator:         types.TaskInitiatorFromContext(ctx),
		Trigger:           "manual",
	}
	langfuse.InjectTracing(ctx, payload)

	payloadJSON, _ := json.Marshal(payload)
	task := asynq.NewTask(types.TypeDataSourceSync, payloadJSON)

	info, err := s.taskEnqueuer.Enqueue(task,
		asynq.Queue(types.QueueSync),
		asynq.MaxRetry(types.DataSourceSyncMaxRetry),
		asynq.Timeout(2*time.Hour),
	)
	if err != nil {
		logger.Errorf(ctx, "failed to enqueue sync task: %v", err)
		syncLog.Status = types.SyncLogStatusFailed
		syncLog.FinishedAt = timePtr(time.Now().UTC())
		syncLog.ErrorMessage = err.Error()
		applySyncOutcomeStatus(ds, true, wasPaused)
		ds.ErrorMessage = fmt.Sprintf("Failed to enqueue sync: %v", err)
		_ = s.persistSyncOutcomeState(ctx, ds, runStartStatus)
		_ = s.syncLogRepo.Update(ctx, syncLog)
		recordKBActivity(ctx, s.audit, ds.TenantID, ds.KnowledgeBaseID, types.AuditActionDataSourceSyncFailed,
			"data_source", ds.ID, types.AuditOutcomeFailed,
			map[string]any{"name": ds.Name, "type": ds.Type, "sync_log_id": syncLog.ID, "trigger": "manual"})
		return nil, err
	}

	logger.Infof(ctx, "sync task enqueued: ds=%s syncLog=%s", dsID, syncLog.ID)
	recordKBActivity(ctx, s.audit, ds.TenantID, ds.KnowledgeBaseID, types.AuditActionDataSourceSyncStarted,
		"data_source", ds.ID, types.AuditOutcomeAccepted,
		map[string]any{"name": ds.Name, "type": ds.Type, "sync_log_id": syncLog.ID,
			"task_id": info.ID, "trigger": "manual", "processing_status": "pending"})
	return syncLog, nil
}

// PauseDataSource pauses a data source's scheduled syncs
func (s *DataSourceService) PauseDataSource(ctx context.Context, id string) error {
	ds, err := s.GetDataSource(ctx, id)
	if err != nil {
		return err
	}

	ds.Status = types.DataSourceStatusPaused
	if err := s.persistOperatorStatus(ctx, ds); err != nil {
		logger.Errorf(ctx, "failed to pause data source: %v", err)
		return err
	}

	// Reconcile the cron entry from the persisted paused state.
	if s.scheduler != nil {
		if err := s.scheduler.Refresh(ctx, id); err != nil {
			logger.Warnf(ctx, "failed to remove cron for paused ds=%s: %v", id, err)
		}
	}

	logger.Infof(ctx, "data source paused: id=%s", id)
	recordKBActivity(ctx, s.audit, ds.TenantID, ds.KnowledgeBaseID, types.AuditActionDataSourcePaused,
		"data_source", ds.ID, types.AuditOutcomeSuccess, map[string]any{"name": ds.Name, "type": ds.Type})
	return nil
}

// ResumeDataSource resumes a paused data source
func (s *DataSourceService) ResumeDataSource(ctx context.Context, id string) error {
	ds, err := s.GetDataSource(ctx, id)
	if err != nil {
		return err
	}

	ds.Status = types.DataSourceStatusActive
	if err := s.persistOperatorStatus(ctx, ds); err != nil {
		logger.Errorf(ctx, "failed to resume data source: %v", err)
		return err
	}
	if latest, loadErr := s.dsRepo.FindByID(ctx, ds.ID); loadErr == nil && latest != nil {
		ds = latest
	}

	// Re-register cron schedule from the authoritative persisted state.
	if s.scheduler != nil {
		if err := s.scheduler.Refresh(ctx, ds.ID); err != nil {
			logger.Warnf(ctx, "failed to re-register cron for ds=%s: %v", ds.ID, err)
		}
	}

	logger.Infof(ctx, "data source resumed: id=%s", id)
	recordKBActivity(ctx, s.audit, ds.TenantID, ds.KnowledgeBaseID, types.AuditActionDataSourceResumed,
		"data_source", ds.ID, types.AuditOutcomeSuccess, map[string]any{"name": ds.Name, "type": ds.Type})
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
func (s *DataSourceService) ProcessSync(ctx context.Context, task *asynq.Task) (retErr error) {
	taskID, _ := asynq.GetTaskID(ctx)
	pendingReconciliation := strings.HasPrefix(taskID, dataSourcePendingReconciliationTaskIDPrefix)
	var payload types.DataSourceSyncPayload
	defer func() {
		pendingReconciliation = pendingReconciliation || payload.PendingReconciliation
		if !pendingReconciliation {
			return
		}
		if payload.SyncLogID == "" {
			payload.SyncLogID = pendingReconciliationSyncLogID(taskID)
		}
		retErr = s.limitPendingReconciliationError(ctx, payload, retErr)
	}()
	if err := json.Unmarshal(task.Payload(), &payload); err != nil {
		logger.Errorf(ctx, "failed to unmarshal sync payload: %v", err)
		return err
	}
	pendingReconciliation = pendingReconciliation || payload.PendingReconciliation
	ctx = payload.Initiator.Apply(ctx)
	ctx = withKBActivityTask(ctx, taskID, payload.Trigger)

	logger.Infof(ctx, "processing data source sync: ds=%s syncLog=%s", payload.DataSourceID, payload.SyncLogID)

	// Get data source
	ds, err := s.GetDataSource(ctx, payload.DataSourceID)
	if err != nil {
		logger.Warnf(ctx, "data source not found (likely deleted), cancelling sync: ds=%s err=%v", payload.DataSourceID, err)
		if syncLog, slErr := s.syncLogRepo.FindByID(ctx, payload.SyncLogID); slErr == nil && syncLog != nil {
			syncLog.Status = types.SyncLogStatusCanceled
			syncLog.FinishedAt = timePtr(time.Now().UTC())
			syncLog.ErrorMessage = "data source has been deleted"
			_ = s.syncLogRepo.Update(ctx, syncLog)
		}
		return nil
	}

	// Get sync log
	syncLog, err := s.syncLogRepo.FindByID(ctx, payload.SyncLogID)
	if err != nil {
		if errors.Is(err, datasource.ErrSyncLogNotFound) {
			logger.Infof(ctx, "skipping data source sync whose log was removed: syncLog=%s", payload.SyncLogID)
			return nil
		}
		logger.Errorf(ctx, "failed to get sync log: %v", err)
		return err
	}
	if syncLog == nil || syncLog.Status != types.SyncLogStatusRunning {
		// A failed attempt writes its log terminal before returning an error.
		// Asynq may redeliver that task, but it must be an idempotent no-op:
		// otherwise a manual/scheduled run created after the terminal write can
		// overlap the old retry and race its cursor/config state.
		logger.Infof(ctx, "skipping terminal data source sync delivery: ds=%s syncLog=%s status=%s",
			payload.DataSourceID, payload.SyncLogID, syncLogStatus(syncLog))
		return nil
	}
	expectedConfigFingerprint := payload.ConfigFingerprint
	if expectedConfigFingerprint == "" {
		// Backward compatibility for tasks queued before generation fencing.
		// They establish their baseline from the configuration read at start.
		expectedConfigFingerprint = ds.SyncConfigFingerprint()
	}
	if ds.SyncConfigFingerprint() != expectedConfigFingerprint {
		s.cancelStaleConfigSync(ctx, syncLog)
		return nil
	}

	kb, kbErr := s.kbService.GetKnowledgeBaseByID(ctx, ds.KnowledgeBaseID)
	if kbErr != nil {
		logger.Warnf(ctx, "knowledge base not found (likely deleted), cancelling sync: kb=%s ds=%s err=%v",
			ds.KnowledgeBaseID, payload.DataSourceID, kbErr)
		syncLog.Status = types.SyncLogStatusCanceled
		syncLog.FinishedAt = timePtr(time.Now().UTC())
		syncLog.ErrorMessage = "knowledge base has been deleted"
		_ = s.syncLogRepo.Update(ctx, syncLog)
		return nil
	}

	runStartStatus := ds.Status
	wasPaused := runStartStatus == types.DataSourceStatusPaused

	// Get connector
	connector, err := s.connectorRegistry.Get(ds.Type)
	if err != nil {
		logger.Errorf(ctx, "connector not found: type=%s", ds.Type)
		syncLog.Status = types.SyncLogStatusFailed
		syncLog.FinishedAt = timePtr(time.Now().UTC())
		syncLog.ErrorMessage = fmt.Sprintf("Connector not found: %s", ds.Type)
		applySyncOutcomeStatus(ds, true, wasPaused)
		ds.ErrorMessage = syncLog.ErrorMessage
		_ = s.persistSyncOutcomeState(ctx, ds, runStartStatus)
		_ = s.syncLogRepo.Update(ctx, syncLog)
		return err
	}

	// Parse configuration
	config, err := ds.ParseConfig()
	if err != nil {
		logger.Errorf(ctx, "failed to parse config: %v", err)
		syncLog.Status = types.SyncLogStatusFailed
		syncLog.FinishedAt = timePtr(time.Now().UTC())
		syncLog.ErrorMessage = fmt.Sprintf("Invalid configuration: %v", err)
		applySyncOutcomeStatus(ds, true, wasPaused)
		ds.ErrorMessage = syncLog.ErrorMessage
		_ = s.persistSyncOutcomeState(ctx, ds, runStartStatus)
		_ = s.syncLogRepo.Update(ctx, syncLog)
		return err
	}
	injectDataSourceRuntimeSettings(config, ds)
	// Surface the KB's multimodal/VLM state to the connector so it only extracts
	// embedded images for OCR when the KB can actually ingest them (never persisted).
	config.MultimodalEnabled = kb.IsMultimodalEnabled()

	// Streaming path: connectors that support it interleave fetch→ingest→
	// checkpoint so a large sync bounds memory and resumes after a timeout
	// instead of restarting (Tencent/WeKnora#2136). Others fall back below.
	if sc, ok := connector.(datasource.StreamingConnector); ok {
		return s.processSyncStreaming(ctx, sc, ds, syncLog, config, payload, runStartStatus)
	}

	// Fetch items based on sync mode
	var items []types.FetchedItem
	var nextCursor *types.SyncCursor
	var fetchErr error

	if payload.ForceFull || ds.SyncMode == types.SyncModeFull {
		// Full sync. Connectors with snapshot reconciliation still re-fetch every
		// current item, but receive the previous cursor so removals and identity
		// switches do not leave stale knowledge behind.
		if cursorAware, ok := connector.(datasource.CursorAwareFullSyncConnector); ok {
			cursor, _ := ds.ParseSyncCursor()
			items, nextCursor, fetchErr = cursorAware.FetchAllWithCursor(
				ctx,
				config,
				config.ResourceIDs,
				cursor,
			)
		} else {
			items, fetchErr = connector.FetchAll(ctx, config, config.ResourceIDs)
		}
		logger.Infof(ctx, "full sync fetched %d items", len(items))
	} else {
		// Incremental sync
		cursor, _ := ds.ParseSyncCursor()
		items, nextCursor, fetchErr = connector.FetchIncremental(ctx, config, cursor)
		logger.Infof(ctx, "incremental sync fetched %d items", len(items))
	}
	currentGeneration, generationErr := s.syncConfigGenerationMatches(
		ctx,
		ds.ID,
		expectedConfigFingerprint,
	)
	if generationErr != nil {
		return generationErr
	}
	if !currentGeneration {
		s.cancelStaleConfigSync(ctx, syncLog)
		return nil
	}

	var fetchWarnings []string
	var partialFetch *datasource.PartialFetchError
	if errors.As(fetchErr, &partialFetch) {
		fetchWarnings = partialFetch.Details
		fetchErr = nil
	}

	if fetchErr != nil {
		// Persist connector cursor even when fetch failed so transient outages
		// (e.g. RSS feed downtime) do not force a full re-ingest on recovery.
		if nextCursor != nil {
			if cursorJSON, cerr := nextCursor.ToJSON(); cerr == nil {
				ds.LastSyncCursor = cursorJSON
				if uerr := s.persistSyncProgressState(ctx, ds); uerr != nil {
					logger.Warnf(ctx, "failed to persist sync cursor after fetch error: %v", uerr)
				}
			}
		}
		logger.Errorf(ctx, "fetch operation failed: %v", fetchErr)
		syncLog.Status = types.SyncLogStatusFailed
		syncLog.FinishedAt = timePtr(time.Now().UTC())
		syncLog.ErrorMessage = fmt.Sprintf("Fetch failed: %v", fetchErr)
		applySyncOutcomeStatus(ds, true, wasPaused)
		ds.ErrorMessage = syncLog.ErrorMessage
		_ = s.persistSyncOutcomeState(ctx, ds, runStartStatus)
		_ = s.syncLogRepo.Update(ctx, syncLog)
		return fetchErr
	}

	// Process fetched items and write to knowledge base
	var result = &types.SyncResult{
		Total: len(items),
	}

	// Set tenant context so KnowledgeService can resolve tenant info correctly
	ctx = context.WithValue(ctx, types.TenantIDContextKey, ds.TenantID)

	tenant, err := s.tenantRepo.GetTenantByID(ctx, ds.TenantID)
	if err != nil {
		logger.Errorf(ctx, "failed to get tenant info: %v", err)
		syncLog.Status = types.SyncLogStatusFailed
		syncLog.FinishedAt = timePtr(time.Now().UTC())
		syncLog.ErrorMessage = fmt.Sprintf("Failed to get tenant info: %v", err)
		applySyncOutcomeStatus(ds, true, wasPaused)
		ds.ErrorMessage = syncLog.ErrorMessage
		_ = s.persistSyncOutcomeState(ctx, ds, runStartStatus)
		_ = s.syncLogRepo.Update(ctx, syncLog)
		return err
	}
	ctx = context.WithValue(ctx, types.TenantInfoContextKey, tenant)

	// Auto-tag: find or create a tag for this data source so synced items are easily identifiable
	autoTagIDs := s.resolveAutoTagIDs(ctx, ds)

	applyItem := func(item types.FetchedItem) {
		s.applyFetchedItem(withKBActivitySuppressed(ctx), ds, &item, autoTagIDs, result)
	}
	if ds.Type == types.ConnectorTypeDingTalk {
		// A DingTalk identity/configuration change can return replacement
		// documents and deletion markers for the previous identity in the same
		// snapshot. Apply content first and defer every physical deletion until
		// all candidates are durable. Otherwise an asynchronous candidate can
		// still be pending (or fail) after the old owned row has already been
		// removed, defeating the last-known-good replacement guarantee. Pending
		// and failed runs retain the previous cursor, so the same deletion
		// markers are emitted again after the candidates converge.
		for _, item := range items {
			if !item.IsDeleted {
				applyItem(item)
			}
		}
		if result.Pending == 0 && result.Failed == 0 {
			for _, item := range items {
				if item.IsDeleted {
					applyItem(item)
				}
			}
		}
	} else {
		for _, item := range items {
			applyItem(item)
		}
	}
	currentGeneration, generationErr = s.syncConfigGenerationMatches(
		ctx,
		ds.ID,
		expectedConfigFingerprint,
	)
	if generationErr != nil {
		return generationErr
	}
	if !currentGeneration {
		s.cancelStaleConfigSync(ctx, syncLog)
		return nil
	}

	resultJSON, _ := result.ToJSON()
	if result.Pending > 0 {
		pendingMessage := fmt.Sprintf(
			"%d document(s) are still processing and will be checked again",
			result.Pending,
		)
		if payload.PendingReconciliation && syncRetryBudgetExhausted(ctx) {
			pendingCount := result.Pending
			result.Pending = 0
			result.Failed += pendingCount
			recordSyncError(result, types.SyncItemError{
				Code:    "ingest_failed",
				Message: "Timed out waiting for document processing to complete",
			})
			resultJSON, _ = result.ToJSON()
			status := types.SyncLogStatusPartial
			if result.Failed == result.Total &&
				result.Created == 0 && result.Updated == 0 &&
				result.Deleted == 0 && result.Skipped == 0 {
				status = types.SyncLogStatusFailed
			}
			timeoutErr := fmt.Errorf(
				"timed out waiting for %d document(s) to finish processing",
				pendingCount,
			)
			s.updateSyncRunResult(
				ctx,
				ds,
				syncLog,
				result,
				resultJSON,
				status,
				timeoutErr.Error(),
				runStartStatus,
			)
			return timeoutErr
		}
		s.updateSyncRunProgress(ctx, syncLog, result, resultJSON, pendingMessage)
		if !payload.PendingReconciliation {
			if err := s.enqueuePendingReconciliation(ctx, payload); err != nil {
				if syncRetryBudgetExhausted(ctx) {
					s.finishPendingReconciliationScheduleFailure(
						ctx,
						ds,
						syncLog,
						result,
						runStartStatus,
						err,
					)
				}
				return err
			}
			return nil
		}
		return ErrDataSourceIngestPending
	}
	if err := allFetchedItemsFailedError(result); err != nil {
		logger.Errorf(ctx, "data source sync failed while processing fetched items: %v", err)
		s.updateSyncRunResult(ctx, ds, syncLog, result, resultJSON, types.SyncLogStatusFailed, err.Error(), runStartStatus)
		return err
	}

	ds.LastSyncAt = timePtr(time.Now().UTC())
	syncStatus, syncErrorMessage := finalizeBatchSyncCursor(ds, nextCursor, result)
	if len(fetchWarnings) > 0 {
		syncStatus = types.SyncLogStatusPartial
		sourceWarning := fmt.Sprintf(
			"Some source resources failed: %s",
			strings.Join(fetchWarnings, "; "),
		)
		if syncErrorMessage == "" {
			syncErrorMessage = sourceWarning
		} else {
			syncErrorMessage += "; " + sourceWarning
		}
		for _, w := range fetchWarnings {
			result.Errors = append(result.Errors, types.SyncItemError{Message: w})
		}
	}
	resultJSON, _ = result.ToJSON()
	s.updateSyncRunResult(ctx, ds, syncLog, result, resultJSON, syncStatus, syncErrorMessage, runStartStatus)

	logger.Infof(ctx, "data source sync completed: ds=%s created=%d updated=%d deleted=%d",
		payload.DataSourceID, syncLog.ItemsCreated, syncLog.ItemsUpdated, syncLog.ItemsDeleted)

	return nil
}

// finalizeBatchSyncCursor advances a batch connector cursor only when every
// fetched item was applied successfully. A connector has already encoded new
// revisions in nextCursor; persisting it after a partial ingestion failure
// would make the failed document look unchanged forever. Replaying successful
// items on the next run is the safe at-least-once tradeoff.
func finalizeBatchSyncCursor(
	ds *types.DataSource,
	nextCursor *types.SyncCursor,
	result *types.SyncResult,
) (string, string) {
	if result != nil && result.Failed > 0 {
		return types.SyncLogStatusPartial,
			fmt.Sprintf("%d document(s) failed to sync and will be retried", result.Failed)
	}
	if result != nil && result.SuppressedDeletions > 0 {
		// Do not consume source-side deletion evidence while deletion sync is
		// disabled. Keeping the prior cursor lets a later opt-in re-emit and
		// physically reconcile those rows.
		return types.SyncLogStatusSuccess, ""
	}
	if ds != nil && nextCursor != nil {
		if cursorJSON, err := nextCursor.ToJSON(); err == nil {
			ds.LastSyncCursor = cursorJSON
		}
	}
	return types.SyncLogStatusSuccess, ""
}

// resolveAutoTagIDs finds or creates the per-data-source tag applied to every
// synced item so results are identifiable in the KB. A tag failure is
// non-fatal: the sync proceeds untagged.
func (s *DataSourceService) resolveAutoTagIDs(ctx context.Context, ds *types.DataSource) []string {
	autoTagIDs := []string{}
	if autoTag, tagErr := s.tagService.FindOrCreateTagByName(ctx, ds.KnowledgeBaseID, ds.Name); tagErr != nil {
		logger.Warnf(ctx, "failed to find/create auto-tag %q: %v (proceeding without tag)", ds.Name, tagErr)
	} else if autoTag != nil {
		autoTagIDs = append(autoTagIDs, autoTag.ID)
		logger.Infof(ctx, "using auto-tag %q (id=%s) for data source sync", ds.Name, autoTag.ID)
	}
	return autoTagIDs
}

// maxSyncResultErrors bounds the per-item error sample retained in
// SyncResult.Errors. That slice is persisted as jsonb and returned in every
// sync-log list response, so an unbounded list on a sync that fails thousands of
// documents means multi-MB DB rows and payloads. The accurate failure count
// lives in SyncResult.Failed (a bounded int); this list only keeps a sample for
// display (Tencent/WeKnora#2136 / #1262).
const maxSyncResultErrors = 100

// recordSyncError appends an error sample to result.Errors, capped at
// maxSyncResultErrors. Callers still increment result.Failed for the exact count.
func recordSyncError(result *types.SyncResult, item types.SyncItemError) {
	if len(result.Errors) < maxSyncResultErrors {
		result.Errors = append(result.Errors, item)
	}
}

// syncLogIdentifier returns a stable, non-reversible reference suitable for
// correlating sync log lines without disclosing source-side document IDs.
func syncLogIdentifier(value string) string {
	if value == "" {
		return "none"
	}
	sum := sha256.Sum256([]byte(value))
	return fmt.Sprintf("sha256:%x", sum[:6])
}

// fetchFailureSyncError maps a connector error item into a structured, user-
// facing sample. Connectors that classify their errors (Feishu) provide a stable
// i18n code + params via metadata so the frontend localises it to the viewer's
// language; the raw status/body/log_id never leaves the server logs. Connectors
// without codes keep the raw text as a Message fallback. Best practice per
// Airbyte/Fivetran/Onyx: humanised, actionable, localised UI; raw detail in logs.
func fetchFailureSyncError(item *types.FetchedItem, rawMsg string) types.SyncItemError {
	e := types.SyncItemError{Title: item.Title}
	if code := item.Metadata["error_reason_code"]; code != "" {
		e.Code = code
		if v := item.Metadata["error_reason_code_value"]; v != "" {
			e.Params = map[string]string{"code": v}
		}
		e.Message = item.Metadata["error_reason"] // fallback if the client lacks the key
	} else {
		e.Message = rawMsg
	}
	return e
}

// applyFetchedItem writes a single fetched item into the knowledge base and
// updates result counters. It is the shared core of the batch loop and the
// streaming handler so item classification (deleted / empty / ingest outcome)
// stays identical across both fetch paths.
func (s *DataSourceService) applyFetchedItem(
	ctx context.Context, ds *types.DataSource, item *types.FetchedItem,
	tagIDs []string, result *types.SyncResult,
) {
	if item.IsDeleted {
		if !ds.SyncDeletions {
			result.SuppressedDeletions++
			result.Skipped++
			return
		}
		deleted, err := s.deleteOwnedFetchedItem(ctx, ds, item.ExternalID)
		if err != nil {
			logger.Warnf(ctx, "failed to delete fetched item item_ref=%s: %v", syncLogIdentifier(item.ExternalID), err)
			result.Failed++
			recordSyncError(result, types.SyncItemError{
				Title:   item.Title,
				Code:    "delete_failed",
				Message: "Delete failed; see server logs",
			})
		} else if deleted {
			result.Deleted++
		} else {
			result.Skipped++
		}
		return
	}

	if len(item.Content) == 0 && item.URL == "" {
		// Check if this is an error item from the connector (failed to fetch content)
		if errMsg, hasErr := item.Metadata["error"]; hasErr {
			logger.Warnf(ctx, "item_ref=%s fetch failed: %s", syncLogIdentifier(item.ExternalID), errMsg)
			result.Failed++
			recordSyncError(result, fetchFailureSyncError(item, errMsg))
		} else {
			logger.Infof(ctx, "skipping item_ref=%s: no content or URL", syncLogIdentifier(item.ExternalID))
			result.Skipped++
		}
		return
	}

	isUpdate, err := s.ingestItem(ctx, ds, item, tagIDs)
	if err != nil {
		var dupErr *types.DuplicateKnowledgeError
		switch {
		case errors.Is(err, ErrDataSourceIngestPending):
			logger.Infof(
				ctx,
				"item_ref=%s is awaiting asynchronous knowledge processing",
				syncLogIdentifier(item.ExternalID),
			)
			result.Pending++
		case errors.As(err, &dupErr):
			// Duplicate file/URL is not a failure — count as skipped.
			logger.Infof(ctx, "item_ref=%s already exists, skipping", syncLogIdentifier(item.ExternalID))
			result.Skipped++
		case item.Metadata["embedded_image"] == "true":
			// An image extracted from a document for OCR is a best-effort
			// enrichment, not the document itself. If the KB cannot ingest it
			// (VLM/object-storage not configured for images, or a transient error),
			// skip it rather than failing the whole sync: the doc body already
			// synced, and the image stays in SubtreeKeep for a later retry once the
			// KB is configured.
			logger.Infof(ctx, "skipping embedded image item_ref=%s, not ingested: %v",
				syncLogIdentifier(item.ExternalID), err)
			result.Skipped++
		default:
			logger.Warnf(ctx, "failed to ingest item_ref=%s: %v", syncLogIdentifier(item.ExternalID), err)
			result.Failed++
			recordSyncError(result, types.SyncItemError{
				Title:   item.Title,
				Code:    "ingest_failed",
				Message: "Ingest failed; see server logs",
			})
		}
	} else if isUpdate {
		result.Updated++
	} else {
		result.Created++
	}
}

// streamStartCursor decides which cursor a streaming fetch should resume from.
// A user-triggered full sync on its first attempt drops the cursor so every
// item is re-fetched; a retried full sync (attempt > 0) and every incremental
// sync resume from the last persisted checkpoint so a timed-out run converges
// instead of restarting from scratch.
func streamStartCursor(ds *types.DataSource, forceFull bool, attempt int) (*types.SyncCursor, error) {
	if forceFull && attempt == 0 {
		return nil, nil
	}
	return ds.ParseSyncCursor()
}

// streamSyncHandler adapts a streaming fetch to the knowledge-base ingest path.
// Emit ingests each item as it arrives (bounding memory) and Checkpoint persists
// the connector cursor plus live progress counts at page boundaries.
type streamSyncHandler struct {
	svc     *DataSourceService
	ds      *types.DataSource
	tagIDs  []string
	result  *types.SyncResult
	syncLog *types.SyncLog
}

// Emit ingests one streamed item. A canceled context aborts the stream so the
// connector stops fetching; per-item ingest failures are recorded in result and
// do NOT abort (matching the batch loop, which never fails the whole sync for
// one bad document).
func (h *streamSyncHandler) Emit(ctx context.Context, item types.FetchedItem) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	h.result.Total++
	h.svc.applyFetchedItem(withKBActivitySuppressed(ctx), h.ds, &item, h.tagIDs, h.result)
	return nil
}

// Checkpoint persists the connector cursor onto the data source and mirrors the
// running counts into the sync log so progress survives a crash and the UI can
// reflect a long sync mid-flight instead of jumping from 0 to done.
func (h *streamSyncHandler) Checkpoint(ctx context.Context, cursor *types.SyncCursor) error {
	if cursor == nil {
		return nil
	}
	cursorJSON, err := cursor.ToJSON()
	if err != nil {
		return err
	}
	h.ds.LastSyncCursor = cursorJSON
	if err := h.svc.persistSyncProgressState(ctx, h.ds); err != nil {
		return err
	}

	// Best-effort live progress; a failure here must not abort the sync.
	h.syncLog.ItemsTotal = h.result.Total
	h.syncLog.ItemsCreated = h.result.Created
	h.syncLog.ItemsUpdated = h.result.Updated
	h.syncLog.ItemsDeleted = h.result.Deleted
	h.syncLog.ItemsSkipped = h.result.Skipped
	h.syncLog.ItemsFailed = h.result.Failed
	if err := h.svc.syncLogRepo.UpdateResult(ctx, h.syncLog); err != nil {
		logger.Warnf(ctx, "failed to persist sync log progress at checkpoint: %v", err)
	}
	return nil
}

const (
	credentialValidationDataSourceID = "credential-validation"
	credentialPreviewDataSourceID    = "credential-preview"
)

func freshSyncCursorJSON() types.JSON {
	return types.JSON([]byte("{}"))
}

func credentialMutationCursor(connectorType string, previous types.JSON) types.JSON {
	if connectorType == types.ConnectorTypeDingTalk && len(previous) > 0 {
		// DingTalk's connector cursor carries a hashed external-identity
		// generation. It will never reuse old revisions for skipping, but keeps
		// them in a cleanup-only scope until the replacement identity has been
		// observed completely and can safely delete the old owned KB rows.
		return append(types.JSON(nil), previous...)
	}
	return freshSyncCursorJSON()
}

func cloneDataSourceForMutation(ds *types.DataSource) *types.DataSource {
	if ds == nil {
		return nil
	}
	cloned := *ds
	cloned.Config = append(types.JSON(nil), ds.Config...)
	cloned.LastSyncCursor = append(types.JSON(nil), ds.LastSyncCursor...)
	return &cloned
}

func (s *DataSourceService) syncConfigGenerationMatches(
	ctx context.Context,
	dataSourceID string,
	expected string,
) (bool, error) {
	current, err := s.dsRepo.FindByID(ctx, dataSourceID)
	if err != nil {
		return false, err
	}
	return current != nil && current.SyncConfigFingerprint() == expected, nil
}

func (s *DataSourceService) cancelStaleConfigSync(ctx context.Context, syncLog *types.SyncLog) {
	if syncLog == nil {
		return
	}
	syncLog.Status = types.SyncLogStatusCanceled
	syncLog.FinishedAt = timePtr(time.Now().UTC())
	syncLog.ErrorMessage = "data source configuration changed after this sync was queued"
	if err := s.syncLogRepo.Update(ctx, syncLog); err != nil {
		logger.Errorf(ctx, "failed to cancel stale data source sync: %v", err)
	}
}

type noRunningSyncDataSourceUpdater interface {
	UpdateIfNoRunningSync(context.Context, *types.DataSource) (bool, error)
}

type dataSourceStatusUpdater interface {
	UpdateStatus(context.Context, string, string) error
}

type dataSourceValidationStateUpdater interface {
	UpdateValidationStateIfConfigUnchanged(
		context.Context,
		string,
		types.JSON,
		string,
		string,
		string,
	) (bool, error)
}

func (s *DataSourceService) persistOperatorStatus(
	ctx context.Context,
	ds *types.DataSource,
) error {
	if updater, ok := s.dsRepo.(dataSourceStatusUpdater); ok {
		return updater.UpdateStatus(ctx, ds.ID, ds.Status)
	}
	// Compatibility for narrow in-memory repositories. Production implements
	// UpdateStatus and never reaches the broad snapshot write.
	return s.dsRepo.Update(ctx, ds)
}

func (s *DataSourceService) persistValidationState(
	ctx context.Context,
	ds *types.DataSource,
	expectedStatus string,
) error {
	if updater, ok := s.dsRepo.(dataSourceValidationStateUpdater); ok {
		_, err := updater.UpdateValidationStateIfConfigUnchanged(
			ctx,
			ds.ID,
			ds.Config,
			expectedStatus,
			ds.Status,
			ds.ErrorMessage,
		)
		return err
	}
	// Compatibility for narrow in-memory repositories. The concrete repository
	// uses a generation-conditional field update.
	return s.dsRepo.Update(ctx, ds)
}

// persistSyncConfigMutation uses the production repository's DB-level
// serialization with SyncLogRepository.Create. Lightweight/fake repositories
// fall back to a final fail-closed check immediately before the update.
func (s *DataSourceService) persistSyncConfigMutation(
	ctx context.Context,
	ds *types.DataSource,
) error {
	if updater, ok := s.dsRepo.(noRunningSyncDataSourceUpdater); ok {
		updated, err := updater.UpdateIfNoRunningSync(ctx, ds)
		if err != nil {
			return err
		}
		if !updated {
			return datasource.ErrSyncInProgress
		}
		return nil
	}
	if err := s.rejectCredentialMutationDuringSync(ctx, ds.ID); err != nil {
		return err
	}
	return s.dsRepo.Update(ctx, ds)
}

// rejectCredentialMutationDuringSync prevents a running task from continuing
// to ingest content fetched with an old external identity after the stored
// credentials have switched. Scheduled and manual tasks create a "running"
// sync log before they are enqueued, so the guard also covers queued work.
//
// A nil repository is tolerated for narrow unit-test/service constructions;
// production always injects the repository.
func (s *DataSourceService) rejectCredentialMutationDuringSync(
	ctx context.Context,
	dataSourceID string,
) error {
	if s.syncLogRepo == nil {
		return nil
	}
	running, err := s.syncLogRepo.HasRunningSync(ctx, dataSourceID)
	if err != nil {
		return fmt.Errorf("check running data source sync: %w", err)
	}
	if running {
		return datasource.ErrSyncInProgress
	}
	return nil
}

func cloneInterfaceMap(values map[string]interface{}) map[string]interface{} {
	if values == nil {
		return nil
	}
	cloned := make(map[string]interface{}, len(values))
	for key, value := range values {
		cloned[key] = value
	}
	return cloned
}

// injectDataSourceRuntimeSettings adds identity used for connector-side tenant
// isolation. These values are runtime context and are never persisted back into
// the data source's stored connector configuration.
func injectDataSourceRuntimeSettings(config *types.DataSourceConfig, ds *types.DataSource) {
	if config == nil || ds == nil {
		return
	}
	injectDataSourceRuntimeIdentity(config, ds.TenantID, ds.ID)
}

func injectDataSourceRuntimeIdentity(
	config *types.DataSourceConfig,
	tenantID uint64,
	dataSourceID string,
) {
	if config == nil {
		return
	}
	if config.Settings == nil {
		config.Settings = make(map[string]interface{})
	}
	config.Settings["tenant_id"] = fmt.Sprintf("%d", tenantID)
	config.Settings["data_source_id"] = dataSourceID
}

// processSyncStreaming runs a sync through a StreamingConnector, ingesting each
// item as it arrives and checkpointing progress so the run is memory-bounded and
// resumable after a timeout.
func (s *DataSourceService) processSyncStreaming(
	ctx context.Context, sc datasource.StreamingConnector,
	ds *types.DataSource, syncLog *types.SyncLog,
	config *types.DataSourceConfig, payload types.DataSourceSyncPayload, runStartStatus string,
) error {
	// Tenant + auto-tag setup must precede fetching because the stream ingests
	// each item on the fly.
	ctx = context.WithValue(ctx, types.TenantIDContextKey, ds.TenantID)
	tenant, err := s.tenantRepo.GetTenantByID(ctx, ds.TenantID)
	if err != nil {
		logger.Errorf(ctx, "failed to get tenant info: %v", err)
		s.updateSyncRunResult(ctx, ds, syncLog, &types.SyncResult{}, nil,
			types.SyncLogStatusFailed, fmt.Sprintf("Failed to get tenant info: %v", err), runStartStatus)
		return err
	}
	ctx = context.WithValue(ctx, types.TenantInfoContextKey, tenant)

	autoTagIDs := s.resolveAutoTagIDs(ctx, ds)

	forceFull := payload.ForceFull || ds.SyncMode == types.SyncModeFull
	attempt, _ := asynq.GetRetryCount(ctx)
	startCursor, err := streamStartCursor(ds, forceFull, attempt)
	if err != nil {
		logger.Errorf(ctx, "failed to parse sync cursor: %v", err)
		s.updateSyncRunResult(ctx, ds, syncLog, &types.SyncResult{}, nil,
			types.SyncLogStatusFailed, fmt.Sprintf("Invalid cursor: %v", err), runStartStatus)
		return err
	}

	result := &types.SyncResult{}
	handler := &streamSyncHandler{svc: s, ds: ds, tagIDs: autoTagIDs, result: result, syncLog: syncLog}

	nextCursor, fetchErr := sc.FetchStream(ctx, config, startCursor, handler)
	if fetchErr != nil {
		// Progress so far is already checkpointed onto ds.LastSyncCursor; leave
		// it in place so the Asynq retry resumes from there. Persist counts.
		logger.Errorf(ctx, "streaming fetch failed: %v", fetchErr)
		resultJSON, _ := result.ToJSON()
		s.updateSyncRunResult(ctx, ds, syncLog, result, resultJSON,
			types.SyncLogStatusFailed, fmt.Sprintf("Fetch failed: %v", fetchErr), runStartStatus)
		return fetchErr
	}

	resultJSON, _ := result.ToJSON()
	if err := allFetchedItemsFailedError(result); err != nil {
		logger.Errorf(ctx, "streaming sync failed while processing fetched items: %v", err)
		s.updateSyncRunResult(ctx, ds, syncLog, result, resultJSON, types.SyncLogStatusFailed, err.Error(), runStartStatus)
		return err
	}

	// Persist the final cursor for the next incremental sync.
	if nextCursor != nil {
		if cursorJSON, cerr := nextCursor.ToJSON(); cerr == nil {
			ds.LastSyncCursor = cursorJSON
		}
	}
	ds.LastSyncAt = timePtr(time.Now().UTC())

	// Surface per-document failures as a partial sync (not silent success), so
	// the sync-log drawer's failure detail explains which docs didn't make it —
	// the visibility gap behind "status normal but not everything syncs"
	// (Tencent/WeKnora#2136). Failed nodes were not advanced in the cursor, so
	// the next run retries them.
	status := types.SyncLogStatusSuccess
	errMsg := ""
	if result.Failed > 0 {
		status = types.SyncLogStatusPartial
		errMsg = fmt.Sprintf("%d document(s) failed to sync", result.Failed)
	}
	s.updateSyncRunResult(ctx, ds, syncLog, result, resultJSON, status, errMsg, runStartStatus)
	logger.Infof(ctx, "streaming sync completed: ds=%s created=%d updated=%d deleted=%d skipped=%d failed=%d",
		payload.DataSourceID, result.Created, result.Updated, result.Deleted, result.Skipped, result.Failed)
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
	runStartStatus string,
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

	applySyncOutcomeStatus(
		ds,
		status == types.SyncLogStatusFailed,
		runStartStatus == types.DataSourceStatusPaused,
	)
	ds.ErrorMessage = errorMessage
	ds.LastSyncResult = resultJSON
	if err := s.persistSyncOutcomeState(ctx, ds, runStartStatus); err != nil {
		logger.Errorf(ctx, "failed to update data source: %v", err)
	}
	// Keep the persisted log in "running" state until every data-source sync
	// field (especially the cursor) has been written. Credential/configuration
	// mutation uses that running row as its exclusion barrier.
	if err := s.syncLogRepo.UpdateResult(ctx, syncLog); err != nil {
		logger.Errorf(ctx, "failed to update sync log: %v", err)
	}
	action := types.AuditActionDataSourceSyncCompleted
	outcome := types.AuditOutcomeSuccess
	if status == types.SyncLogStatusFailed {
		action = types.AuditActionDataSourceSyncFailed
		outcome = types.AuditOutcomeFailed
	} else if status == types.SyncLogStatusPartial {
		outcome = types.AuditOutcomePartial
	}
	recordKBActivity(ctx, s.audit, ds.TenantID, ds.KnowledgeBaseID, action,
		"data_source", ds.ID, outcome,
		map[string]any{
			"name": ds.Name, "type": ds.Type, "sync_log_id": syncLog.ID,
			"total": result.Total, "created": result.Created, "updated": result.Updated,
			"deleted": result.Deleted, "skipped": result.Skipped, "failed": result.Failed,
		})
}

// updateSyncRunProgress mirrors a retryable asynchronous-ingest attempt without
// making the log terminal. The running row remains the database exclusion
// barrier, so manual/scheduled runs cannot overlap while the same task checks
// whether its knowledge workers have completed.
func (s *DataSourceService) updateSyncRunProgress(
	ctx context.Context,
	syncLog *types.SyncLog,
	result *types.SyncResult,
	resultJSON types.JSON,
	message string,
) {
	syncLog.ItemsTotal = result.Total
	syncLog.ItemsCreated = result.Created
	syncLog.ItemsUpdated = result.Updated
	syncLog.ItemsDeleted = result.Deleted
	syncLog.ItemsSkipped = result.Skipped
	syncLog.ItemsFailed = result.Failed
	syncLog.Status = types.SyncLogStatusRunning
	syncLog.FinishedAt = nil
	syncLog.ErrorMessage = message
	syncLog.Result = resultJSON
	if err := s.syncLogRepo.UpdateResult(ctx, syncLog); err != nil {
		logger.Errorf(ctx, "failed to update pending sync progress: %v", err)
	}
}

const dataSourcePendingReconciliationTaskIDPrefix = "dssync-pending:"

func (s *DataSourceService) enqueuePendingReconciliation(
	ctx context.Context,
	payload types.DataSourceSyncPayload,
) error {
	if s.taskEnqueuer == nil {
		return errors.New("data-source task enqueuer is not configured")
	}
	if payload.SyncLogID == "" {
		return errors.New("cannot enqueue pending reconciliation without a sync log ID")
	}

	payload.PendingReconciliation = true
	payloadJSON, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal pending data-source reconciliation: %w", err)
	}
	taskID := dataSourcePendingReconciliationTaskIDPrefix + payload.SyncLogID
	_, err = s.taskEnqueuer.Enqueue(
		asynq.NewTask(types.TypeDataSourceSync, payloadJSON),
		asynq.Queue(types.QueueSync),
		asynq.MaxRetry(types.DataSourceIngestPendingMaxRetry),
		asynq.Timeout(2*time.Hour),
		asynq.ProcessIn(types.DataSourceIngestPendingRetryDelay),
		asynq.TaskID(taskID),
	)
	if errors.Is(err, asynq.ErrTaskIDConflict) {
		logger.Infof(ctx, "pending data-source reconciliation already enqueued: syncLog=%s", payload.SyncLogID)
		return nil
	}
	if err != nil {
		return fmt.Errorf("enqueue pending data-source reconciliation: %w", err)
	}
	logger.Infof(ctx, "pending data-source reconciliation enqueued: syncLog=%s", payload.SyncLogID)
	return nil
}

func pendingReconciliationSyncLogID(taskID string) string {
	if !strings.HasPrefix(taskID, dataSourcePendingReconciliationTaskIDPrefix) {
		return ""
	}
	return strings.TrimPrefix(taskID, dataSourcePendingReconciliationTaskIDPrefix)
}

func dataSourceTaskRetryMetadata(ctx context.Context) (retryCount, maxRetry int, ok bool) {
	retryCount, retryOK := asynq.GetRetryCount(ctx)
	maxRetry, maxRetryOK := asynq.GetMaxRetry(ctx)
	if retryOK && maxRetryOK {
		return retryCount, maxRetry, true
	}
	return types.TaskRetryMetadata(ctx)
}

func syncRetryBudgetExhausted(ctx context.Context) bool {
	retried, maxRetry, ok := dataSourceTaskRetryMetadata(ctx)
	return ok && retried >= maxRetry
}

func (s *DataSourceService) limitPendingReconciliationError(
	ctx context.Context,
	payload types.DataSourceSyncPayload,
	taskErr error,
) error {
	if taskErr == nil ||
		errors.Is(taskErr, ErrDataSourceIngestPending) ||
		errors.Is(taskErr, asynq.SkipRetry) {
		return taskErr
	}

	retried, maxRetry, ok := dataSourceTaskRetryMetadata(ctx)
	if !ok ||
		maxRetry <= types.DataSourceSyncMaxRetry ||
		retried < types.DataSourceSyncMaxRetry {
		return taskErr
	}

	if err := s.finishRunningPendingReconciliation(ctx, payload, taskErr); err != nil {
		return errors.Join(
			taskErr,
			fmt.Errorf("finalize pending data-source reconciliation: %w", err),
		)
	}
	return fmt.Errorf("%w: %w", taskErr, asynq.SkipRetry)
}

func (s *DataSourceService) finishRunningPendingReconciliation(
	ctx context.Context,
	payload types.DataSourceSyncPayload,
	taskErr error,
) error {
	if payload.SyncLogID == "" {
		return errors.New("pending reconciliation has no sync log ID")
	}
	if s.syncLogRepo == nil {
		return errors.New("sync log repository is not configured")
	}
	syncLog, err := s.syncLogRepo.FindByID(ctx, payload.SyncLogID)
	if err != nil {
		return fmt.Errorf("load sync log %s: %w", payload.SyncLogID, err)
	}
	if syncLog == nil {
		return fmt.Errorf("sync log %s was not found", payload.SyncLogID)
	}
	if syncLog.Status != types.SyncLogStatusRunning {
		return nil
	}
	syncLog.Status = types.SyncLogStatusFailed
	syncLog.FinishedAt = timePtr(time.Now().UTC())
	syncLog.ErrorMessage = fmt.Sprintf(
		"Pending ingestion reconciliation stopped after %d retries: %v",
		types.DataSourceSyncMaxRetry,
		taskErr,
	)
	if err := s.syncLogRepo.Update(ctx, syncLog); err != nil {
		return fmt.Errorf("update sync log %s: %w", payload.SyncLogID, err)
	}
	return nil
}

func (s *DataSourceService) finishPendingReconciliationScheduleFailure(
	ctx context.Context,
	ds *types.DataSource,
	syncLog *types.SyncLog,
	result *types.SyncResult,
	runStartStatus string,
	scheduleErr error,
) {
	pendingCount := result.Pending
	result.Pending = 0
	result.Failed += pendingCount
	recordSyncError(result, types.SyncItemError{
		Code:    "ingest_reconciliation_enqueue_failed",
		Message: "Failed to schedule another document-processing status check",
	})
	resultJSON, _ := result.ToJSON()
	status := types.SyncLogStatusPartial
	if result.Failed == result.Total &&
		result.Created == 0 && result.Updated == 0 &&
		result.Deleted == 0 && result.Skipped == 0 {
		status = types.SyncLogStatusFailed
	}
	s.updateSyncRunResult(
		ctx,
		ds,
		syncLog,
		result,
		resultJSON,
		status,
		fmt.Sprintf("Failed to schedule document-processing status check: %v", scheduleErr),
		runStartStatus,
	)
}

// applySyncOutcomeStatus derives only the run's proposed status. The repository
// applies it with a single SQL CASE against the status observed at run start.
// Reading the row here would introduce an ABA race: active -> paused -> active
// can make a stale paused snapshot overwrite the operator's final resume.
func applySyncOutcomeStatus(
	ds *types.DataSource,
	failed bool,
	runStartedPaused bool,
) {
	switch {
	case runStartedPaused:
		ds.Status = types.DataSourceStatusPaused
	case failed:
		ds.Status = types.DataSourceStatusError
	default:
		ds.Status = types.DataSourceStatusActive
	}
}

type conditionalSyncStateUpdater interface {
	UpdateSyncStateIfStatusUnchanged(
		ctx context.Context,
		ds *types.DataSource,
		expectedStatus string,
	) error
}

func (s *DataSourceService) persistSyncOutcomeState(
	ctx context.Context,
	ds *types.DataSource,
	runStartStatus string,
) error {
	if runStartStatus == "" {
		runStartStatus = types.DataSourceStatusActive
	}
	if repo, ok := s.dsRepo.(conditionalSyncStateUpdater); ok {
		return repo.UpdateSyncStateIfStatusUnchanged(ctx, ds, runStartStatus)
	}
	return s.dsRepo.UpdateSyncState(ctx, ds)
}

func (s *DataSourceService) persistSyncProgressState(
	ctx context.Context,
	ds *types.DataSource,
) error {
	if repo, ok := s.dsRepo.(conditionalSyncStateUpdater); ok {
		// Checkpoints never own status. Passing the same value as expected and
		// desired makes the SQL update a no-op for status while preserving any
		// pause/resume that raced with the checkpoint.
		return repo.UpdateSyncStateIfStatusUnchanged(ctx, ds, ds.Status)
	}
	return s.dsRepo.UpdateSyncState(ctx, ds)
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
		detail = result.Errors[0].Display()
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
	tenantID, _ := ctx.Value(types.TenantIDContextKey).(uint64)
	injectDataSourceRuntimeIdentity(config, tenantID, credentialValidationDataSourceID)

	if err := connector.Validate(ctx, config); err != nil {
		return err
	}

	return nil
}

// PreviewResources lists resources with candidate credentials without
// persisting them. It is used when an edit switches external identity and the
// stored data source still contains the previous credential set.
func (s *DataSourceService) PreviewResources(
	ctx context.Context,
	connectorType string,
	dataSourceID string,
	credentials map[string]interface{},
	settings map[string]interface{},
	parentID string,
	validateOnly bool,
) ([]types.Resource, error) {
	connector, err := s.connectorRegistry.Get(connectorType)
	if err != nil {
		return nil, err
	}

	config := &types.DataSourceConfig{Type: connectorType}
	tenantID, _ := ctx.Value(types.TenantIDContextKey).(uint64)
	runtimeDataSourceID := credentialPreviewDataSourceID
	if dataSourceID != "" {
		existing, err := s.dsRepo.FindByID(ctx, dataSourceID)
		if err != nil {
			return nil, err
		}
		if tenantID == 0 ||
			existing.TenantID != tenantID ||
			existing.Type != connectorType {
			return nil, datasource.ErrDataSourceInvalid
		}
		stored, err := existing.ParseConfig()
		if err != nil {
			return nil, datasource.ErrInvalidConfig
		}
		if stored != nil {
			config.Credentials = cloneInterfaceMap(stored.Credentials)
			config.Settings = cloneInterfaceMap(stored.Settings)
		}
		runtimeDataSourceID = existing.ID
	}
	// A non-nil map is an explicit candidate credential set and replaces the
	// stored set atomically. nil means "use the stored credentials" for an
	// owned edit preview.
	if credentials != nil {
		config.Credentials = cloneInterfaceMap(credentials)
	}
	if settings != nil {
		config.Settings = cloneInterfaceMap(settings)
	}
	injectDataSourceRuntimeIdentity(config, tenantID, runtimeDataSourceID)
	if validateOnly {
		if err := connector.Validate(ctx, config); err != nil {
			return nil, err
		}
		return []types.Resource{}, nil
	}
	return connector.ListResources(ctx, config, parentID)
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
	injectDataSourceRuntimeSettings(config, ds)

	return connector.Validate(ctx, config)
}

// ingestItem writes a single FetchedItem into the knowledge base.
// Updates create the replacement first, then remove the previous owned row so
// an immediate create/storage failure preserves the last known-good knowledge.
//
// Routing logic:
//   - Has Content bytes → CreateKnowledgeFromFile (走完整的文档解析 pipeline)
//   - Has URL only      → CreateKnowledgeFromURL  (让 WeKnora 下载并解析)
//
// Returns (isUpdate, error) — isUpdate is true when an existing item was replaced.
func (s *DataSourceService) ingestItem(ctx context.Context, ds *types.DataSource, item *types.FetchedItem, tagIDs []string) (bool, error) {
	channel := ds.Type // e.g. "feishu", "notion"

	metadata := make(map[string]string, len(item.Metadata)+5)
	for k, v := range item.Metadata {
		metadata[k] = v
	}
	// Connector metadata is untrusted source material. Force the service-owned
	// identity fields after copying it so an upstream document cannot escape its
	// data-source ownership boundary or forge replacement-control metadata.
	metadata["external_id"] = item.ExternalID
	metadata["source_resource_id"] = item.SourceResourceID
	metadata["datasource_id"] = ds.ID
	delete(metadata, dataSourceReplacementOfMetadataKey)
	delete(metadata, dataSourceReplacementFingerprintMetadataKey)
	delete(metadata, dataSourceIngestPendingMetadataKey)

	// Check whether this data source already owns a knowledge item with the
	// external_id. DingTalk replacements are reconciled separately because the
	// previous row must remain available until asynchronous parsing of the new
	// row has actually completed.
	var existing *types.Knowledge
	if item.ExternalID != "" {
		var err error
		if ds.Type == types.ConnectorTypeDingTalk {
			var handled bool
			existing, handled, err = s.reconcileDingTalkReplacement(ctx, ds, item)
			if err == nil && handled {
				s.sweepStaleSubtree(ctx, ds, item)
				return existing != nil, nil
			}
		} else {
			existing, err = s.findOwnedKnowledgeByExternalID(ctx, ds, item.ExternalID)
		}
		if err != nil {
			logger.Warnf(ctx, "failed to check existing knowledge for item_ref=%s: %v", syncLogIdentifier(item.ExternalID), err)
			return existing != nil, fmt.Errorf("find previous knowledge: %w", err)
		}
	}
	isUpdate := existing != nil
	if ds.Type == types.ConnectorTypeDingTalk && len(item.Content) > 0 {
		metadata[dataSourceIngestPendingMetadataKey] = "true"
		metadata[dataSourceReplacementFingerprintMetadataKey] = dataSourceReplacementFingerprint(item)
		if existing != nil {
			metadata[dataSourceReplacementOfMetadataKey] = existing.ID
		}
	}

	// Case 1: content already fetched → build a FileHeader from bytes and call CreateKnowledgeFromFile
	if len(item.Content) > 0 {
		fh, err := bytesToFileHeader(item.Content, item.FileName)
		if err != nil {
			return isUpdate, fmt.Errorf("build file header: %w", err)
		}
		created, err := s.knowledgeService.CreateKnowledgeFromFile(
			ctx,
			ds.KnowledgeBaseID,
			fh,
			metadata,
			nil,           // use KB default for multimodal
			item.FileName, // customFileName — must include extension for file-type validation
			tagIDs,        // auto-tag from data source
			channel,
			nil,
		)
		if err != nil {
			var dupErr *types.DuplicateKnowledgeError
			if errors.As(err, &dupErr) && dupIsSameNode(dupErr, ds, item) {
				if ds.Type == types.ConnectorTypeDingTalk {
					if updateErr := s.updateOwnedDuplicateMetadata(ctx, dupErr.Knowledge, item, metadata); updateErr != nil {
						return isUpdate, updateErr
					}
					s.sweepStaleSubtree(ctx, ds, item)
					return true, nil
				}
				// Identical content is already present in the KB under THIS node's
				// own external_id, so the parent effectively exists — reconcile the
				// subtree so children removed from the doc do not linger.
				s.sweepStaleSubtree(ctx, ds, item)
				return isUpdate, err
			}
			if errors.As(err, &dupErr) {
				return isUpdate, errors.New("content duplicates a different knowledge item")
			}
			return isUpdate, err
		}
		if err := s.finishKnowledgeReplacement(ctx, ds, item, existing, created); err != nil {
			return isUpdate, err
		}
		s.sweepStaleSubtree(ctx, ds, item)
		return isUpdate, nil
	}

	// Case 2: only a remote URL — let WeKnora handle downloading and parsing
	if item.URL != "" {
		created, err := s.knowledgeService.CreateKnowledgeFromURL(
			ctx,
			ds.KnowledgeBaseID,
			item.URL,
			item.FileName,
			"",  // auto-detect file type
			nil, // use KB default for multimodal
			item.Title,
			tagIDs, // auto-tag from data source
			channel,
			nil,
		)
		if err != nil {
			var dupErr *types.DuplicateKnowledgeError
			if errors.As(err, &dupErr) && dupIsSameNode(dupErr, ds, item) {
				// Identical content is already present in the KB under THIS node's
				// own external_id, so the parent effectively exists — reconcile the
				// subtree so children removed from the doc do not linger.
				s.sweepStaleSubtree(ctx, ds, item)
				return isUpdate, err
			}
			if errors.As(err, &dupErr) {
				return isUpdate, errors.New("content duplicates a different knowledge item")
			}
			return isUpdate, err
		}
		if err := s.finishKnowledgeReplacement(ctx, ds, item, existing, created); err != nil {
			return isUpdate, err
		}
		s.sweepStaleSubtree(ctx, ds, item)
		return isUpdate, nil
	}

	return isUpdate, fmt.Errorf("item has neither content nor URL")
}

const (
	dataSourceReplacementOfMetadataKey          = "datasource_replacement_of"
	dataSourceReplacementFingerprintMetadataKey = "datasource_replacement_fingerprint"
	dataSourceIngestPendingMetadataKey          = "datasource_ingest_pending"
)

// ErrDataSourceIngestPending asks the worker to retry the same still-running
// sync after asynchronous knowledge parsing has had time to complete.
var ErrDataSourceIngestPending = errors.New(
	"data-source knowledge is still processing; the source cursor was preserved",
)

// Kept as an internal alias for replacement-focused helpers and regression
// tests that distinguish this state from an ordinary ingestion failure.
var errDataSourceReplacementPending = ErrDataSourceIngestPending

// finishKnowledgeReplacement removes an immediately failed replacement and
// preserves the previous row. DingTalk parsing is asynchronous, so its previous
// row is retired only after the replacement reaches ParseStatusCompleted; a
// pending replacement is reported as retryable so the batch cursor cannot make
// that source revision look complete.
func (s *DataSourceService) finishKnowledgeReplacement(
	ctx context.Context,
	ds *types.DataSource,
	item *types.FetchedItem,
	previous *types.Knowledge,
	created *types.Knowledge,
) error {
	if created == nil || created.ID == "" {
		return errors.New("replacement knowledge was created without an ID; previous knowledge was preserved")
	}
	if created.ParseStatus == "failed" {
		if cleanupErr := s.knowledgeService.DeleteKnowledge(ctx, created.ID); cleanupErr != nil {
			return fmt.Errorf(
				"replacement processing was not enqueued; preserve previous knowledge; cleanup failed: %w",
				cleanupErr,
			)
		}
		return errors.New("replacement processing was not enqueued; previous knowledge was preserved")
	}
	if previous != nil && created.ID == previous.ID {
		return nil
	}
	createdMetadata := created.GetMetadata()
	if ds != nil &&
		ds.Type == types.ConnectorTypeDingTalk &&
		item != nil &&
		createdMetadata[dataSourceIngestPendingMetadataKey] == "true" {
		if created.ParseStatus != types.ParseStatusCompleted {
			return errDataSourceReplacementPending
		}
		return s.promoteDingTalkReplacement(ctx, ds, item, previous, created)
	}
	if previous == nil {
		return nil
	}
	if err := s.knowledgeService.DeleteKnowledge(ctx, previous.ID); err != nil {
		rollbackErr := s.knowledgeService.DeleteKnowledge(ctx, created.ID)
		if rollbackErr != nil {
			return fmt.Errorf(
				"delete previous knowledge: %v; rollback replacement: %w",
				err,
				rollbackErr,
			)
		}
		return fmt.Errorf("delete previous knowledge: %w", err)
	}
	return nil
}

func dataSourceReplacementFingerprint(item *types.FetchedItem) string {
	if item == nil {
		return ""
	}
	// Length-delimited JSON avoids ambiguous concatenation while hashing only
	// source-version material. The digest is an internal correlation token; raw
	// content and source identifiers never enter logs or control metadata.
	material := struct {
		Content  []byte `json:"content,omitempty"`
		URL      string `json:"url,omitempty"`
		FileName string `json:"file_name,omitempty"`
		Revision string `json:"revision,omitempty"`
	}{
		Content:  item.Content,
		URL:      item.URL,
		FileName: item.FileName,
		Revision: item.Metadata["revision"],
	}
	encoded, _ := json.Marshal(material)
	sum := sha256.Sum256(encoded)
	return fmt.Sprintf("sha256:%x", sum[:])
}

// updateOwnedDuplicateMetadata handles a DingTalk rename or move whose document
// body hash did not change. Content deduplication returns the existing owned row;
// updating its display/source metadata in place is the only correct operation,
// otherwise acknowledging the new revision would permanently retain the old
// title and parent resource. A non-completed duplicate is never acknowledged.
func (s *DataSourceService) updateOwnedDuplicateMetadata(
	ctx context.Context,
	knowledge *types.Knowledge,
	item *types.FetchedItem,
	sourceMetadata map[string]string,
) error {
	if knowledge == nil {
		return errors.New("owned duplicate did not include a knowledge row")
	}
	switch knowledge.ParseStatus {
	case types.ParseStatusCompleted:
		// Safe to update below.
	case types.ParseStatusFailed, types.ParseStatusCancelled, types.ParseStatusDeleting:
		if err := s.knowledgeService.DeleteKnowledge(ctx, knowledge.ID); err != nil {
			return fmt.Errorf("remove failed duplicate knowledge: %w", err)
		}
		return errors.New("failed duplicate knowledge was removed and will be retried")
	default:
		return errDataSourceReplacementPending
	}

	metadata := knowledge.GetMetadata()
	for key, value := range sourceMetadata {
		switch key {
		case dataSourceReplacementOfMetadataKey,
			dataSourceReplacementFingerprintMetadataKey,
			dataSourceIngestPendingMetadataKey:
			continue
		default:
			metadata[key] = value
		}
	}
	delete(metadata, dataSourceReplacementOfMetadataKey)
	delete(metadata, dataSourceReplacementFingerprintMetadataKey)
	delete(metadata, dataSourceIngestPendingMetadataKey)
	encoded, err := json.Marshal(metadata)
	if err != nil {
		return fmt.Errorf("encode duplicate metadata: %w", err)
	}
	knowledge.Metadata = types.JSON(encoded)
	if item.Title != "" {
		knowledge.Title = item.Title
	}
	if item.FileName != "" {
		knowledge.FileName = item.FileName
	}
	if item.URL != "" {
		knowledge.Source = item.URL
	}
	knowledge.UpdatedAt = time.Now().UTC()
	if err := s.knowledgeService.GetRepository().UpdateKnowledge(ctx, knowledge); err != nil {
		return fmt.Errorf("update renamed or moved knowledge: %w", err)
	}
	return nil
}

// reconcileDingTalkReplacement resolves a replacement left beside its
// last-known-good predecessor by an earlier run. A matching completed candidate
// is promoted; failed candidates are removed and recreated; in-flight candidates
// keep the old row and make the current run retry without advancing its cursor.
func (s *DataSourceService) reconcileDingTalkReplacement(
	ctx context.Context,
	ds *types.DataSource,
	item *types.FetchedItem,
) (*types.Knowledge, bool, error) {
	rows, err := s.findOwnedKnowledgesByExternalID(ctx, ds, item.ExternalID)
	if err != nil {
		return nil, false, err
	}

	var previous *types.Knowledge
	var matching *types.Knowledge
	currentFingerprint := dataSourceReplacementFingerprint(item)
	for _, row := range rows {
		if row == nil {
			continue
		}
		metadata := row.GetMetadata()
		if metadata[dataSourceIngestPendingMetadataKey] != "true" {
			// Prefer the newest completed legacy/base row if an older release
			// happened to leave more than one unmarked copy.
			rowCompleted := row.ParseStatus == types.ParseStatusCompleted
			previousCompleted := previous != nil &&
				previous.ParseStatus == types.ParseStatusCompleted
			if previous == nil ||
				(rowCompleted && !previousCompleted) ||
				(rowCompleted == previousCompleted && row.CreatedAt.After(previous.CreatedAt)) {
				previous = row
			}
			continue
		}
		if metadata[dataSourceReplacementFingerprintMetadataKey] == currentFingerprint {
			matching = row
			continue
		}
		// The source changed again before this candidate was promoted. Cancel and
		// remove the stale candidate while retaining the preceding good row.
		if err := s.knowledgeService.DeleteKnowledge(ctx, row.ID); err != nil {
			return previous, false, fmt.Errorf("remove stale replacement: %w", err)
		}
	}
	if matching == nil {
		return previous, false, nil
	}

	switch matching.ParseStatus {
	case types.ParseStatusCompleted:
		if err := s.promoteDingTalkReplacement(ctx, ds, item, previous, matching); err != nil {
			return previous, false, err
		}
		return previous, true, nil
	case types.ParseStatusFailed, types.ParseStatusCancelled, types.ParseStatusDeleting:
		if err := s.knowledgeService.DeleteKnowledge(ctx, matching.ID); err != nil {
			return previous, false, fmt.Errorf("remove failed replacement: %w", err)
		}
		return previous, false, nil
	default:
		return previous, false, errDataSourceReplacementPending
	}
}

// promoteDingTalkReplacement is deliberately ordered old-delete then marker
// clear. If old-row cleanup fails, both good copies remain and the retained
// marker retries cleanup. If marker persistence fails after the old row is gone,
// the next run sees the missing predecessor, clears the marker, and converges.
func (s *DataSourceService) promoteDingTalkReplacement(
	ctx context.Context,
	ds *types.DataSource,
	item *types.FetchedItem,
	previous *types.Knowledge,
	replacement *types.Knowledge,
) error {
	if replacement == nil || replacement.ParseStatus != types.ParseStatusCompleted {
		return errDataSourceReplacementPending
	}
	metadata := replacement.GetMetadata()
	previousID := metadata[dataSourceReplacementOfMetadataKey]
	if previous != nil && previous.ID == previousID && previous.ID != replacement.ID {
		if err := s.knowledgeService.DeleteKnowledge(ctx, previous.ID); err != nil {
			return fmt.Errorf("delete previous knowledge after replacement completed: %w", err)
		}
	}

	delete(metadata, dataSourceReplacementOfMetadataKey)
	delete(metadata, dataSourceReplacementFingerprintMetadataKey)
	delete(metadata, dataSourceIngestPendingMetadataKey)
	encoded, err := json.Marshal(metadata)
	if err != nil {
		return fmt.Errorf("encode promoted replacement metadata: %w", err)
	}
	replacement.Metadata = types.JSON(encoded)
	if err := s.knowledgeService.GetRepository().UpdateKnowledge(ctx, replacement); err != nil {
		return fmt.Errorf("mark replacement promoted: %w", err)
	}
	return nil
}

// dupIsSameNode reports whether a duplicate-content error means the parent still
// exists in the KB *under this item's own external_id* — i.e. a content-dedup hit
// against this same node, so reconciling its subtree is safe. File deduplication
// keys on file_hash plus file_type (CheckKnowledgeExists), so an updated node
// whose rebuilt body
// happens to hash-collide with a DIFFERENT knowledge item (another node, or a
// manually-uploaded file with no external_id) would otherwise sweep this node's
// children even though the new parent version was not accepted. In that case the
// matched row's external_id differs (or is absent), so we keep the last known-good
// parent and children intact for a later retry.
func dupIsSameNode(
	dupErr *types.DuplicateKnowledgeError,
	ds *types.DataSource,
	item *types.FetchedItem,
) bool {
	return dupErr != nil && dupErr.Knowledge != nil &&
		dupErr.Knowledge.GetMetadata()["external_id"] == item.ExternalID &&
		dupErr.Knowledge.GetMetadata()["datasource_id"] == ds.ID
}

// sweepStaleSubtree deletes STALE sub-items of item — knowledge whose external_id
// is prefixed with "<item.ExternalID>#" (e.g. attachment children of a docx node)
// that is NOT listed in item.SubtreeKeep, i.e. no longer present in the source.
//
// It runs only AFTER the parent item exists in the KB (freshly (re)created, or
// confirmed present via a duplicate-hash error), so a genuinely failed parent
// write never destroys existing children. Children still present in the source
// are preserved via SubtreeKeep even when they could not be re-ingested this
// cycle (e.g. a transient attachment download failure), so a still-present
// attachment never loses its previously-synced good copy. The "<id>#" prefix
// never matches the parent's own "<id>" external_id, so the parent is never
// self-swept.
func (s *DataSourceService) sweepStaleSubtree(ctx context.Context, ds *types.DataSource, item *types.FetchedItem) {
	if !item.ReplacesSubtree || item.ExternalID == "" {
		return
	}
	repo := s.knowledgeService.GetRepository()
	children, err := repo.FindByMetadataKeyPrefix(ctx, ds.TenantID, ds.KnowledgeBaseID, "external_id", types.SubtreeChildPrefix(item.ExternalID))
	if err != nil {
		logger.Warnf(ctx, "failed to list subtree of item_ref=%s: %v", syncLogIdentifier(item.ExternalID), err)
		return
	}
	if len(children) == 0 {
		return
	}
	ids := make([]string, 0, len(children))
	for _, child := range children {
		if child.GetMetadata()["datasource_id"] != ds.ID {
			continue
		}
		// A child still present in the source is preserved even if it could not be
		// re-ingested this sync; only children that vanished from the source are
		// stale and swept. Every child here was selected by the external_id-prefix
		// query, so its external_id is guaranteed present and readable (a malformed
		// row could not have matched the SQL predicate), and GetMetadata resolves
		// it identically to the keep-set entries the connector built. SubtreeKeep
		// holds one entry per still-present sub-item of this node (a small set), so
		// a linear scan is cheaper than materializing a lookup map.
		if slices.Contains(item.SubtreeKeep, child.GetMetadata()["external_id"]) {
			continue
		}
		ids = append(ids, child.ID)
	}
	if len(ids) == 0 {
		return
	}
	// Batch the deletion so a node whose attachment set shrank from N pays one
	// round of the delete fan-out rather than N sequential ones.
	if derr := s.knowledgeService.DeleteKnowledgeList(ctx, ids); derr != nil {
		logger.Warnf(ctx, "failed to delete %d stale sub-item(s) of item_ref=%s: %v",
			len(ids), syncLogIdentifier(item.ExternalID), derr)
	}
}

type knowledgeMetadataKeysFinder interface {
	FindByMetadataKeys(
		context.Context,
		uint64,
		string,
		map[string]string,
	) (*types.Knowledge, error)
}

type knowledgeMetadataKeysListFinder interface {
	FindAllByMetadataKeys(
		context.Context,
		uint64,
		string,
		map[string]string,
	) ([]*types.Knowledge, error)
}

func (s *DataSourceService) findOwnedKnowledgesByExternalID(
	ctx context.Context,
	ds *types.DataSource,
	externalID string,
) ([]*types.Knowledge, error) {
	if ds == nil || externalID == "" {
		return nil, nil
	}
	repo := s.knowledgeService.GetRepository()
	values := map[string]string{
		"external_id":   externalID,
		"datasource_id": ds.ID,
	}
	if finder, ok := repo.(knowledgeMetadataKeysListFinder); ok {
		rows, err := finder.FindAllByMetadataKeys(
			ctx,
			ds.TenantID,
			ds.KnowledgeBaseID,
			values,
		)
		if err != nil {
			return nil, err
		}
		owned := rows[:0]
		for _, row := range rows {
			if row == nil {
				continue
			}
			metadata := row.GetMetadata()
			if metadata["external_id"] == externalID && metadata["datasource_id"] == ds.ID {
				owned = append(owned, row)
			}
		}
		return owned, nil
	}

	var existing *types.Knowledge
	var err error
	if finder, ok := repo.(knowledgeMetadataKeysFinder); ok {
		existing, err = finder.FindByMetadataKeys(
			ctx,
			ds.TenantID,
			ds.KnowledgeBaseID,
			values,
		)
	} else {
		existing, err = repo.FindByMetadataKey(
			ctx,
			ds.TenantID,
			ds.KnowledgeBaseID,
			"external_id",
			externalID,
		)
	}
	if err != nil || existing == nil {
		return nil, err
	}
	metadata := existing.GetMetadata()
	if metadata["external_id"] != externalID || metadata["datasource_id"] != ds.ID {
		return nil, nil
	}
	return []*types.Knowledge{existing}, nil
}

func (s *DataSourceService) findOwnedKnowledgeByExternalID(
	ctx context.Context,
	ds *types.DataSource,
	externalID string,
) (*types.Knowledge, error) {
	rows, err := s.findOwnedKnowledgesByExternalID(ctx, ds, externalID)
	if err != nil || len(rows) == 0 {
		return nil, err
	}
	// Prefer the unmarked last-known-good row while an asynchronous replacement
	// coexists with it. Callers that need every row (source deletion and DingTalk
	// promotion) use findOwnedKnowledgesByExternalID directly.
	for i := len(rows) - 1; i >= 0; i-- {
		if rows[i].GetMetadata()[dataSourceIngestPendingMetadataKey] != "true" {
			return rows[i], nil
		}
	}
	return rows[len(rows)-1], nil
}

func (s *DataSourceService) deleteOwnedFetchedItem(
	ctx context.Context,
	ds *types.DataSource,
	externalID string,
) (bool, error) {
	existing, err := s.findOwnedKnowledgesByExternalID(ctx, ds, externalID)
	if err != nil || len(existing) == 0 {
		return false, err
	}
	for _, row := range existing {
		if err := s.knowledgeService.DeleteKnowledge(ctx, row.ID); err != nil {
			return false, err
		}
	}
	return true, nil
}

// bytesToFileHeader wraps a []byte into a *multipart.FileHeader so it can be
// consumed by KnowledgeService.CreateKnowledgeFromFile.
func bytesToFileHeader(data []byte, filename string) (*multipart.FileHeader, error) {
	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)

	// Create a form file part
	partHeader := make(textproto.MIMEHeader)
	partHeader.Set("Content-Disposition", fmt.Sprintf(`form-data; name="file"; filename="%s"`, filename))
	partHeader.Set("Content-Type", "application/octet-stream")

	part, err := writer.CreatePart(partHeader)
	if err != nil {
		return nil, fmt.Errorf("create multipart part: %w", err)
	}

	if _, err := part.Write(data); err != nil {
		return nil, fmt.Errorf("write data to part: %w", err)
	}

	if err := writer.Close(); err != nil {
		return nil, fmt.Errorf("close multipart writer: %w", err)
	}

	// Parse the multipart data to get a FileHeader
	reader := multipart.NewReader(&buf, writer.Boundary())
	form, err := reader.ReadForm(int64(len(data)) + 1024)
	if err != nil {
		return nil, fmt.Errorf("read multipart form: %w", err)
	}

	files := form.File["file"]
	if len(files) == 0 {
		return nil, fmt.Errorf("no file in multipart form")
	}

	return files[0], nil
}

func timePtr(t time.Time) *time.Time {
	utc := t.UTC()
	return &utc
}

func syncLogStatus(log *types.SyncLog) string {
	if log == nil {
		return "missing"
	}
	return log.Status
}
