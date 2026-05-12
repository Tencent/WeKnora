// Package doctor implements `weknora doctor` — 4-item self-check (spec §1.2).
//
// Status semantics (4-tier):
//
//	ok   — passed
//	warn — soft problem; non-blocking (e.g. server minor older than CLI,
//	       keychain unavailable so falling back to file store)
//	fail — failed; "hint" actionable
//	skip — cascade-skipped (prereq failed) or --offline mode
//
// Envelope (v0.2):
//   - any check is fail   → envelope.ok=false, exit 1 (RunE returns SilentError
//     so the data envelope is still emitted; the framework's error-envelope
//     printer is bypassed for this command)
//   - warn only / all ok  → envelope.ok=true,  exit 0
//   - warn does NOT flip envelope.ok
//
// data.summary.all_passed gives the agent a one-line short-circuit; v0.2
// keeps it true ONLY when no warn / fail / skip checks are present. Agents
// SHOULD also inspect data.checks[].status to distinguish warn from ok.
package doctor

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/Tencent/WeKnora/cli/internal/agent"
	"github.com/Tencent/WeKnora/cli/internal/build"
	"github.com/Tencent/WeKnora/cli/internal/cmdutil"
	"github.com/Tencent/WeKnora/cli/internal/compat"
	"github.com/Tencent/WeKnora/cli/internal/format"
	"github.com/Tencent/WeKnora/cli/internal/iostreams"
	"github.com/Tencent/WeKnora/cli/internal/secrets"
	sdk "github.com/Tencent/WeKnora/client"
)

type Options struct {
	NoCache bool
	Offline bool
	JSONOut bool
}

// Status is the per-check outcome on the wire (JSON Marshal still emits the
// underlying string). Typed so cascade comparisons can't typo against bare
// "ok"/"fail"/"skip" string literals.
type Status string

const (
	StatusOK   Status = "ok"
	StatusWarn Status = "warn"
	StatusFail Status = "fail"
	StatusSkip Status = "skip"
)

// Check is one row in the report.
type Check struct {
	Name    string `json:"name"`
	Status  Status `json:"status"`
	Details string `json:"details,omitempty"`
	Hint    string `json:"hint,omitempty"`
}

// Summary is the agent-friendly short-circuit payload (spec §1.2).
//
// AllPassed is true only when there are zero warn/fail/skip rows; warn does
// not block exit-0 (envelope.ok stays true) but it does flip AllPassed so
// agents reading just the boolean still notice the soft issue.
type Summary struct {
	AllPassed bool `json:"all_passed"`
	Passed    int  `json:"passed"`
	Warned    int  `json:"warned,omitempty"`
	Failed    int  `json:"failed"`
	Skipped   int  `json:"skipped"`
}

// Result is the full envelope data.
type Result struct {
	Summary Summary `json:"summary"`
	Checks  []Check `json:"checks"`
}

// Services groups the narrow interfaces doctor needs. Implemented by
// realServices (production) and fakeServices (tests).
type Services interface {
	PingBaseURL(ctx context.Context) error
	GetCurrentUser(ctx context.Context) (*sdk.CurrentUserResponse, error)
	GetSystemInfo(ctx context.Context) (*sdk.SystemInfo, error)
}

