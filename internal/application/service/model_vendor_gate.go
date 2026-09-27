package service

import (
	"context"
	"fmt"

	modelruntime "github.com/Tencent/WeKnora/internal/models/runtime"
	"github.com/Tencent/WeKnora/internal/plugin/manifest"
	plugintenancy "github.com/Tencent/WeKnora/internal/plugin/tenancy"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// SetPluginGate installs the tenant plugin switches, so a model whose vendor
// a plugin provides stops working in a workspace that turned the plugin off.
// Until it is set (startup, tests) every vendor is allowed.
func (s *modelService) SetPluginGate(gate interfaces.PluginGate) { s.gate = gate }

// CheckModelVendor implements interfaces.ModelService: a model whose vendor
// comes from a plugin the model's workspace turned off, or may not see, is
// refused. A plugin vendor is declarative, so its models call the vendor
// directly and the plugin invoker never sees them; this is where they are
// cut off. The check goes by the vendor the model runtime would use, not by
// the provider the row stores.
//
// A workspace's model follows that workspace's switch, also where another
// workspace uses it (a shared knowledge base's embeddings). A built-in model,
// offered to every workspace, follows the switch of the one using it.
func (s *modelService) CheckModelVendor(ctx context.Context, model *types.Model) error {
	if s.gate == nil || model == nil {
		return nil
	}
	tenantID := model.TenantID
	if caller, ok := types.TenantIDFromContext(ctx); ok && (model.IsBuiltin || tenantID == 0) {
		tenantID = caller
	}
	if tenantID == 0 {
		return nil // no workspace to ask
	}
	vendor := modelruntime.VendorID(model.Parameters.Provider, model.Parameters.BaseURL)
	if s.gate.CallFilter(ctx, tenantID)(manifest.PointModelVendors, vendor) {
		return nil
	}
	return fmt.Errorf("%w: model %s uses vendor %s, whose plugin this workspace has turned off; "+
		"enable the plugin to use the model", plugintenancy.ErrPluginOff, model.Name, vendor)
}
