package host

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/plugin/manifest"
	"github.com/Tencent/WeKnora/internal/plugin/reconcile"
	"github.com/Tencent/WeKnora/pluginsdk/pluginapi"
)

var (
	buildOnce sync.Once
	binary    string
	buildErr  error
)

// echoBinary builds testdata/echoplugin once per test run.
func echoBinary(t *testing.T) string {
	t.Helper()
	buildOnce.Do(func() {
		dir, err := os.MkdirTemp("", "echoplugin")
		if err != nil {
			buildErr = err
			return
		}
		binary = filepath.Join(dir, "echo")
		out, err := exec.Command("go", "build", "-o", binary, "./testdata/echoplugin").CombinedOutput()
		if err != nil {
			buildErr = err
			t.Logf("%s", out)
		}
	})
	if buildErr != nil {
		t.Fatalf("build echo plugin: %v", buildErr)
	}
	return binary
}

// install lays out an extracted package with the echo binary.
func install(t *testing.T, version string, fakeVersion string) *reconcile.Loaded {
	t.Helper()
	dir := t.TempDir()
	bin := filepath.Join(dir, "bin", runtime.GOOS+"-"+runtime.GOARCH)
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(echoBinary(t))
	if err != nil {
		t.Fatal(err)
	}
	// Extraction writes plain files; the host must make the entry runnable.
	name := "echo"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	if err := os.WriteFile(filepath.Join(bin, name), data, 0o644); err != nil {
		t.Fatal(err)
	}
	if fakeVersion != "" {
		_ = os.WriteFile(filepath.Join(dir, "version"), []byte(fakeVersion), 0o644)
	}
	m := &manifest.Manifest{
		SchemaVersion: manifest.SchemaVersion, ID: "acme.echo", Version: version, APIVersion: pluginapi.APIVersion,
		Name: manifest.Text("Echo", nil), Publisher: manifest.Publisher{ID: "acme"},
		Runtime: manifest.Runtime{Type: manifest.RuntimeHost, Kind: "binary", Entry: "bin/{os}-{arch}/echo"},
		Contributes: manifest.Contributions{
			manifest.PointWebSearch: {{ID: "echo", Name: manifest.Text("Echo", nil)}},
		},
	}
	return &reconcile.Loaded{Manifest: m, Dir: dir}
}

type reports struct {
	mu  sync.Mutex
	log []string
}

func (r *reports) ReportRuntime(id string, healthy bool, _ error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.log = append(r.log, id+":"+strconv.FormatBool(healthy))
}

func (r *reports) String() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return strings.Join(r.log, ",")
}

func fastTimings(t *testing.T) {
	t.Helper()
	oldBackoff, oldInterval, oldGrace := restartBackoffFloor, healthInterval, stopGrace
	restartBackoffFloor, healthInterval, stopGrace = 20*time.Millisecond, 100*time.Millisecond, 2*time.Second
	t.Cleanup(func() { restartBackoffFloor, healthInterval, stopGrace = oldBackoff, oldInterval, oldGrace })
}

