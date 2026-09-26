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
	"github.com/Tencent/WeKnora/internal/infrastructure/chunker"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/plugin/activate"
	pluginevents "github.com/Tencent/WeKnora/internal/plugin/events"
	"github.com/Tencent/WeKnora/internal/plugin/host"
	"github.com/Tencent/WeKnora/internal/plugin/hostapi"
	"github.com/Tencent/WeKnora/internal/plugin/hostpool"
	"github.com/Tencent/WeKnora/internal/plugin/install"
	"github.com/Tencent/WeKnora/internal/plugin/manifest"
	"github.com/Tencent/WeKnora/internal/plugin/market"
	pluginoauth "github.com/Tencent/WeKnora/internal/plugin/oauth"
	"github.com/Tencent/WeKnora/internal/plugin/pkg"
	"github.com/Tencent/WeKnora/internal/plugin/reconcile"
	pluginregistry "github.com/Tencent/WeKnora/internal/plugin/registry"
	"github.com/Tencent/WeKnora/internal/plugin/remote"
	plugintenancy "github.com/Tencent/WeKnora/internal/plugin/tenancy"
	plugintrust "github.com/Tencent/WeKnora/internal/plugin/trust"
	"github.com/Tencent/WeKnora/internal/plugin/webhook"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/Tencent/WeKnora/pluginsdk/client"
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
	Remote     *remote.Manager
	Delegation *pluginDelegation
	WebSearch  *activate.WebSearch
	Connectors *activate.Connectors
	Parsers    *activate.Parsers
	UIPages    *activate.UIPages
	MCP        *activate.MCPServers
	Skills     *activate.Skills
	Vendors    *activate.ModelVendors
	Invoker    *activate.Invoker
}

// list orders the activators: the runtimes first, so a code plugin is
// reachable before anything routes calls to it.
func (a pluginActivators) list() []reconcile.Activator {
	return []reconcile.Activator{
		a.Host, a.Remote, a.Delegation, a.WebSearch, a.Connectors, a.Parsers, a.UIPages, a.Vendors, a.MCP, a.Skills,
	}
}

// newPluginHostAPI serves the Host API and gives calls a way back to it: the
// embedded host's plugins reach this node on loopback, plugins elsewhere
// (remote, on a plugin host) only through WEKNORA_PLUGIN_HOST_API_URL.
func newPluginHostAPI(
	cfg *config.Config, iv *activate.Invoker, repo interfaces.PluginKVRepository, cleaner interfaces.ResourceCleaner,
	hooks *webhook.Tokens, oauth *pluginoauth.Service,
) *hostapi.Handler {
	iv.SetWebhooks(hooks, webhook.PublicBase())
	iv.SetOAuth(oauth)
	issuer := hostapi.NewIssuerFromEnv()
	// Plugins on this node always use loopback; the public address is for
	// plugins elsewhere (remote, on a plugin host), which reach this node
	// the way the deployment routes to it.
	iv.SetHostAPI(issuer, fmt.Sprintf("http://127.0.0.1:%d", serverPort(cfg)))
	iv.SetPublicHostAPI(strings.TrimSpace(os.Getenv("WEKNORA_PLUGIN_HOST_API_URL")))
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
	skills *service.TenantSkillService, reg *pluginregistry.Registry,
) {
	a.MCP.Bind(t, repo)
	a.MCP.SetInvoker(a.Invoker)
	a.Invoker.Bind(t, repo)
	a.Skills.Bind(t)
	skills.SetPluginSkills(a.Skills)
	chunker.SetPluginSplitter(activate.PluginChunker(a.Invoker, reg, t))
}

// newPluginHostManager is this node's embedded plugin host. It runs the
// kinds WEKNORA_PLUGIN_EMBEDDED_KINDS names (default: every kind this
// machine can run; "none" leaves all host plugins to standalone hosts).
func newPluginHostManager(cfg *config.Config) *host.Manager {
	m := host.NewManager()
	m.SetKinds(host.KindsFromEnv("WEKNORA_PLUGIN_EMBEDDED_KINDS"))
	m.SetHostAPIAddr(fmt.Sprintf("127.0.0.1:%d", serverPort(cfg)))
	return m
}

// serverPort is the port this node serves on (the Host API's, on loopback).
func serverPort(cfg *config.Config) int {
	if cfg != nil && cfg.Server != nil && cfg.Server.Port > 0 {
		return cfg.Server.Port
	}
	return 8080
}

// newPluginHostPool finds standalone plugin hosts (weknora plugin-host) in
// Redis. It is nil without Redis or a cluster key: host plugins then run on
// this node or nowhere.
func newPluginHostPool(rdb *redis.Client) *hostpool.Pool {
	if rdb == nil {
		return nil
	}
	key, err := hostpool.ClusterKey()
	if err != nil {
		logger.Warnf(context.Background(), "[plugin] standalone plugin hosts are off: %v", err)
		return nil
	}
	return hostpool.NewPool(rdb, key)
}

// newPluginInvoker reaches code plugins through this node's plugin host, a
// standalone plugin host, or a remote plugin's registered URL.
func newPluginInvoker(h *host.Manager, r *remote.Manager, pool *hostpool.Pool) *activate.Invoker {
	return activate.NewInvoker(pluginClients{host: h, remote: r, pool: pool})
}

// pluginClients finds a code plugin in whichever runtime serves it.
type pluginClients struct {
	host   *host.Manager
	remote *remote.Manager
	pool   *hostpool.Pool
}

