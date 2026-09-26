package tenancy

import (
	"context"
	"errors"
	"testing"

	"github.com/Tencent/WeKnora/internal/plugin/manifest"
	"github.com/Tencent/WeKnora/internal/plugin/registry"
	"github.com/Tencent/WeKnora/internal/types"
)

type memRepo struct {
	rows map[uint64]map[string]types.PluginTenantSetting
	err  error
}

func (m *memRepo) List(_ context.Context, tenantID uint64) ([]types.PluginTenantSetting, error) {
	if m.err != nil {
		return nil, m.err
	}
	var out []types.PluginTenantSetting
	for _, r := range m.rows[tenantID] {
		out = append(out, r)
	}
	return out, nil
}

func (m *memRepo) Get(_ context.Context, tenantID uint64, pluginID string) (*types.PluginTenantSetting, error) {
	if m.err != nil {
		return nil, m.err
	}
	if r, ok := m.rows[tenantID][pluginID]; ok {
		return &r, nil
	}
	return nil, nil
}

func (m *memRepo) Upsert(_ context.Context, s *types.PluginTenantSetting, columns ...string) error {
	if m.rows == nil {
		m.rows = map[uint64]map[string]types.PluginTenantSetting{}
	}
	if m.rows[s.TenantID] == nil {
		m.rows[s.TenantID] = map[string]types.PluginTenantSetting{}
	}
	row, exists := m.rows[s.TenantID][s.PluginID]
	if !exists || len(columns) == 0 {
		m.rows[s.TenantID][s.PluginID] = *s
		return nil
	}
	for _, c := range columns {
		switch c {
		case "enabled":
			row.Enabled = s.Enabled
		case "config":
			row.Config = s.Config
		}
	}
	row.UpdatedBy, row.UpdatedAt = s.UpdatedBy, s.UpdatedAt
	m.rows[s.TenantID][s.PluginID] = row
	return nil
}

func newService(t *testing.T) (*Service, *memRepo) {
	t.Helper()
	reg := registry.New()
	for _, m := range []*manifest.Manifest{
		builtin("weknora.feishu", false, manifest.Contributions{
			manifest.PointConnectors: {{ID: "feishu", Name: manifest.Text("Feishu", nil), Aliases: []string{"feishu"}}},
			manifest.PointIMChannels: {{ID: "feishu", Name: manifest.Text("Feishu", nil), Aliases: []string{"feishu"}}},
		}),
		builtin("weknora.agent-tools", true, manifest.Contributions{
			manifest.PointTools: {
				{ID: "thinking", Name: manifest.Text("Thinking", nil), Aliases: []string{"thinking"}},
			},
		}),
	} {
		if err := reg.Register(m); err != nil {
			t.Fatal(err)
		}
	}
	repo := &memRepo{}
	return NewService(reg, repo), repo
}

func builtin(id string, required bool, c manifest.Contributions) *manifest.Manifest {
	return &manifest.Manifest{
		SchemaVersion: manifest.SchemaVersion, ID: id, Version: "1.0.0", Name: manifest.Text(id, nil),
		Publisher: manifest.Publisher{ID: manifest.BuiltinPublisher}, Builtin: true, Required: required,
		Runtime: manifest.Runtime{Type: manifest.RuntimeBuiltin}, Contributes: c,
	}
}

func TestDisablingHidesEveryContributionOfThePlugin(t *testing.T) {
	s, _ := newService(t)
	ctx := context.Background()
	if !s.ContributionEnabled(ctx, 1, manifest.PointConnectors, "feishu") {
		t.Fatal("plugins start enabled")
	}
	if err := s.SetEnabled(ctx, 1, "weknora.feishu", false, "u1"); err != nil {
		t.Fatal(err)
	}
	filter := s.EnabledFilter(ctx, 1)
	if filter(manifest.PointConnectors, "feishu") || filter(manifest.PointIMChannels, "weknora.feishu/feishu") {
		t.Fatal("both contributions of a disabled plugin must be off, by alias and qualified ID")
	}
	if !s.ContributionEnabled(ctx, 2, manifest.PointConnectors, "feishu") {
		t.Fatal("another tenant is unaffected")
	}
	if !filter(manifest.PointConnectors, "unknown-type") {
		t.Fatal("unknown contributions count as enabled")
	}

	list, err := s.List(ctx, 1)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range list {
		if p.Manifest.ID == "weknora.feishu" && (p.Enabled || p.UpdatedAt.IsZero()) {
			t.Fatalf("list should show the switch: %+v", p)
		}
	}

	if err := s.SetEnabled(ctx, 1, "weknora.feishu", true, "u1"); err != nil {
		t.Fatal(err)
	}
	if !s.ContributionEnabled(ctx, 1, manifest.PointConnectors, "feishu") {
		t.Fatal("re-enabling restores the contribution")
	}
}

func TestSetEnabledRejectsRequiredAndUnknown(t *testing.T) {
	s, _ := newService(t)
	ctx := context.Background()
	if err := s.SetEnabled(ctx, 1, "weknora.agent-tools", false, "u1"); !errors.Is(err, ErrRequiredPlugin) {
		t.Fatalf("want ErrRequiredPlugin, got %v", err)
	}
	if err := s.SetEnabled(ctx, 1, "weknora.agent-tools", true, "u1"); err != nil {
		t.Fatalf("enabling a required plugin is a no-op, got %v", err)
	}
	if err := s.SetEnabled(ctx, 1, "acme.nope", false, "u1"); !errors.Is(err, ErrUnknownPlugin) {
		t.Fatalf("want ErrUnknownPlugin, got %v", err)
	}
}

