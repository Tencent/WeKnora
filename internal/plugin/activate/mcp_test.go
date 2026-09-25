package activate

import (
	"context"
	"errors"
	"testing"

	"github.com/Tencent/WeKnora/internal/plugin/install"
	"github.com/Tencent/WeKnora/internal/plugin/plugintest"
	"github.com/Tencent/WeKnora/internal/plugin/reconcile"
	"github.com/Tencent/WeKnora/internal/plugin/registry"
	"github.com/Tencent/WeKnora/internal/plugin/tenancy"
	"github.com/Tencent/WeKnora/internal/types"
)

const searchManifest = `schemaVersion: 1
id: acme.search
version: 1.0.0
name: { en-US: ACME Search }
publisher: { id: acme }
runtime: { type: declarative }
config: { tenant: tenant.yaml, system: system.yaml }
contributes:
  mcpServers:
    - id: search
      name: ACME Search
      mcp:
        url: https://mcp.acme.example/mcp
        headers:
          Authorization: Bearer ${config.api_key}
          X-Region: ${system.region}
`

type env struct {
	ctx       context.Context
	mcp       *MCPServers
	tenancy   *tenancy.Service
	installer *install.Service
	stored    *fakeMCPRepo
	repo      *mcpRepository
}

// fakeMCPRepo stores one ordinary service per tenant.
type fakeMCPRepo struct{ updates int }

func (f *fakeMCPRepo) own(tenantID uint64) *types.MCPService {
	return &types.MCPService{ID: "stored", TenantID: tenantID, Name: "Mine", Enabled: true}
}

func (f *fakeMCPRepo) Create(context.Context, *types.MCPService) error { return nil }
func (f *fakeMCPRepo) GetByID(_ context.Context, t uint64, id string) (*types.MCPService, error) {
	if id == "stored" {
		return f.own(t), nil
	}
	return nil, nil
}

func (f *fakeMCPRepo) List(_ context.Context, t uint64) ([]*types.MCPService, error) {
	return []*types.MCPService{f.own(t)}, nil
}

func (f *fakeMCPRepo) ListEnabled(_ context.Context, t uint64) ([]*types.MCPService, error) {
	return []*types.MCPService{f.own(t)}, nil
}

func (f *fakeMCPRepo) ListByIDs(_ context.Context, t uint64, ids []string) ([]*types.MCPService, error) {
	for _, id := range ids {
		if id == "stored" {
			return []*types.MCPService{f.own(t)}, nil
		}
	}
	return nil, nil
}
func (f *fakeMCPRepo) Update(context.Context, *types.MCPService) error { f.updates++; return nil }
func (f *fakeMCPRepo) Delete(context.Context, uint64, string) error    { return nil }

func setup(t *testing.T) *env {
	t.Helper()
	ctx := context.Background()
	plugins, store, reg := plugintest.NewMemRepo(), &plugintest.MemStore{}, registry.New()
	ten := tenancy.NewService(reg, &plugintest.MemTenantSettings{})
	mcp := NewMCPServers()
	mcp.Bind(ten, plugins)
	r := reconcile.New(reconcile.Options{
		Repo: plugins, Store: store, Registry: reg, CacheDir: t.TempDir(), Activators: []reconcile.Activator{mcp},
	})
	installer := install.NewService(plugins, store, r, "")
	pkg := plugintest.Zip(t, map[string]string{
		"plugin.yaml": searchManifest,
		"tenant.yaml": "type: object\nproperties: { api_key: { type: string, x-secret: true } }\nrequired: [api_key]\n",
		"system.yaml": "type: object\nproperties: { region: { type: string } }\n",
	})
	if _, err := installer.Install(ctx, install.Request{Data: pkg}); err != nil {
		t.Fatal(err)
	}
	stored := &fakeMCPRepo{}
	return &env{
		ctx: ctx, mcp: mcp, tenancy: ten, installer: installer, stored: stored,
		repo: mcp.Repository(stored).(*mcpRepository),
	}
}

func TestPluginMCPServiceFollowsTenantSwitchAndConfig(t *testing.T) {
	e := setup(t)
	list, _ := e.repo.List(e.ctx, 1)
	if len(list) != 1 {
		t.Fatalf("a plugin the tenant has not enabled must stay hidden, got %d services", len(list))
	}
	if err := e.tenancy.SetEnabled(e.ctx, 1, "acme.search", true, "u"); err != nil {
		t.Fatal(err)
	}

	list, _ = e.repo.List(e.ctx, 1)
	if len(list) != 2 {
		t.Fatalf("want stored + plugin service, got %d", len(list))
	}
	svc := list[1]
	if svc.PluginID != "acme.search" || !svc.IsBuiltin || svc.Enabled || svc.PluginError == "" {
		t.Fatalf("an unconfigured plugin service must be listed disabled: %+v", svc)
	}
	enabled, _ := e.repo.ListEnabled(e.ctx, 1)
	if len(enabled) != 1 {
		t.Fatal("agents must not get an unconfigured plugin service")
	}

	if _, err := e.installer.SetSystemConfig(e.ctx, "acme.search", map[string]any{"region": "eu"}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.tenancy.SetConfig(e.ctx, 1, "acme.search", map[string]any{"api_key": "k-1"}, "u"); err != nil {
		t.Fatal(err)
	}
	got, err := e.repo.GetByID(e.ctx, 1, svc.ID)
	if err != nil || got == nil || !got.Enabled {
		t.Fatalf("GetByID = %+v, %v", got, err)
	}
	if got.Headers["Authorization"] != "Bearer k-1" || got.Headers["X-Region"] != "eu" {
		t.Fatalf("headers = %v", got.Headers)
	}
	if !got.UpdatedAt.After(svc.UpdatedAt) {
		t.Fatal("changing configuration must move UpdatedAt so the MCP client reconnects")
	}
	byIDs, _ := e.repo.ListByIDs(e.ctx, 1, []string{"stored", svc.ID})
	if len(byIDs) != 2 {
		t.Fatalf("ListByIDs = %d", len(byIDs))
	}

	// Each tenant has its own service ID and configuration.
	if err := e.tenancy.SetEnabled(e.ctx, 2, "acme.search", true, "u"); err != nil {
		t.Fatal(err)
	}
	other, _ := e.repo.List(e.ctx, 2)
	if other[1].ID == svc.ID || other[1].Enabled {
		t.Fatalf("tenant 2 = %+v", other[1])
	}

	if err := e.repo.Update(e.ctx, got); !errors.Is(err, ErrPluginService) {
		t.Fatalf("Update = %v", err)
	}
	if err := e.repo.Delete(e.ctx, 1, svc.ID); !errors.Is(err, ErrPluginService) {
		t.Fatalf("Delete = %v", err)
	}

	if _, err := e.installer.SetEnabled(e.ctx, "acme.search", false); err != nil {
		t.Fatal(err)
	}
	if list, _ := e.repo.List(e.ctx, 1); len(list) != 1 {
		t.Fatal("disabling the plugin platform-wide must remove its services")
	}
}