func (c pluginClients) Client(ctx context.Context, m *manifest.Manifest) (*client.Client, error) {
	switch {
	case c.remote.Owns(m.ID):
		return c.remote.Client(m.ID)
	case c.host.Local(m.ID) || c.pool == nil || m.Runtime.Type != manifest.RuntimeHost:
		return c.host.Client(m.ID)
	default:
		return c.pool.Client(ctx, m.ID, m.Version)
	}
}

func (c pluginClients) OnThisNode(pluginID string) bool { return c.host.Local(pluginID) }

// pluginDelegation fails a host plugin this node leaves to standalone
// plugin hosts when there are none to leave it to. With hosts configured it
// only logs: they may start later, and calls say which plugin is missing.
type pluginDelegation struct {
	host *host.Manager
	pool *hostpool.Pool
}

func newPluginDelegation(h *host.Manager, pool *hostpool.Pool) *pluginDelegation {
	return &pluginDelegation{host: h, pool: pool}
}

func (d *pluginDelegation) Name() string { return "plugin-hosts" }

func (d *pluginDelegation) Activate(ctx context.Context, l *reconcile.Loaded) error {
	m := l.Manifest
	if m.Runtime.Type != manifest.RuntimeHost || d.host.Runs(m.Runtime.Kind) {
		return nil
	}
	if d.pool == nil {
		return fmt.Errorf("this node does not run %s plugins (WEKNORA_PLUGIN_EMBEDDED_KINDS) and no plugin host "+
			"is configured; run weknora plugin-host with Redis and the same SYSTEM_AES_KEY", m.Runtime.Kind)
	}
	if !d.pool.Runs(ctx, m.ID, m.Version) {
		logger.Infof(ctx, "[plugin] %s@%s waits for a plugin host that runs %s plugins",
			m.ID, m.Version, m.Runtime.Kind)
	}
	return nil
}

func (d *pluginDelegation) Deactivate(context.Context, string) error { return nil }

func newMCPServiceRepository(db *gorm.DB, plugins *activate.MCPServers) interfaces.MCPServiceRepository {
	return plugins.Repository(repository.NewMCPServiceRepository(db))
}

func newPluginReconciler(
	repo interfaces.PluginRepository,
	store reconcile.PackageStore,
	reg *pluginregistry.Registry,
	rdb *redis.Client,
	activators pluginActivators,
	trusted *plugintrust.Store,
) *reconcile.Reconciler {
	return reconcile.New(reconcile.Options{
		Repo: repo, Store: store, Registry: reg, Redis: rdb, Activators: activators.list(),
		Admit: admitTrusted(trusted),
	})
}

// admitTrusted loads only packages at or above the platform's minimum trust
// level, so raising it also stops plugins installed before.
func admitTrusted(trusted *plugintrust.Store) func(*pkg.Package) error {
	return func(p *pkg.Package) error {
		_, err := trusted.Check(p)
		return err
	}
}

func newPluginInstaller(
	repo interfaces.PluginRepository,
	store reconcile.PackageStore,
	r *reconcile.Reconciler,
	trusted *plugintrust.Store,
	settings interfaces.SystemSettingService,
) *install.Service {
	return install.NewService(repo, store, r, handler.Version).
		WithChecks(activate.CheckModelVendors).WithTrust(trusted).
		WithTenantPlugins(func(ctx context.Context) bool {
			return settings.GetBool(ctx, "tenant.plugin_remote_enabled", "WEKNORA_PLUGIN_TENANT_REMOTE", false)
		})
}

// newPluginAdminHandler serves plugin installation, with the marketplace
// WEKNORA_PLUGIN_INDEX_URL names.
func newPluginAdminHandler(service *install.Service, tenants interfaces.TenantService) *handler.PluginAdminHandler {
	return handler.NewPluginAdminHandler(service).WithMarket(market.FromEnv(handler.Version)).WithTenants(tenants)
}

// startPluginReconciler loads installed plugins before the server takes
// traffic, then keeps this node in step with the others.
func startPluginReconciler(
	r *reconcile.Reconciler, hostManager *host.Manager, remoteManager *remote.Manager,
	events *pluginevents.Dispatcher, cleaner interfaces.ResourceCleaner,
) {
	pluginevents.SetDefault(events)
	if kinds := hostManager.Kinds(); len(kinds) > 0 {
		logger.Infof(context.Background(), "[plugin] this node runs %s host plugins", strings.Join(kinds, ", "))
	}
	hostManager.SetReporter(r)
	remoteManager.SetReporter(r)
	ctx, cancel := context.WithCancel(context.Background())
	r.Start(ctx)
	// The first reconcile loaded the plugins: events held since startup
	// (documents the startup reset failed) now find their subscribers.
	pluginevents.FlushDeferred(ctx)
	cleaner.RegisterWithName("PluginReconciler", func() error {
		cancel()
		hostManager.Close()
		remoteManager.Close()
		return nil
	})
}

// bindPluginHostDataSources enables the Host API's datasources scope once
// the data source service exists.
func bindPluginHostDataSources(
	h *hostapi.Handler, repo interfaces.DataSourceRepository, svc interfaces.DataSourceService,
) {
	if store, ok := repo.(hostapi.DataSourceStore); ok {
		h.SetDataSources(hostapi.NewDataSources(store, svc))
	}
}
