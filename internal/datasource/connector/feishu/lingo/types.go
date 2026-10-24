// Package lingo implements the Feishu Lingo (飞书词典) data source connector.
//
// Each Lingo repository is exposed as a selectable resource and each dictionary
// entity becomes one Markdown knowledge item. The Feishu entity ID is used as
// the item's ExternalID, so re-syncing updates the existing knowledge instead
// of creating a duplicate. Items are placed under a configurable knowledge base
// folder (default: 飞书词典) through the FileName hierarchy.
package lingo

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strconv"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
)

// defaultRepoID is a synthetic resource used when the tenant has no listable
// Lingo repository but the entities endpoint still returns organisation-wide
// terms. Listing entities with this ID omits the repo_id query parameter.
const defaultRepoID = "default"

// Repository is one Feishu Lingo repository.
type Repository struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// Entity is one Feishu Lingo dictionary entry.
//
// update_time is a Unix-seconds string and is the primary incremental signal.
// rich_text is HTML; description is the plain-text fallback.
type Entity struct {
	ID          string    `json:"id"`
	MainKeys    []MainKey `json:"main_keys"`
	Aliases     []MainKey `json:"aliases,omitempty"`
	Description string    `json:"description"`
	RichText    string    `json:"rich_text"`
	CreateTime  string    `json:"create_time"`
	UpdateTime  string    `json:"update_time"`
}

// MainKey is a term name returned by the Lingo API.
type MainKey struct {
	Key string `json:"key"`
}

type repoListResponse struct {
	Code int    `json:"code"`
	Msg  string `json:"msg"`
	Data struct {
		Items []Repository `json:"items"`
	} `json:"data"`
}

type entityListResponse struct {
	Code int    `json:"code"`
	Msg  string `json:"msg"`
	Data struct {
		Entities  []Entity `json:"entities"`
		HasMore   bool     `json:"has_more"`
		PageToken string   `json:"page_token"`
	} `json:"data"`
}

// lingoCursor is persisted in SyncCursor.ConnectorCursor.
//
// EntityTimes maps entity ID to its last seen version (update_time, or a
// content hash when the API omits it). EntityRepos lets deletion events carry
// the resource they came from and prevents cross-scope false positives.
type lingoCursor struct {
	LastSyncTime time.Time         `json:"last_sync_time"`
	EntityTimes  map[string]string `json:"entity_times,omitempty"`
	EntityRepos  map[string]string `json:"entity_repos,omitempty"`
}

func decodeLingoCursor(cur *types.SyncCursor) *lingoCursor {
	if cur == nil || len(cur.ConnectorCursor) == 0 {
		return nil
	}
	raw, err := json.Marshal(cur.ConnectorCursor)
	if err != nil {
		return nil
	}
	var decoded lingoCursor
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return nil
	}
	if len(decoded.EntityTimes) == 0 {
		return nil
	}
	return &decoded
}

func (c *lingoCursor) encode() *types.SyncCursor {
	if c == nil {
		return nil
	}
	raw, err := json.Marshal(c)
	if err != nil {
		return nil
	}
	var cursorMap map[string]interface{}
	if err := json.Unmarshal(raw, &cursorMap); err != nil {
		return nil
	}
	return &types.SyncCursor{
		LastSyncTime:    c.LastSyncTime,
		ConnectorCursor: cursorMap,
	}
}

// entityTitle returns the primary term name.
func entityTitle(e Entity) string {
	if len(e.MainKeys) == 0 {
		return ""
	}
	return strings.TrimSpace(e.MainKeys[0].Key)
}

// entityAliases returns every additional term name without duplicates.
func entityAliases(e Entity) []string {
	seen := make(map[string]struct{})
	extra := make([]MainKey, 0, len(e.MainKeys)+len(e.Aliases))
	if len(e.MainKeys) > 1 {
		extra = append(extra, e.MainKeys[1:]...)
	}
	extra = append(extra, e.Aliases...)
	out := make([]string, 0, len(extra))
	for _, key := range extra {
		name := strings.TrimSpace(key.Key)
		if name == "" {
			continue
		}
		if _, ok := seen[name]; ok {
			continue
		}
		seen[name] = struct{}{}
		out = append(out, name)
	}
	return out
}

// entityVersion returns a stable change signal for incremental sync.
func entityVersion(e Entity) string {
	if v := strings.TrimSpace(e.UpdateTime); v != "" {
		return v
	}
	sum := sha256.Sum256([]byte(e.Description + "\x00" + e.RichText))
	return "h:" + hex.EncodeToString(sum[:8])
}

// unixSeconds parses a Unix-seconds string; invalid or empty values yield zero.
func unixSeconds(value string) time.Time {
	seconds, err := strconv.ParseInt(strings.TrimSpace(value), 10, 64)
	if err != nil || seconds <= 0 {
		return time.Time{}
	}
	return time.Unix(seconds, 0).UTC()
}
