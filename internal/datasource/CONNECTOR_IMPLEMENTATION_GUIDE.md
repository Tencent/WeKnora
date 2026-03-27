# Adding a New Connector - Step-by-Step Guide

This guide walks you through implementing a new connector (e.g., Feishu, Notion, Confluence) for the data source sync framework.

## Overview

A connector is an adapter that translates between WeKnora's data model and an external platform's API. It handles:
- Connection validation (credentials, connectivity)
- Resource listing (documents, spaces, folders the user can choose from)
- Full sync (fetch all items from selected resources)
- Incremental sync (fetch only changed items since last sync)

## Step 1: Create Connector Package Structure

```bash
mkdir -p internal/datasource/connector/yourtype/
```

Create three files:
- `client.go` - API client wrapper
- `connector.go` - Implements Connector interface
- `types.go` - Platform-specific data structures

## Step 2: Define Platform Types (types.go)

```go
package yourtype

import "time"

// Platform-specific configuration
type Config struct {
    BaseURL   string `json:"base_url"`
    APIToken  string `json:"api_token"`
    
    // Or OAuth fields:
    AccessToken  string `json:"access_token"`
    RefreshToken string `json:"refresh_token"`
    ExpiresAt    time.Time `json:"expires_at"`
}

// Platform-specific resource representation
type YourResource struct {
    ID        string
    Name      string
    Type      string // "document", "folder", "space", etc.
    ModifiedAt time.Time
    URL       string
}

// Platform-specific item representation
type YourItem struct {
    ID          string
    Title       string
    Content     string
    ContentHTML string
    ModifiedAt  time.Time
    URL         string
    CreatedBy   string
}

// Platform-specific pagination/cursor
type YourCursor struct {
    Offset      int       `json:"offset,omitempty"`
    LastModified time.Time `json:"last_modified,omitempty"`
    PageToken   string    `json:"page_token,omitempty"`
}
```

## Step 3: Implement API Client (client.go)

```go
package yourtype

import (
    "context"
    "fmt"
    "net/http"
    "encoding/json"
)

type Client struct {
    baseURL    string
    apiToken   string
    httpClient *http.Client
}

// NewClient creates a new API client
func NewClient(config *Config) *Client {
    return &Client{
        baseURL:    config.BaseURL,
        apiToken:   config.APIToken,
        httpClient: &http.Client{Timeout: 30 * time.Second},
    }
}

// Example methods
func (c *Client) GetResources(ctx context.Context) ([]YourResource, error) {
    // Call platform API
    // Parse response
    // Return resources
}

func (c *Client) GetDocument(ctx context.Context, docID string) (*YourItem, error) {
    // Fetch single document
}

func (c *Client) GetDocumentsModifiedSince(ctx context.Context, since time.Time) ([]YourItem, error) {
    // Fetch documents modified since timestamp
}
```

## Step 4: Implement Connector Interface (connector.go)

