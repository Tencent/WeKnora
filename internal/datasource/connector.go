package datasource

import (
	"context"
	"fmt"
	"math"
	"sort"
	"sync"

	"github.com/Tencent/WeKnora/internal/plugin/configschema"
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
	//
	// parentID controls lazy (on-demand) loading of hierarchical resources:
	//   - parentID == "" → return the top-level resources (e.g. Feishu wiki spaces).
	//   - parentID != "" → return only the direct children of that resource.
	// Connectors whose listing is already flat or returns the full tree in a single
	// call may ignore parentID for the root call and return an empty slice for any
	// non-empty parentID.
	ListResources(ctx context.Context, config *types.DataSourceConfig, parentID string) ([]types.Resource, error)

	// ResolveResourceAncestors resolves, for each of the given resource IDs, the
	// ExternalIDs of every ancestor whose direct children must be loaded so a
	// lazily-loaded picker can reveal a pre-existing (possibly deeply nested)
	// selection. The returned set is deduplicated and unordered.
	//
	// It exists so connectors that load their tree one level at a time (e.g. the
	// Feishu wiki) can expose, in O(depth) per selection, the path back to the
	// root without re-traversing the whole tree. Connectors that already return
	// the full tree (Notion) or a flat list (Yuque) have nothing to reveal and
	// return an empty slice.
	ResolveResourceAncestors(
		ctx context.Context, config *types.DataSourceConfig, resourceIDs []string,
	) ([]string, error)

	// FetchAll performs a full sync of the specified resources.
	// Returns all items from the given resource IDs.
	FetchAll(ctx context.Context, config *types.DataSourceConfig, resourceIDs []string) ([]types.FetchedItem, error)

	// FetchIncremental performs an incremental sync based on the provided cursor.
	// Returns items that have changed since the last sync, a new cursor for the next sync,
	// and an error if the operation fails.
	FetchIncremental(ctx context.Context, config *types.DataSourceConfig, cursor *types.SyncCursor) ([]types.FetchedItem, *types.SyncCursor, error)
}

// StreamHandler receives items and progress checkpoints emitted during a
// streaming fetch. The service implements it to ingest each item as it arrives
// (bounding memory to one item instead of the whole wiki) and to persist the
// connector cursor at page boundaries, so a sync that times out mid-traversal
// resumes from the last checkpoint instead of restarting from scratch
// (Tencent/WeKnora#2136).
type StreamHandler interface {
	// Emit ingests a single fetched item. Returning an error aborts the
	// stream: the connector stops fetching and propagates the error, since a
	// failed ingest means the sync is failing and further API calls are wasted.
	Emit(ctx context.Context, item types.FetchedItem) error

	// Checkpoint persists the cursor reached so far. The cursor is only valid
	// for the duration of the call (the connector may keep mutating its backing
	// maps afterwards), so implementations must serialize it synchronously.
	//
	// The cursor MUST be a complete resumable snapshot, not a delta: resuming
	// from it must reproduce all progress so far. This is what lets the service
	// treat a checkpoint as a safe restart point and, for a full sync, drop the
	// prior baseline without losing already-synced state.
	Checkpoint(ctx context.Context, cursor *types.SyncCursor) error
}

// StreamingConnector is an optional interface. Connectors that implement it let
// the service interleave fetch→ingest→checkpoint so a large sync persists
// incrementally and resumes after a timeout, rather than holding every item in
// memory and losing all progress on retry. Connectors that do not implement it
// fall back to FetchAll / FetchIncremental unchanged.
type StreamingConnector interface {
	Connector

	// FetchStream walks the configured resources starting from cursor (nil =
	// from the beginning / full sync), calling h.Emit for each changed item and
	// h.Checkpoint at page boundaries. It returns the final cursor for the next
	// sync. Nodes already recorded in cursor at their current edit time are
	// skipped, which is what makes a resumed sync converge.
	FetchStream(
		ctx context.Context, config *types.DataSourceConfig,
		cursor *types.SyncCursor, h StreamHandler,
	) (*types.SyncCursor, error)
}

// FullStreamingConnector lets a streaming connector re-fetch every item while
// retaining the previous cursor exclusively for safe deletion reconciliation.
type FullStreamingConnector interface {
	StreamingConnector

	FetchFullStream(
		ctx context.Context, config *types.DataSourceConfig,
		cursor *types.SyncCursor, h StreamHandler,
	) (*types.SyncCursor, error)
}

// FullSyncWithCursor is optional. The batch sync path uses it for ForceFull and
// sync_mode=full so a connector can re-fetch every document while still
// reconciling deletions against the previous cursor. Connectors that omit it
// keep FetchAll's no-cursor behaviour and therefore cannot emit deletions on a
// full sync.
type FullSyncWithCursor interface {
	FetchAllFromCursor(
		ctx context.Context,
		config *types.DataSourceConfig,
		resourceIDs []string,
		cursor *types.SyncCursor,
	) ([]types.FetchedItem, *types.SyncCursor, error)
}

