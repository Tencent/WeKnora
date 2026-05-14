package doctor

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/cli/internal/cmdutil"
	"github.com/Tencent/WeKnora/cli/internal/compat"
	"github.com/Tencent/WeKnora/cli/internal/iostreams"
	"github.com/Tencent/WeKnora/cli/internal/secrets"
	sdk "github.com/Tencent/WeKnora/client"
)

// withCredStoreFactory swaps the package-level credStoreFactory hook for the
// duration of t. Tests that assert the credential_storage outcome use this
// rather than touching the real OS keyring (which is environment-dependent
// and would make CI flake on macOS vs Linux vs WSL). Restored in t.Cleanup.
//
// The hook intentionally lives at package scope (rather than as a runChecks
// parameter) so the production call site stays a zero-arg function — keeping
// the lazy-resolve buildServices contract unchanged (round-4 fix).
func withCredStoreFactory(t *testing.T, fn func() (secrets.Store, error)) {
	t.Helper()
	saved := credStoreFactory
	credStoreFactory = fn
	t.Cleanup(func() { credStoreFactory = saved })
}

type fakeServices struct {
	systemInfo     *sdk.SystemInfo
	systemErr      error
	userResp       *sdk.CurrentUserResponse
	userErr        error
	pingErr        error
	systemInfoHits atomic.Int32 // count GetSystemInfo invocations
}

func (f *fakeServices) GetSystemInfo(ctx context.Context) (*sdk.SystemInfo, error) {
	f.systemInfoHits.Add(1)
	return f.systemInfo, f.systemErr
}
func (f *fakeServices) GetCurrentUser(ctx context.Context) (*sdk.CurrentUserResponse, error) {
	return f.userResp, f.userErr
}
func (f *fakeServices) PingBaseURL(ctx context.Context) error { return f.pingErr }

func goodUserResp() *sdk.CurrentUserResponse {
	r := &sdk.CurrentUserResponse{}
	r.Data.User = &sdk.AuthUser{ID: "u1"}
	return r
}

func TestDoctor_AllOK(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	out, _ := iostreams.SetForTest(t)
	// Pin credential_storage to ok so this test doesn't probe the host OS
	// keyring — macOS dev machines have Keychain, Linux CI without libsecret
	// would otherwise warn and break the AllPassed assertion.
	withCredStoreFactory(t, func() (secrets.Store, error) {
		return secrets.NewMemStore(), nil
	})
	svc := &fakeServices{
		systemInfo: &sdk.SystemInfo{Version: "1.0.0"},
		userResp:   goodUserResp(),
	}
	r := runChecks(context.Background(), &Options{}, svc, "1.0.0")
	if !r.Summary.AllPassed {
		t.Errorf("expected all_passed, got summary %+v", r.Summary)
	}
	if r.Summary.Passed != 4 {
		t.Errorf("expected Passed=4 (one per check), got %+v", r.Summary)
	}
	if r.Summary.Failed != 0 || r.Summary.Skipped != 0 {
		t.Errorf("expected 0 fail / 0 skip, got %+v", r.Summary)
	}
	emit(&cmdutil.JSONOptions{}, r)
	if !strings.Contains(out.String(), `"all_passed":true`) {
		t.Errorf("bare output should embed all_passed=true, got %q", out.String())
	}
}

func TestDoctor_BaseURLFails_DownstreamSkip(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	_, _ = iostreams.SetForTest(t)
	svc := &fakeServices{
		pingErr:    errors.New("connect refused"),
		userResp:   goodUserResp(),
		systemInfo: &sdk.SystemInfo{Version: "1.0.0"},
	}
	r := runChecks(context.Background(), &Options{}, svc, "1.0.0")
	if r.Summary.Skipped != 2 {
		t.Errorf("expected 2 skipped (auth_credential + server_version), got %d", r.Summary.Skipped)
	}
	if r.Checks[0].Status != StatusFail {
		t.Errorf("base_url_reachable status = %q, want fail", r.Checks[0].Status)
	}
	if !strings.Contains(r.Checks[0].Hint, "auth login") {
		t.Errorf("base_url fail hint should reference `auth login`, got %q", r.Checks[0].Hint)
	}
	if r.Checks[1].Status != StatusSkip {
		t.Errorf("auth_credential status = %q, want skip", r.Checks[1].Status)
	}
	if r.Checks[2].Status != StatusSkip {
		t.Errorf("server_version status = %q, want skip", r.Checks[2].Status)
	}
	// credential_storage 与网络无关,应该独立运行(不受 base_url fail 影响)
	if r.Checks[3].Name != "credential_storage" {
		t.Errorf("Checks[3] = %q, want credential_storage", r.Checks[3].Name)
	}
}

