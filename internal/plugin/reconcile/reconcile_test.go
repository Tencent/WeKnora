package reconcile

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/plugin/manifest"
	"github.com/Tencent/WeKnora/internal/plugin/pkg"
	"github.com/Tencent/WeKnora/internal/plugin/plugintest"
	"github.com/Tencent/WeKnora/internal/plugin/registry"
	"github.com/Tencent/WeKnora/internal/types"
)

// recorder is an Activator that logs its calls.
type recorder struct {
	calls []string
	fail  bool
}

func (a *recorder) Name() string { return "recorder" }

func (a *recorder) Activate(_ context.Context, l *Loaded) error {
	a.calls = append(a.calls, "activate "+l.Manifest.ID+"@"+l.Manifest.Version)
	if a.fail {
		return fmt.Errorf("boom")
	}
	return nil
}

func (a *recorder) Deactivate(_ context.Context, id string) error {
	a.calls = append(a.calls, "deactivate "+id)
	return nil
}

func TestReconcileLoadsUpgradesAndUnloads(t *testing.T) {
	ctx := context.Background()
	repo, store, reg, act := plugintest.NewMemRepo(), &plugintest.MemStore{}, registry.New(), &recorder{}
	r := New(Options{Repo: repo, Store: store, Registry: reg, CacheDir: t.TempDir(), Activators: []Activator{act}})

	plugintest.Install(t, repo, store, plugintest.KitPackage(t, "1.0.0"), types.PluginStateEnabled)
	if err := r.Reconcile(ctx); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if _, ok := reg.Resolve(manifest.PointSkills, "acme.kit/triage"); !ok {
		t.Fatal("skill contribution should be registered")
	}
	loaded := r.Loaded()
	if len(loaded) != 1 {
		t.Fatalf("loaded = %d", len(loaded))
	}
	skill, err := os.ReadFile(filepath.Join(loaded[0].Dir, "skills/triage/SKILL.md"))
	if err != nil || !strings.HasSuffix(string(skill), "1.0.0") {
		t.Fatalf("extracted skill = %q, %v", skill, err)
	}
	if s, _ := r.Status("acme.kit"); s.State != StateReady || s.Version != "1.0.0" {
		t.Fatalf("status = %+v", s)
	}

	// A second pass with nothing changed does nothing.
	_ = r.Reconcile(ctx)
	if len(act.calls) != 1 {
		t.Fatalf("calls = %v", act.calls)
	}

	plugintest.Install(t, repo, store, plugintest.KitPackage(t, "1.1.0"), types.PluginStateEnabled)
	if err := r.Reconcile(ctx); err != nil {
		t.Fatalf("Reconcile upgrade: %v", err)
	}
	if m, _ := reg.Plugin("acme.kit"); m.Version != "1.1.0" {
		t.Fatalf("registry has %s", m.Version)
	}

	row, _ := repo.GetPlugin(ctx, "acme.kit")
	row.DesiredState = types.PluginStateDisabled
	_ = repo.SavePlugin(ctx, row)
	if err := r.Reconcile(ctx); err != nil {
		t.Fatalf("Reconcile disable: %v", err)
	}
	if _, ok := reg.Plugin("acme.kit"); ok {
		t.Fatal("disabled plugin should be unregistered")
	}
	if _, ok := r.Status("acme.kit"); ok {
		t.Fatal("unloaded plugin should have no status")
	}
	want := "activate acme.kit@1.0.0,deactivate acme.kit,activate acme.kit@1.1.0,deactivate acme.kit"
	if got := strings.Join(act.calls, ","); got != want {
		t.Fatalf("calls = %s", got)
	}
}

func TestReconcileReportsFailures(t *testing.T) {
	ctx := context.Background()
	repo, store, reg := plugintest.NewMemRepo(), &plugintest.MemStore{}, registry.New()
	r := New(Options{Repo: repo, Store: store, Registry: reg, CacheDir: t.TempDir()})

	plugintest.Install(t, repo, store, plugintest.KitPackage(t, "1.0.0"), types.PluginStateEnabled)
	// Tamper with the stored package: the digest check must refuse it.
	for uri := range store.Blobs {
		store.Blobs[uri] = plugintest.KitPackage(t, "6.6.6")
	}
	err := r.Reconcile(ctx)
	if err == nil || !strings.Contains(err.Error(), "does not match") {
		t.Fatalf("want digest error, got %v", err)
	}
	if s, _ := r.Status("acme.kit"); s.State != StateFailed {
		t.Fatalf("status = %+v", s)
	}
	if _, ok := reg.Plugin("acme.kit"); ok {
		t.Fatal("a package that failed verification must not be registered")
	}
}

