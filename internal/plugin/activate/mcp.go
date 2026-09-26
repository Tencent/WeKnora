// Package activate connects installed plugins' declarative contributions to
// the domains that use them: MCP servers, skills and model vendors.
package activate

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/plugin/install"
	"github.com/Tencent/WeKnora/internal/plugin/manifest"
	"github.com/Tencent/WeKnora/internal/plugin/reconcile"
	"github.com/Tencent/WeKnora/internal/plugin/tenancy"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// ErrPluginService is returned when a caller tries to change an MCP service
// a plugin provides.
var ErrPluginService = errors.New("this MCP service is provided by a plugin and cannot be changed")

// mcpNamespace seeds the IDs of plugin MCP services.
var mcpNamespace = uuid.MustParse("5b0c8e5e-4f7c-4a55-9d0e-6f1d0f4c7a21")

type mcpServer struct {
	manifest    *manifest.Manifest
	contrib     manifest.Contribution
	qualifiedID string
	activatedAt time.Time
}

// MCPServers makes the MCP servers of installed plugins appear as read-only
// MCP services in every workspace that enabled the plugin. Each workspace
// gets its own service ID, because headers carry its own configuration.
type MCPServers struct {
	mu      sync.RWMutex
	tenancy *tenancy.Service
	plugins interfaces.PluginRepository
	servers map[string][]mcpServer // plugin ID → servers
	iv      *Invoker
}

// NewMCPServers creates the MCP server activator. It lists nothing until
// Bind gives it the tenant switches: the MCP repository it decorates is
// needed long before the plugin services exist.
func NewMCPServers() *MCPServers {
	return &MCPServers{servers: map[string][]mcpServer{}}
}

// Bind supplies the tenant switches and configuration and the installed
// plugin rows (for platform configuration).
func (a *MCPServers) Bind(t *tenancy.Service, plugins interfaces.PluginRepository) {
	a.mu.Lock()
	a.tenancy, a.plugins = t, plugins
	a.mu.Unlock()
}

// Name implements reconcile.Activator.
func (a *MCPServers) Name() string { return "mcpServers" }

// Activate implements reconcile.Activator.
func (a *MCPServers) Activate(_ context.Context, l *reconcile.Loaded) error {
	var list []mcpServer
	now := time.Now()
	for _, c := range l.Manifest.Contributes[manifest.PointMCPServers] {
		list = append(list, mcpServer{
			manifest: l.Manifest, contrib: c, qualifiedID: l.Manifest.ID + "/" + c.ID, activatedAt: now,
		})
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if len(list) == 0 {
		delete(a.servers, l.Manifest.ID)
	} else {
		a.servers[l.Manifest.ID] = list
	}
	return nil
}

// Deactivate implements reconcile.Activator.
func (a *MCPServers) Deactivate(_ context.Context, pluginID string) error {
	a.mu.Lock()
	delete(a.servers, pluginID)
	a.mu.Unlock()
	return nil
}

// serviceID is a plugin MCP service's ID in one workspace. It is stable
// across nodes and restarts, so agents can keep it in their configuration.
func serviceID(tenantID uint64, qualifiedID string) string {
	return uuid.NewSHA1(mcpNamespace, fmt.Appendf(nil, "%d/%s", tenantID, qualifiedID)).String()
}

func (a *MCPServers) snapshot() []mcpServer {
	a.mu.RLock()
	defer a.mu.RUnlock()
	if a.tenancy == nil {
		return nil
	}
	var out []mcpServer
	for _, list := range a.servers {
		out = append(out, list...)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].qualifiedID < out[j].qualifiedID })
	return out
}

// Services returns the plugin MCP services a workspace sees.
func (a *MCPServers) Services(ctx context.Context, tenantID uint64) []*types.MCPService {
	servers := a.snapshot()
	if len(servers) == 0 {
		return nil
	}
	enabled := a.tenancy.EnabledFilter(ctx, tenantID)
	var out []*types.MCPService
	for _, s := range servers {
		if !enabled(manifest.PointMCPServers, s.qualifiedID) {
			continue
		}
		out = append(out, a.service(ctx, tenantID, s))
	}
	return out
}

// service builds one workspace's view of a plugin MCP server. A server whose
// headers cannot be filled (the workspace has not configured the plugin yet)
// is listed disabled, so agents skip it and the UI can say why.
func (a *MCPServers) service(ctx context.Context, tenantID uint64, s mcpServer) *types.MCPService {
	svc := &types.MCPService{
		ID:            serviceID(tenantID, s.qualifiedID),
		TenantID:      tenantID,
		Name:          s.contrib.Name.Default,
		Description:   s.contrib.Description.Default,
		Enabled:       true,
		IsBuiltin:     true,
		PluginID:      s.manifest.ID,
		PluginVersion: s.manifest.Version,
		PluginServer:  s.contrib.ID,
		ToolViews:     toolViews(s.contrib),
		CreatedAt:     s.activatedAt,
		UpdatedAt:     s.activatedAt,
	}
	if s.contrib.ServedByPlugin() {
		// Configuration travels with each call, so the connection never
		// goes stale when it changes.
		svc.TransportType = types.MCPTransportPlugin
		return svc
	}
	url := s.contrib.MCP.URL
	svc.URL = &url
	svc.TransportType = types.MCPTransportType(s.contrib.MCP.Transport)
	if svc.TransportType == "" {
		svc.TransportType = types.MCPTransportHTTPStreamable
	}
	headers, updated, err := a.headers(ctx, tenantID, s)
	if err != nil {
		logger.Debugf(ctx, "[plugin] MCP server %s in tenant %d: %v", s.qualifiedID, tenantID, err)
		svc.Enabled = false
		svc.PluginError = err.Error()
		return svc
	}
	svc.Headers = headers
	if updated.After(svc.UpdatedAt) {
		// The MCP manager reconnects when UpdatedAt moves, so a changed
		// credential takes effect on the next call.
		svc.UpdatedAt = updated
	}
	return svc
}