// NewCmd builds `weknora doctor`.
func NewCmd(f *cmdutil.Factory) *cobra.Command {
	opts := &Options{}
	cmd := &cobra.Command{
		Use:   "doctor",
		Short: "Run 4 self-checks: base URL, auth, server version, credential storage",
		Args:  cobra.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			svc, err := buildServices(f)
			if err != nil {
				return err
			}
			cliVer, _, _ := build.Info()
			r := runChecks(c.Context(), opts, svc, cliVer)
			emit(opts, r)
			// v0.2 exit-code policy: fail → exit 1; warn / ok / skip → exit 0.
			// SilentError suppresses both the human "error: ..." line and the
			// error envelope printer, so the data envelope already written by
			// emit() is the only stdout content.
			if r.Summary.Failed > 0 {
				return cmdutil.SilentError
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&opts.NoCache, "no-cache", false, "Bypass server-info cache (located at $XDG_CACHE_HOME/weknora/server-info.yaml); force re-probe")
	cmd.Flags().BoolVar(&opts.Offline, "offline", false, "Skip network checks; only verify local keyring/file storage (credential_storage check still runs)")
	cmd.Flags().BoolVar(&opts.JSONOut, "json", false, "Output JSON envelope")
	agent.SetAgentHelp(cmd, "Returns 4 health checks. AGENT short-circuit: read data.summary.all_passed; if false, inspect data.checks[].status (ok/warn/fail/skip). exit 1 only when any status=fail; warn does not change envelope.ok.")
	return cmd
}

// cascade implements the two short-circuits every gated check shares:
// offline-mode skip and prereq-failed skip. Returns true when the check has
// been completed (Status set on c) and the caller should NOT run its body.
//
// A prereq is considered "passing" if its Status is OK or Warn — warn signals
// a soft problem that does not block downstream functionality. Only fail/skip
// cascades into a downstream skip.
func cascade(c *Check, offline bool, prereqs ...*Check) bool {
	if offline {
		c.Status, c.Details = StatusSkip, "offline mode"
		return true
	}
	for _, p := range prereqs {
		if p.Status != StatusOK && p.Status != StatusWarn {
			c.Status, c.Details = StatusSkip, "prereq failed: "+p.Name
			return true
		}
	}
	return false
}

// runChecks executes the 4-item check matrix with cascade-skip semantics.
// Pure function over Services so tests can drive it directly.
func runChecks(ctx context.Context, opts *Options, svc Services, cliVer string) Result {
	checks := []Check{
		{Name: "base_url_reachable"},
		{Name: "auth_credential"},
		{Name: "server_version"},
		{Name: "credential_storage"},
	}

	// 1. base_url_reachable — gated by offline only.
	if !cascade(&checks[0], opts.Offline) {
		t0 := time.Now()
		if err := svc.PingBaseURL(ctx); err != nil {
			checks[0].Status = StatusFail
			checks[0].Hint = "verify the host configured for the active context (run `weknora auth login --host=...`) and network reachability"
			checks[0].Details = err.Error()
		} else {
			checks[0].Status = StatusOK
			checks[0].Details = fmt.Sprintf("reachable in %s", time.Since(t0).Round(time.Millisecond))
		}
	}

	// 2. auth_credential — needs base_url.
	if !cascade(&checks[1], opts.Offline, &checks[0]) {
		if _, err := svc.GetCurrentUser(ctx); err != nil {
			checks[1].Status = StatusFail
			checks[1].Hint = "run `weknora auth login`"
			checks[1].Details = err.Error()
		} else {
			checks[1].Status = StatusOK
		}
	}

	// 3. server_version — needs auth_credential.
	if !cascade(&checks[2], opts.Offline, &checks[1]) {
		info, fromCache, err := loadOrProbeServerInfo(ctx, opts, svc)
		if err != nil {
			checks[2].Status = StatusFail
			checks[2].Details = err.Error()
		} else {
			fillVersionCheck(&checks[2], info, cliVer, fromCache)
		}
	}

	// 4. credential_storage — independent of network; never gated by offline.
	fillCredentialStorageCheck(&checks[3])

	return Result{Summary: summarize(checks), Checks: checks}
}

// fillVersionCheck applies compat.Compat to (server, cli) version pair and
// sets Status/Details/Hint on c. fromCache toggles the "cached" suffix —
// the loader knows authoritatively which branch it took, time-based
// derivation from ProbedAt is unreliable since SaveCache uses time.Now().
//
// v0.2 mapping:
//
//	compat.OK        → StatusOK
//	compat.SoftWarn  → StatusWarn  (server older but in-range; soft skew)
//	compat.HardError → StatusFail  (incompatible major; upgrade required)
func fillVersionCheck(c *Check, info *compat.Info, cliVer string, fromCache bool) {
	level, hint := compat.Compat(info.ServerVersion, cliVer)
	suffix := ""
	if fromCache {
		suffix = " (cached, pass --no-cache to refresh)"
	}
	switch level {
	case compat.HardError:
		c.Status = StatusFail
		c.Hint = hint
		c.Details = "server " + info.ServerVersion + suffix
	case compat.SoftWarn:
		c.Status = StatusWarn
		// Lowercase, no trailing punctuation: matches existing details style
		// ("reachable in N", "keyring or file storage available"). The hint
		// from compat.Compat already says "server is older (server X, client Y)…"
		c.Details = hint + suffix
		c.Hint = "some new commands may degrade gracefully; upgrade server when convenient"
	default:
		c.Status = StatusOK
		if hint != "" {
			c.Details = hint + suffix
		} else {
			c.Details = fmt.Sprintf("server %s%s", info.ServerVersion, suffix)
		}
	}
}

// credStoreFactory is the seam tests use to inject a fake-store outcome —
// keyring success, file-store fallback, or hard failure — without touching
// the lazy-resolve buildServices contract (round-4 fix). Production stays
// at secrets.NewBestEffortStore.
var credStoreFactory = func() (secrets.Store, error) { return secrets.NewBestEffortStore() }

// SetCredStoreFactoryForTest overrides the credential-storage factory used by
// runChecks and returns a cleanup function that restores the previous value.
// Exported so out-of-package tests (notably cli/acceptance/contract) can
// pin the credential_storage outcome — otherwise the check probes the real
// OS keyring, which is present on macOS dev machines but not on Linux CI
// runners without libsecret, producing host-dependent test flakes.
//
// Usage:
//
//	restore := doctor.SetCredStoreFactoryForTest(func() (secrets.Store, error) {
//	    return secrets.NewMemStore(), nil // not *FileStore → StatusOK
//	})
//	defer restore()
func SetCredStoreFactoryForTest(fn func() (secrets.Store, error)) (restore func()) {
	saved := credStoreFactory
	credStoreFactory = fn
	return func() { credStoreFactory = saved }
}

// fillCredentialStorageCheck distinguishes the three terminal states the
// secrets layer can produce:
//
//	keyring usable  → StatusOK   (preferred path)
//	file fallback   → StatusWarn (keyring unavailable, secrets still persist
//	                  with 0600 file perms — agent containers / WSL hit this)
//	construction fails → StatusFail
//
// Detection of "fallback to file store" relies on the type returned by
// secrets.NewBestEffortStore: a *FileStore concrete value means keyring was
// unavailable and the layer degraded. The Ref() URI scheme would also work
// but type-assertion is structurally more robust to scheme renames.
func fillCredentialStorageCheck(c *Check) {
	store, err := credStoreFactory()
	if err != nil {
		c.Status = StatusFail
		c.Details = err.Error()
		c.Hint = "verify keyring access; falls back to file store"
		return
	}
	if _, isFile := store.(*secrets.FileStore); isFile {
		c.Status = StatusWarn
		c.Details = "falling back to file store: keychain unavailable"
		c.Hint = "secrets persist at 0600 under $XDG_CONFIG_HOME/weknora/secrets/; install / unlock keyring for OS-backed storage"
		return
	}
	c.Status = StatusOK
	c.Details = "keyring or file storage available"
}

// loadOrProbeServerInfo respects --no-cache: load fresh cache when allowed,
// else call compat.Probe (which wraps svc.GetSystemInfo) and persist. Cache
// write is best-effort. Returns fromCache so the caller can render the
// "cached" presentation hint without a brittle ProbedAt heuristic.
func loadOrProbeServerInfo(ctx context.Context, opts *Options, svc Services) (info *compat.Info, fromCache bool, err error) {
	if !opts.NoCache {
		if cached, fresh, _ := compat.LoadCache(); fresh && cached != nil {
			return cached, true, nil
		}
	}
	probed, err := compat.Probe(ctx, svc)
	if err != nil {
		return nil, false, err
	}
	_ = compat.SaveCache(probed)
	return probed, false, nil
}

func summarize(cs []Check) Summary {
	s := Summary{}
	for _, c := range cs {
		switch c.Status {
		case StatusOK:
			s.Passed++
		case StatusWarn:
			s.Warned++
		case StatusFail:
			s.Failed++
		case StatusSkip:
			s.Skipped++
		}
	}
	s.AllPassed = s.Failed == 0 && s.Skipped == 0 && s.Warned == 0
	return s
}

// emit renders the doctor result. JSON path constructs the envelope directly
// rather than calling format.Success because envelope.ok must reflect "no
// fail" — warn does not flip it (per package doc), but fail does. We can't
// use format.Failure either, since that drops the data field.
func emit(opts *Options, r Result) {
	if opts.JSONOut {
		_ = format.WriteEnvelope(iostreams.IO.Out, format.Envelope{
			OK:   r.Summary.Failed == 0,
			Data: r,
		})
		return
	}
	for _, c := range r.Checks {
		// %-2s for the glyph: most are 1 column, leaves room for one trailing
		// space. Status word follows so screen-readers / non-glyph terminals
		// still get the textual classification.
		line := fmt.Sprintf("%-2s  %-20s  %s", marker(c.Status), c.Name, c.Status)
		if c.Details != "" {
			line += "  (" + c.Details + ")"
		}
		fmt.Fprintln(iostreams.IO.Out, line)
		if c.Hint != "" {
			fmt.Fprintf(iostreams.IO.Out, "    hint: %s\n", c.Hint)
		}
	}
	fmt.Fprintf(iostreams.IO.Out, "\nsummary: %d passed, %d warned, %d failed, %d skipped\n",
		r.Summary.Passed, r.Summary.Warned, r.Summary.Failed, r.Summary.Skipped)
}

// marker returns the human-mode prefix glyph for a check status.
//
// Agent / JSON consumers read the stable status string from
// data.checks[].status; the glyphs are presentation-only and never appear
// in --json output.
func marker(s Status) string {
	switch s {
	case StatusFail:
		return "✗"
	case StatusWarn:
		return "⚠"
	case StatusSkip:
		return "⊘"
	default:
		return "✓"
	}
}

// buildServices wires the Factory closures into the doctor.Services interface.
// Reads the active context's host so PingBaseURL targets the user's actual
// server, not localhost.
//
// Critically: this does NOT pre-resolve f.Client(). doctor's package promise
// (top comment) is that credential_storage runs even when no auth is set up —
// e.g. first-time `weknora doctor` to diagnose setup. Pre-resolving Client
// here would early-exit with auth.unauthenticated before any check runs,
// contradicting the docs. Instead, GetCurrentUser / GetSystemInfo lazily
// resolve and surface their own failure as a per-check StatusFail.
func buildServices(f *cmdutil.Factory) (Services, error) {
	cfg, err := f.Config()
	if err != nil {
		return nil, err
	}
	host := ""
	if ctx, ok := cfg.Contexts[cfg.CurrentContext]; ok {
		host = ctx.Host
	}
	// WEKNORA_BASE_URL still wins as a test/dev override; production reads host.
	if v := os.Getenv("WEKNORA_BASE_URL"); v != "" {
		host = v
	}
	return &realServices{f: f, host: host}, nil
}

type realServices struct {
	f    *cmdutil.Factory
	host string
}

// pingTimeout caps the HEAD /health probe so a wedged TCP connection
// can't hang doctor indefinitely.
const pingTimeout = 5 * time.Second

func (s *realServices) PingBaseURL(ctx context.Context) error {
	if s.host == "" {
		return fmt.Errorf("no host configured for active context")
	}
	url := s.host + "/health"
	ctx, cancel := context.WithTimeout(ctx, pingTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, url, nil)
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 500 {
		return fmt.Errorf("server returned %d", resp.StatusCode)
	}
	return nil
}

// GetCurrentUser lazily resolves the SDK client. When no context is configured
// or credentials missing, f.Client() returns auth.unauthenticated; we surface
// that as the auth_credential check's failure rather than aborting doctor.
func (s *realServices) GetCurrentUser(ctx context.Context) (*sdk.CurrentUserResponse, error) {
	cli, err := s.f.Client()
	if err != nil {
		return nil, err
	}
	return cli.GetCurrentUser(ctx)
}

// GetSystemInfo lazily resolves the SDK client (same rationale as GetCurrentUser).
// In the cascade ordering, auth_credential gates server_version, so this only
// runs when auth_credential succeeded — but the lazy resolution keeps doctor
// useful when only credential_storage is checkable (e.g., user not yet logged in).
func (s *realServices) GetSystemInfo(ctx context.Context) (*sdk.SystemInfo, error) {
	cli, err := s.f.Client()
	if err != nil {
		return nil, err
	}
	return cli.GetSystemInfo(ctx)
}