// ConnectorRegistry manages the registration and lookup of available
// connectors. Installed plugins add and remove connectors while the server
// runs, so it is safe for concurrent use.
type ConnectorRegistry struct {
	mu         sync.RWMutex
	connectors map[string]Connector
	// plugins holds the metadata of connectors installed plugins registered.
	plugins map[string]ConnectorMetadata
}

// NewConnectorRegistry creates a new connector registry
func NewConnectorRegistry() *ConnectorRegistry {
	return &ConnectorRegistry{
		connectors: make(map[string]Connector),
		plugins:    make(map[string]ConnectorMetadata),
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
	r.mu.Lock()
	defer r.mu.Unlock()
	r.connectors[connector.Type()] = connector
	return nil
}

// RegisterPlugin adds or replaces a plugin's connector with the metadata it
// is listed under. It refuses to shadow a builtin connector.
func (r *ConnectorRegistry) RegisterPlugin(connector Connector, meta ConnectorMetadata) error {
	if connector == nil {
		return ErrConnectorNil
	}
	t := connector.Type()
	if t == "" {
		return ErrConnectorTypeEmpty
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.connectors[t]; exists {
		if _, isPlugin := r.plugins[t]; !isPlugin {
			return fmt.Errorf("connector type %s already exists", t)
		}
	}
	meta.Type = t
	r.connectors[t] = connector
	r.plugins[t] = meta
	return nil
}

// Unregister removes a plugin's connector; builtins stay. Syncs already
// running keep the connector they started with.
func (r *ConnectorRegistry) Unregister(connectorType string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, isPlugin := r.plugins[connectorType]; !isPlugin {
		return
	}
	delete(r.plugins, connectorType)
	delete(r.connectors, connectorType)
}

// Get retrieves a connector by type
func (r *ConnectorRegistry) Get(connectorType string) (Connector, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	connector, exists := r.connectors[connectorType]
	if !exists {
		return nil, ErrConnectorNotFound
	}
	return connector, nil
}

// List returns all registered connector types
func (r *ConnectorRegistry) List() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
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

	// Setup guide shown next to the credential form: where to create the
	// app, which scopes to grant and where to grant them.
	DocURL              string   `json:"doc_url,omitempty"`
	PermissionDocURL    string   `json:"permission_doc_url,omitempty"`
	PermissionPageURL   string   `json:"permission_page_url,omitempty"`
	RequiredPermissions []string `json:"required_permissions,omitempty"`
	// ConfigSchema describes DataSourceConfig.Credentials for this
	// connector; the editor renders the credential form from it.
	ConfigSchema *configschema.Schema `json:"config_schema,omitempty"`
	// SettingsSchema describes DataSourceConfig.Settings (non-secret
	// options) for connectors whose settings form is not built into the
	// frontend, such as plugin connectors.
	SettingsSchema *configschema.Schema `json:"settings_schema,omitempty"`
	// Names and Descriptions localize Name and Description, keyed by
	// locale, for plugin connectors (builtins use frontend locale keys).
	Names        map[string]string `json:"names,omitempty"`
	Descriptions map[string]string `json:"descriptions,omitempty"`
	// PluginID names the installed plugin providing the connector.
	PluginID string `json:"plugin_id,omitempty"`
	// PluginVersion and Editor locate the plugin's own editor page (a
	// file under ui/), shown below the generated form.
	PluginVersion string `json:"plugin_version,omitempty"`
	Editor        string `json:"editor,omitempty"`
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
		Capabilities: []string{"incremental", "deletion_sync"},
	},
	types.ConnectorTypeLark: {
		Type:         types.ConnectorTypeLark,
		Name:         "Lark",
		Description:  "Sync documents, wikis, and content from Lark (Feishu international)",
		Priority:     1,
		AuthType:     "oauth2",
		Capabilities: []string{"incremental", "deletion_sync"},
	},
	types.ConnectorTypeFeishuDrive: {
		Type:         types.ConnectorTypeFeishuDrive,
		Name:         "Feishu Drive (飞书云盘)",
		Description:  "Sync documents and files from a Feishu Drive folder",
		Priority:     2,
		AuthType:     "oauth2",
		Capabilities: []string{"incremental", "deletion_sync"},
	},
	types.ConnectorTypeLarkDrive: {
		Type:         types.ConnectorTypeLarkDrive,
		Name:         "Lark Drive",
		Description:  "Sync documents and files from a Lark Drive folder",
		Priority:     3,
		AuthType:     "oauth2",
		Capabilities: []string{"incremental", "deletion_sync"},
	},
	types.ConnectorTypeNotion: {
		Type:         types.ConnectorTypeNotion,
		Name:         "Notion",
		Description:  "Sync pages and databases from Notion",
		Priority:     4,
		AuthType:     "api_key",
		Capabilities: []string{"incremental"},
	},
	types.ConnectorTypeConfluence: {
		Type:         types.ConnectorTypeConfluence,
		Name:         "Confluence",
		Description:  "Sync spaces and pages from Atlassian Confluence",
		Priority:     5,
		AuthType:     "api_key",
		Capabilities: []string{"incremental", "deletion_sync"},
	},
	types.ConnectorTypeYuque: {
		Type:         types.ConnectorTypeYuque,
		Name:         "Yuque (语雀)",
		Description:  "Sync knowledge bases and documents from Yuque",
		Priority:     6,
		AuthType:     "api_key",
		Capabilities: []string{"incremental"},
	},
	types.ConnectorTypeIMA: {
		Type:         types.ConnectorTypeIMA,
		Name:         "Tencent IMA (ima.qq.com)",
		Description:  "Sync knowledge bases and documents from Tencent IMA",
		Priority:     8,
		AuthType:     "api_key",
		Capabilities: []string{"incremental", "deletion_sync"},
	},
	types.ConnectorTypeGitHub: {
		Type:         types.ConnectorTypeGitHub,
		Name:         "GitHub",
		Description:  "Sync repositories, wikis, and issues from GitHub",
		Priority:     20,
		AuthType:     "oauth2",
		Capabilities: []string{"incremental"},
	},
	types.ConnectorTypeGoogleDrive: {
		Type:         types.ConnectorTypeGoogleDrive,
		Name:         "Google Drive",
		Description:  "Sync documents and files from Google Drive",
		Priority:     21,
		AuthType:     "oauth2",
		Capabilities: []string{"incremental"},
	},
	types.ConnectorTypeOneDrive: {
		Type:         types.ConnectorTypeOneDrive,
		Name:         "OneDrive / SharePoint",
		Description:  "Sync documents and files from Microsoft OneDrive",
		Priority:     22,
		AuthType:     "oauth2",
		Capabilities: []string{"incremental"},
	},
	types.ConnectorTypeDingTalk: {
		Type:         types.ConnectorTypeDingTalk,
		Name:         "DingTalk (钉钉)",
		Description:  "Sync online documents from DingTalk knowledge bases",
		Priority:     7,
		AuthType:     "oauth2",
		Capabilities: []string{"incremental", "deletion_sync"},
	},
	types.ConnectorTypeWebCrawler: {
		Type:         types.ConnectorTypeWebCrawler,
		Name:         "Web Crawler (Sitemap)",
		Description:  "Crawl websites via Sitemap.xml",
		Priority:     23,
		AuthType:     "none",
		Capabilities: []string{},
	},
	types.ConnectorTypeSlack: {
		Type:         types.ConnectorTypeSlack,
		Name:         "Slack",
		Description:  "Sync channel messages and files from Slack",
		Priority:     24,
		AuthType:     "oauth2",
		Capabilities: []string{"incremental"},
	},
	types.ConnectorTypeIMAP: {
		Type:         types.ConnectorTypeIMAP,
		Name:         "Email (IMAP)",
		Description:  "Sync email content from IMAP servers",
		Priority:     25,
		AuthType:     "password",
		Capabilities: []string{},
	},
	types.ConnectorTypeRSS: {
		Type:         types.ConnectorTypeRSS,
		Name:         "RSS / Atom Feed",
		Description:  "Sync articles from RSS/Atom feeds",
		Priority:     9,
		AuthType:     "custom",
		Capabilities: []string{"incremental"},
	},
	types.ConnectorTypeGitLab: {
		Type:         types.ConnectorTypeGitLab,
		Name:         "GitLab",
		Description:  "Sync files from GitLab projects",
		Priority:     10,
		AuthType:     "token",
		Capabilities: []string{"incremental", "hierarchical"},
	},
}

