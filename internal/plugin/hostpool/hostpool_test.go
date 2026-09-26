package hostpool

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"

	"github.com/Tencent/WeKnora/internal/plugin/host"
	"github.com/Tencent/WeKnora/internal/plugin/manifest"
	"github.com/Tencent/WeKnora/internal/plugin/reconcile"
	"github.com/Tencent/WeKnora/pluginsdk/client"
	"github.com/Tencent/WeKnora/pluginsdk/pluginapi"
)

// echoPackage builds the host package's echo test plugin into an extracted
// package directory.
func echoPackage(t *testing.T) *reconcile.Loaded {
	t.Helper()
	dir := t.TempDir()
	name := "echo"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	bin := filepath.Join(dir, "bin", runtime.GOOS+"-"+runtime.GOARCH, name)
	out, err := exec.Command("go", "build", "-o", bin, "../host/testdata/echoplugin").CombinedOutput()
	if err != nil {
		t.Fatalf("build echo plugin: %v\n%s", err, out)
	}
	m := &manifest.Manifest{
		SchemaVersion: manifest.SchemaVersion, ID: "acme.echo", Version: "1.0.0", APIVersion: pluginapi.APIVersion,
		Name: manifest.Text("Echo", nil), Publisher: manifest.Publisher{ID: "acme"},
		Runtime: manifest.Runtime{Type: manifest.RuntimeHost, Kind: host.KindBinary, Entry: "bin/{os}-{arch}/echo"},
		Contributes: manifest.Contributions{
			manifest.PointWebSearch: {{ID: "echo", Name: manifest.Text("Echo", nil)}},
		},
	}
	return &reconcile.Loaded{Manifest: m, Dir: dir}
}

func search(ctx context.Context, c *client.Client, q string) (string, error) {
	var out pluginapi.SearchOutput
	err := c.Call(ctx, pluginapi.SearchPath("echo"), pluginapi.Envelope{}, pluginapi.SearchInput{Query: q}, &out)
	if err != nil {
		return "", err
	}
	return out.Results[0].Title, nil
}

func TestPoolReachesAPluginThroughAHost(t *testing.T) {
	ctx := context.Background()
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Setenv("SYSTEM_AES_KEY", "0123456789abcdef0123456789abcdef")
	key, err := ClusterKey()
	if err != nil {
		t.Fatal(err)
	}

	// The plugin host: a manager running the plugin behind its gateway.
	mgr := host.NewManager()
	defer mgr.Close()
	if err := mgr.Activate(ctx, echoPackage(t)); err != nil {
		t.Fatal(err)
	}
	gw := httptest.NewServer(mgr.Gateway(key))
	defer gw.Close()
	a := NewAnnouncer(rdb, mgr, "h1", gw.URL+"/")
	if err := a.Announce(ctx); err != nil {
		t.Fatal(err)
	}

	pool := NewPool(rdb, key)
	c, err := pool.Client(ctx, "acme.echo", "1.0.0")
	if err != nil {
		t.Fatal(err)
	}
	if got, err := search(ctx, c, "hello"); err != nil || got != "hello" {
		t.Fatalf("search via host = %q, %v", got, err)
	}
	if !pool.Runs(ctx, "acme.echo", "1.0.0") || pool.Runs(ctx, "acme.echo", "2.0.0") {
		t.Fatal("Runs should match the running version only")
	}
	// A version no host runs is unavailable, after a while for a host to
	// finish upgrading to it.
	defer func(d time.Duration) { upgradeWait = d }(upgradeWait)
	upgradeWait = 300 * time.Millisecond
	start := time.Now()
	if _, err := pool.Client(ctx, "acme.echo", "2.0.0"); !isCode(err, pluginapi.CodeUnavailable) {
		t.Fatalf("other version = %v", err)
	}
	if took := time.Since(start); took < upgradeWait {
		t.Fatalf("gave up on an upgrade after %s", took)
	}
	if _, err := pool.Client(ctx, "acme.other", "1.0.0"); !isCode(err, pluginapi.CodeUnavailable) {
		t.Fatalf("a plugin no host runs = %v", err)
	}

	// The gateway refuses calls without the cluster key, and relays a
	// version it does not run as unavailable.
	unsigned := client.New(gw.URL+"/p/acme.echo/1.0.0", nil, nil)
	if _, err := search(ctx, unsigned, "x"); !isCode(err, pluginapi.CodeUnauthorized) {
		t.Fatalf("unsigned = %v", err)
	}
	stale := client.New(gw.URL+"/p/acme.echo/0.9.0", nil, client.Signed(key))
	if _, err := search(ctx, stale, "x"); !isCode(err, pluginapi.CodeUnavailable) {
		t.Fatalf("stale version = %v", err)
	}
	resp, err := http.Get(gw.URL + "/healthz")
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("healthz = %v, %v", resp, err)
	}
	_ = resp.Body.Close()
}

