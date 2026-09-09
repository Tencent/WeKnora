package approval

import (
	"context"
	"fmt"

	"github.com/Tencent/WeKnora/internal/types"
)

type enabledChecker interface {
	IsEnabled(context.Context, uint64, string, string) (bool, error)
}

// BulkEnabledChecker avoids a query per tool when enumerating a directory.
// Implementations return an explicit decision for each requested name.
type BulkEnabledChecker interface {
	EnabledTools(context.Context, uint64, string, []string) (map[string]bool, error)
}

// EnabledTools uses batch policies where available while retaining compatibility
// with custom gates implementing only the original single-tool contract.
func EnabledTools(
	ctx context.Context,
	checker enabledChecker,
	tenantID uint64,
	serviceID string,
	names []string,
) (map[string]bool, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if tenantID == 0 || serviceID == "" {
		return nil, fmt.Errorf("MCP policy identity is required")
	}
	if bulk, ok := checker.(BulkEnabledChecker); ok {
		return bulk.EnabledTools(ctx, tenantID, serviceID, names)
	}
	return enabledToolsIndividually(ctx, checker, tenantID, serviceID, names)
}

func enabledToolsIndividually(
	ctx context.Context,
	checker enabledChecker,
	tenantID uint64,
	serviceID string,
	names []string,
) (map[string]bool, error) {
	result := make(map[string]bool, len(names))
	for _, name := range names {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		enabled := true
		if checker != nil {
			var err error
			enabled, err = checker.IsEnabled(ctx, tenantID, serviceID, name)
			if err != nil {
				return nil, err
			}
		}
		result[name] = enabled
	}
	return result, nil
}

// EnabledTools delegates directory policy checks to the configured checker.
func (g *Gate) EnabledTools(
	ctx context.Context,
	tenantID uint64,
	serviceID string,
	names []string,
) (map[string]bool, error) {
	if g == nil {
		return EnabledTools(ctx, nil, tenantID, serviceID, names)
	}
	return EnabledTools(ctx, g.checker, tenantID, serviceID, names)
}

// EnabledTools reads service policies in one batch when supported by the service.
func (a *Adapter) EnabledTools(
	ctx context.Context,
	tenantID uint64,
	serviceID string,
	names []string,
) (map[string]bool, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if tenantID == 0 || serviceID == "" {
		return nil, fmt.Errorf("MCP policy identity is required")
	}
	if a == nil || a.Svc == nil {
		return enabledToolsIndividually(ctx, nil, tenantID, serviceID, names)
	}
	lister, ok := a.Svc.(interface {
		ListByService(context.Context, uint64, string) ([]*types.MCPToolApproval, error)
	})
	if !ok {
		return enabledToolsIndividually(ctx, a, tenantID, serviceID, names)
	}
	rows, err := lister.ListByService(ctx, tenantID, serviceID)
	if err != nil {
		return nil, err
	}
	result, err := enabledToolsIndividually(ctx, nil, tenantID, serviceID, names)
	if err != nil {
		return nil, err
	}
	for _, row := range rows {
		if row == nil || row.TenantID != tenantID || row.ServiceID != serviceID {
			continue
		}
		if _, requested := result[row.ToolName]; requested {
			result[row.ToolName] = row.Enabled
		}
	}
	return result, nil
}