// A package the platform refuses (below its minimum trust) fails with the
// reason and is not fetched again every pass.
func TestAdmitRefusesPackages(t *testing.T) {
	ctx := context.Background()
	repo, store, reg, act := plugintest.NewMemRepo(), &plugintest.MemStore{}, registry.New(), &recorder{}
	calls := 0
	r := New(Options{
		Repo: repo, Store: store, Registry: reg, CacheDir: t.TempDir(), Activators: []Activator{act},
		Admit: func(*pkg.Package) error { calls++; return fmt.Errorf("below the minimum trust") },
	})
	plugintest.Install(t, repo, store, plugintest.KitPackage(t, "1.0.0"), types.PluginStateEnabled)
	if err := r.Reconcile(ctx); err == nil || !strings.Contains(err.Error(), "minimum trust") {
		t.Fatalf("want admit error, got %v", err)
	}
	if s, _ := r.Status("acme.kit"); s.State != StateFailed || !strings.Contains(s.Error, "minimum trust") {
		t.Fatalf("status = %+v", s)
	}
	if _, ok := reg.Plugin("acme.kit"); ok || len(act.calls) != 0 {
		t.Fatalf("a refused package was loaded: %v", act.calls)
	}
	_ = r.Reconcile(ctx)
	if calls != 1 {
		t.Fatalf("admit ran %d times; a refused version waits for a change", calls)
	}
}

// A plugin whose extracted files disappeared (a purged temp directory) is
// extracted and loaded again.
func TestReconcileReextractsMissingFiles(t *testing.T) {
	ctx := context.Background()
	repo, store, act := plugintest.NewMemRepo(), &plugintest.MemStore{}, &recorder{}
	r := New(Options{
		Repo: repo, Store: store, Registry: registry.New(), CacheDir: t.TempDir(), Activators: []Activator{act},
	})
	plugintest.Install(t, repo, store, plugintest.KitPackage(t, "1.0.0"), types.PluginStateEnabled)
	if err := r.Reconcile(ctx); err != nil {
		t.Fatal(err)
	}
	dir := r.Loaded()[0].Dir
	if err := os.RemoveAll(dir); err != nil {
		t.Fatal(err)
	}
	if err := r.Reconcile(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(r.Loaded()[0].Dir, "skills/triage/SKILL.md")); err != nil {
		t.Fatalf("files not extracted again: %v", err)
	}
	want := "activate acme.kit@1.0.0,deactivate acme.kit,activate acme.kit@1.0.0"
	if got := strings.Join(act.calls, ","); got != want {
		t.Fatalf("calls = %s", got)
	}
}

func TestActivatorFailureMarksPluginFailed(t *testing.T) {
	ctx := context.Background()
	repo, store := plugintest.NewMemRepo(), &plugintest.MemStore{}
	r := New(Options{
		Repo: repo, Store: store, Registry: registry.New(), CacheDir: t.TempDir(),
		Activators: []Activator{&recorder{fail: true}},
	})
	plugintest.Install(t, repo, store, plugintest.KitPackage(t, "1.0.0"), types.PluginStateEnabled)
	if err := r.Reconcile(ctx); err == nil || !strings.Contains(err.Error(), "recorder: boom") {
		t.Fatalf("want activator error, got %v", err)
	}
	if s, _ := r.Status("acme.kit"); s.State != StateFailed || !strings.Contains(s.Error, "boom") {
		t.Fatalf("status = %+v", s)
	}
}