func TestDoctor_Offline_OnlyKeyringChecked(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	_, _ = iostreams.SetForTest(t)
	svc := &fakeServices{}
	r := runChecks(context.Background(), &Options{Offline: true}, svc, "1.0.0")
	if r.Summary.Skipped < 3 {
		t.Errorf("expected >=3 skip in offline mode, got %d", r.Summary.Skipped)
	}
	last := r.Checks[3]
	if last.Name != "credential_storage" {
		t.Errorf("last check = %q, want credential_storage", last.Name)
	}
	if last.Status == StatusSkip {
		t.Error("credential_storage should NOT be skipped even in offline mode")
	}
}

func TestDoctor_AuthFails_VersionSkipped(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	_, _ = iostreams.SetForTest(t)
	svc := &fakeServices{
		userErr:    errors.New("HTTP error 401: unauthenticated"),
		systemInfo: &sdk.SystemInfo{Version: "1.0.0"},
	}
	r := runChecks(context.Background(), &Options{}, svc, "1.0.0")
	if r.Checks[0].Status != StatusOK {
		t.Errorf("base_url should pass, got %q", r.Checks[0].Status)
	}
	if r.Checks[1].Status != StatusFail {
		t.Errorf("auth_credential should fail, got %q", r.Checks[1].Status)
	}
	if r.Checks[2].Status != StatusSkip {
		t.Errorf("server_version should skip due to auth fail, got %q", r.Checks[2].Status)
	}
}

func TestDoctor_CacheHit_SkipsProbe(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	_, _ = iostreams.SetForTest(t)
	// Pre-populate fresh cache
	if err := compat.SaveCache(&compat.Info{ServerVersion: "1.0.0", ProbedAt: time.Now()}); err != nil {
		t.Fatalf("seed cache: %v", err)
	}
	svc := &fakeServices{userResp: goodUserResp()}
	r := runChecks(context.Background(), &Options{}, svc, "1.0.0")
	if r.Checks[2].Status != StatusOK {
		t.Errorf("server_version should be ok via cache, got %q (%s)", r.Checks[2].Status, r.Checks[2].Details)
	}
	if svc.systemInfoHits.Load() != 0 {
		t.Errorf("expected 0 GetSystemInfo calls (cache hit), got %d", svc.systemInfoHits.Load())
	}
	if !strings.Contains(r.Checks[2].Details, "cached") {
		t.Errorf("details should mention cache, got %q", r.Checks[2].Details)
	}
}

// TestDoctor_NoConfig_StillRunsCredentialStorage guards the package-doc
// promise that credential_storage runs even with zero configuration. Round-4
// reviewer surfaced that buildServices used to abort on f.Client() failure,
// silently violating the doc for any first-time user running `weknora doctor`
// to diagnose setup. The lazy-resolve fix means missing auth surfaces as
// auth_credential=fail, not a top-level command exit.
func TestDoctor_NoConfig_StillRunsCredentialStorage(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	_, _ = iostreams.SetForTest(t)
	// Pin credential_storage to ok regardless of host keyring presence
	// (Linux CI without libsecret would otherwise warn).
	withCredStoreFactory(t, func() (secrets.Store, error) {
		return secrets.NewMemStore(), nil
	})

	f := cmdutil.New()
	svc, err := buildServices(f)
	if err != nil {
		t.Fatalf("buildServices must succeed even with no config; got: %v", err)
	}
	r := runChecks(context.Background(), &Options{}, svc, "1.0.0")

	// All 4 checks must run (no early-exit). Last one is credential_storage and
	// has no network/auth dependency, so it must report ok.
	if got := len(r.Checks); got != 4 {
		t.Fatalf("expected 4 checks executed, got %d", got)
	}
	if r.Checks[3].Name != "credential_storage" {
		t.Errorf("Checks[3] = %q, want credential_storage", r.Checks[3].Name)
	}
	if r.Checks[3].Status != StatusOK {
		t.Errorf("credential_storage must run / pass even without auth, got %q (%s)",
			r.Checks[3].Status, r.Checks[3].Details)
	}
	// base_url_reachable fails (no host); cascade then skips auth_credential
	// and server_version. Whether auth/version are skip vs fail is an internal
	// cascade detail; the user-visible promise is that 4 checks executed and
	// the independent credential_storage one passed.
	if r.Checks[0].Status != StatusFail {
		t.Errorf("base_url must fail without host, got %q", r.Checks[0].Status)
	}
	if r.Checks[1].Status == StatusOK {
		t.Errorf("auth_credential must NOT report ok without config, got %q", r.Checks[1].Status)
	}
}

