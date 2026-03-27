# WeKnora Data Source Sync Framework

## Overview

The data source sync framework enables WeKnora to automatically import and synchronize content from external platforms (Feishu, Notion, Confluence, etc.) into knowledge bases. This is the foundational layer upon which all specific connectors are built.

## Architecture

### Core Components

```
┌─────────────────────────────────────────────────────────────┐
│                     External Data Sources                  │
│     (Feishu, Notion, Confluence, GitHub, Google Drive...) │
└─────────────┬───────────────────────────────────────────────┘
              │
              ▼
┌─────────────────────────────────────────────────────────────┐
│                  Connector Registry & Adapters              │
│    Each platform (feishu/, notion/, confluence/, etc)     │
│           implements the Connector interface               │
└─────────────┬───────────────────────────────────────────────┘
              │
              ▼
┌─────────────────────────────────────────────────────────────┐
│            DataSourceService (Business Logic)               │
│  ├─ CreateDataSource / UpdateDataSource                    │
│  ├─ ManualSync / ListAvailableResources                    │
│  ├─ ValidateConnection / PauseDataSource                   │
│  └─ ProcessSync (asynq task handler)                       │
└─────────────┬───────────────────────────────────────────────┘
              │
              ▼
┌─────────────────────────────────────────────────────────────┐
│        HTTP Handler & API Routes (/api/v1/datasource)      │
│      REST endpoints for UI/programmatic access             │
└─────────────┬───────────────────────────────────────────────┘
              │
              ▼
┌─────────────────────────────────────────────────────────────┐
│              Database (MySQL / PostgreSQL)                  │
│  ├─ data_sources (configuration & state)                   │
│  └─ sync_logs (history & metadata)                         │
└─────────────────────────────────────────────────────────────┘
```

### Task Flow

```
Manual Trigger or Scheduled Job (cron)
        ↓
  Scheduler enqueues asynq Task
        ↓
  ProcessSync (task handler) executes
        ↓
  Connector.Fetch* methods pull data
        ↓
  Diff engine identifies changes (new/update/delete)
        ↓
  KnowledgeService creates/updates Knowledge entries
        ↓
  Documents parsed → chunks → vectors → indexed
```

## File Structure

```
internal/datasource/
├── connector.go          # Connector interface & registry
├── errors.go            # Error definitions
└── README.md            # This file

internal/types/
├── datasource.go        # Data models (DataSource, SyncLog, etc)
└── interfaces/datasource.go  # Service interfaces

internal/application/
├── repository/datasource_repo.go  # Database access layer
└── service/datasource_service.go  # Business logic

internal/handler/
└── datasource.go        # HTTP request handlers

internal/router/
├── router.go           # Route registration
├── task.go             # Asynq task registration
└── sync_task.go        # Lite mode task registration

migrations/versioned/
└── 000028_datasource_tables.up/down.sql  # Database schema
```

## Data Models

### DataSource
Represents a configured external data source:
- **ID**: Unique identifier
- **Type**: Connector type (feishu, notion, confluence, etc)
- **Config**: Encrypted credentials and configuration (JSON)
- **SyncSchedule**: Cron expression (e.g., "0 */6 * * *")
- **SyncMode**: "incremental" or "full"
- **Status**: active | paused | error
- **ConflictStrategy**: overwrite | skip
- **SyncDeletions**: Whether to sync deletions from source
- **LastSyncAt**: Timestamp of last successful sync
- **LastSyncCursor**: State for incremental sync
- **LastSyncResult**: Summary of last sync

### SyncLog
Tracks execution of each sync operation:
- **Status**: running | success | partial | failed | canceled
- **StartedAt/FinishedAt**: Execution timestamps
- **Items***: Counters (total, created, updated, deleted, skipped, failed)
- **ErrorMessage**: Error details if failed
- **Result**: Detailed sync result (JSON)

### Connector Interface
All external data sources implement this interface:

```go
type Connector interface {
    Type() string
    Validate(ctx, config) error
    ListResources(ctx, config) ([]Resource, error)
    FetchAll(ctx, config, resourceIDs) ([]FetchedItem, error)
    FetchIncremental(ctx, config, cursor) ([]FetchedItem, *SyncCursor, error)
}
```

