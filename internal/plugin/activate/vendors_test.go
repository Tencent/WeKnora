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
	vendors := &ModelVendors{rt: rt, registered: map[string][]string{}}
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
