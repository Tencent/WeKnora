package remote

import (
	"bytes"
	"context"
	"errors"
	"io"
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
	down atomic.Bool
}

func newService(t *testing.T, version, secret string) *service {
	t.Helper()
	p := pluginsdk.New(pluginsdk.Info{ID: "acme.search", Version: version})
	p.WebSearch("web", pluginsdk.WebSearchFunc(
		func(context.Context, *pluginsdk.Call, pluginapi.SearchInput) (*pluginapi.SearchOutput, error) {
			return &pluginapi.SearchOutput{}, nil
		}))
	h := p.Handler()
	s := &service{}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.down.Load() {
			http.Error(w, "down", http.StatusBadGateway)
			return
		}
		body, _ := io.ReadAll(r.Body)
		err := pluginapi.VerifySignature([]byte(secret), r.Header.Get(pluginapi.TimestampHeader),
			r.Header.Get(pluginapi.SignatureHeader), body, time.Now())
		if err != nil {
			http.Error(w, err.Error(), http.StatusUnauthorized)
			return
		}
		r.Body = io.NopCloser(bytes.NewReader(body))
		h.ServeHTTP(w, r)
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

func TestActivateVerifiesAndSignsCalls(t *testing.T) {
	allowLoopback(t)
	ctx := context.Background()
	svc := newService(t, "1.0.0", "s3cret")
	m := NewManager()
	defer m.Close()

	if err := m.Activate(ctx, loaded("1.0.0", svc.URL, "wrong")); err == nil ||
		!strings.Contains(err.Error(), "not healthy") {
		t.Fatalf("wrong secret = %v", err)
	}
	if err := m.Activate(ctx, loaded("2.0.0", svc.URL, "s3cret")); err == nil ||
		!strings.Contains(err.Error(), "the package is acme.search@2.0.0") {
		t.Fatalf("other version = %v", err)
	}
	if err := m.Activate(ctx, loaded("1.0.0", "", "s3cret")); err == nil {
		t.Fatal("no URL should fail")
	}
	if err := m.Activate(ctx, loaded("1.0.0", svc.URL, "s3cret")); err != nil {
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
	err := m.Activate(context.Background(), loaded("1.0.0", svc.URL, "s3cret"))
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
	if err := m.Activate(context.Background(), loaded("1.0.0", svc.URL, sealed)); err != nil {
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
	if err := m.Activate(context.Background(), loaded("1.0.0", svc.URL, "s3cret")); err != nil {
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