func TestHostsAgeOutAndWithdraw(t *testing.T) {
	ctx := context.Background()
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	mgr := host.NewManager()
	pool := NewPool(rdb, []byte("k"))
	now := time.Now()
	pool.now = func() time.Time { return now }

	runCtx, stop := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() { NewAnnouncer(rdb, mgr, "h1", "http://h1:8081").Run(runCtx); close(done) }()
	waitFor(t, func() bool { return mr.Exists(keyPrefix() + "h1") })
	hosts, err := pool.Hosts(ctx)
	if err != nil || len(hosts) != 1 || hosts[0].URL != "http://h1:8081" || len(hosts[0].Kinds) == 0 {
		t.Fatalf("hosts = %+v, %v", hosts, err)
	}

	// A stopped host withdraws its record.
	stop()
	<-done
	if mr.Exists(keyPrefix() + "h1") {
		t.Fatal("a stopped host must withdraw its record")
	}
	now = now.Add(refreshAfter)
	if hosts, _ := pool.Hosts(ctx); len(hosts) != 0 {
		t.Fatalf("hosts after withdrawal = %+v", hosts)
	}

	// Liveness is the record's TTL, not the host's clock: a host whose
	// clock is far behind stays listed while it announces, and one that
	// stopped announcing drops out when Redis expires its record.
	b, _ := json.Marshal(Info{ID: "h2", URL: "http://h2:8081", UpdatedAt: time.Now().Add(-time.Hour)})
	_ = mr.Set(keyPrefix()+"h2", string(b))
	mr.SetTTL(keyPrefix()+"h2", HeartbeatTTL)
	_, _ = mr.SetAdd(indexKey(), "h2")
	now = now.Add(refreshAfter)
	if hosts, _ := pool.Hosts(ctx); len(hosts) != 1 || hosts[0].ID != "h2" {
		t.Fatalf("a live host with a skewed clock = %+v", hosts)
	}
	mr.FastForward(HeartbeatTTL)
	now = now.Add(refreshAfter)
	if hosts, _ := pool.Hosts(ctx); len(hosts) != 0 {
		t.Fatalf("expired hosts = %+v", hosts)
	}
	if ok, _ := mr.SIsMember(indexKey(), "h2"); ok {
		t.Fatal("an expired host must leave the index")
	}
}

// During an upgrade on a standalone host, app nodes reach the new version
// as soon as the host started it, and those not yet switched still reach
// the old one.
func TestUpgradeOnAHostHandsOver(t *testing.T) {
	ctx := context.Background()
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Setenv("SYSTEM_AES_KEY", "0123456789abcdef0123456789abcdef")
	key, err := ClusterKey()
	if err != nil {
		t.Fatal(err)
	}
	mgr := host.NewStandaloneManager([]string{host.KindBinary})
	defer mgr.Close()
	v1 := echoPackage(t)
	if err := mgr.Activate(ctx, v1); err != nil {
		t.Fatal(err)
	}
	gw := httptest.NewServer(mgr.Gateway(key))
	defer gw.Close()
	runCtx, stop := context.WithCancel(ctx)
	defer stop()
	go NewAnnouncer(rdb, mgr, "h1", gw.URL).Run(runCtx)

	pool := NewPool(rdb, key)
	waitFor(t, func() bool { return pool.Runs(ctx, "acme.echo", "1.0.0") })
	old, err := pool.Client(ctx, "acme.echo", "1.0.0")
	if err != nil {
		t.Fatal(err)
	}

	v2 := *v1
	m2 := *v1.Manifest
	m2.Version = "2.0.0"
	v2.Manifest = &m2
	_ = os.WriteFile(filepath.Join(v2.Dir, "version"), []byte("2.0.0"), 0o644)
	if err := mgr.Activate(ctx, &v2); err != nil {
		t.Fatal(err)
	}
	// Well within a heartbeat.
	start := time.Now()
	c, err := pool.Client(ctx, "acme.echo", "2.0.0")
	if err != nil {
		t.Fatal(err)
	}
	if took := time.Since(start); took > 2*time.Second {
		t.Fatalf("the new version showed up after %s", took)
	}
	if got, err := search(ctx, c, "hello"); err != nil || got != "hello" {
		t.Fatalf("search on the new version = %q, %v", got, err)
	}
	if got, err := search(ctx, old, "hello"); err != nil || got != "hello" {
		t.Fatalf("search on the version handed over = %q, %v", got, err)
	}
}

func TestClusterKey(t *testing.T) {
	t.Setenv("SYSTEM_AES_KEY", "")
	t.Setenv("JWT_SECRET", "")
	if _, err := ClusterKey(); err == nil {
		t.Fatal("no shared secret must be an error")
	}
	t.Setenv("JWT_SECRET", "s")
	a, _ := ClusterKey()
	t.Setenv("JWT_SECRET", "t")
	b, _ := ClusterKey()
	if len(a) != 32 || string(a) == string(b) {
		t.Fatal("the key derives from the secret")
	}
}

func isCode(err error, code pluginapi.ErrorCode) bool {
	pe, ok := pluginapi.AsError(err)
	return ok && pe.Code == code
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal("timed out")
		}
		time.Sleep(10 * time.Millisecond)
	}
}
