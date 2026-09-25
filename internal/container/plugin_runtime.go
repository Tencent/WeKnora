package container

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
	"go.uber.org/dig"
	"gorm.io/gorm"

	"github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/application/service"
	"github.com/Tencent/WeKnora/internal/config"
	"github.com/Tencent/WeKnora/internal/handler"
	"github.com/Tencent/WeKnora/internal/plugin/activate"
	"github.com/Tencent/WeKnora/internal/plugin/host"
	"github.com/Tencent/WeKnora/internal/plugin/hostapi"
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

	Host       *host.Manager
	WebSearch  *activate.WebSearch
	Connectors *activate.Connectors
	Parsers    *activate.Parsers
	MCP        *activate.MCPServers
	Skills     *activate.Skills
	Vendors    *activate.ModelVendors
	Invoker    *activate.Invoker
}

// list orders the activators: the host first, so a code plugin's process is
// running before anything routes calls to it.
func (a pluginActivators) list() []reconcile.Activator {
	return []reconcile.Activator{a.Host, a.WebSearch, a.Connectors, a.Parsers, a.Vendors, a.MCP, a.Skills}
}

// newPluginHostAPI serves the Host API and gives calls a way back to it: the
// embedded host's plugins reach this node on loopback.
func newPluginHostAPI(
	cfg *config.Config, iv *activate.Invoker, repo interfaces.PluginKVRepository, cleaner interfaces.ResourceCleaner,
) *hostapi.Handler {
	issuer := hostapi.NewIssuerFromEnv()
	url := strings.TrimSpace(os.Getenv("WEKNORA_PLUGIN_HOST_API_URL"))
	if url == "" {
		port := 8080
		if cfg != nil && cfg.Server != nil && cfg.Server.Port > 0 {
			port = cfg.Server.Port
		}
		url = fmt.Sprintf("http://127.0.0.1:%d", port)
	}
	iv.SetHostAPI(issuer, url)
	kv := hostapi.NewKV(repo)
	ctx, cancel := context.WithCancel(context.Background())
	kv.StartSweeper(ctx, 10*time.Minute)
	cleaner.RegisterWithName("PluginKVSweeper", func() error { cancel(); return nil })
	return hostapi.NewHandler(issuer, kv)
}

// bindPluginActivators hands the activators what they need once the plugin
// services exist.
func bindPluginActivators(
	a pluginActivators, t *plugintenancy.Service, repo interfaces.PluginRepository,
	skills *service.TenantSkillService,
) {
	a.MCP.Bind(t, repo)
	a.Invoker.Bind(t, repo)
	a.Skills.Bind(t)
	skills.SetPluginSkills(a.Skills)
}

// newPluginInvoker reaches code plugins through this node's plugin host.
func newPluginInvoker(h *host.Manager) *activate.Invoker { return activate.NewInvoker(h) }

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
	return install.NewService(repo, store, r, handler.Version).WithChecks(activate.CheckModelVendors)
}

// startPluginReconciler loads installed plugins before the server takes
// traffic, then keeps this node in step with the others.
func startPluginReconciler(r *reconcile.Reconciler, hostManager *host.Manager, cleaner interfaces.ResourceCleaner) {
	hostManager.SetReporter(r)
	ctx, cancel := context.WithCancel(context.Background())
	r.Start(ctx)
	cleaner.RegisterWithName("PluginReconciler", func() error {
		cancel()
		hostManager.Close()
		return nil
	})
}
