package remote

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/plugin/manifest"
	"github.com/Tencent/WeKnora/internal/plugin/reconcile"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/utils"
	"github.com/Tencent/WeKnora/pluginsdk"
	"github.com/Tencent/WeKnora/pluginsdk/pluginapi"
)

// service is a remote plugin that checks WeKnora's signature and can be
// taken down.
type service struct {
	*httptest.Server
	down    atomic.Bool
	handler atomic.Value // http.Handler
}

// serve makes the service answer as a version of the plugin.
func (s *service) serve(version string) {
	p := pluginsdk.New(pluginsdk.Info{ID: "acme.search", Version: version})
	p.WebSearch("web", pluginsdk.WebSearchFunc(
		func(context.Context, *pluginsdk.Call, pluginapi.SearchInput) (*pluginapi.SearchOutput, error) {
			return &pluginapi.SearchOutput{}, nil
		}))
	s.handler.Store(p.Handler())
}

func newService(t *testing.T, version, secret string) *service {
	t.Helper()
	s := &service{}
	s.serve(version)
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.down.Load() {
			http.Error(w, "down", http.StatusBadGateway)
			return
		}
		_, release, perr := pluginapi.ReadSigned(r, []byte(secret), 1<<20, nil, time.Now())
		if perr != nil {
			http.Error(w, perr.Error(), http.StatusUnauthorized)
			return
		}
		defer release()
		s.handler.Load().(http.Handler).ServeHTTP(w, r)
	}))
	t.Cleanup(s.Close)
	return s
}

type reports struct {
	mu  sync.Mutex
	got []bool
}

func (r *reports) ReportRuntime(_ string, healthy bool, _ error) {
	r.mu.Lock()
	r.got = append(r.got, healthy)
	r.mu.Unlock()
}

func (r *reports) seen() []bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]bool(nil), r.got...)
}

func loaded(version, url, secret string) *reconcile.Loaded {
	return &reconcile.Loaded{
		Manifest: &manifest.Manifest{
			ID: "acme.search", Version: version, APIVersion: manifest.ExtensionAPIVersion,
			Runtime: manifest.Runtime{Type: manifest.RuntimeRemote},
			Contributes: map[manifest.Point][]manifest.Contribution{
				manifest.PointWebSearch: {{ID: "web"}},
			},
		},
		Installed: types.InstalledPlugin{ID: "acme.search", RemoteURL: url, RemoteSecret: secret},
	}
}

func allowLoopback(t *testing.T) {
	utils.SetSSRFWhitelistFromRaw("127.0.0.1")
	t.Cleanup(func() { utils.SetSSRFWhitelistFromRaw("") })
}

// A version the service does not serve yet waits for it, and is ready as
// soon as the service serves it: the previous version keeps the calls
// until then.
func TestUpgradeWaitsForTheService(t *testing.T) {
	allowLoopback(t)
	ctx := context.Background()
	svc := newService(t, "1.0.0", "s3cret")
	m := NewManager()
	defer m.Close()
	st, err := m.Stage(ctx, nil, loaded("2.0.0", svc.URL, "s3cret"))
	var pending *reconcile.PendingError
	if !errors.As(err, &pending) {
		t.Fatalf("stage = %v", err)
	}
	waiting := st.(reconcile.Waiting)
	if ready, _ := waiting.Ready(ctx); ready {
		t.Fatal("ready while the service serves 1.0.0")
	}
	svc.serve("2.0.0")
	if ready, _ := waiting.Ready(ctx); !ready {
		t.Fatal("not ready once the service serves 2.0.0")
	}
	st.Commit()
	if _, err := m.Client("acme.search"); err != nil {
		t.Fatal(err)
	}
}

func TestActivateVerifiesAndSignsCalls(t *testing.T) {
	allowLoopback(t)
	ctx := context.Background()
	svc := newService(t, "1.0.0", "s3cret")
	m := NewManager()
	defer m.Close()

	if err := reconcile.Activate(ctx, m, loaded("1.0.0", svc.URL, "wrong")); err == nil ||
		!strings.Contains(err.Error(), "not healthy") {
		t.Fatalf("wrong secret = %v", err)
	}
	var pending *reconcile.PendingError
	if err := reconcile.Activate(ctx, m, loaded("2.0.0", svc.URL, "s3cret")); !errors.As(err, &pending) ||
		!strings.Contains(err.Error(), "to serve 2.0.0 (it serves 1.0.0)") {
		t.Fatalf("other version = %v", err)
	}
	if err := reconcile.Activate(ctx, m, loaded("1.0.0", "", "s3cret")); err == nil {
		t.Fatal("no URL should fail")
	}
	if err := reconcile.Activate(ctx, m, loaded("1.0.0", svc.URL, "s3cret")); err != nil {
		t.Fatal(err)
	}
	c, err := m.Client("acme.search")
	if err != nil {
		t.Fatal(err)
	}
	var out pluginapi.SearchOutput
	if err := c.Call(ctx, pluginapi.SearchPath("web"), pluginapi.Envelope{}, pluginapi.SearchInput{Query: "q"},
		&out); err != nil {
		t.Fatal(err)
	}

	if err := m.Deactivate(ctx, "acme.search"); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Client("acme.search"); err == nil {
		t.Fatal("a removed plugin should have no client")
	}
}

