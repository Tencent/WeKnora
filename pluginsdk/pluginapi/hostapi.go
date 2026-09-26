package pluginapi

import (
	"encoding/json"
	"time"
)

// Host API: how a plugin calls back into WeKnora. The URL and a short-lived
// bearer token arrive in Context.Host of each call; the token is bound to the
// tenant of that call and the scopes the plugin was granted (manifest
// permissions.hostApi). Errors use ErrorBody.

// HostKVPath is the key-value endpoint (scope "kv"): GET and DELETE take
// ?key=, PUT takes a KVPut body.
const HostKVPath = "/api/v1/plugin-host/kv"

// HostKVListPath lists keys: ?prefix=&after=&limit=.
const HostKVListPath = "/api/v1/plugin-host/kv/list"

// KVEntry is one stored value.
type KVEntry struct {
	Key       string          `json:"key"`
	Value     json.RawMessage `json:"value"`
	ExpiresAt *time.Time      `json:"expiresAt,omitempty"`
	UpdatedAt time.Time       `json:"updatedAt"`
}

// KVPut stores a value; TTLSeconds 0 keeps it until deleted.
type KVPut struct {
	Key        string          `json:"key"`
	Value      json.RawMessage `json:"value"`
	TTLSeconds int             `json:"ttlSeconds,omitempty"`
}

// KVList is a page of keys in key order; pass Next as after for the next
// page (empty when there is none).
type KVList struct {
	Entries []KVEntry `json:"entries"`
	Next    string    `json:"next,omitempty"`
}

// Limits of the key-value store.
const (
	KVMaxKeyBytes   = 256
	KVMaxValueBytes = 64 << 10
	KVMaxKeys       = 10000 // per plugin and tenant
	KVMaxListLimit  = 1000
)

// HostDataSourcesPath lists the workspace's data sources of the plugin's
// connectors (scope "datasources"): GET answers a DataSourceList.
const HostDataSourcesPath = "/api/v1/plugin-host/datasources"

// HostDataSourceSyncPath starts an incremental sync of one of them: POST
// answers a SyncStarted. A plugin calls it when the source tells it
// something changed (a webhook), instead of waiting for the schedule.
func HostDataSourceSyncPath(id string) string { return HostDataSourcesPath + "/" + id + "/sync" }

// DataSourceInfo is one data source of the plugin's connectors.
type DataSourceInfo struct {
	ID              string `json:"id"`
	Name            string `json:"name"`
	KnowledgeBaseID string `json:"knowledgeBaseId"`
	// Connector is the local ID of the connector (contributes.connectors[].id).
	Connector string `json:"connector"`
	// ResourceIDs are the selected resources (projects, spaces).
	ResourceIDs []string   `json:"resourceIds"`
	Status      string     `json:"status"`
	LastSyncAt  *time.Time `json:"lastSyncAt,omitempty"`
}

// DataSourceList lists data sources.
type DataSourceList struct {
	DataSources []DataSourceInfo `json:"dataSources"`
}

// Sync states SyncStarted reports.
const (
	SyncQueued = "queued"
	// SyncRunning means a sync had already started; it is not queued again.
	SyncRunning = "running"
)

// SyncStarted says what became of a sync request.
type SyncStarted struct {
	Status    string `json:"status"`
	SyncLogID string `json:"syncLogId,omitempty"`
}
