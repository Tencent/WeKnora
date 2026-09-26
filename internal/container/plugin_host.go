package container

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/config"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/plugin/host"
	"github.com/Tencent/WeKnora/internal/plugin/hostpool"
	"github.com/Tencent/WeKnora/internal/plugin/manifest"
	"github.com/Tencent/WeKnora/internal/plugin/reconcile"
	pluginregistry "github.com/Tencent/WeKnora/internal/plugin/registry"
	plugintrust "github.com/Tencent/WeKnora/internal/plugin/trust"
)

// Standalone plugin host settings.
const (
	envPluginHostAddr  = "WEKNORA_PLUGIN_HOST_ADDR"  // listen address, default :8081
	envPluginHostURL   = "WEKNORA_PLUGIN_HOST_URL"   // gateway URL app nodes use
	envPluginHostKinds = "WEKNORA_PLUGIN_HOST_KINDS" // kinds to run, default all available
)

// RunPluginHost runs this binary as a standalone plugin host
// (weknora plugin-host) until ctx ends. It shares the app's database,
// object storage and Redis, runs the installed host plugins of the kinds it
// supports, announces them in Redis and serves them to app nodes through a
// gateway signed with the cluster key. It never migrates the database;
// app nodes do.
func RunPluginHost(ctx context.Context) error {
	if _, set := os.LookupEnv("AUTO_MIGRATE"); !set {
		_ = os.Setenv("AUTO_MIGRATE", "false")
	}
	key, err := hostpool.ClusterKey()
	if err != nil {
		return err
	}
	rdb, err := initRedisClient()
	if err != nil {
		return err
	}
	if rdb == nil {
		return errors.New("a plugin host needs Redis (REDIS_ADDR) to announce itself to app nodes")
	}
	cfg, err := config.LoadConfig()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	db, err := initDatabase(cfg)
	if err != nil {
		return fmt.Errorf("database: %w", err)
	}
	store, err := newPluginPackageStore(cfg)
	if err != nil {
		return fmt.Errorf("package storage: %w", err)
	}

	kinds := host.KindsFromEnv(envPluginHostKinds)
	if len(kinds) == 0 {
		return fmt.Errorf("%s leaves this plugin host nothing to run", envPluginHostKinds)
	}
	mgr := host.NewStandaloneManager(kinds)
	// Plugins call the Host API on an app node, not through their egress
	// proxy.
	hostAPI := strings.TrimSpace(os.Getenv("WEKNORA_PLUGIN_HOST_API_URL"))
	if u, err := url.Parse(hostAPI); err == nil && u.Hostname() != "" {
		port := u.Port()
		if port == "" {
			port = "80"
			if u.Scheme == "https" {
				port = "443"
			}
		}
		mgr.SetDirectHosts(net.JoinHostPort(u.Hostname(), port))
	}
	trusted, err := plugintrust.FromEnv()
	if err != nil {
		return err
	}
	r := reconcile.New(reconcile.Options{
		Repo: repository.NewPluginRepository(db), Store: store, Registry: pluginregistry.New(), Redis: rdb,
		Activators: []reconcile.Activator{mgr}, Runtimes: []manifest.RuntimeType{manifest.RuntimeHost},
		Accept: func(m *manifest.Manifest) bool { return mgr.Runs(m.Runtime.Kind) }, Role: "plugin-host",
		Admit: admitTrusted(trusted),
	})
	mgr.SetReporter(r)

	addr := os.Getenv(envPluginHostAddr)
	if addr == "" {
		addr = ":8081"
	}
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("listen %s: %w", addr, err)
	}
	advertise, err := advertiseURL(ln.Addr())
	if err != nil {
		_ = ln.Close()
		return err
	}
	srv := &http.Server{Handler: mgr.Gateway(key), ReadHeaderTimeout: 10 * time.Second}
	serveErr := make(chan error, 1)
	go func() { serveErr <- srv.Serve(ln) }()

	runCtx, stop := context.WithCancel(ctx)
	defer stop()
	r.Start(runCtx)
	hostname, _ := os.Hostname()
	announced := make(chan struct{})
	go func() {
		hostpool.NewAnnouncer(rdb, mgr, r.NodeName(), advertise).Run(runCtx)
		close(announced)
	}()
	logger.Infof(ctx, "[plugin-host] %s runs %s plugins, serving app nodes at %s",
		hostname, strings.Join(kinds, ", "), advertise)

	select {
	case <-ctx.Done():
	case err := <-serveErr:
		stop()
		<-announced
		mgr.Close()
		return fmt.Errorf("gateway: %w", err)
	}
	// Withdraw first so app nodes stop picking this host, then let calls in
	// flight finish before the plugins stop.
	stop()
	<-announced
	shutdown, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdown); err != nil {
		logger.Warnf(ctx, "[plugin-host] shutdown: %v", err)
	}
	mgr.Close()
	return nil
}

// advertiseURL is the gateway URL app nodes use: WEKNORA_PLUGIN_HOST_URL,
// or http://<hostname>:<port>, which suits compose and Kubernetes pod DNS.
func advertiseURL(addr net.Addr) (string, error) {
	if u := strings.TrimSpace(os.Getenv(envPluginHostURL)); u != "" {
		if _, err := url.Parse(u); err != nil {
			return "", fmt.Errorf("%s: %w", envPluginHostURL, err)
		}
		return strings.TrimSuffix(u, "/"), nil
	}
	tcp, ok := addr.(*net.TCPAddr)
	if !ok {
		return "", fmt.Errorf("set %s", envPluginHostURL)
	}
	name, err := os.Hostname()
	if err != nil || name == "" {
		return "", fmt.Errorf("no hostname to advertise; set %s", envPluginHostURL)
	}
	return "http://" + net.JoinHostPort(name, strconv.Itoa(tcp.Port)), nil
}