// ListAvailableConnectors returns the metadata of every known connector,
// including ones not registered yet, with their credential forms, sorted by
// priority and then type.
func ListAvailableConnectors() []ConnectorMetadata {
	metadata := make([]ConnectorMetadata, 0, len(ConnectorMetadataRegistry))
	for _, meta := range ConnectorMetadataRegistry {
		metadata = append(metadata, withForm(meta))
	}
	sort.Slice(metadata, func(i, j int) bool {
		if metadata[i].Priority != metadata[j].Priority {
			return metadata[i].Priority < metadata[j].Priority
		}
		return metadata[i].Type < metadata[j].Type
	})
	return metadata
}

// Metadata returns the metadata of the connectors registered here, in
// ListAvailableConnectors order. A registered connector without an entry in
// ConnectorMetadataRegistry is listed last under its type.
func (r *ConnectorRegistry) Metadata() []ConnectorMetadata {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]ConnectorMetadata, 0, len(r.connectors))
	seen := make(map[string]bool, len(r.connectors))
	for _, meta := range ListAvailableConnectors() {
		if _, ok := r.connectors[meta.Type]; ok {
			out = append(out, meta)
			seen[meta.Type] = true
		}
	}
	rest := make([]string, 0)
	for t := range r.connectors {
		if !seen[t] {
			rest = append(rest, t)
		}
	}
	sort.Strings(rest)
	for _, t := range rest {
		if meta, ok := r.plugins[t]; ok {
			out = append(out, meta)
			continue
		}
		out = append(out, ConnectorMetadata{Type: t, Name: t, Priority: math.MaxInt32})
	}
	return out
}
