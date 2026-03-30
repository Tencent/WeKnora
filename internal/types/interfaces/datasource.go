package interfaces

import (
	"context"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/hibiken/asynq"
)

// DataSourceService defines the interface for data source management operations
type DataSourceService interface {
	// CreateDataSource creates a new data source configuration
	CreateDataSource(ctx context.Context, ds *types.DataSource) (*types.DataSource, error)

	// GetDataSource retrieves a data source by ID
	GetDataSource(ctx context.Context, id string) (*types.DataSource, error)

	// ListDataSources lists all data sources for a knowledge base
	ListDataSources(ctx context.Context, kbID string) ([]*types.DataSource, error)

	// UpdateDataSource updates an existing data source
	UpdateDataSource(ctx context.Context, ds *types.DataSource) (*types.DataSource, error)

	// DeleteDataSource deletes a data source (soft delete)
	DeleteDataSource(ctx context.Context, id string) error

	// ValidateConnection tests the connection to an external data source
	ValidateConnection(ctx context.Context, dsID string) error

	// ValidateCredentials tests connectivity using raw credentials without persisting anything.
	// This is used by the frontend "Test Connection" button before creating a data source.
	ValidateCredentials(ctx context.Context, connectorType string, credentials map[string]interface{}) error

	// ListAvailableResources lists resources available for sync in the external system
	ListAvailableResources(ctx context.Context, dsID string) ([]types.Resource, error)

	// ManualSync triggers an immediate sync for a data source
	ManualSync(ctx context.Context, dsID string) (*types.SyncLog, error)

	// PauseDataSource pauses a data source's scheduled syncs
	PauseDataSource(ctx context.Context, id string) error

	// ResumeDataSource resumes a paused data source
	ResumeDataSource(ctx context.Context, id string) error

	// GetSyncLogs retrieves sync history for a data source
	GetSyncLogs(ctx context.Context, dsID string, limit int, offset int) ([]*types.SyncLog, error)

	// GetSyncLog retrieves a specific sync log entry
	GetSyncLog(ctx context.Context, syncLogID string) (*types.SyncLog, error)

	// ProcessSync handles the actual sync operation (called by asynq task)
	ProcessSync(ctx context.Context, task *asynq.Task) error
}

// DataSourceRepository defines database access patterns for data sources
type DataSourceRepository interface {
	// Create inserts a new data source record
	Create(ctx context.Context, ds *types.DataSource) error

	// FindByID retrieves a data source by ID
	FindByID(ctx context.Context, id string) (*types.DataSource, error)

	// FindByKnowledgeBase lists all data sources for a knowledge base
	FindByKnowledgeBase(ctx context.Context, kbID string) ([]*types.DataSource, error)

	// Update updates an existing data source
	Update(ctx context.Context, ds *types.DataSource) error

	// Delete performs a soft delete
	Delete(ctx context.Context, id string) error

	// FindActive retrieves all active data sources (used for scheduling)
	FindActive(ctx context.Context) ([]*types.DataSource, error)
}

// SyncLogRepository defines database access patterns for sync logs
type SyncLogRepository interface {
	// Create inserts a new sync log entry
	Create(ctx context.Context, log *types.SyncLog) error

	// FindByID retrieves a sync log by ID
	FindByID(ctx context.Context, id string) (*types.SyncLog, error)

	// FindByDataSource lists sync logs for a data source with pagination
	FindByDataSource(ctx context.Context, dsID string, limit int, offset int) ([]*types.SyncLog, error)

	// FindLatest retrieves the most recent sync log for a data source
	FindLatest(ctx context.Context, dsID string) (*types.SyncLog, error)

	// HasRunningSync checks if a data source has any sync currently in "running" status.
	// Used to prevent overlapping sync executions.
	HasRunningSync(ctx context.Context, dsID string) (bool, error)

	// Update updates an existing sync log entry
	Update(ctx context.Context, log *types.SyncLog) error

	// CancelPendingByDataSource marks all non-terminal sync logs for a data source as canceled.
	CancelPendingByDataSource(ctx context.Context, dsID string) error

	// CleanupOldLogs deletes sync logs older than the retention period
	CleanupOldLogs(ctx context.Context, retentionDays int) error
}
