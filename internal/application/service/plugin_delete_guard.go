package service

import (
	"context"
	"fmt"
	"strings"

	apperrors "github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/types"
)

// pluginOwnedDeleteError refuses to delete one resource a plugin install owns.
// The plugin page is the only uninstall entry, whether the plugin owns one
// piece or many.
func pluginOwnedDeleteError(info *types.PluginInfoForResource, kind string) error {
	if info == nil {
		return nil
	}
	name := strings.TrimSpace(info.PluginName)
	if name == "" {
		name = info.PluginInstallID
	}
	what := "技能"
	if kind == types.PluginResourceKindMCPService {
		what = "MCP 服务"
	}
	return apperrors.NewConflictError(fmt.Sprintf(
		"%s属于插件 %s，请到插件页卸载整个插件",
		what,
		name,
	))
}

// refusePluginOwnedCatalog rejects a direct skill or catalog delete when a
// plugin install owns catalogID.
func (s *TenantSkillService) refusePluginOwnedCatalog(
	ctx context.Context,
	tenantID uint64,
	catalogID string,
) error {
	if s == nil || s.pluginLookup == nil || strings.TrimSpace(catalogID) == "" {
		return nil
	}
	info, err := s.pluginLookup.LookupPluginByResource(
		ctx, tenantID, types.PluginResourceKindSkillCatalog, catalogID,
	)
	if err != nil {
		return fmt.Errorf("failed to check plugin ownership: %w", err)
	}
	return pluginOwnedDeleteError(info, types.PluginResourceKindSkillCatalog)
}
