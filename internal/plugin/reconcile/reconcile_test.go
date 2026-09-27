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
	// failVersion fails staging that version only.
	failVersion string
	// log, when set, gets the calls of several recorders in order.
	log  *[]string
	name string
}

func (a *recorder) Name() string {
	if a.name != "" {
		return a.name
	}
	return "recorder"
}

func (a *recorder) record(call string) {
	a.calls = append(a.calls, call)
	if a.log != nil {
		*a.log = append(*a.log, a.Name()+" "+call)
	}
}

func (a *recorder) Stage(_ context.Context, _, next *Loaded) (Staged, error) {
	v := next.Manifest.ID + "@" + next.Manifest.Version
	a.record("stage " + v)
	if a.fail || next.Manifest.Version == a.failVersion {
		return nil, fmt.Errorf("boom")
	}
	return Swap{
		OnCommit: func() { a.record("commit " + v) },
		OnAbort:  func() { a.record("abort " + v) },
	}, nil
}

func (a *recorder) Deactivate(_ context.Context, id string) error {
	a.record("deactivate " + id)
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
	if len(act.calls) != 2 {
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
	want := "stage acme.kit@1.0.0,commit acme.kit@1.0.0,stage acme.kit@1.1.0,commit acme.kit@1.1.0,deactivate acme.kit"
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
	want := "stage acme.kit@1.0.0,commit acme.kit@1.0.0,stage acme.kit@1.0.0,commit acme.kit@1.0.0"
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
	if _, ok := r.registry.Plugin("acme.kit"); ok || len(r.Loaded()) != 0 {
		t.Fatal("a plugin that failed to stage must not be half loaded")
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
	if len(act.calls) != 2 {
		t.Fatalf("calls = %v", act.calls)
	}
	now = now.Add(retryFloor)
	_ = r.Reconcile(ctx)
	if len(act.calls) != 2 {
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
	if len(act.calls) != 4 {
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

	if len(act.calls) != 6 || act.calls[2] != "stage acme.kit@1.0.0" {
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

// A version that moves to another runtime is staged like any upgrade: the
// runtimes swap at the commit, and nothing is deactivated first.
func TestRuntimeSwitchIsStaged(t *testing.T) {
	ctx := context.Background()
	repo, store, rt := plugintest.NewMemRepo(), &plugintest.MemStore{}, &recorder{}
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
	want := "stage acme.kit@1.0.0,commit acme.kit@1.0.0,stage acme.kit@1.1.0,commit acme.kit@1.1.0," +
		"stage acme.kit@2.0.0,commit acme.kit@2.0.0"
	if got := strings.Join(rt.calls, ","); got != want {
		t.Fatalf("calls = %s", got)
	}
}

// pending is a runtime that finishes starting in the background.
type pending struct {
	recorder
	report func(bool, error)
	ready  bool
}

func (a *pending) Stage(ctx context.Context, prev, next *Loaded) (Staged, error) {
	s, _ := a.recorder.Stage(ctx, prev, next)
	a.report, a.ready = next.Report, false
	return &waitStage{Staged: s, a: a}, Pending(fmt.Errorf("rolling out"))
}

type waitStage struct {
	Staged
	a *pending
}

func (w *waitStage) Ready(context.Context) (bool, error) { return w.a.ready, nil }

// A pending first load loads the plugin as degraded, is not retried, and
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
	if len(act.calls) != 2 {
		t.Fatalf("a pending plugin was activated again: %v", act.calls)
	}
	act.report(true, nil)
	if s, _ := r.Status("acme.kit"); s.State != StateReady {
		t.Fatalf("status after the report = %+v", s)
	}
}

// A pending upgrade waits: the previous version serves until the new one
// is ready, then it commits; a report from the previous version, or news
// that the new one is still starting, changes nothing.
func TestPendingUpgradeWaitsForTheRuntime(t *testing.T) {
	ctx := context.Background()
	reg := registry.New()
	repo, store, act := plugintest.NewMemRepo(), &plugintest.MemStore{}, &pending{}
	r := New(Options{Repo: repo, Store: store, Registry: reg, CacheDir: t.TempDir(), Activators: []Activator{act}})
	plugintest.Install(t, repo, store, plugintest.KitPackage(t, "1.0.0"), types.PluginStateEnabled)
	_ = r.Reconcile(ctx)
	act.ready = true
	act.report(true, nil)
	stale := act.report

	plugintest.Install(t, repo, store, plugintest.KitPackage(t, "1.1.0"), types.PluginStateEnabled)
	if err := r.Reconcile(ctx); err != nil {
		t.Fatalf("a pending upgrade is not a failure: %v", err)
	}
	s, _ := r.Status("acme.kit")
	if s.State != StateReady || s.Version != "1.0.0" ||
		s.UpgradeVersion != "1.1.0" || s.UpgradeState != UpgradePending {
		t.Fatalf("status while pending = %+v", s)
	}
	if m, _ := reg.Plugin("acme.kit"); m.Version != "1.0.0" {
		t.Fatalf("registry has %s while the upgrade waits", m.Version)
	}
	stale(true, nil)
	act.report(false, fmt.Errorf("image pull backoff"))
	_ = r.Reconcile(ctx)
	r.CheckStaging(ctx)
	if s, _ := r.Status("acme.kit"); s.Version != "1.0.0" || !strings.Contains(s.UpgradeError, "image pull") {
		t.Fatalf("status while still pending = %+v", s)
	}
	if got := strings.Join(act.calls, ","); got != "stage acme.kit@1.0.0,commit acme.kit@1.0.0,stage acme.kit@1.1.0" {
		t.Fatalf("calls = %s", got)
	}

	act.ready = true
	act.report(true, nil)
	s, _ = r.Status("acme.kit")
	if s.State != StateReady || s.Version != "1.1.0" || s.UpgradeVersion != "" {
		t.Fatalf("status after the upgrade = %+v", s)
	}
	if m, _ := reg.Plugin("acme.kit"); m.Version != "1.1.0" {
		t.Fatalf("registry has %s after the upgrade", m.Version)
	}
}

// A pending upgrade the platform rolls back, or replaces with another
// version, is aborted; the one serving stays.
func TestPendingUpgradeIsAbortedByARollback(t *testing.T) {
	ctx := context.Background()
	repo, store, act := plugintest.NewMemRepo(), &plugintest.MemStore{}, &pending{}
	r := New(Options{
		Repo: repo, Store: store, Registry: registry.New(), CacheDir: t.TempDir(), Activators: []Activator{act},
	})
	plugintest.Install(t, repo, store, plugintest.KitPackage(t, "1.0.0"), types.PluginStateEnabled)
	_ = r.Reconcile(ctx)
	plugintest.Install(t, repo, store, plugintest.KitPackage(t, "1.1.0"), types.PluginStateEnabled)
	_ = r.Reconcile(ctx)

	row, _ := repo.GetPlugin(ctx, "acme.kit")
	row.ActiveVersion = "1.0.0"
	_ = repo.SavePlugin(ctx, row)
	_ = r.Reconcile(ctx)
	want := "stage acme.kit@1.0.0,commit acme.kit@1.0.0,stage acme.kit@1.1.0,abort acme.kit@1.1.0"
	if got := strings.Join(act.calls, ","); got != want {
		t.Fatalf("calls = %s", got)
	}
	if s, _ := r.Status("acme.kit"); s.Version != "1.0.0" || s.UpgradeVersion != "" {
		t.Fatalf("status after the rollback = %+v", s)
	}
	act.ready = true
	r.CheckStaging(ctx)
	if l := r.Loaded(); l[0].Manifest.Version != "1.0.0" {
		t.Fatalf("an aborted upgrade committed: %s", l[0].Manifest.Version)
	}
}

// An upgrade that fails to stage leaves the running version serving: the
// activators that staged it abort, nothing is deactivated, and the status
// reports the version that failed until it loads or is rolled back.
func TestFailedUpgradeKeepsThePreviousVersion(t *testing.T) {
	ctx := context.Background()
	var log []string
	runtime := &recorder{name: "runtime", failVersion: "1.1.0", log: &log}
	domain := &recorder{name: "domain", log: &log}
	repo, store, reg := plugintest.NewMemRepo(), &plugintest.MemStore{}, registry.New()
	r := New(Options{
		Repo: repo, Store: store, Registry: reg, CacheDir: t.TempDir(), Activators: []Activator{runtime, domain},
	})
	now := time.Unix(1000, 0)
	r.now = func() time.Time { return now }

	plugintest.Install(t, repo, store, plugintest.KitPackage(t, "1.0.0"), types.PluginStateEnabled)
	if err := r.Reconcile(ctx); err != nil {
		t.Fatal(err)
	}
	// Domains stage first; runtimes commit first.
	want := "domain stage acme.kit@1.0.0,runtime stage acme.kit@1.0.0," +
		"runtime commit acme.kit@1.0.0,domain commit acme.kit@1.0.0"
	if got := strings.Join(log, ","); got != want {
		t.Fatalf("calls = %s", got)
	}

	log = nil
	plugintest.Install(t, repo, store, plugintest.KitPackage(t, "1.1.0"), types.PluginStateEnabled)
	err := r.Reconcile(ctx)
	if err == nil || !strings.Contains(err.Error(), "previous version keeps running") {
		t.Fatalf("want an upgrade error, got %v", err)
	}
	want = "domain stage acme.kit@1.1.0,runtime stage acme.kit@1.1.0,domain abort acme.kit@1.1.0"
	if got := strings.Join(log, ","); got != want {
		t.Fatalf("calls = %s", got)
	}
	if m, _ := reg.Plugin("acme.kit"); m.Version != "1.0.0" {
		t.Fatalf("registry has %s", m.Version)
	}
	if l := r.Loaded(); len(l) != 1 || l[0].Manifest.Version != "1.0.0" {
		t.Fatalf("loaded = %+v", l)
	}
	s, _ := r.Status("acme.kit")
	if s.State != StateReady || s.Version != "1.0.0" || s.UpgradeVersion != "1.1.0" ||
		!strings.Contains(s.UpgradeError, "runtime: boom") {
		t.Fatalf("status = %+v", s)
	}

	// Tried again after the backoff, still failing, still serving 1.0.0.
	log = nil
	now = now.Add(retryFloor)
	_ = r.Reconcile(ctx)
	if len(log) != 3 {
		t.Fatalf("calls = %v", log)
	}
	if s, _ := r.Status("acme.kit"); s.Version != "1.0.0" || s.UpgradeVersion != "1.1.0" {
		t.Fatalf("status = %+v", s)
	}

	// Rolled back: nothing to load, and the failed upgrade is forgotten.
	log = nil
	row, _ := repo.GetPlugin(ctx, "acme.kit")
	row.ActiveVersion = "1.0.0"
	_ = repo.SavePlugin(ctx, row)
	if err := r.Reconcile(ctx); err != nil {
		t.Fatal(err)
	}
	if len(log) != 0 {
		t.Fatalf("calls = %v", log)
	}
	if s, _ := r.Status("acme.kit"); s.State != StateReady || s.UpgradeVersion != "" || s.UpgradeError != "" {
		t.Fatalf("status = %+v", s)
	}

	// Upgraded again once the cause is fixed.
	runtime.failVersion = ""
	row.ActiveVersion = "1.1.0"
	_ = repo.SavePlugin(ctx, row)
	if err := r.Reconcile(ctx); err != nil {
		t.Fatal(err)
	}
	if s, _ := r.Status("acme.kit"); s.Version != "1.1.0" || s.UpgradeVersion != "" {
		t.Fatalf("status = %+v", s)
	}
}

// A new version whose package cannot be read leaves the running one too.
func TestUnreadableUpgradeKeepsThePreviousVersion(t *testing.T) {
	ctx := context.Background()
	repo, store, reg, act := plugintest.NewMemRepo(), &plugintest.MemStore{}, registry.New(), &recorder{}
	r := New(Options{Repo: repo, Store: store, Registry: reg, CacheDir: t.TempDir(), Activators: []Activator{act}})
	plugintest.Install(t, repo, store, plugintest.KitPackage(t, "1.0.0"), types.PluginStateEnabled)
	if err := r.Reconcile(ctx); err != nil {
		t.Fatal(err)
	}
	plugintest.Install(t, repo, store, plugintest.KitPackage(t, "1.1.0"), types.PluginStateEnabled)
	v, _ := repo.GetVersion(ctx, "acme.kit", "1.1.0")
	store.Blobs[v.PackageURI] = plugintest.KitPackage(t, "6.6.6")
	if err := r.Reconcile(ctx); err == nil || !strings.Contains(err.Error(), "does not match") {
		t.Fatalf("want a digest error, got %v", err)
	}
	if s, _ := r.Status("acme.kit"); s.State != StateReady || s.Version != "1.0.0" || s.UpgradeVersion != "1.1.0" {
		t.Fatalf("status = %+v", s)
	}
	if len(act.calls) != 2 {
		t.Fatalf("calls = %v", act.calls)
	}
}

// retiring is a runtime that retires what it no longer runs.
type retiring struct{ recorder }

func (a *retiring) Stage(ctx context.Context, prev, next *Loaded) (Staged, error) {
	s, err := a.recorder.Stage(ctx, prev, next)
	if err != nil {
		return nil, err
	}
	v := next.Manifest.Version
	return Swap{OnCommit: s.Commit, OnAbort: s.Abort, OnRetire: func() { a.record("retire before " + v) }}, nil
}

// Runtimes retire what they no longer run only after every activator
// committed, so the runtime a plugin moved to has taken over by then.
func TestRetiringFollowsEveryCommit(t *testing.T) {
	ctx := context.Background()
	var log []string
	a := &retiring{recorder{name: "a", log: &log}}
	b := &retiring{recorder{name: "b", log: &log}}
	repo, store := plugintest.NewMemRepo(), &plugintest.MemStore{}
	r := New(Options{
		Repo: repo, Store: store, Registry: registry.New(), CacheDir: t.TempDir(), Activators: []Activator{a, b},
	})
	plugintest.Install(t, repo, store, plugintest.KitPackage(t, "1.0.0"), types.PluginStateEnabled)
	if err := r.Reconcile(ctx); err != nil {
		t.Fatal(err)
	}
	want := "b stage acme.kit@1.0.0,a stage acme.kit@1.0.0,a commit acme.kit@1.0.0,b commit acme.kit@1.0.0," +
		"a retire before 1.0.0,b retire before 1.0.0"
	if got := strings.Join(log, ","); got != want {
		t.Fatalf("calls = %s", got)
	}
}

// flakyRepo fails reading versions while down.
type flakyRepo struct {
	*plugintest.MemRepo
	down bool
}

func (r *flakyRepo) GetVersion(ctx context.Context, id, version string) (*types.PluginVersion, error) {
	if r.down {
		return nil, fmt.Errorf("database is down")
	}
	return r.MemRepo.GetVersion(ctx, id, version)
}

// A loaded plugin whose active version cannot be read (a database error, a
// pass whose request went away) keeps serving and keeps its status, which
// runtime reports go on updating.
func TestFailedCheckKeepsALoadedPluginServing(t *testing.T) {
	ctx := context.Background()
	repo, store, act := &flakyRepo{MemRepo: plugintest.NewMemRepo()}, &plugintest.MemStore{}, &recorder{}
	r := New(Options{
		Repo: repo, Store: store, Registry: registry.New(), CacheDir: t.TempDir(), Activators: []Activator{act},
	})
	plugintest.Install(t, repo.MemRepo, store, plugintest.KitPackage(t, "1.0.0"), types.PluginStateEnabled)
	if err := r.Reconcile(ctx); err != nil {
		t.Fatal(err)
	}
	repo.down = true
	if err := r.Reconcile(ctx); err == nil || !strings.Contains(err.Error(), "keeps running") {
		t.Fatalf("want the check error, got %v", err)
	}
	if s, _ := r.Status("acme.kit"); s.State != StateReady || s.Version != "1.0.0" {
		t.Fatalf("status after a failed check = %+v", s)
	}
	r.ReportRuntime("acme.kit", false, fmt.Errorf("crashed"))
	if s, _ := r.Status("acme.kit"); s.State != StateDegraded {
		t.Fatalf("a runtime report after a failed check was ignored: %+v", s)
	}
	if len(r.Loaded()) != 1 || len(act.calls) != 2 {
		t.Fatalf("loaded %d, calls %v", len(r.Loaded()), act.calls)
	}

	// A failed status left over from before heals once the plugin checks
	// out again.
	repo.down = false
	r.setStatus("acme.kit", Status{Version: "1.0.0", State: StateFailed, Error: "stale"})
	if err := r.Reconcile(ctx); err != nil {
		t.Fatal(err)
	}
	if s, _ := r.Status("acme.kit"); s.State != StateReady || s.Error != "" {
		t.Fatalf("a stale failed status stayed: %+v", s)
	}
}

// A plugin not loaded yet whose version cannot be read fails as before.
func TestFailedCheckOfANewPluginFails(t *testing.T) {
	ctx := context.Background()
	repo, store := &flakyRepo{MemRepo: plugintest.NewMemRepo(), down: true}, &plugintest.MemStore{}
	r := New(Options{Repo: repo, Store: store, Registry: registry.New(), CacheDir: t.TempDir()})
	plugintest.Install(t, repo.MemRepo, store, plugintest.KitPackage(t, "1.0.0"), types.PluginStateEnabled)
	_ = r.Reconcile(ctx)
	if s, _ := r.Status("acme.kit"); s.State != StateFailed {
		t.Fatalf("status = %+v", s)
	}
}