func TestDoctor_NoCache_BypassesCache(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	_, _ = iostreams.SetForTest(t)
	// Pre-populate fresh cache; --no-cache should ignore it
	if err := compat.SaveCache(&compat.Info{ServerVersion: "9.9.9", ProbedAt: time.Now()}); err != nil {
		t.Fatalf("seed cache: %v", err)
	}
	svc := &fakeServices{
		userResp:   goodUserResp(),
		systemInfo: &sdk.SystemInfo{Version: "1.0.0"},
	}
	r := runChecks(context.Background(), &Options{NoCache: true}, svc, "1.0.0")
	if svc.systemInfoHits.Load() != 1 {
		t.Errorf("expected 1 GetSystemInfo call (--no-cache), got %d", svc.systemInfoHits.Load())
	}
	if !strings.Contains(r.Checks[2].Details, "1.0.0") {
		t.Errorf("details should reflect probed version 1.0.0 not cached 9.9.9, got %q", r.Checks[2].Details)
	}
}

// TestDoctor_VersionSkewWarns covers the v0.2 soft-skew path: server is older
// than CLI by ≥ 1 minor (same major, in compat range) → server_version=warn,
// envelope.ok stays true, all_passed=false (so agents reading just the
// boolean still notice). The compat decision lives in cli/internal/compat;
// this test pins the doctor-side mapping (compat.SoftWarn → StatusWarn).
func TestDoctor_VersionSkewWarns(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	_, _ = iostreams.SetForTest(t)
	// Force keyring path to ok so credential_storage doesn't itself warn —
	// we want this test to assert ONLY on server_version.
	withCredStoreFactory(t, func() (secrets.Store, error) { return secrets.NewMemStore(), nil })

	svc := &fakeServices{
		systemInfo: &sdk.SystemInfo{Version: "1.0.0"}, // server 1.0
		userResp:   goodUserResp(),
	}
	r := runChecks(context.Background(), &Options{}, svc, "1.5.0") // CLI 1.5

	v := r.Checks[2]
	if v.Name != "server_version" {
		t.Fatalf("Checks[2] = %q, want server_version", v.Name)
	}
	if v.Status != StatusWarn {
		t.Errorf("server_version status = %q, want warn (server older than CLI)", v.Status)
	}
	// compat.Compat hint contains "server is older" — that's the load-bearing
	// substring agents may pattern-match. Not asserting full message text
	// since compat.Compat owns the wording.
	if !strings.Contains(v.Details, "older") {
		t.Errorf("server_version details should mention older server, got %q", v.Details)
	}
	// Summary: warned counted, failed=0, all_passed flipped.
	if r.Summary.Warned != 1 {
		t.Errorf("expected Warned=1, got summary %+v", r.Summary)
	}
	if r.Summary.Failed != 0 {
		t.Errorf("expected Failed=0, got summary %+v", r.Summary)
	}
	if r.Summary.AllPassed {
		t.Error("AllPassed must be false when any check is warn")
	}

	// Wire shape: warn-only run still has summary.failed=0 (bare data
	// carries the signal; exit code stays 0).
	out, _ := iostreams.SetForTest(t)
	emit(&cmdutil.JSONOptions{}, r)
	body := out.String()
	if strings.Contains(body, `"ok":`) {
		t.Errorf("bare doctor output must not carry envelope keys, got %q", body)
	}
	if !strings.Contains(body, `"failed":0`) {
		t.Errorf("bare output must carry failed:0 on warn-only run, got %q", body)
	}
	if !strings.Contains(body, `"status":"warn"`) {
		t.Errorf("bare output must surface status=warn, got %q", body)
	}
}

