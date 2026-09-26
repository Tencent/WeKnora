package hostpool

import (
	"context"
	"net/http"
	"net/http/httptest"
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
	if _, err := pool.Client(ctx, "acme.echo", "2.0.0"); !isCode(err, pluginapi.CodeUnavailable) {
		t.Fatalf("other version = %v", err)
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

	// A host that stops announcing drops out when its record expires.
	stop()
	<-done
	if mr.Exists(keyPrefix() + "h1") {
		t.Fatal("a stopped host must withdraw its record")
	}
	now = now.Add(refreshAfter)
	if hosts, _ := pool.Hosts(ctx); len(hosts) != 0 {
		t.Fatalf("hosts after withdrawal = %+v", hosts)
	}

	// A record that outlived its heartbeat (clock skew, a stuck host) is
	// ignored even if Redis still has it.
	_ = NewAnnouncer(rdb, mgr, "h2", "http://h2:8081").Announce(ctx)
	now = now.Add(refreshAfter + HeartbeatTTL)
	if hosts, _ := pool.Hosts(ctx); len(hosts) != 0 {
		t.Fatalf("stale hosts = %+v", hosts)
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
