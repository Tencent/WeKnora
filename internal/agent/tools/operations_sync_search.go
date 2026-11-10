package tools

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/Tencent/WeKnora/internal/agent/operations"
)

func syncAndSearchDetail(op operations.Operation, params map[string]json.RawMessage) []string {
	var lines []string
	if op.ID == "web_search_provider.update" {
		if value, ok := rawBool(params, "is_default"); ok {
			lines = append(lines, fmt.Sprintf("Workspace default: %t (affects agents that follow the default)", value))
		}
	}
	if op.ID != "data_source.update_sync" {
		return lines
	}
	if value, ok := rawString(params, "sync_schedule"); ok {
		if value == "" {
			value = "manual only (scheduled sync disabled)"
		}
		lines = append(lines, "Sync schedule: "+value)
	}
	for _, key := range []string{"sync_mode", "conflict_strategy"} {
		if value, ok := rawString(params, key); ok {
			lines = append(lines, key+": "+value)
		}
	}
	if value, ok := rawBool(params, "sync_deletions"); ok {
		lines = append(lines, fmt.Sprintf("Sync upstream deletions: %t", value))
		if value {
			lines = append(lines, "Future syncs may delete knowledge base documents removed upstream.")
		}
	}
	if ids, ok := rawStringList(params, "resource_ids"); ok {
		lines = append(lines, "Replace complete sync selection with resource IDs: "+strings.Join(ids, ", "))
	}
	lines = append(lines, "Applies to future syncs; does not start synchronization or resume a paused source.")
	return lines
}