func search(t *testing.T, m *Manager, q string) (string, error) {
	t.Helper()
	c, err := m.Client("acme.echo")
	if err != nil {
		return "", err
	}
	var out pluginapi.SearchOutput
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	err = c.Call(ctx, pluginapi.SearchPath("echo"), pluginapi.Envelope{}, pluginapi.SearchInput{Query: q}, &out)
	if err != nil {
		return "", err
	}
	return out.Results[0].Title, nil
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestHostRunsRestartsAndStopsAPlugin(t *testing.T) {
	fastTimings(t)
	t.Setenv("DB_PASSWORD", "must-not-leak")
	ctx := context.Background()
	m := NewManager()
	rep := &reports{}
	m.SetReporter(rep)
	defer m.Close()

	if err := m.Activate(ctx, install(t, "1.0.0", "")); err != nil {
		t.Fatalf("Activate: %v", err)
	}
	if got, err := search(t, m, "hello"); err != nil || got != "hello" {
		t.Fatalf("search = %q, %v", got, err)
	}
	env, err := search(t, m, "env")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(env, "DB_PASSWORD") || !strings.Contains(env, "HTTPS_PROXY") ||
		!strings.Contains(env, pluginapi.EnvToken) {
		t.Fatalf("plugin environment = %s", env)
	}

	pid1, _ := search(t, m, "pid")
	_, _ = search(t, m, "crash")
	waitFor(t, "restart", func() bool {
		pid, err := search(t, m, "pid")
		return err == nil && pid != pid1
	})
	if !strings.Contains(rep.String(), "acme.echo:false") || !strings.HasSuffix(rep.String(), "acme.echo:true") {
		t.Fatalf("reports = %s", rep)
	}

	pid2, _ := search(t, m, "pid")
	if err := m.Deactivate(ctx, "acme.echo"); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Client("acme.echo"); err == nil {
		t.Fatal("a stopped plugin must have no client")
	}
	if runtime.GOOS != "windows" {
		n, _ := strconv.Atoi(pid2)
		waitFor(t, "the process to exit after Deactivate", func() bool {
			proc, err := os.FindProcess(n)
			return err != nil || proc.Signal(syscall.Signal(0)) != nil
		})
	}
}

func TestHostRefusesAProcessThatIsNotThePackage(t *testing.T) {
	fastTimings(t)
	m := NewManager()
	defer m.Close()
	err := m.Activate(context.Background(), install(t, "1.0.0", "9.9.9"))
	if err == nil || !strings.Contains(err.Error(), "reports acme.echo@9.9.9") {
		t.Fatalf("want a manifest mismatch, got %v", err)
	}
	l := install(t, "1.0.0", "")
	l.Manifest.Contributes[manifest.PointWebSearch] = append(l.Manifest.Contributes[manifest.PointWebSearch],
		manifest.Contribution{ID: "missing", Name: manifest.Text("Missing", nil)})
	if err := m.Activate(context.Background(), l); err == nil || !strings.Contains(err.Error(), "webSearch/missing") {
		t.Fatalf("want a missing contribution error, got %v", err)
	}
	l = install(t, "1.0.0", "")
	l.Manifest.Runtime.Entry = "bin/{os}-{arch}/nope"
	if err := m.Activate(context.Background(), l); err == nil || !strings.Contains(err.Error(), "has no") {
		t.Fatalf("want a missing build error, got %v", err)
	}
}

func TestUpgradeSwapsProcessesInPlace(t *testing.T) {
	fastTimings(t)
	ctx := context.Background()
	m := NewManager()
	defer m.Close()
	if err := m.Activate(ctx, install(t, "1.0.0", "")); err != nil {
		t.Fatal(err)
	}
	pid1, _ := search(t, m, "pid")
	if err := m.Activate(ctx, install(t, "1.1.0", "")); err != nil {
		t.Fatal(err)
	}
	pid2, err := search(t, m, "pid")
	if err != nil || pid2 == pid1 {
		t.Fatalf("after upgrade pid = %s (was %s), %v", pid2, pid1, err)
	}
}

// An upgrade does not wait for the old process to finish its calls: it
// drains them in the background, up to the stop grace period.
func TestUpgradeLetsTheOldProcessFinishItsCalls(t *testing.T) {
	fastTimings(t)
	stopGrace = 20 * time.Second
	ctx := context.Background()
	m := NewManager()
	defer m.Close()
	if err := m.Activate(ctx, install(t, "1.0.0", "")); err != nil {
		t.Fatal(err)
	}
	inFlight := make(chan error, 1)
	go func() {
		got, err := search(t, m, "sleep:3s")
		if err == nil && got != "slept" {
			err = fmt.Errorf("answer %q", got)
		}
		inFlight <- err
	}()
	time.Sleep(300 * time.Millisecond)
	start := time.Now()
	if err := m.Activate(ctx, install(t, "1.1.0", "")); err != nil {
		t.Fatal(err)
	}
	if took := time.Since(start); took > 2*time.Second {
		t.Fatalf("the upgrade waited %s for the old process", took)
	}
	if err := <-inFlight; err != nil {
		t.Fatalf("the call in flight on the old version failed: %v", err)
	}
}

func TestEgressProxyEnforcesThePolicy(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("hello from " + r.Host))
	}))
	defer upstream.Close()
	p, err := startEgressProxy("acme.echo", []string{"api.allowed.test", "*.wild.test"}, nil, t.Logf)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	// Every name resolves to the test server; the policy is what differs.
	p.dial = func(ctx context.Context, network, _ string) (net.Conn, error) {
		var d net.Dialer
		return d.DialContext(ctx, network, upstream.Listener.Addr().String())
	}
	proxyURL, _ := url.Parse(p.URL())
	c := &http.Client{Transport: &http.Transport{Proxy: http.ProxyURL(proxyURL)}}
	for host, want := range map[string]int{
		"api.allowed.test": 200, "x.wild.test": 200, "wild.test": 403, "evil.test": 403,
	} {
		resp, err := c.Get("http://" + host + "/")
		if err != nil {
			t.Fatalf("%s: %v", host, err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != want {
			t.Errorf("%s: status %d, want %d", host, resp.StatusCode, want)
		}
	}
}

func TestEgressProxyTunnelsHTTPS(t *testing.T) {
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("secure"))
	}))
	defer upstream.Close()
	p, err := startEgressProxy("acme.echo", []string{"*.wild.test"}, nil, t.Logf)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	p.dial = func(ctx context.Context, network, _ string) (net.Conn, error) {
		var d net.Dialer
		return d.DialContext(ctx, network, upstream.Listener.Addr().String())
	}
	proxyURL, _ := url.Parse(p.URL())
	tr := upstream.Client().Transport.(*http.Transport).Clone()
	tr.Proxy = http.ProxyURL(proxyURL)
	tr.TLSClientConfig.InsecureSkipVerify = true
	c := &http.Client{Transport: tr}

	resp, err := c.Get("https://api.wild.test/")
	if err != nil {
		t.Fatal(err)
	}
	body := make([]byte, 16)
	n, _ := resp.Body.Read(body)
	_ = resp.Body.Close()
	if string(body[:n]) != "secure" {
		t.Fatalf("tunnelled body = %q", body[:n])
	}
	if _, err := c.Get("https://evil.test/"); err == nil || !strings.Contains(err.Error(), "Forbidden") {
		t.Fatalf("CONNECT to a host outside the policy must be refused, got %v", err)
	}
}

func TestProtocolVersionsAgree(t *testing.T) {
	if manifest.ExtensionAPIVersion != pluginapi.APIVersion {
		t.Fatalf("manifest accepts %q, the protocol package speaks %q",
			manifest.ExtensionAPIVersion, pluginapi.APIVersion)
	}
}

// Hosts WeKnora names itself (the Host API on another node) pass the proxy
// even on a private address; other private addresses stay refused.
func TestEgressProxyPassesDirectHosts(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("host api"))
	}))
	defer upstream.Close()
	u, _ := url.Parse(upstream.URL)
	p, err := startEgressProxy("acme.echo", []string{"*"}, []string{u.Host}, t.Logf)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	proxyURL, _ := url.Parse(p.URL())
	c := &http.Client{Transport: &http.Transport{Proxy: func(*http.Request) (*url.URL, error) { return proxyURL, nil }}}
	resp, err := c.Get(upstream.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != 200 || string(body) != "host api" {
		t.Fatalf("direct host = %d %s", resp.StatusCode, body)
	}
	resp, err = c.Get("http://localhost:" + u.Port() + "/")
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode == 200 {
		t.Fatal("a private address that is not a direct host went through")
	}
}
