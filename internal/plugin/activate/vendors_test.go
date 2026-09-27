package activate

import (
	"context"
	"strings"
	"testing"

	modelruntime "github.com/Tencent/WeKnora/internal/models/runtime"
	"github.com/Tencent/WeKnora/internal/plugin/pkg"
	"github.com/Tencent/WeKnora/internal/plugin/plugintest"
	"github.com/Tencent/WeKnora/internal/plugin/reconcile"
	"github.com/Tencent/WeKnora/internal/plugin/registry"
	"github.com/Tencent/WeKnora/internal/types"
)

const vendorManifest = `schemaVersion: 1
id: acme.ai
version: 1.0.0
name: { en-US: ACME AI }
publisher: { id: acme }
runtime: { type: declarative }
contributes:
  modelVendors:
    - id: acme
      name: { en-US: ACME AI, zh-CN: ACME 智能 }
      path: vendors/acme.yaml
`

func vendorPackage(t *testing.T, vendorYAML string) []byte {
	return plugintest.Zip(t, map[string]string{
		"plugin.yaml":       vendorManifest,
		"vendors/acme.yaml": vendorYAML,
		"vendors/acme.svg":  `<svg xmlns="http://www.w3.org/2000/svg"></svg>`,
	})
}

func TestModelVendorsFollowThePlugin(t *testing.T) {
	ctx := context.Background()
	rt := modelruntime.New()
	vendors := &ModelVendors{rt: rt, registered: newRegistrations()}
	repo, store := plugintest.NewMemRepo(), &plugintest.MemStore{}
	r := reconcile.New(reconcile.Options{
		Repo: repo, Store: store, Registry: registry.New(), CacheDir: t.TempDir(),
		Activators: []reconcile.Activator{vendors},
	})
	plugintest.Install(t, repo, store, vendorPackage(t, `
base_url: https://api.acme.example/v1
icon: acme.svg
model_types: [chat, embedding]
models:
  - { id: acme-large, context_window: 128000 }
`), types.PluginStateEnabled)
	if err := r.Reconcile(ctx); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	v, ok := rt.Get("acme.ai/acme")
	if !ok {
		t.Fatal("vendor not registered")
	}
	if v.Name != "ACME AI" || v.Names["zh-CN"] != "ACME 智能" || len(v.Icon) == 0 {
		t.Fatalf("vendor = name %q names %v icon %d bytes", v.Name, v.Names, len(v.Icon))
	}

	row, _ := repo.GetPlugin(ctx, "acme.ai")
	row.DesiredState = types.PluginStateDisabled
	_ = repo.SavePlugin(ctx, row)
	_ = r.Reconcile(ctx)
	if _, ok := rt.Get("acme.ai/acme"); ok {
		t.Fatal("disabling the plugin must remove its vendor")
	}
}

func TestCheckModelVendorsRejectsBrokenDefinitions(t *testing.T) {
	for name, def := range map[string]string{
		"unknown key": "base_url: https://x\nnmae: typo\n",
		"deploy key":  "api_key: sk-123\n",
		"bad api":     "api: carrier-pigeon\n",
		"bad icon":    "icon: ../../etc/passwd\n",
	} {
		p, err := pkg.Open(vendorPackage(t, def))
		if err != nil {
			t.Fatalf("%s: Open: %v", name, err)
		}
		if err := CheckModelVendors(p); err == nil || !strings.Contains(err.Error(), "model vendor acme") {
			t.Errorf("%s: want a vendor error, got %v", name, err)
		}
	}
	p, _ := pkg.Open(vendorPackage(t, "base_url: https://api.acme.example/v1\nicon: acme.svg\n"))
	if err := CheckModelVendors(p); err != nil {
		t.Fatalf("valid vendor rejected: %v", err)
	}
}

// An upgrade whose vendor definition is broken leaves the running version's
// vendor in place; a good one replaces it and drops the vendors it no
// longer declares.
func TestModelVendorUpgradeIsAllOrNothing(t *testing.T) {
	ctx := context.Background()
	rt := modelruntime.New()
	vendors := &ModelVendors{rt: rt, registered: newRegistrations()}
	reg, repo, store := registry.New(), plugintest.NewMemRepo(), &plugintest.MemStore{}
	r := reconcile.New(reconcile.Options{
		Repo: repo, Store: store, Registry: reg, CacheDir: t.TempDir(),
		Activators: []reconcile.Activator{vendors},
	})
	withVersion := func(version, manifestTail, vendorYAML string) []byte {
		m := strings.Replace(vendorManifest, "version: 1.0.0", "version: "+version, 1) + manifestTail
		return plugintest.Zip(t, map[string]string{
			"plugin.yaml": m, "vendors/acme.yaml": vendorYAML, "vendors/beta.yaml": vendorYAML,
		})
	}
	beta := "    - { id: beta, name: { en-US: Beta }, path: vendors/beta.yaml }\n"
	plugintest.Install(t, repo, store, withVersion("1.0.0", beta, "base_url: https://v1.example/v1\n"),
		types.PluginStateEnabled)
	if err := r.Reconcile(ctx); err != nil {
		t.Fatal(err)
	}

	plugintest.Install(t, repo, store, withVersion("1.1.0", beta, "base_url: https://v2.example/v1\nnmae: typo\n"),
		types.PluginStateEnabled)
	if err := r.Reconcile(ctx); err == nil {
		t.Fatal("want the broken definition to fail the upgrade")
	}
	for _, id := range []string{"acme.ai/acme", "acme.ai/beta"} {
		if v, ok := rt.Get(id); !ok || v.GetDefaultURL(types.ModelTypeKnowledgeQA) != "https://v1.example/v1" {
			t.Fatalf("%s after a failed upgrade: %v", id, ok)
		}
	}

	plugintest.Install(t, repo, store, withVersion("1.2.0", "", "base_url: https://v3.example/v1\n"),
		types.PluginStateEnabled)
	if err := r.Reconcile(ctx); err != nil {
		t.Fatal(err)
	}
	if v, ok := rt.Get("acme.ai/acme"); !ok || v.GetDefaultURL(types.ModelTypeKnowledgeQA) != "https://v3.example/v1" {
		t.Fatalf("acme after the upgrade: %v", ok)
	}
	if _, ok := rt.Get("acme.ai/beta"); ok {
		t.Fatal("a vendor the new version dropped must go")
	}
}