func (a *MCPServers) headers(ctx context.Context, tenantID uint64, s mcpServer) (types.MCPHeaders, time.Time, error) {
	var (
		tenantCfg, systemCfg map[string]any
		updated              time.Time
		loadErr              error
	)
	lookup := func(scope, key string) (string, bool) {
		var cfg map[string]any
		switch scope {
		case manifest.ScopeConfig:
			if tenantCfg == nil && loadErr == nil {
				var at time.Time
				tenantCfg, at, loadErr = a.tenancy.OpenConfig(ctx, tenantID, s.manifest.ID)
				updated = later(updated, at)
			}
			cfg = tenantCfg
		case manifest.ScopeSystem:
			if systemCfg == nil && loadErr == nil {
				var at time.Time
				systemCfg, at, loadErr = install.OpenSystemConfig(ctx, a.plugins, s.manifest)
				updated = later(updated, at)
			}
			cfg = systemCfg
		}
		v, ok := cfg[key]
		if !ok || v == nil {
			return "", false
		}
		return fmt.Sprint(v), true
	}
	out := types.MCPHeaders{}
	for name, value := range s.contrib.MCP.Headers {
		expanded, err := manifest.ExpandTemplate(value, lookup)
		if loadErr != nil {
			return nil, updated, loadErr
		}
		if err != nil {
			return nil, updated, err
		}
		out[name] = expanded
	}
	return out, updated, nil
}

// toolViews are a contribution's result views as the chat reads them.
func toolViews(c manifest.Contribution) map[string]json.RawMessage {
	if len(c.ToolViews) == 0 {
		return nil
	}
	out := make(map[string]json.RawMessage, len(c.ToolViews))
	for name, v := range c.ToolViews {
		if b, err := json.Marshal(v); err == nil {
			out[name] = b
		}
	}
	return out
}

func later(a, b time.Time) time.Time {
	if b.After(a) {
		return b
	}
	return a
}

// find returns the plugin MCP service with an ID in a workspace.
func (a *MCPServers) find(ctx context.Context, tenantID uint64, id string) *types.MCPService {
	for _, svc := range a.Services(ctx, tenantID) {
		if svc.ID == id {
			return svc
		}
	}
	return nil
}

// owns reports whether an ID belongs to a plugin MCP server in a workspace,
// enabled or not.
func (a *MCPServers) owns(tenantID uint64, id string) bool {
	for _, s := range a.snapshot() {
		if serviceID(tenantID, s.qualifiedID) == id {
			return true
		}
	}
	return false
}

// Repository wraps the MCP service repository so plugin MCP services are
// listed and resolved alongside stored ones and cannot be modified.
func (a *MCPServers) Repository(inner interfaces.MCPServiceRepository) interfaces.MCPServiceRepository {
	return &mcpRepository{MCPServiceRepository: inner, plugins: a}
}

type mcpRepository struct {
	interfaces.MCPServiceRepository
	plugins *MCPServers
}

func (r *mcpRepository) GetByID(ctx context.Context, tenantID uint64, id string) (*types.MCPService, error) {
	if svc := r.plugins.find(ctx, tenantID, id); svc != nil {
		return svc, nil
	}
	return r.MCPServiceRepository.GetByID(ctx, tenantID, id)
}

func (r *mcpRepository) List(ctx context.Context, tenantID uint64) ([]*types.MCPService, error) {
	stored, err := r.MCPServiceRepository.List(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	return append(stored, r.plugins.Services(ctx, tenantID)...), nil
}

func (r *mcpRepository) ListEnabled(ctx context.Context, tenantID uint64) ([]*types.MCPService, error) {
	stored, err := r.MCPServiceRepository.ListEnabled(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	for _, svc := range r.plugins.Services(ctx, tenantID) {
		if svc.Enabled {
			stored = append(stored, svc)
		}
	}
	return stored, nil
}

func (r *mcpRepository) ListByIDs(ctx context.Context, tenantID uint64, ids []string) ([]*types.MCPService, error) {
	stored, err := r.MCPServiceRepository.ListByIDs(ctx, tenantID, ids)
	if err != nil {
		return nil, err
	}
	want := make(map[string]bool, len(ids))
	for _, id := range ids {
		want[id] = true
	}
	for _, svc := range r.plugins.Services(ctx, tenantID) {
		if want[svc.ID] {
			stored = append(stored, svc)
		}
	}
	return stored, nil
}

func (r *mcpRepository) Update(ctx context.Context, svc *types.MCPService) error {
	if r.plugins.owns(svc.TenantID, svc.ID) {
		return ErrPluginService
	}
	return r.MCPServiceRepository.Update(ctx, svc)
}

func (r *mcpRepository) Delete(ctx context.Context, tenantID uint64, id string) error {
	if r.plugins.owns(tenantID, id) {
		return ErrPluginService
	}
	return r.MCPServiceRepository.Delete(ctx, tenantID, id)
}
