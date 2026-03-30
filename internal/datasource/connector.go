package datasource

import (
	"context"

	"github.com/Tencent/WeKnora/internal/types"
)

// Connector is the interface that all external data source connectors must implement.
// Each connector (Feishu, Notion, Confluence, etc.) provides an implementation of this interface.
type Connector interface {
	// Type returns the connector type identifier (e.g., "feishu", "notion")
	Type() string

	// Validate verifies that the provided configuration is valid by testing connectivity
	// and checking credentials. Returns error if validation fails.
	Validate(ctx context.Context, config *types.DataSourceConfig) error

	// ListResources lists available resources that can be synced (documents, spaces, folders, etc.)
	// Returns a list of Resource objects that the user can select for syncing.
	ListResources(ctx context.Context, config *types.DataSourceConfig) ([]types.Resource, error)

	// FetchAll performs a full sync of the specified resources.
	// Returns all items from the given resource IDs.
	FetchAll(ctx context.Context, config *types.DataSourceConfig, resourceIDs []string) ([]types.FetchedItem, error)

	// FetchIncremental performs an incremental sync based on the provided cursor.
	// Returns items that have changed since the last sync, a new cursor for the next sync,
	// and an error if the operation fails.
	FetchIncremental(ctx context.Context, config *types.DataSourceConfig, cursor *types.SyncCursor) ([]types.FetchedItem, *types.SyncCursor, error)
}

// ConnectorRegistry manages the registration and lookup of available connectors
type ConnectorRegistry struct {
	connectors map[string]Connector
}

// NewConnectorRegistry creates a new connector registry
func NewConnectorRegistry() *ConnectorRegistry {
	return &ConnectorRegistry{
		connectors: make(map[string]Connector),
	}
}

// Register registers a connector with the registry
func (r *ConnectorRegistry) Register(connector Connector) error {
	if connector == nil {
		return ErrConnectorNil
	}
	if connector.Type() == "" {
		return ErrConnectorTypeEmpty
	}
	r.connectors[connector.Type()] = connector
	return nil
}

// Get retrieves a connector by type
func (r *ConnectorRegistry) Get(connectorType string) (Connector, error) {
	connector, exists := r.connectors[connectorType]
	if !exists {
		return nil, ErrConnectorNotFound
	}
	return connector, nil
}

// List returns all registered connector types
func (r *ConnectorRegistry) List() []string {
	types := make([]string, 0, len(r.connectors))
	for t := range r.connectors {
		types = append(types, t)
	}
	return types
}

// ConnectorMetadata provides metadata about available connectors
type ConnectorMetadata struct {
	Type         string   `json:"type"`
	Name         string   `json:"name"`
	Description  string   `json:"description"`
	Icon         string   `json:"icon,omitempty"`
	Priority     int      `json:"priority"`     // Priority order for UI display (lower = higher priority)
	AuthType     string   `json:"auth_type"`    // "oauth2", "api_key", "token", etc.
	Capabilities []string `json:"capabilities"` // "incremental", "webhook", "deletion_sync", etc.
}

// GetConnectorMetadata returns metadata for all available connectors
// This is used by the frontend to display connector options
var ConnectorMetadataRegistry = map[string]ConnectorMetadata{
	types.ConnectorTypeFeishu: {
		Type:         types.ConnectorTypeFeishu,
		Name:         "Feishu (飞书)",
		Description:  "Sync documents, wikis, and content from Feishu",
		Priority:     0,
		AuthType:     "oauth2",
		Capabilities: []string{"incremental", "webhook", "deletion_sync"},
	},
	types.ConnectorTypeNotion: {
		Type:         types.ConnectorTypeNotion,
		Name:         "Notion",
		Description:  "Sync pages and databases from Notion",
		Priority:     1,
		AuthType:     "api_key",
		Capabilities: []string{"incremental"},
	},
	types.ConnectorTypeConfluence: {
		Type:         types.ConnectorTypeConfluence,
		Name:         "Confluence",
		Description:  "Sync spaces and pages from Atlassian Confluence",
		Priority:     2,
		AuthType:     "api_key",
		Capabilities: []string{"incremental"},
	},
	types.ConnectorTypeYuque: {
		Type:         types.ConnectorTypeYuque,
		Name:         "Yuque (语雀)",
		Description:  "Sync knowledge bases and documents from Yuque",
		Priority:     3,
		AuthType:     "api_key",
		Capabilities: []string{"incremental"},
	},
	types.ConnectorTypeGitHub: {
		Type:         types.ConnectorTypeGitHub,
		Name:         "GitHub",
		Description:  "Sync repositories, wikis, and issues from GitHub",
		Priority:     4,
		AuthType:     "oauth2",
		Capabilities: []string{"incremental"},
	},
	types.ConnectorTypeGoogleDrive: {
		Type:         types.ConnectorTypeGoogleDrive,
		Name:         "Google Drive",
		Description:  "Sync documents and files from Google Drive",
		Priority:     5,
		AuthType:     "oauth2",
		Capabilities: []string{"incremental"},
	},
	types.ConnectorTypeOneDrive: {
		Type:         types.ConnectorTypeOneDrive,
		Name:         "OneDrive / SharePoint",
		Description:  "Sync documents and files from Microsoft OneDrive",
		Priority:     6,
		AuthType:     "oauth2",
		Capabilities: []string{"incremental"},
	},
	types.ConnectorTypeDingTalk: {
		Type:         types.ConnectorTypeDingTalk,
		Name:         "DingTalk (钉钉)",
		Description:  "Sync documents and content from DingTalk",
		Priority:     7,
		AuthType:     "api_key",
		Capabilities: []string{"incremental"},
	},
	types.ConnectorTypeWebCrawler: {
		Type:         types.ConnectorTypeWebCrawler,
		Name:         "Web Crawler (Sitemap)",
		Description:  "Crawl websites via Sitemap.xml",
		Priority:     9,
		AuthType:     "none",
		Capabilities: []string{},
	},
	types.ConnectorTypeSlack: {
		Type:         types.ConnectorTypeSlack,
		Name:         "Slack",
		Description:  "Sync channel messages and files from Slack",
		Priority:     10,
		AuthType:     "oauth2",
		Capabilities: []string{"incremental"},
	},
	types.ConnectorTypeIMAP: {
		Type:         types.ConnectorTypeIMAP,
		Name:         "Email (IMAP)",
		Description:  "Sync email content from IMAP servers",
		Priority:     11,
		AuthType:     "password",
		Capabilities: []string{},
	},
	types.ConnectorTypeRSS: {
		Type:         types.ConnectorTypeRSS,
		Name:         "RSS / Atom Feed",
		Description:  "Sync articles from RSS/Atom feeds",
		Priority:     12,
		AuthType:     "none",
		Capabilities: []string{},
	},
}

// ListAvailableConnectors returns all available connector metadata
// sorted by priority
func ListAvailableConnectors() []ConnectorMetadata {
	metadata := make([]ConnectorMetadata, 0, len(ConnectorMetadataRegistry))
	for _, meta := range ConnectorMetadataRegistry {
		metadata = append(metadata, meta)
	}

	// Sort by priority (insertion sort for simplicity)
	for i := 1; i < len(metadata); i++ {
		key := metadata[i]
		j := i - 1
		for j >= 0 && metadata[j].Priority > key.Priority {
			metadata[j+1] = metadata[j]
			j--
		}
		metadata[j+1] = key
	}

	return metadata
}
