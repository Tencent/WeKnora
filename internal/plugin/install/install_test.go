package install

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/plugin/plugintest"
	"github.com/Tencent/WeKnora/internal/plugin/reconcile"
	"github.com/Tencent/WeKnora/internal/plugin/registry"
	"github.com/Tencent/WeKnora/internal/types"
)

func newService(t *testing.T) (*Service, *plugintest.MemRepo, *plugintest.MemStore, *registry.Registry) {
	t.Helper()
	repo, store, reg := plugintest.NewMemRepo(), &plugintest.MemStore{}, registry.New()
	r := reconcile.New(reconcile.Options{Repo: repo, Store: store, Registry: reg, CacheDir: t.TempDir()})
	return NewService(repo, store, r, "0.5.0"), repo, store, reg
}

func TestInstallUpgradeRollbackUninstall(t *testing.T) {
	ctx := context.Background()
	s, repo, store, reg := newService(t)

	v1 := plugintest.KitPackage(t, "1.0.0")
	preview, err := s.Inspect(ctx, v1)
	if err != nil || preview.Change != ChangeInstall || preview.Manifest.ID != "acme.kit" {
		t.Fatalf("Inspect = %+v, %v", preview, err)
	}
	view, err := s.Install(ctx, Request{
		Data: v1, Source: Source{Kind: "upload"},
		ExpectedDigest: preview.Digest, UserID: "admin",
	})
	if err != nil {
		t.Fatalf("Install: %v", err)
	}
	if view.ActiveVersion != "1.0.0" || view.DesiredState != types.PluginStateEnabled ||
		view.Node == nil || view.Node.State != reconcile.StateReady {
		t.Fatalf("view = %+v node=%+v", view.InstalledPlugin, view.Node)
	}
	if _, ok := reg.Plugin("acme.kit"); !ok {
		t.Fatal("install must load the plugin on this node")
	}

	v2 := plugintest.KitPackage(t, "1.1.0")
	if p, _ := s.Inspect(ctx, v2); p.Change != ChangeUpgrade || p.InstalledVersion != "1.0.0" {
		t.Fatalf("upgrade preview = %+v", p)
	}
	if _, err := s.Install(ctx, Request{Data: v2, ExpectedDigest: preview.Digest}); !isInvalid(err) {
		t.Fatalf("a digest other than the reviewed one must be refused, got %v", err)
	}
	if _, err := s.Install(ctx, Request{Data: v2}); err != nil {
		t.Fatalf("upgrade: %v", err)
	}
	view, err = s.Activate(ctx, "acme.kit", "1.0.0")
	if err != nil || view.ActiveVersion != "1.0.0" || len(view.Versions) != 2 {
		t.Fatalf("rollback = %+v, %v", view, err)
	}
	if m, _ := reg.Plugin("acme.kit"); m.Version != "1.0.0" {
		t.Fatalf("registry runs %s after rollback", m.Version)
	}

	if view, err = s.SetEnabled(ctx, "acme.kit", false); err != nil || view.Node != nil {
		t.Fatalf("disable = %+v, %v", view, err)
	}
	if _, ok := reg.Plugin("acme.kit"); ok {
		t.Fatal("a disabled plugin must be unloaded")
	}

	if err := s.Uninstall(ctx, "acme.kit"); err != nil {
		t.Fatalf("Uninstall: %v", err)
	}
	if row, _ := repo.GetPlugin(ctx, "acme.kit"); row != nil {
		t.Fatal("row survived uninstall")
	}
	if len(store.Blobs) != 0 {
		t.Fatalf("packages survived uninstall: %d", len(store.Blobs))
	}
	if _, err := s.Get(ctx, "acme.kit"); !errors.Is(err, ErrNotInstalled) {
		t.Fatalf("Get after uninstall = %v", err)
	}
}

func TestInstallRejects(t *testing.T) {
	ctx := context.Background()
	s, _, _, _ := newService(t)
	if _, err := s.Install(ctx, Request{Data: []byte("nope")}); !isInvalid(err) {
		t.Fatalf("garbage must be invalid, got %v", err)
	}

	pkg := plugintest.KitPackage(t, "1.0.0")
	if _, err := s.Install(ctx, Request{Data: pkg}); err != nil {
		t.Fatal(err)
	}
	// Same version, different bytes.
	other := plugintest.KitPackageWith(t, "1.0.0", "changed")
	if _, err := s.Install(ctx, Request{Data: other}); !isInvalid(err) ||
		!strings.Contains(err.Error(), "new version") {
		t.Fatalf("want same-version conflict, got %v", err)
	}
	// Reinstalling the identical package is fine.
	if p, _ := s.Inspect(ctx, pkg); p.Change != ChangeReinstall {
		t.Fatalf("change = %s", p.Change)
	}
	if _, err := s.Install(ctx, Request{Data: pkg}); err != nil {
		t.Fatalf("reinstall: %v", err)
	}

	old := NewService(plugintest.NewMemRepo(), &plugintest.MemStore{}, nil, "0.1.0")
	engines := plugintest.KitPackageWith(t, "2.0.0", "", "engines: { weknora: \">=0.2.0\" }")
	if _, err := old.Inspect(ctx, engines); !isInvalid(err) || !strings.Contains(err.Error(), "needs WeKnora") {
		t.Fatalf("want engines error, got %v", err)
	}
}

func TestFetchURLRefusesPrivateAddresses(t *testing.T) {
	s, _, _, _ := newService(t)
	for _, u := range []string{"http://127.0.0.1:8080/p.wkp", "file:///etc/passwd", "http://169.254.169.254/x"} {
		if _, err := s.FetchURL(context.Background(), u); !isInvalid(err) {
			t.Errorf("FetchURL(%s) = %v, want refusal", u, err)
		}
	}
}

func isInvalid(err error) bool {
	var ie *InvalidError
	return errors.As(err, &ie)
}