// A plugin whose activation failed (a service that was down) is tried again
// with backoff, and loads once the cause is gone.
func TestFailedActivationIsRetriedWithBackoff(t *testing.T) {
	ctx := context.Background()
	repo, store, act := plugintest.NewMemRepo(), &plugintest.MemStore{}, &recorder{fail: true}
	r := New(Options{
		Repo: repo, Store: store, Registry: registry.New(), CacheDir: t.TempDir(), Activators: []Activator{act},
	})
	now := time.Unix(1000, 0)
	r.now = func() time.Time { return now }
	plugintest.Install(t, repo, store, plugintest.KitPackage(t, "1.0.0"), types.PluginStateEnabled)

	if err := r.Reconcile(ctx); err == nil {
		t.Fatal("want the activation error")
	}
	now = now.Add(retryFloor - time.Second)
	_ = r.Reconcile(ctx)
	if len(act.calls) != 1 {
		t.Fatalf("retried before the backoff: %v", act.calls)
	}
	now = now.Add(time.Second)
	_ = r.Reconcile(ctx)
	if len(act.calls) != 3 { // deactivate the half-loaded plugin, activate again
		t.Fatalf("calls = %v", act.calls)
	}
	now = now.Add(retryFloor)
	_ = r.Reconcile(ctx)
	if len(act.calls) != 3 {
		t.Fatalf("the second retry should wait twice as long: %v", act.calls)
	}

	act.fail = false
	now = now.Add(retryFloor)
	if err := r.Reconcile(ctx); err != nil {
		t.Fatal(err)
	}
	if s, _ := r.Status("acme.kit"); s.State != StateReady {
		t.Fatalf("status = %+v", s)
	}
	_ = r.Reconcile(ctx)
	if len(act.calls) != 5 {
		t.Fatalf("a loaded plugin should stay loaded: %v", act.calls)
	}

	// A failed plugin that is disabled is unloaded all the same.
	act.fail = true
	plugintest.Install(t, repo, store, plugintest.KitPackage(t, "1.1.0"), types.PluginStateEnabled)
	_ = r.Reconcile(ctx)
	row, _ := repo.GetPlugin(ctx, "acme.kit")
	row.DesiredState = types.PluginStateDisabled
	_ = repo.SavePlugin(ctx, row)
	_ = r.Reconcile(ctx)
	if len(r.Loaded()) != 0 {
		t.Fatal("a disabled plugin must be unloaded even if it failed")
	}
}

func TestRetryDelay(t *testing.T) {
	if retryDelay(1) != retryFloor || retryDelay(2) != 2*retryFloor || retryDelay(50) != retryCeiling {
		t.Fatalf("delays = %s %s %s", retryDelay(1), retryDelay(2), retryDelay(50))
	}
}

// A remote plugin moved to another URL, or given a new secret, is loaded
// again so its runtime picks the change up.
func TestReconcileReloadsOnRuntimeTargetChange(t *testing.T) {
	ctx := context.Background()
	repo, store, reg, act := plugintest.NewMemRepo(), &plugintest.MemStore{}, registry.New(), &recorder{}
	r := New(Options{Repo: repo, Store: store, Registry: reg, CacheDir: t.TempDir(), Activators: []Activator{act}})

	plugintest.Install(t, repo, store, plugintest.KitPackage(t, "1.0.0"), types.PluginStateEnabled)
	row, _ := repo.GetPlugin(ctx, "acme.kit")
	row.RemoteURL, row.RemoteSecret = "https://a.example.com", "s1"
	_ = repo.SavePlugin(ctx, row)
	if err := r.Reconcile(ctx); err != nil {
		t.Fatal(err)
	}
	if got := r.Loaded()[0].Installed.RemoteURL; got != "https://a.example.com" {
		t.Fatalf("Loaded carries URL %q", got)
	}
	_ = r.Reconcile(ctx)

	row.RemoteURL = "https://b.example.com"
	_ = repo.SavePlugin(ctx, row)
	_ = r.Reconcile(ctx)
	row.RemoteSecret = "s2"
	_ = repo.SavePlugin(ctx, row)
	_ = r.Reconcile(ctx)

	if len(act.calls) != 5 || act.calls[1] != "deactivate acme.kit" {
		t.Fatalf("calls = %v, want a reload per change", act.calls)
	}
	if got := r.Loaded()[0].Installed.RemoteSecret; got != "s2" {
		t.Fatalf("Loaded carries secret %q", got)
	}
}

// A standalone plugin host loads host plugins only.
func TestRuntimesLimitWhatANodeLoads(t *testing.T) {
	ctx := context.Background()
	repo, store, reg, act := plugintest.NewMemRepo(), &plugintest.MemStore{}, registry.New(), &recorder{}
	r := New(Options{
		Repo: repo, Store: store, Registry: reg, CacheDir: t.TempDir(), Activators: []Activator{act},
		Runtimes: []manifest.RuntimeType{manifest.RuntimeHost}, Role: "plugin-host",
	})
	plugintest.Install(t, repo, store, plugintest.KitPackage(t, "1.0.0"), types.PluginStateEnabled)
	if err := r.Reconcile(ctx); err != nil {
		t.Fatal(err)
	}
	if len(act.calls) != 0 || len(r.Loaded()) != 0 {
		t.Fatalf("a declarative plugin was loaded: %v", act.calls)
	}
	if !strings.HasPrefix(r.NodeName(), "plugin-host:") {
		t.Fatalf("node name = %s", r.NodeName())
	}

	// Accept filters on the manifest: nothing is loaded, nothing reported.
	r = New(Options{
		Repo: repo, Store: store, Registry: registry.New(), CacheDir: t.TempDir(), Activators: []Activator{act},
		Accept: func(m *manifest.Manifest) bool { return m.Version != "1.0.0" },
	})
	if err := r.Reconcile(ctx); err != nil || len(r.Loaded()) != 0 {
		t.Fatalf("an unaccepted plugin was loaded: %v", err)
	}
	if _, ok := r.Status("acme.kit"); ok {
		t.Fatal("an unaccepted plugin has no status here")
	}
}