```go
package yourtype

import (
    "context"
    "fmt"
    
    "github.com/Tencent/WeKnora/internal/types"
)

type YourConnector struct {
    client *Client
}

// NewConnector creates a new connector
func NewConnector() *YourConnector {
    return &YourConnector{}
}

// Type returns the connector type identifier
func (c *YourConnector) Type() string {
    return types.ConnectorTypeYourType // Must match constant in types/datasource.go
}

// Validate verifies that the configuration is valid
func (c *YourConnector) Validate(ctx context.Context, config *types.DataSourceConfig) error {
    if config == nil {
        return fmt.Errorf("config is nil")
    }
    
    // Parse your type-specific config
    yourConfig := &Config{}
    if err := parseConfig(config, yourConfig); err != nil {
        return fmt.Errorf("invalid config: %w", err)
    }
    
    // Create client
    client := NewClient(yourConfig)
    
    // Test connection
    _, err := client.GetResources(ctx)
    if err != nil {
        return fmt.Errorf("connection failed: %w", err)
    }
    
    return nil
}

// ListResources lists available resources (documents, spaces, folders)
func (c *YourConnector) ListResources(ctx context.Context, config *types.DataSourceConfig) ([]types.Resource, error) {
    yourConfig := &Config{}
    if err := parseConfig(config, yourConfig); err != nil {
        return nil, err
    }
    
    client := NewClient(yourConfig)
    yourResources, err := client.GetResources(ctx)
    if err != nil {
        return nil, err
    }
    
    // Convert to WeKnora Resource format
    resources := make([]types.Resource, len(yourResources))
    for i, yr := range yourResources {
        resources[i] = types.Resource{
            ExternalID: yr.ID,
            Name:       yr.Name,
            Type:       yr.Type,
            URL:        yr.URL,
            ModifiedAt: yr.ModifiedAt,
        }
    }
    
    return resources, nil
}

// FetchAll performs a full sync
func (c *YourConnector) FetchAll(ctx context.Context, config *types.DataSourceConfig, resourceIDs []string) ([]types.FetchedItem, error) {
    yourConfig := &Config{}
    if err := parseConfig(config, yourConfig); err != nil {
        return nil, err
    }
    
    client := NewClient(yourConfig)
    
    var allItems []types.FetchedItem
    
    // Fetch all documents from specified resources
    for _, resourceID := range resourceIDs {
        // Get documents from this resource (implementation depends on platform)
        yourItems, err := client.GetDocumentsFromResource(ctx, resourceID)
        if err != nil {
            return nil, fmt.Errorf("failed to fetch resource %s: %w", resourceID, err)
        }
        
        // Convert to FetchedItem format
        for _, yi := range yourItems {
            item := types.FetchedItem{
                ExternalID:       yi.ID,
                Title:            yi.Title,
                Content:          []byte(yi.Content),
                ContentType:      "text/markdown",
                FileName:         fmt.Sprintf("%s.md", yi.Title),
                URL:              yi.URL,
                UpdatedAt:        yi.ModifiedAt,
                SourceResourceID: resourceID,
                Metadata: map[string]string{
                    "created_by":     yi.CreatedBy,
                    "platform":       "yourtype",
                },
            }
            allItems = append(allItems, item)
        }
    }
    
    return allItems, nil
}

// FetchIncremental performs an incremental sync
func (c *YourConnector) FetchIncremental(ctx context.Context, config *types.DataSourceConfig, cursor *types.SyncCursor) ([]types.FetchedItem, *types.SyncCursor, error) {
    yourConfig := &Config{}
    if err := parseConfig(config, yourConfig); err != nil {
        return nil, nil, err
    }
    
    client := NewClient(yourConfig)
    
    // Determine start time for incremental fetch
    var sinceTime time.Time
    if cursor != nil && !cursor.LastSyncTime.IsZero() {
        sinceTime = cursor.LastSyncTime
    } else {
        sinceTime = time.Now().AddDate(0, 0, -7) // Default: last 7 days
    }
    
    // Fetch changed items
    yourItems, err := client.GetDocumentsModifiedSince(ctx, sinceTime)
    if err != nil {
        return nil, nil, fmt.Errorf("incremental fetch failed: %w", err)
    }
    
    // Convert to FetchedItem format
    items := make([]types.FetchedItem, len(yourItems))
    for i, yi := range yourItems {
        items[i] = types.FetchedItem{
            ExternalID:  yi.ID,
            Title:       yi.Title,
            Content:     []byte(yi.Content),
            ContentType: "text/markdown",
            FileName:    fmt.Sprintf("%s.md", yi.Title),
            URL:         yi.URL,
            UpdatedAt:   yi.ModifiedAt,
            Metadata: map[string]string{
                "created_by": yi.CreatedBy,
                "platform":   "yourtype",
            },
        }
    }
    
    // Create new cursor for next sync
    nextCursor := &types.SyncCursor{
        LastSyncTime: time.Now(),
        ConnectorCursor: map[string]interface{}{
            "last_modified": time.Now(),
        },
    }
    
    return items, nextCursor, nil
}

// Helper function to parse config
func parseConfig(config *types.DataSourceConfig, target interface{}) error {
    data, err := json.Marshal(config.Credentials)
    if err != nil {
        return err
    }
    return json.Unmarshal(data, target)
}
```

## Step 5: Register in Container

Edit `internal/container/container.go`:

```go
import (
    // ... existing imports
    yourconnector "github.com/Tencent/WeKnora/internal/datasource/connector/yourtype"
)

func setupContainer() (*dig.Container, error) {
    container := dig.New()
    
    // ... existing registrations ...
    
    // Register your connector
    must(container.Provide(func() *datasource.Connector {
        var c datasource.Connector = yourconnector.NewConnector()
        return &c
    }))
    
    // ... rest of setup ...
}
```

Better yet, register through the registry in the connector setup:

