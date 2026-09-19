package plugin

import (
	"fmt"
	"strings"
)

// IsolationType describes where an external plugin is executed. It is kept
// separate from the network policy so adding a future sandbox backend does not
// require changing the plugin protocols.
type IsolationType string

const (
	IsolationProcess IsolationType = "process"
	IsolationOCI     IsolationType = "oci"
)

// ExecutionPlan is the single resolved deployment decision for one plugin:
// where it runs (process vs OCI), whether it may network, and the final
// manifest-bounded allowlist. It is the only output of ResolveExecutionPlan;
// there is no separate NetworkMode or UserNetworkConfig layer.
type ExecutionPlan struct {
	Isolation IsolationType
	Network   NetworkPolicy
	Allowlist []string
}

// ResolveExecutionPlan computes the effective execution plan from the plugin's
// manifest (the upper bound on permissions) and the administrator's trust
// choice. The trust level can only disable or narrow networking; it can never
// grant a destination the manifest did not request.
func ResolveExecutionPlan(manifest Manifest, trust PluginTrustLevel) (ExecutionPlan, error) {
	plan := ExecutionPlan{Isolation: IsolationProcess, Network: NetworkNone}
	if strings.HasPrefix(strings.TrimSpace(manifest.Entrypoint), "docker://") {
		plan.Isolation = IsolationOCI
	}

	switch trust {
	case "", TrustOffline:
		return plan, nil
	case TrustTrusted:
		if plan.Isolation == IsolationOCI {
			return ExecutionPlan{}, fmt.Errorf("trusted plugin %q cannot use an OCI entrypoint; use isolated", manifest.ID)
		}
		plan.Network = manifest.EffectiveNetworkPolicy()
		if plan.Network == NetworkAllowlist {
			plan.Allowlist = normalizeDestinations(manifest.Permissions.AllowedDestinations)
		}
		return plan, nil
	case TrustIsolated:
		if plan.Isolation != IsolationOCI {
			return ExecutionPlan{}, fmt.Errorf("isolated plugin %q must use an OCI entrypoint", manifest.ID)
		}
		plan.Network = manifest.EffectiveNetworkPolicy()
		if plan.Network == NetworkAllowlist {
			plan.Allowlist = normalizeDestinations(manifest.Permissions.AllowedDestinations)
		}
		return plan, nil
	default:
		return ExecutionPlan{}, fmt.Errorf("unsupported plugin trust level %q", trust)
	}
}

// normalizeDestinations lowercases, trims, dedupes and drops empty allowlist
// entries so downstream matching is deterministic.
func normalizeDestinations(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.ToLower(strings.TrimSpace(value))
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	return result
}
