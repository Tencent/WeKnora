package approval

import (
	"context"
	"fmt"

	"github.com/Tencent/WeKnora/internal/types"
)

// SessionPolicyReader loads all IM restrictions for a service in one read.
type SessionPolicyReader interface {
	ListByService(context.Context, uint64, string) ([]*types.MCPToolSessionPolicy, error)
}

// WithSessionPolicies adds an IM-only restriction to both discovery and
// execution. It does not change approval or risk decisions.
func (g *Gate) WithSessionPolicies(reader SessionPolicyReader) *Gate {
	g.sessionPolicies = reader
	return g
}

func (g *Gate) restrictIMTools(
	ctx context.Context, tenantID uint64, serviceID string, enabled map[string]bool,
) (map[string]bool, error) {
	conversation, isIM := types.IMConversationFromContext(ctx)
	if !isIM && types.PrincipalAuthSourceFromContext(ctx) != types.AuthSourceIM {
		return enabled, nil
	}
	if g == nil || g.sessionPolicies == nil {
		return enabled, nil
	}
	if tenantID == 0 || serviceID == "" {
		return nil, fmt.Errorf("MCP session policy identity is required")
	}
	// One read per service, independent of the number of tools. A failed read
	// refuses discovery/execution instead of treating a policy outage as absence.
	policies, err := g.sessionPolicies.ListByService(ctx, tenantID, serviceID)
	if err != nil {
		return nil, fmt.Errorf("read MCP tool session policies: %w", err)
	}
	for _, p := range policies {
		if p == nil || p.TenantID != tenantID || p.ServiceID != serviceID {
			continue
		}
		if allowed, requested := enabled[p.ToolName]; requested {
			enabled[p.ToolName] = allowed && p.Allows(conversation)
		}
	}
	return enabled, nil
}