func TestActivateRefusesPrivateAddresses(t *testing.T) {
	svc := newService(t, "1.0.0", "s3cret")
	m := NewManager()
	defer m.Close()
	err := reconcile.Activate(context.Background(), m, loaded("1.0.0", svc.URL, "s3cret"))
	if err == nil || !strings.Contains(err.Error(), "not allowed") {
		t.Fatalf("loopback without whitelist = %v", err)
	}
}

func TestSealedSecret(t *testing.T) {
	allowLoopback(t)
	t.Setenv("SYSTEM_AES_KEY", strings.Repeat("k", 32))
	sealed, err := utils.EncryptAESGCM("s3cret", utils.GetAESKey())
	if err != nil {
		t.Fatal(err)
	}
	svc := newService(t, "1.0.0", "s3cret")
	m := NewManager()
	defer m.Close()
	if err := reconcile.Activate(context.Background(), m, loaded("1.0.0", svc.URL, sealed)); err != nil {
		t.Fatal(err)
	}
}

func TestHealthChecksReportOutages(t *testing.T) {
	allowLoopback(t)
	svc := newService(t, "1.0.0", "s3cret")
	m := NewManager()
	m.interval = 10 * time.Millisecond
	rep := &reports{}
	m.SetReporter(rep)
	defer m.Close()
	if err := reconcile.Activate(context.Background(), m, loaded("1.0.0", svc.URL, "s3cret")); err != nil {
		t.Fatal(err)
	}

	svc.down.Store(true)
	waitFor(t, func() bool { return len(rep.seen()) == 1 })
	var pe *pluginapi.Error
	if _, err := m.Client("acme.search"); !errors.As(err, &pe) || !pe.Retryable {
		t.Fatalf("client while down = %v", err)
	}

	svc.down.Store(false)
	waitFor(t, func() bool { return len(rep.seen()) == 2 })
	if got := rep.seen(); got[0] || !got[1] {
		t.Fatalf("reports = %v, want degraded then healthy", got)
	}
	if _, err := m.Client("acme.search"); err != nil {
		t.Fatal(err)
	}
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal("timed out")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// A new version that does not check out leaves the running endpoint in
// place; one that does takes over only at the commit.
func TestStagedEndpointWaitsForTheCommit(t *testing.T) {
	allowLoopback(t)
	ctx := context.Background()
	v1, v2 := newService(t, "1.0.0", "s3cret"), newService(t, "2.0.0", "s3cret")
	m := NewManager()
	defer m.Close()
	running := loaded("1.0.0", v1.URL, "s3cret")
	if err := reconcile.Activate(ctx, m, running); err != nil {
		t.Fatal(err)
	}
	c1, _ := m.Client("acme.search")

	// The new version's service is not deployed yet: v1 still answers 1.0.0.
	if _, err := m.Stage(ctx, running, loaded("2.0.0", v1.URL, "s3cret")); err == nil {
		t.Fatal("want a version mismatch")
	}
	if c, err := m.Client("acme.search"); err != nil || c != c1 {
		t.Fatalf("a failed stage replaced the endpoint: %v", err)
	}

	staged, err := m.Stage(ctx, running, loaded("2.0.0", v2.URL, "s3cret"))
	if err != nil {
		t.Fatal(err)
	}
	if c, _ := m.Client("acme.search"); c != c1 {
		t.Fatal("a staged endpoint took calls before its commit")
	}
	staged.Commit()
	if c, err := m.Client("acme.search"); err != nil || c == c1 {
		t.Fatalf("the commit did not switch endpoints: %v", err)
	}
}

// A plugin that moves off its remote service keeps it until the old
// runtime retires, after every commit, and a remote plugin's switch leaves a service the
// kubernetes driver deployed to that driver.
func TestMovingAwayWithdrawsTheServiceAtTheCommit(t *testing.T) {
	allowLoopback(t)
	ctx := context.Background()
	svc := newService(t, "1.0.0", "s3cret")
	m := NewManager()
	defer m.Close()
	running := loaded("1.0.0", svc.URL, "s3cret")
	if err := reconcile.Activate(ctx, m, running); err != nil {
		t.Fatal(err)
	}
	onHost := loaded("2.0.0", "", "")
	onHost.Manifest.Runtime = manifest.Runtime{Type: manifest.RuntimeHost, Kind: "binary", Entry: "bin/x"}
	staged, err := m.Stage(ctx, running, onHost)
	if err != nil {
		t.Fatal(err)
	}
	if !m.Owns("acme.search") {
		t.Fatal("staging withdrew the running service")
	}
	staged.Commit()
	if !m.Owns("acme.search") {
		t.Fatal("the service was withdrawn before every activator committed")
	}
	staged.(reconcile.Retiring).Retire()
	if m.Owns("acme.search") {
		t.Fatal("retiring kept the remote service")
	}

	if err := m.ServeDeployed(ctx, running.Manifest, svc.URL, "s3cret"); err != nil {
		t.Fatal(err)
	}
	staged, _ = m.Stage(ctx, nil, onHost)
	staged.Commit()
	staged.(reconcile.Retiring).Retire()
	if !m.Owns("acme.search") {
		t.Fatal("a remote switch withdrew a deployed service")
	}
	m.WithdrawDeployed(ctx, "acme.search")
	if m.Owns("acme.search") {
		t.Fatal("WithdrawDeployed kept the deployed service")
	}
}
