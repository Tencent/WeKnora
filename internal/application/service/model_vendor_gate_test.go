package service

import (
	"context"
	"errors"
	"testing"

	modelruntime "github.com/Tencent/WeKnora/internal/models/runtime"
	"github.com/Tencent/WeKnora/internal/plugin/manifest"
	pluginregistry "github.com/Tencent/WeKnora/internal/plugin/registry"
	plugintenancy "github.com/Tencent/WeKnora/internal/plugin/tenancy"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/utils"
)

// switchRepo holds tenant plugin switches in memory.
type switchRepo struct{ on map[uint64]bool }

func (r *switchRepo) List(_ context.Context, tenantID uint64) ([]types.PluginTenantSetting, error) {
	if enabled, ok := r.on[tenantID]; ok {
		return []types.PluginTenantSetting{{TenantID: tenantID, PluginID: "acme.ai", Enabled: enabled}}, nil
	}
	return nil, nil
}

func (r *switchRepo) Get(ctx context.Context, tenantID uint64, _ string) (*types.PluginTenantSetting, error) {
	rows, _ := r.List(ctx, tenantID)
	if len(rows) == 0 {
		return nil, nil
	}
	return &rows[0], nil
}

func (r *switchRepo) Upsert(context.Context, *types.PluginTenantSetting, ...string) error { return nil }

// Models of a plugin vendor stop working in a workspace that turned the
// plugin off, however the row spells the vendor; a built-in model follows
// the switch of the workspace using it; a row naming no vendor is never
// taken for the plugin's.
func TestModelsOfATurnedOffPluginVendorAreRefused(t *testing.T) {
	def := `{"name":"ACME AI","base_url":"https://api.acme.example/v1","url_patterns":["acme.example"],
		"model_types":["chat"]}`
	if err := modelruntime.RegisterPlugin("acme.ai/acme", []byte(def), t.TempDir()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { modelruntime.Unregister("acme.ai/acme") })
	utils.SetSSRFWhitelistFromRaw("api.acme.example")
	t.Cleanup(func() { utils.SetSSRFWhitelistFromRaw("") })
	reg := pluginregistry.New()
	if err := reg.Register(&manifest.Manifest{
		SchemaVersion: manifest.SchemaVersion, ID: "acme.ai", Version: "1.0.0", Name: manifest.Text("ACME", nil),
		Publisher: manifest.Publisher{ID: "acme"}, APIVersion: "weknora.plugin/v1",
		Runtime: manifest.Runtime{Type: manifest.RuntimeDeclarative},
		Contributes: manifest.Contributions{manifest.PointModelVendors: {{
			ID: "acme", Name: manifest.Text("ACME AI", nil), Path: "vendors/acme.yaml",
		}}},
	}); err != nil {
		t.Fatal(err)
	}
	switches := &switchRepo{on: map[uint64]bool{1: true, 2: false}}

	chatModel := func(owner uint64, builtin bool, provider, baseURL string) error {
		model := &types.Model{
			ID: "m", TenantID: owner, IsBuiltin: builtin, Name: "acme-large", Type: types.ModelTypeKnowledgeQA,
			Source: types.ModelSourceRemote, Status: types.ModelStatusActive,
			Parameters: types.ModelParameters{Provider: provider, BaseURL: baseURL},
		}
		svc := NewModelService(&stubModelRepoForDelete{model: model}, nil, nil, nil, nil, nil)
		svc.(*modelService).SetPluginGate(plugintenancy.NewService(reg, switches))
		caller := owner
		if builtin {
			caller = 3 // never switched it on
		}
		ctx := context.WithValue(context.Background(), types.TenantIDContextKey, caller)
		_, err := svc.GetChatModel(ctx, "m")
		return err
	}

	for _, provider := range []string{"acme.ai/acme", "ACME.AI/Acme", " acme.ai/acme"} {
		if err := chatModel(2, false, provider, ""); !errors.Is(err, plugintenancy.ErrPluginOff) {
			t.Errorf("turned off, provider %q: %v", provider, err)
		}
		if err := chatModel(1, false, provider, ""); err != nil {
			t.Errorf("turned on, provider %q: %v", provider, err)
		}
	}
	if err := chatModel(1, true, "acme.ai/acme", ""); !errors.Is(err, plugintenancy.ErrPluginOff) {
		t.Errorf("a built-in model in a workspace that never turned the plugin on: %v", err)
	}
	if err := chatModel(2, false, "", "https://api.acme.example/v1"); err != nil {
		t.Errorf("a row naming no vendor was taken for the plugin's: %v", err)
	}
}
