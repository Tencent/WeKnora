package activate

import (
	"context"
	"errors"
	"testing"

	"github.com/Tencent/WeKnora/internal/application/service"
	"github.com/Tencent/WeKnora/internal/plugin/plugintest"
	"github.com/Tencent/WeKnora/internal/plugin/reconcile"
	"github.com/Tencent/WeKnora/internal/plugin/registry"
	"github.com/Tencent/WeKnora/internal/plugin/tenancy"
	"github.com/Tencent/WeKnora/internal/types"
)

func TestPluginSkillArchiveInstallsLikeAnUpload(t *testing.T) {
	ctx := context.Background()
	repo, store, reg := plugintest.NewMemRepo(), &plugintest.MemStore{}, registry.New()
	ten := tenancy.NewService(reg, &plugintest.MemTenantSettings{})
	skills := NewSkills()
	skills.Bind(ten)
	r := reconcile.New(reconcile.Options{
		Repo: repo, Store: store, Registry: reg, CacheDir: t.TempDir(), Activators: []reconcile.Activator{skills},
	})
	plugintest.Install(t, repo, store, plugintest.KitPackageWith(t, "1.0.0",
		"Triage incoming issues by severity."), types.PluginStateEnabled)
	if err := r.Reconcile(ctx); err != nil {
		t.Fatal(err)
	}

	if _, err := skills.Archive(ctx, 1, "plugin:acme.kit/triage"); !errors.Is(err, ErrUnknownSkill) {
		t.Fatalf("a workspace without the plugin enabled must be refused, got %v", err)
	}
	if err := ten.SetEnabled(ctx, 1, "acme.kit", true, "u"); err != nil {
		t.Fatal(err)
	}
	archive, err := skills.Archive(ctx, 1, "plugin:acme.kit/triage")
	if err != nil {
		t.Fatalf("Archive: %v", err)
	}
	bundle, err := service.ParseSkillBundle(archive)
	if err != nil {
		t.Fatalf("the archive must be a valid skill bundle: %v", err)
	}
	if bundle.Name != "triage" {
		t.Fatalf("bundle = %+v", bundle)
	}
	for _, bad := range []string{"plugin:acme.kit/nope", "plugin:acme.kit", "plugin:other.kit/triage"} {
		if _, err := skills.Archive(ctx, 1, bad); !errors.Is(err, ErrUnknownSkill) {
			t.Errorf("Archive(%s) = %v", bad, err)
		}
	}
}
