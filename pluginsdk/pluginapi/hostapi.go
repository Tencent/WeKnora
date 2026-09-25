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