## API Endpoints

### Data Source Management
```
POST   /api/v1/datasource              # Create
GET    /api/v1/datasource              # List (by kb_id)
GET    /api/v1/datasource/:id          # Get
PUT    /api/v1/datasource/:id          # Update
DELETE /api/v1/datasource/:id          # Delete
```

### Operations
```
POST   /api/v1/datasource/:id/validate      # Test connection
GET    /api/v1/datasource/:id/resources     # List available resources
POST   /api/v1/datasource/:id/sync          # Trigger manual sync
POST   /api/v1/datasource/:id/pause         # Pause
POST   /api/v1/datasource/:id/resume        # Resume
```

### Logs
```
GET    /api/v1/datasource/:id/logs          # List sync logs
GET    /api/v1/datasource/logs/:log_id      # Get specific log
```

### Metadata
```
GET    /api/v1/datasource/types             # Available connectors
```

## Implementation Guide

### Adding a New Connector

1. **Create the connector package** in `internal/datasource/connector/<type>/`
   ```go
   // connector/<type>/client.go - API client
   // connector/<type>/connector.go - Implements Connector interface
   // connector/<type>/types.go - Type-specific models
   ```

2. **Implement the Connector interface**
   ```go
   type YourConnector struct {
       // fields
   }
   
   func (c *YourConnector) Type() string {
       return types.ConnectorTypeYourType
   }
   
   func (c *YourConnector) Validate(ctx, config) error {
       // Test connectivity and credentials
   }
   
   func (c *YourConnector) ListResources(ctx, config) ([]types.Resource, error) {
       // List available resources (documents, spaces, etc)
   }
   
   func (c *YourConnector) FetchAll(ctx, config, resourceIDs) ([]types.FetchedItem, error) {
       // Full sync - fetch all items from specified resources
   }
   
   func (c *YourConnector) FetchIncremental(ctx, config, cursor) ([]types.FetchedItem, *types.SyncCursor, error) {
       // Incremental sync - fetch only changed items since cursor
   }
   ```

3. **Register the connector** in the container (in container.go)
   ```go
   container.Provide(func() datasource.Connector {
       return yourconnector.NewConnector()
   })
   ```

4. **Add metadata** in `connector.go`
   ```go
   types.ConnectorTypeYourType: {
       Type: types.ConnectorTypeYourType,
       Name: "Your Platform",
       AuthType: "oauth2",
       // ...
   }
   ```

## Database Schema

### data_sources table
- Stores data source configurations
- Indexes: tenant_id, knowledge_base_id, type, status
- Encrypted config field (AES-256-GCM)

### sync_logs table
- Stores sync operation history
- Indexes: data_source_id, tenant_id, status, started_at
- Foreign key to data_sources with cascade delete

## Key Design Decisions

1. **Adapter Pattern**: Each connector is a separate implementation, easy to add new ones
2. **Async Task Queue**: Syncs are non-blocking, using asynq (or sync mode in Lite)
3. **Incremental Sync**: Supports cursor-based incremental syncs to minimize API calls
4. **Encryption**: API keys/tokens stored encrypted with AES-256-GCM
5. **Multi-tenant**: All operations are tenant-isolated
6. **Channel Tracking**: Knowledge.Channel field tracks data source type for origin tracking

## Future Enhancements

1. **Webhook Support**: Real-time push from platforms that support webhooks
2. **Conflict Resolution**: Advanced merge/conflict strategies for overlapping content
3. **Rate Limiting**: Per-connector rate limiting and backoff strategies
4. **Scheduling**: Full cron scheduler with time zone support
5. **Monitoring**: Metrics, alerting, and sync health dashboards
6. **Filtering**: User-defined filters for selective syncing (by title, date, tags, etc)
7. **Transformation**: Content transformation pipelines (e.g., extract tables, summarize)
8. **Deduplication**: Smart deduplication across multiple sources

## Testing

The framework is designed to be testable:
- Mock connectors can be created for testing
- No external dependencies required for core logic
- Database operations isolated and mockable

## Error Handling

All operations return detailed error information:
- Connection failures are captured and status updated
- Partial failures are logged (some items succeed, some fail)
- Error messages stored in DataSource for user troubleshooting
