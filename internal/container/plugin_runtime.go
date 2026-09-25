package container

import (
	"context"

	"github.com/redis/go-redis/v9"
	"go.uber.org/dig"
	"gorm.io/gorm"

	"github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/config"
	"github.com/Tencent/WeKnora/internal/handler"
	"github.com/Tencent/WeKnora/internal/plugin/activate"
	"github.com/Tencent/WeKnora/internal/plugin/install"
	"github.com/Tencent/WeKnora/internal/plugin/reconcile"
	pluginregistry "github.com/Tencent/WeKnora/internal/plugin/registry"
	plugintenancy "github.com/Tencent/WeKnora/internal/plugin/tenancy"
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

	MCP *activate.MCPServers
}

func (a pluginActivators) list() []reconcile.Activator {
	return []reconcile.Activator{a.MCP}
}

// bindPluginActivators hands the activators what they need once the plugin
// services exist.
func bindPluginActivators(a pluginActivators, t *plugintenancy.Service, repo interfaces.PluginRepository) {
	a.MCP.Bind(t, repo)
}

func newMCPServiceRepository(db *gorm.DB, plugins *activate.MCPServers) interfaces.MCPServiceRepository {
	return plugins.Repository(repository.NewMCPServiceRepository(db))
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
