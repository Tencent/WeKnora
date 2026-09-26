package pluginapi

import "time"

// ConnectorValidatePath is the validate endpoint of connector id.
func ConnectorValidatePath(id string) string { return "/v1/connectors/" + id + "/validate" }

// ConnectorListResourcesPath is the list-resources endpoint of connector id.
func ConnectorListResourcesPath(id string) string { return "/v1/connectors/" + id + "/list-resources" }

// ConnectorResolveAncestorsPath is the resolve-ancestors endpoint of connector id.
func ConnectorResolveAncestorsPath(id string) string {
	return "/v1/connectors/" + id + "/resolve-ancestors"
}

// ConnectorFetchPath is the streaming fetch endpoint of connector id.
func ConnectorFetchPath(id string) string { return "/v1/connectors/" + id + "/fetch" }

// The connector's instance configuration arrives in Envelope.Config.Instance
// as {"credentials": {...}, "settings": {...}, "resourceIds": [...]}.

// ListResourcesInput asks for the resources under ParentID ("" = top level).
type ListResourcesInput struct {
	ParentID string `json:"parentId,omitempty"`
}

// ListResourcesOutput lists resources a user can pick for syncing.
type ListResourcesOutput struct {
	Resources []Resource `json:"resources"`
}

// Resource is something a data source can sync (a space, folder, feed).
type Resource struct {
	ExternalID  string         `json:"externalId"`
	Name        string         `json:"name"`
	Type        string         `json:"type,omitempty"`
	Description string         `json:"description,omitempty"`
	URL         string         `json:"url,omitempty"`
	ModifiedAt  *time.Time     `json:"modifiedAt,omitempty"`
	ParentID    string         `json:"parentId,omitempty"`
	HasChildren bool           `json:"hasChildren,omitempty"`
	Metadata    map[string]any `json:"metadata,omitempty"`
}

// ResolveAncestorsInput asks which ancestors of the given resources a
// lazily loaded picker must expand.
type ResolveAncestorsInput struct {
	ResourceIDs []string `json:"resourceIds"`
}

// ResolveAncestorsOutput is the deduplicated set of ancestor IDs.
type ResolveAncestorsOutput struct {
	Ancestors []string `json:"ancestors"`
}

// Fetch modes.
const (
	FetchFull        = "full"
	FetchIncremental = "incremental"
)

// FetchInput starts or resumes a sync. The answer is a stream of "item"
// events (FetchedItem), "checkpoint" events (Cursor) at page boundaries and
// an "end" event carrying the final Cursor.
type FetchInput struct {
	Mode string `json:"mode"`
	// Cursor is the last checkpoint or final cursor; nil starts from scratch.
	Cursor *Cursor `json:"cursor,omitempty"`
	// ResourceIDs are the selected resources (also in config.instance).
	ResourceIDs []string `json:"resourceIds,omitempty"`
}

// Cursor is the connector's resumable state. WeKnora stores it and hands it
// back; its content is the plugin's business. A checkpoint must be a
// complete snapshot: resuming from it reproduces all progress so far.
type Cursor struct {
	LastSyncTime *time.Time     `json:"lastSyncTime,omitempty"`
	State        map[string]any `json:"state,omitempty"`
}

// FetchedItem is one document fetched from the source.
type FetchedItem struct {
	ExternalID string `json:"externalId"`
	Title      string `json:"title"`
	// Content is the document body; Markdown preferred. JSON carries it as
	// base64.
	Content          []byte            `json:"content,omitempty"`
	ContentType      string            `json:"contentType,omitempty"`
	FileName         string            `json:"fileName,omitempty"`
	URL              string            `json:"url,omitempty"`
	UpdatedAt        *time.Time        `json:"updatedAt,omitempty"`
	CreatedAt        *time.Time        `json:"createdAt,omitempty"`
	Metadata         map[string]string `json:"metadata,omitempty"`
	IsDeleted        bool              `json:"isDeleted,omitempty"`
	SourceResourceID string            `json:"sourceResourceId,omitempty"`
}