// TestDoctor_HardErrorStillFails guards that a different-major skew remains
// fail (not warn). Without this, a later refactor could silently downgrade
// HardError to warn and we'd lose exit-1 on incompatible servers.
func TestDoctor_HardErrorStillFails(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	_, _ = iostreams.SetForTest(t)
	withCredStoreFactory(t, func() (secrets.Store, error) { return secrets.NewMemStore(), nil })

	svc := &fakeServices{
		systemInfo: &sdk.SystemInfo{Version: "2.0.0"},
		userResp:   goodUserResp(),
	}
	r := runChecks(context.Background(), &Options{}, svc, "1.0.0")
	if r.Checks[2].Status != StatusFail {
		t.Errorf("server_version status = %q, want fail (different major)", r.Checks[2].Status)
	}
	if r.Summary.Failed != 1 {
		t.Errorf("expected Failed=1, got %+v", r.Summary)
	}
}

// TestDoctor_KeychainFallbackWarns covers credential_storage's v0.2 third
// state: keyring unavailable, fell back to FileStore (agent containers,
// headless CI, WSL without DBus). The check should warn — secrets still
// persist (0600 file perms) but the OS-backed path was unreachable.
func TestDoctor_KeychainFallbackWarns(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	_, _ = iostreams.SetForTest(t)

	withCredStoreFactory(t, func() (secrets.Store, error) {
		// FileStore concrete type → fillCredentialStorageCheck reads warn.
		return secrets.NewFileStore()
	})

	svc := &fakeServices{
		userResp:   goodUserResp(),
		systemInfo: &sdk.SystemInfo{Version: "1.0.0"},
	}
	r := runChecks(context.Background(), &Options{}, svc, "1.0.0")

	cs := r.Checks[3]
	if cs.Name != "credential_storage" {
		t.Fatalf("Checks[3] = %q, want credential_storage", cs.Name)
	}
	if cs.Status != StatusWarn {
		t.Errorf("credential_storage status = %q, want warn (file fallback)", cs.Status)
	}
	if !strings.Contains(strings.ToLower(cs.Details), "file store") {
		t.Errorf("details should mention file store, got %q", cs.Details)
	}
	if r.Summary.Warned != 1 {
		t.Errorf("expected Warned=1, got %+v", r.Summary)
	}
	if r.Summary.Failed != 0 {
		t.Errorf("expected Failed=0 (warn doesn't fail), got %+v", r.Summary)
	}
}

// TestDoctor_CredStoreFactoryError surfaces the constructor failure path as
// fail (not warn) — distinguishes "cannot persist credentials at all" from
// "downgraded to file store".
func TestDoctor_CredStoreFactoryError(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	_, _ = iostreams.SetForTest(t)
	withCredStoreFactory(t, func() (secrets.Store, error) {
		return nil, errors.New("disk full")
	})

	r := runChecks(context.Background(), &Options{Offline: true}, &fakeServices{}, "1.0.0")
	if r.Checks[3].Status != StatusFail {
		t.Errorf("credential_storage status = %q, want fail on constructor error", r.Checks[3].Status)
	}
	if !strings.Contains(r.Checks[3].Details, "disk full") {
		t.Errorf("details should propagate underlying error, got %q", r.Checks[3].Details)
	}
}

// TestDoctor_BareJSON_WarnDoesNotSignalFail pins the wire contract: warn
// keeps summary.failed=0 (so exit code stays 0). emit() is the seam between
// Result and what agents observe.
func TestDoctor_BareJSON_WarnDoesNotSignalFail(t *testing.T) {
	out, _ := iostreams.SetForTest(t)
	r := Result{
		Summary: Summary{AllPassed: false, Passed: 3, Warned: 1},
		Checks: []Check{
			{Name: "base_url_reachable", Status: StatusOK},
			{Name: "auth_credential", Status: StatusOK},
			{Name: "server_version", Status: StatusWarn, Details: "server is older"},
			{Name: "credential_storage", Status: StatusOK},
		},
	}
	emit(&cmdutil.JSONOptions{}, r)
	got := out.String()
	if !strings.Contains(got, `"failed":0`) {
		t.Errorf("warn-only result must have summary.failed=0 (exit-0 signal), got %q", got)
	}
	if strings.Contains(got, `"ok":`) {
		t.Errorf("bare output must not carry envelope keys, got %q", got)
	}
}