func TestEnabledFilterFailsOpen(t *testing.T) {
	s, repo := newService(t)
	ctx := context.Background()
	if err := s.SetEnabled(ctx, 1, "weknora.feishu", false, "u1"); err != nil {
		t.Fatal(err)
	}
	repo.err = errors.New("db down")
	if !s.ContributionEnabled(ctx, 1, manifest.PointConnectors, "feishu") {
		t.Fatal("unreadable switches must not hide integrations")
	}
}

func TestInstalledPluginsWaitForTenantOptIn(t *testing.T) {
	s, _ := newService(t)
	ctx := context.Background()
	kit := &manifest.Manifest{
		SchemaVersion: manifest.SchemaVersion, ID: "acme.kit", Version: "1.0.0", Name: manifest.Text("Kit", nil),
		Publisher: manifest.Publisher{ID: "acme"}, Runtime: manifest.Runtime{Type: manifest.RuntimeDeclarative},
		Contributes: manifest.Contributions{
			manifest.PointMCPServers: {{
				ID: "search", Name: manifest.Text("Search", nil),
				MCP: &manifest.MCPServer{URL: "https://mcp.acme.example/mcp"},
			}},
		},
	}
	if err := s.registry.Register(kit); err != nil {
		t.Fatal(err)
	}
	if s.ContributionEnabled(ctx, 1, manifest.PointMCPServers, "acme.kit/search") {
		t.Fatal("an installed plugin must start disabled in every tenant")
	}
	if !s.ContributionEnabled(ctx, 1, manifest.PointConnectors, "feishu") {
		t.Fatal("builtins start enabled")
	}
	list, _ := s.List(ctx, 1)
	for _, p := range list {
		if p.Manifest.ID == "acme.kit" && p.Enabled {
			t.Fatal("List must report the installed plugin as disabled")
		}
	}
	if err := s.SetEnabled(ctx, 1, "acme.kit", true, "u1"); err != nil {
		t.Fatal(err)
	}
	if !s.ContributionEnabled(ctx, 1, manifest.PointMCPServers, "acme.kit/search") ||
		s.ContributionEnabled(ctx, 2, manifest.PointMCPServers, "acme.kit/search") {
		t.Fatal("opting in applies to that tenant only")
	}
}

func TestTenantConfigRoundTrip(t *testing.T) {
	s, _ := newService(t)
	ctx := context.Background()
	kit := &manifest.Manifest{
		SchemaVersion: manifest.SchemaVersion, ID: "acme.kit", Version: "1.0.0", Name: manifest.Text("Kit", nil),
		Publisher: manifest.Publisher{ID: "acme"}, Runtime: manifest.Runtime{Type: manifest.RuntimeDeclarative},
		Config: manifest.ConfigSchemas{
			TenantSchema: []byte(`{"type":"object","required":["api_key"],` +
				`"properties":{"api_key":{"type":"string","x-secret":true},"region":{"type":"string"}}}`),
		},
		Contributes: manifest.Contributions{
			manifest.PointMCPServers: {{
				ID: "search", Name: manifest.Text("Search", nil),
				MCP: &manifest.MCPServer{URL: "https://mcp.acme.example/mcp"},
			}},
		},
	}
	if err := s.registry.Register(kit); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Config(ctx, 1, "weknora.feishu"); !errors.Is(err, ErrNoTenantConfig) {
		t.Fatalf("want ErrNoTenantConfig, got %v", err)
	}
	if _, err := s.SetConfig(ctx, 1, "acme.kit", map[string]any{"region": "eu"}, "u1"); err == nil {
		t.Fatal("a missing required secret must be rejected")
	}
	got, err := s.SetConfig(ctx, 1, "acme.kit", map[string]any{"api_key": "k-1", "region": "eu"}, "u1")
	if err != nil || got.Values["api_key"] != "***" || got.Values["region"] != "eu" {
		t.Fatalf("SetConfig = %+v, %v", got, err)
	}
	// Saving config must not flip the switch, and flipping the switch must
	// keep the config.
	if s.ContributionEnabled(ctx, 1, manifest.PointMCPServers, "acme.kit/search") {
		t.Fatal("configuring must not enable the plugin")
	}
	if err := s.SetEnabled(ctx, 1, "acme.kit", true, "u1"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SetConfig(ctx, 1, "acme.kit", map[string]any{"api_key": "***", "region": "us"}, "u1"); err != nil {
		t.Fatal(err)
	}
	values, _, err := s.OpenConfig(ctx, 1, "acme.kit")
	if err != nil || values["api_key"] != "k-1" || values["region"] != "us" {
		t.Fatalf("OpenConfig = %v, %v", values, err)
	}
	if !s.ContributionEnabled(ctx, 1, manifest.PointMCPServers, "acme.kit/search") {
		t.Fatal("saving config must keep the plugin enabled")
	}
}
