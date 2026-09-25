package handler

import (
	"github.com/gin-gonic/gin"

	"github.com/Tencent/WeKnora/internal/plugin/manifest"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// pluginGated gives an integration handler the tenant's plugin switches.
// It is set after construction (SetPluginGate) so the many handler tests that
// build handlers directly need no gate; without one everything is enabled.
type pluginGated struct {
	gate interfaces.PluginGate
}

// SetPluginGate installs the tenant plugin switches.
func (g *pluginGated) SetPluginGate(gate interfaces.PluginGate) { g.gate = gate }

// pluginFilter returns whether a contribution is enabled for the caller's
// tenant. A disabled plugin's contributions are left out of type listings
// and cannot back new instances; existing instances keep working.
func (g *pluginGated) pluginFilter(c *gin.Context) func(manifest.Point, string) bool {
	tenantID := c.GetUint64(types.TenantIDContextKey.String())
	if tenantID == 0 {
		if v, ok := c.Request.Context().Value(types.TenantIDContextKey).(uint64); ok {
			tenantID = v
		}
	}
	if g.gate == nil || tenantID == 0 {
		return func(manifest.Point, string) bool { return true }
	}
	return g.gate.EnabledFilter(c.Request.Context(), tenantID)
}

// disabledIntegrationError is the message for creating an instance of a
// contribution whose plugin the workspace has disabled.
const disabledIntegrationError = "this integration is disabled for the workspace; enable its plugin first"
