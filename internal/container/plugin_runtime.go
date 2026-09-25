package container

import (
	"context"

	"github.com/redis/go-redis/v9"
	"go.uber.org/dig"

	"github.com/Tencent/WeKnora/internal/config"
	"github.com/Tencent/WeKnora/internal/handler"
	"github.com/Tencent/WeKnora/internal/plugin/install"
	"github.com/Tencent/WeKnora/internal/plugin/reconcile"
	pluginregistry "github.com/Tencent/WeKnora/internal/plugin/registry"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// newPluginPackageStore keeps plugin packages in the deployment's object
// storage. Packages belong to the platform, not a tenant, so they skip the
// tenant resource catalog.
func newPluginPackageStore(cfg *config.Config) (reconcile.PackageStore, error) {
	fs, err := initRawFileService(cfg)
	if err != nil {
		return nil, err
	}
	return reconcile.NewFileStore(fs), nil
}

// pluginActivators are the domains installed plugins contribute to.
type pluginActivators struct {
	dig.In
}

func (a pluginActivators) list() []reconcile.Activator {
	return nil
}

func newPluginReconciler(
	repo interfaces.PluginRepository,
	store reconcile.PackageStore,
	reg *pluginregistry.Registry,
	rdb *redis.Client,
	activators pluginActivators,
) *reconcile.Reconciler {
	return reconcile.New(reconcile.Options{
		Repo: repo, Store: store, Registry: reg, Redis: rdb, Activators: activators.list(),
	})
}

func newPluginInstaller(
	repo interfaces.PluginRepository,
	store reconcile.PackageStore,
	r *reconcile.Reconciler,
) *install.Service {
	return install.NewService(repo, store, r, handler.Version)
}

// startPluginReconciler loads installed plugins before the server takes
// traffic, then keeps this node in step with the others.
func startPluginReconciler(r *reconcile.Reconciler, cleaner interfaces.ResourceCleaner) {
	ctx, cancel := context.WithCancel(context.Background())
	r.Start(ctx)
	cleaner.RegisterWithName("PluginReconciler", func() error { cancel(); return nil })
}
