// Package doctor implements `weknora doctor` — 4-item self-check (spec §1.2).
//
// Status semantics (3-tier, from larksuite/cli's pass/fail/warn/skip set):
//
//	ok   — passed
//	fail — failed; "hint" actionable
//	skip — cascade-skipped (prereq failed) or --offline mode
//
// Envelope: ok=true normally; data.summary.all_passed gives the agent a
// one-line short-circuit (spec §1.2 防 envelope.ok=true 误判).
//
// Special: base URL completely unreachable + non-offline → no checks can be
// initiated → caller may decide to surface envelope.ok=false. v0.1 minimal
// approach: even base_url fail still runs credential_storage (independent),
// so envelope.ok stays true; agents read data.summary.failed > 0.
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

// Options captures the CLI flags for `weknora doctor`.
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
type Summary struct {
	AllPassed bool `json:"all_passed"`
	Passed    int  `json:"passed"`
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
		RunE: func(c *cobra.Command, _ []string) error {
			svc, err := buildServices(f)
			if err != nil {
				return err
			}
			cliVer, _, _ := build.Info()
			r := runChecks(c.Context(), opts, svc, cliVer)
			emit(opts, r)
			return nil // doctor 自身不返回 error;失败状态在 data.checks
		},
	}
	cmd.Flags().BoolVar(&opts.NoCache, "no-cache", false, "Bypass server-info cache (located at $XDG_CACHE_HOME/weknora/server-info.yaml); force re-probe")
	cmd.Flags().BoolVar(&opts.Offline, "offline", false, "Skip network checks; only verify local keyring/file storage (credential_storage check still runs)")
	cmd.Flags().BoolVar(&opts.JSONOut, "json", false, "Output JSON envelope")
	agent.SetAgentHelp(cmd, "Returns 4 health checks. AGENT short-circuit: read data.summary.all_passed; if false, inspect data.checks[].status (ok/fail/skip).")
	return cmd
}

// cascade implements the two short-circuits every gated check shares:
// offline-mode skip and prereq-failed skip. Returns true when the check has
// been completed (Status set on c) and the caller should NOT run its body.
func cascade(c *Check, offline bool, prereqs ...*Check) bool {
	if offline {
		c.Status, c.Details = StatusSkip, "offline mode"
		return true
	}
	for _, p := range prereqs {
		if p.Status != StatusOK {
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
	if _, err := secrets.NewBestEffortStore(); err != nil {
		checks[3].Status = StatusFail
		checks[3].Details = err.Error()
		checks[3].Hint = "verify keyring access; falls back to file store"
	} else {
		checks[3].Status = StatusOK
		checks[3].Details = "keyring or file storage available"
	}

	return Result{Summary: summarize(checks), Checks: checks}
}

// fillVersionCheck applies compat.Compat to (server, cli) version pair and
// sets Status/Details/Hint on c. fromCache toggles the "cached" suffix —
// the loader knows authoritatively which branch it took, time-based
// derivation from ProbedAt is unreliable since SaveCache uses time.Now().
func fillVersionCheck(c *Check, info *compat.Info, cliVer string, fromCache bool) {
	level, hint := compat.Compat(info.ServerVersion, cliVer)
	suffix := ""
	if fromCache {
		suffix = " (cached, pass --no-cache to refresh)"
	}
	if level == compat.HardError {
		c.Status = StatusFail
		c.Hint = hint
		c.Details = "server " + info.ServerVersion + suffix
		return
	}
	c.Status = StatusOK
	if hint != "" {
		c.Details = hint + suffix
	} else {
		c.Details = fmt.Sprintf("server %s%s", info.ServerVersion, suffix)
	}
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
		case StatusFail:
			s.Failed++
		case StatusSkip:
			s.Skipped++
		}
	}
	s.AllPassed = s.Failed == 0 && s.Skipped == 0
	return s
}

func emit(opts *Options, r Result) {
	if opts.JSONOut {
		_ = format.WriteEnvelope(iostreams.IO.Out, format.Success(r, nil))
		return
	}
	for _, c := range r.Checks {
		marker := "[ok]"
		switch c.Status {
		case StatusFail:
			marker = "[fail]"
		case StatusSkip:
			marker = "[skip]"
		}
		line := fmt.Sprintf("%-6s  %-20s  %s", marker, c.Name, c.Status)
		if c.Details != "" {
			line += "  (" + c.Details + ")"
		}
		fmt.Fprintln(iostreams.IO.Out, line)
		if c.Hint != "" {
			fmt.Fprintf(iostreams.IO.Out, "    hint: %s\n", c.Hint)
		}
	}
	fmt.Fprintf(iostreams.IO.Out, "\nsummary: %d passed, %d failed, %d skipped\n",
		r.Summary.Passed, r.Summary.Failed, r.Summary.Skipped)
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

