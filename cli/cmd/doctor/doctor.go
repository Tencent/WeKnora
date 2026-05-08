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

// Check is one row in the report.
type Check struct {
	Name    string `json:"name"`
	Status  string `json:"status"` // ok / fail / skip
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
	cmd.Flags().BoolVar(&opts.NoCache, "no-cache", false, "Bypass server-info cache; force re-probe")
	cmd.Flags().BoolVar(&opts.Offline, "offline", false, "Skip network checks; only verify local keyring/file storage")
	cmd.Flags().BoolVar(&opts.JSONOut, "json", false, "Output JSON envelope")
	agent.SetAgentHelp(cmd, "Returns 4 health checks. AGENT short-circuit: read data.summary.all_passed; if false, inspect data.checks[].status (ok/fail/skip).")
	return cmd
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

	// 1. base_url_reachable
	if opts.Offline {
		checks[0].Status = "skip"
		checks[0].Details = "offline mode"
	} else {
		t0 := time.Now()
		if err := svc.PingBaseURL(ctx); err != nil {
			checks[0].Status = "fail"
			checks[0].Hint = "verify --base-url and network reachability"
			checks[0].Details = err.Error()
		} else {
			checks[0].Status = "ok"
			checks[0].Details = fmt.Sprintf("reachable in %s", time.Since(t0).Round(time.Millisecond))
		}
	}

	// 2. auth_credential — depends on base_url
	switch {
	case opts.Offline:
		checks[1].Status = "skip"
		checks[1].Details = "offline mode"
	case checks[0].Status == "fail":
		checks[1].Status = "skip"
		checks[1].Details = "prereq failed: base_url_reachable"
	default:
		_, err := svc.GetCurrentUser(ctx)
		if err != nil {
			checks[1].Status = "fail"
			checks[1].Hint = "run `weknora auth login`"
			checks[1].Details = err.Error()
		} else {
			checks[1].Status = "ok"
		}
	}

	// 3. server_version — depends on auth_credential
	switch {
	case opts.Offline:
		checks[2].Status = "skip"
		checks[2].Details = "offline mode"
	case checks[1].Status != "ok":
		checks[2].Status = "skip"
		checks[2].Details = "prereq failed: auth_credential"
	default:
		info, fromCache, err := loadOrProbeServerInfo(ctx, opts, svc)
		if err != nil {
			checks[2].Status = "fail"
			checks[2].Details = err.Error()
		} else {
			level, hint := compat.Compat(info.ServerVersion, cliVer)
			suffix := ""
			if fromCache {
				suffix = " (cached, pass --no-cache to refresh)"
			}
			if level == compat.HardError {
				checks[2].Status = "fail"
				checks[2].Hint = hint
				checks[2].Details = "server " + info.ServerVersion + suffix
			} else {
				checks[2].Status = "ok"
				if hint != "" {
					checks[2].Details = hint + suffix
				} else {
					checks[2].Details = fmt.Sprintf("server %s%s", info.ServerVersion, suffix)
				}
			}
		}
	}

	// 4. credential_storage — independent of network
	if _, err := secrets.NewBestEffortStore(); err != nil {
		checks[3].Status = "fail"
		checks[3].Details = err.Error()
		checks[3].Hint = "verify keyring access; falls back to file store"
	} else {
		checks[3].Status = "ok"
		checks[3].Details = "keyring or file storage available"
	}

	return Result{Summary: summarize(checks), Checks: checks}
}

// loadOrProbeServerInfo respects --no-cache:
//   --no-cache or stale/missing cache → call svc.GetSystemInfo + SaveCache
//   else → return cached Info (fromCache=true)
//
// SaveCache failure does NOT propagate — best-effort write. The probe
// data is still returned to the caller for the compat check.
func loadOrProbeServerInfo(ctx context.Context, opts *Options, svc Services) (info *compat.Info, fromCache bool, err error) {
	if !opts.NoCache {
		if cached, fresh, _ := compat.LoadCache(); fresh && cached != nil {
			return cached, true, nil
		}
	}
	sys, err := svc.GetSystemInfo(ctx)
	if err != nil {
		return nil, false, err
	}
	fresh := &compat.Info{ServerVersion: sys.Version, ProbedAt: time.Now()}
	_ = compat.SaveCache(fresh) // best-effort
	return fresh, false, nil
}

func summarize(cs []Check) Summary {
	s := Summary{}
	for _, c := range cs {
		switch c.Status {
		case "ok":
			s.Passed++
		case "fail":
			s.Failed++
		case "skip":
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
		case "fail":
			marker = "[fail]"
		case "skip":
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
func buildServices(f *cmdutil.Factory) (Services, error) {
	cli, err := f.Client()
	if err != nil {
		return nil, err
	}
	return &realServices{cli: cli}, nil
}

type realServices struct {
	cli *sdk.Client
}

// pingTimeout caps the HEAD /health probe so a wedged TCP connection
// can't hang doctor indefinitely.
const pingTimeout = 5 * time.Second

func (s *realServices) PingBaseURL(ctx context.Context) error {
	// WEKNORA_BASE_URL is the test-scaffold override; production should plumb
	// config.Host through (TODO v0.2: add Client.BaseURL() accessor in SDK).
	url := envOrDefault("WEKNORA_BASE_URL", "http://localhost:8080") + "/health"
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

func (s *realServices) GetCurrentUser(ctx context.Context) (*sdk.CurrentUserResponse, error) {
	return s.cli.GetCurrentUser(ctx)
}
func (s *realServices) GetSystemInfo(ctx context.Context) (*sdk.SystemInfo, error) {
	return s.cli.GetSystemInfo(ctx)
}

func envOrDefault(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}
