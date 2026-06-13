package datasource

import "errors"

// Error definitions for datasource operations
var (
	// Connector errors
	ErrConnectorNil       = errors.New("connector is nil")
	ErrConnectorTypeEmpty = errors.New("connector type is empty")
	ErrConnectorNotFound  = errors.New("connector type not found in registry")

	// DataSource errors
	ErrDataSourceNotFound  = errors.New("data source not found")
	ErrDataSourceInvalid   = errors.New("data source configuration is invalid")
	ErrDataSourceNotActive = errors.New("data source is not active")

	// Configuration errors
	ErrInvalidConfig      = errors.New("invalid configuration")
	ErrInvalidCredentials = errors.New("invalid credentials")

	// Sync errors
	ErrSyncFailed       = errors.New("sync operation failed")
	ErrSyncCanceled     = errors.New("sync operation was canceled")
	ErrFetchFailed      = errors.New("failed to fetch items from source")
	ErrResourceNotFound = errors.New("resource not found in source system")

	// Knowledge base errors
	ErrKnowledgeBaseNotFound = errors.New("knowledge base not found")

	// Sync log errors
	ErrSyncLogNotFound = errors.New("sync log not found")
)
