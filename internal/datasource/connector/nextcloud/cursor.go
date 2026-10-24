package nextcloud

import (
	"encoding/json"
	"fmt"
	"maps"
	"time"

	"github.com/Tencent/WeKnora/internal/datasource"
	"github.com/Tencent/WeKnora/internal/types"
)

const cursorSchemaVersion = 1

// Files uses paths as identities. A move is an addition and a deletion, even
// when the optional server file ID is unchanged. Empty fingerprints retry
// failed fetches. A forced full run retains its deletion baseline on resume.
type cursor struct {
	SchemaVersion    int               `json:"schema_version"`
	Scope            string            `json:"scope"`
	Files            map[string]string `json:"files"`
	FullSync         bool              `json:"full_sync,omitempty"`
	FullSyncBaseline map[string]string `json:"full_sync_baseline,omitempty"`
}

func decodeCursor(old *types.SyncCursor) (cursor, error) {
	c := cursor{SchemaVersion: cursorSchemaVersion}
	if old != nil && len(old.ConnectorCursor) > 0 {
		c.SchemaVersion = 0
		raw, err := json.Marshal(old.ConnectorCursor)
		if err == nil {
			err = json.Unmarshal(raw, &c)
		}
		if err != nil || c.SchemaVersion != cursorSchemaVersion {
			return cursor{}, fmt.Errorf("%w: invalid or unsupported Nextcloud cursor", datasource.ErrInvalidConfig)
		}
	}
	for _, files := range []map[string]string{c.Files, c.FullSyncBaseline} {
		for p := range files {
			if normalized, err := normalizePath(p); err != nil || normalized != p {
				return cursor{}, fmt.Errorf("%w: invalid Nextcloud cursor path", datasource.ErrInvalidConfig)
			}
		}
	}
	if c.Files == nil {
		c.Files = make(map[string]string)
	}
	return c, nil
}

func prepareCursor(old *types.SyncCursor, scope string, full bool) (cursor, error) {
	c, err := decodeCursor(old)
	if err != nil {
		return cursor{}, err
	}
	if c.Scope != "" && c.Scope != scope {
		return cursor{}, fmt.Errorf("%w: Nextcloud deployment or account changed; create a new data source",
			datasource.ErrInvalidConfig)
	}
	c.Scope = scope
	if full && !c.FullSync {
		c.FullSyncBaseline = maps.Clone(c.Files)
		c.Files = make(map[string]string)
		c.FullSync = true
	}
	return c, nil
}

// A JSON round trip gives the service an independent, complete snapshot.
func (c cursor) toSyncCursor() *types.SyncCursor {
	raw, _ := json.Marshal(c)
	fields := map[string]any{}
	_ = json.Unmarshal(raw, &fields)
	return &types.SyncCursor{LastSyncTime: time.Now().UTC(), ConnectorCursor: fields}
}
