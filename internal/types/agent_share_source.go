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

// NormalizeAgentSourceTenantID maps a self-referential source selector to the
// absent selector. An agent from the caller's own workspace is not (and
// cannot be) shared to that workspace, so requesting it "from itself" means
// the own agent, which the local resolution path handles; leaving the value
// as-is would send it into the shared lookup and die as a missing share.
func NormalizeAgentSourceTenantID(source, callerTenantID uint64) uint64 {
	if source != 0 && source == callerTenantID {
		return 0
	}
	return source
}
