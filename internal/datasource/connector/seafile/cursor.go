package seafile

import (
	"encoding/json"
	"fmt"
	"maps"
	"time"

	"github.com/Tencent/WeKnora/internal/datasource"
	"github.com/Tencent/WeKnora/internal/types"
)

const cursorSchemaVersion = 1

// cursor is the connector state persisted between runs. Files maps every
// known path to its fingerprint; an empty fingerprint marks a path whose
// fetch failed and must be retried. FullSync and FullSyncBaseline exist only
// while a forced full run is in progress (same shape as Confluence).
type cursor struct {
	SchemaVersion    int               `json:"schema_version"`
	RepoID           string            `json:"repo_id"`
	RepoName         string            `json:"repo_name"`
	Files            map[string]string `json:"files"`
	FullSync         bool              `json:"full_sync,omitempty"`
	FullSyncBaseline map[string]string `json:"full_sync_baseline,omitempty"`
}

// decodeCursor accepts a nil or empty cursor as a fresh state; anything else
// must carry the current schema version.
func decodeCursor(old *types.SyncCursor) (cursor, error) {
	c := cursor{SchemaVersion: cursorSchemaVersion}
	if old != nil && len(old.ConnectorCursor) > 0 {
		c.SchemaVersion = 0
		raw, err := json.Marshal(old.ConnectorCursor)
		if err == nil {
			err = json.Unmarshal(raw, &c)
		}
		if err != nil {
			return cursor{}, fmt.Errorf("%w: invalid Seafile cursor: %w", datasource.ErrInvalidConfig, err)
		}
		if c.SchemaVersion != cursorSchemaVersion {
			return cursor{}, fmt.Errorf("%w: unsupported Seafile cursor schema %d",
				datasource.ErrInvalidConfig, c.SchemaVersion)
		}
	}
	if c.Files == nil {
		c.Files = make(map[string]string)
	}
	if c.FullSyncBaseline == nil {
		c.FullSyncBaseline = make(map[string]string)
	}
	return c, nil
}

// toSyncCursor produces a snapshot independent of the working maps through a
// JSON round trip, so the service can serialize it after the run moves on.
// Neither step can fail: the cursor is plain strings and string maps.
func (c *cursor) toSyncCursor() *types.SyncCursor {
	raw, _ := json.Marshal(c)
	fields := map[string]interface{}{}
	_ = json.Unmarshal(raw, &fields)
	return &types.SyncCursor{LastSyncTime: time.Now().UTC(), ConnectorCursor: fields}
}

// prepareCursor decodes the previous cursor, enforces the library binding and
// switches into full-sync mode when requested. The returned flag reports
// whether the cursor changed and therefore needs saving.
func prepareCursor(old *types.SyncCursor, repoID string, forceFull bool) (cursor, bool, error) {
	c, err := decodeCursor(old)
	if err != nil {
		return cursor{}, false, err
	}
	if c.RepoID != "" && c.RepoID != repoID {
		return cursor{}, false, fmt.Errorf(
			"%w: this data source already synced Seafile library %q; restore that library "+
				"or create a new data source instead of switching to %s",
			datasource.ErrInvalidConfig, c.RepoName, repoID)
	}
	changed := false
	if c.RepoID == "" {
		c.RepoID = repoID
		changed = true
	}
	if forceFull && !c.FullSync {
		c.FullSyncBaseline = maps.Clone(c.Files)
		c.Files = make(map[string]string)
		c.FullSync = true
		changed = true
	}
	return c, changed, nil
}