```go
// In the service initialization section
connectorRegistry := datasource.NewConnectorRegistry()
connectorRegistry.Register(yourconnector.NewConnector())
connectorRegistry.Register(feishuconnector.NewConnector())
// ... etc

container.Provide(func() *datasource.ConnectorRegistry {
    return connectorRegistry
})
```

## Step 6: Add Connector Type Constant

Edit `internal/types/datasource.go`:

```go
const (
    // ... existing types ...
    ConnectorTypeYourType = "yourtype"
)
```

## Step 7: Add Metadata

Edit `internal/datasource/connector.go`:

```go
var ConnectorMetadataRegistry = map[string]ConnectorMetadata{
    // ... existing entries ...
    types.ConnectorTypeYourType: {
        Type:         types.ConnectorTypeYourType,
        Name:         "Your Platform Name",
        Description:  "Sync documents from Your Platform",
        Priority:     X,  // Lower number = higher priority in UI
        AuthType:     "oauth2",  // or "api_key", "token", "password"
        Capabilities: []string{"incremental", "webhook", "deletion_sync"},
    },
}
```

## Step 8: Test Your Connector

```go
// Example test
func TestYourConnectorValidate(t *testing.T) {
    connector := NewConnector()
    
    config := &types.DataSourceConfig{
        Type: types.ConnectorTypeYourType,
        Credentials: map[string]interface{}{
            "api_token": "test_token",
        },
    }
    
    err := connector.Validate(context.Background(), config)
    // assert no error
}

func TestYourConnectorFetchAll(t *testing.T) {
    connector := NewConnector()
    config := &types.DataSourceConfig{
        Type: types.ConnectorTypeYourType,
        Credentials: map[string]interface{}{
            "api_token": "test_token",
        },
        ResourceIDs: []string{"resource_1"},
    }
    
    items, err := connector.FetchAll(context.Background(), config, []string{"resource_1"})
    // assert results
}
```

## Checklist

- [ ] Created `internal/datasource/connector/yourtype/` package
- [ ] Implemented `types.go` with platform data structures
- [ ] Implemented `client.go` with API wrapper
- [ ] Implemented `connector.go` with Connector interface
- [ ] Added connector type constant
- [ ] Registered in container
- [ ] Added metadata entry
- [ ] Added unit tests
- [ ] Tested manually with real API
- [ ] Documented any special requirements

## Common Patterns

### OAuth Flow
If using OAuth, store tokens in config:
```go
type Config struct {
    AccessToken  string
    RefreshToken string
    ExpiresAt    time.Time
}

// Refresh tokens when expired
func (c *Client) ensureValidToken(ctx context.Context) error {
    if time.Now().After(c.config.ExpiresAt) {
        return c.refreshToken(ctx)
    }
    return nil
}
```

### Pagination
For platforms with pagination:
```go
func (c *Client) GetDocumentsPage(ctx context.Context, pageToken string) (*Page, error) {
    // Returns {Items, NextPageToken}
}
```

### Incremental Sync
For timestamp-based incremental sync:
```go
func (c *Client) GetModifiedSince(ctx context.Context, since time.Time) ([]Item, error) {
    // Uses API parameter like &modified_after=2026-03-26T10:00:00Z
}
```

### Deletion Tracking
For platforms that report deletions:
```go
type Item struct {
    IsDeleted bool // Set when item is deleted
}
```

## Testing with Real API

1. Set up test credentials
2. Create small test resource (e.g., single document)
3. Test each method:
   ```go
   connector := NewConnector()
   config := &types.DataSourceConfig{...}
   
   // Test Validate
   err := connector.Validate(ctx, config)
   
   // Test ListResources
   resources, err := connector.ListResources(ctx, config)
   
   // Test FetchAll
   items, err := connector.FetchAll(ctx, config, []string{resources[0].ExternalID})
   
   // Test FetchIncremental
   items, cursor, err := connector.FetchIncremental(ctx, config, nil)
   ```

---

## Example: Feishu Connector Reference

The Feishu connector would be a good first implementation since:
1. Feishu API is well-documented
2. WeKnora already has `internal/im/feishu/` for reference
3. Popular in China (key market)
4. Has webhook support for real-time sync

Structure:
```
internal/datasource/connector/feishu/
├── client.go       (Feishu API client)
├── connector.go    (Implements Connector)
└── types.go        (Feishu types)
```

Key files to reference:
- `internal/im/feishu/adapter.go` - Feishu API patterns
- `internal/im/feishu/longconn.go` - Connection handling

This will be a good model for other connectors.
