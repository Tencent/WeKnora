package types

import (
	"fmt"
	"strconv"
	"strings"
)

const AgentSourceTenantIDParam = "agent_source_tenant_id"

// ParseAgentSourceTenantID parses the optional shared-agent source workspace selector.
// Empty or whitespace input is treated as absent (0, nil). Non-empty but invalid
// values return an error so callers fail closed instead of silently falling back.
func ParseAgentSourceTenantID(raw string) (uint64, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0, nil
	}
	value, err := strconv.ParseUint(raw, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid %s: %w", AgentSourceTenantIDParam, err)
	}
	return value, nil
}