// TestDoctor_BareJSON_FailRaisesSummary pins the dual: any fail surfaces in
// summary.failed (caller maps that to exit 1 via SilentError).
func TestDoctor_BareJSON_FailRaisesSummary(t *testing.T) {
	out, _ := iostreams.SetForTest(t)
	r := Result{
		Summary: Summary{AllPassed: false, Passed: 2, Failed: 1, Skipped: 1},
		Checks: []Check{
			{Name: "base_url_reachable", Status: StatusFail, Details: "refused"},
			{Name: "auth_credential", Status: StatusSkip},
			{Name: "server_version", Status: StatusOK},
			{Name: "credential_storage", Status: StatusOK},
		},
	}
	emit(&cmdutil.JSONOptions{}, r)
	got := out.String()
	if !strings.Contains(got, `"failed":1`) {
		t.Errorf("fail must surface in summary.failed, got %q", got)
	}
}

// TestDoctor_HumanMarker_Warn confirms the human-mode glyph appears for warn
// rows. Glyph choice is presentation-only; we pin via substring (no width
// alignment assertion since terminal-width handling is environmental).
func TestDoctor_HumanMarker_Warn(t *testing.T) {
	out, _ := iostreams.SetForTest(t)
	r := Result{
		Summary: Summary{Warned: 1},
		Checks: []Check{
			{Name: "server_version", Status: StatusWarn, Details: "older"},
		},
	}
	emit(nil, r)
	got := out.String()
	if !strings.Contains(got, "⚠") {
		t.Errorf("human output should contain ⚠ glyph for warn, got %q", got)
	}
	if !strings.Contains(got, "warn") {
		t.Errorf("human output should still contain status word `warn`, got %q", got)
	}
}

// TestDoctor_WarnedField_OmittedAtZero protects the JSON wire compactness:
// `warned` carries omitempty, so a clean run has no warned key. Existing
// agents inspecting older envelopes shouldn't see a sudden new field unless
// it actually fired.
func TestDoctor_WarnedField_OmittedAtZero(t *testing.T) {
	out, _ := iostreams.SetForTest(t)
	r := Result{
		Summary: Summary{AllPassed: true, Passed: 4},
		Checks: []Check{
			{Name: "base_url_reachable", Status: StatusOK},
			{Name: "auth_credential", Status: StatusOK},
			{Name: "server_version", Status: StatusOK},
			{Name: "credential_storage", Status: StatusOK},
		},
	}
	emit(&cmdutil.JSONOptions{}, r)
	got := out.String()
	if strings.Contains(got, `"warned"`) {
		t.Errorf("envelope should omit `warned` field when zero, got %q", got)
	}
}

// TestDoctor_RunE_FailReturnsSilentError is a behavior test on NewCmd: when
// any check is fail, RunE must return cmdutil.SilentError so the framework
// exit-1 path runs WITHOUT overwriting the data envelope emit() already
// wrote. This is the v0.2 contract change from v0.1's "always nil".
//
// SilenceErrors/SilenceUsage on the leaf cobra.Command suppress cobra's own
// "Error: ..." + usage dump that would otherwise leak to stderr when running
// the leaf in isolation (root.go sets these on the root, not on every leaf).
func TestDoctor_RunE_FailReturnsSilentError(t *testing.T) {
	// Force base_url to fail by leaving host empty.
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	_, _ = iostreams.SetForTest(t)
	withCredStoreFactory(t, func() (secrets.Store, error) { return secrets.NewMemStore(), nil })

	f := cmdutil.New()
	cmd := NewCmd(f)
	cmd.SilenceErrors = true
	cmd.SilenceUsage = true
	cmd.SetArgs([]string{"--json"})
	cmd.SetContext(context.Background())
	err := cmd.Execute()
	if !errors.Is(err, cmdutil.SilentError) {
		t.Errorf("RunE on fail must return SilentError, got %v", err)
	}
}

// TestDoctor_RunE_WarnReturnsNil — soft skew path stays exit-0. Pairs with
// TestDoctor_RunE_FailReturnsSilentError: warn does NOT trigger SilentError.
func TestDoctor_RunE_WarnReturnsNil(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	_, _ = iostreams.SetForTest(t)
	withCredStoreFactory(t, func() (secrets.Store, error) { return secrets.NewFileStore() })

	r := runChecks(context.Background(), &Options{Offline: true}, &fakeServices{}, "1.0.0")
	// Smoke-check: warn-only result, no fails.
	if r.Summary.Failed != 0 {
		t.Fatalf("setup error: expected Failed=0, got %+v", r.Summary)
	}
	if r.Summary.Warned == 0 {
		t.Fatalf("setup error: expected Warned>=1, got %+v", r.Summary)
	}
}