// inPlace is a runtime that swaps versions itself.
type inPlace struct{ recorder }

func (a *inPlace) ActivatesInPlace() {}

// kitRuntime is acme.kit at a version running in the given runtime.
func kitRuntime(t *testing.T, version, runtime string) []byte {
	apiVersion := ""
	if runtime != "declarative" {
		apiVersion = "apiVersion: weknora.plugin/v1\n"
	}
	return plugintest.Zip(t, map[string]string{
		"plugin.yaml": "schemaVersion: 1\nid: acme.kit\nversion: " + version + "\n" + apiVersion +
			"name: { en-US: ACME Kit }\npublisher: { id: acme }\nruntime: { type: " + runtime + " }\n" +
			"contributes:\n  skills:\n    - { id: triage, name: Triage, path: skills/triage }\n",
		"skills/triage/SKILL.md": "---\nname: triage\ndescription: Triage issues by severity.\n---\n" + version,
	})
}

// A runtime swaps an upgrade in place, but a version that moves to another
// runtime first stops the previous one wherever it ran.
func TestRuntimeSwitchDeactivatesInPlaceActivators(t *testing.T) {
	ctx := context.Background()
	repo, store, rt := plugintest.NewMemRepo(), &plugintest.MemStore{}, &inPlace{}
	r := New(Options{
		Repo: repo, Store: store, Registry: registry.New(), CacheDir: t.TempDir(), Activators: []Activator{rt},
	})
	plugintest.Install(t, repo, store, kitRuntime(t, "1.0.0", "remote"), types.PluginStateEnabled)
	_ = r.Reconcile(ctx)
	plugintest.Install(t, repo, store, kitRuntime(t, "1.1.0", "remote"), types.PluginStateEnabled)
	_ = r.Reconcile(ctx)
	plugintest.Install(t, repo, store, kitRuntime(t, "2.0.0", "declarative"), types.PluginStateEnabled)
	if err := r.Reconcile(ctx); err != nil {
		t.Fatal(err)
	}
	want := "activate acme.kit@1.0.0,activate acme.kit@1.1.0,deactivate acme.kit,activate acme.kit@2.0.0"
	if got := strings.Join(rt.calls, ","); got != want {
		t.Fatalf("calls = %s", got)
	}
}

// pending is a runtime that finishes starting in the background.
type pending struct {
	recorder
	report func(bool, error)
}

func (a *pending) Activate(ctx context.Context, l *Loaded) error {
	_ = a.recorder.Activate(ctx, l)
	a.report = l.Report
	return Pending(fmt.Errorf("rolling out"))
}

// A pending activation loads the plugin as degraded, is not retried, and
// becomes ready when the activator reports back.
func TestPendingActivation(t *testing.T) {
	ctx := context.Background()
	repo, store, act := plugintest.NewMemRepo(), &plugintest.MemStore{}, &pending{}
	r := New(Options{
		Repo: repo, Store: store, Registry: registry.New(), CacheDir: t.TempDir(), Activators: []Activator{act},
	})
	plugintest.Install(t, repo, store, plugintest.KitPackage(t, "1.0.0"), types.PluginStateEnabled)
	if err := r.Reconcile(ctx); err != nil {
		t.Fatalf("a pending activation is not a failure: %v", err)
	}
	if s, _ := r.Status("acme.kit"); s.State != StateDegraded || !strings.Contains(s.Error, "rolling out") {
		t.Fatalf("status = %+v", s)
	}
	_ = r.Reconcile(ctx)
	if len(act.calls) != 1 {
		t.Fatalf("a pending plugin was activated again: %v", act.calls)
	}
	act.report(true, nil)
	if s, _ := r.Status("acme.kit"); s.State != StateReady {
		t.Fatalf("status after the report = %+v", s)
	}

	// A report from a version that is gone changes nothing.
	stale := act.report
	plugintest.Install(t, repo, store, plugintest.KitPackage(t, "1.1.0"), types.PluginStateEnabled)
	_ = r.Reconcile(ctx)
	stale(true, nil)
	if s, _ := r.Status("acme.kit"); s.State != StateDegraded || s.Version != "1.1.0" {
		t.Fatalf("status after a stale report = %+v", s)
	}
}
