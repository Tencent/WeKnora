package configcmd

import (
	"fmt"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/Tencent/WeKnora/cli/internal/cmdutil"
	"github.com/Tencent/WeKnora/cli/internal/config"
	"github.com/Tencent/WeKnora/cli/internal/format"
	"github.com/Tencent/WeKnora/cli/internal/iostreams"
	"github.com/Tencent/WeKnora/cli/internal/projectlink"
	"github.com/Tencent/WeKnora/cli/internal/secrets"
	"github.com/Tencent/WeKnora/cli/internal/xdg"
)

// viewFields enumerates the fields surfaced for `--format json` discovery on
// `config view`. Flat shape — each entry is a top-level key of envelope.data.
var viewFields = []string{
	"active_profile", "profile_source", "auth_source", "host",
	"kb_id", "kb_source",
	"log_level", "log_level_source",
	"format_default",
	"config_file", "cache_dir", "secrets", "project_link",
}

// viewData is the flat JSON payload describing the resolved config and where
// each value came from. Sources are stable human strings (e.g. "--profile
// flag") so an agent can explain the resolution without re-deriving it.
type viewData struct {
	ActiveProfile  string `json:"active_profile"`
	ProfileSource  string `json:"profile_source"`
	AuthSource     string `json:"auth_source"`
	Host           string `json:"host"`
	KBID           string `json:"kb_id"`
	KBSource       string `json:"kb_source"`
	LogLevel       string `json:"log_level"`
	LogLevelSource string `json:"log_level_source"`
	FormatDefault  string `json:"format_default"`
	ConfigFile     string `json:"config_file"`
	CacheDir       string `json:"cache_dir"`
	Secrets        string `json:"secrets"`
	ProjectLink    string `json:"project_link"`
}

// NewCmdView builds `weknora config view`. Read-only inspection of the
// resolved config and resolution chain. Never builds the SDK client and
// never issues a network request: KB resolution uses ResolveKBLocal, secrets
// storage is detected from the store type, and everything else is read from
// config.yaml / env / flags.
func NewCmdView(f *cmdutil.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "view",
		Short: "Show the resolved configuration and where each value came from",
		Long: `Print the configuration currently in effect and its resolution chain:
the active profile (and its source), the profile host, the resolved knowledge
base (and source), log level, default output format, and the on-disk paths for
config / cache / secrets / project link.

Read-only: no values are mutated and no network request is issued. Succeeds
even when no profile or KB is configured (sources report "(none)" /
"(unresolved)").`,
		Args: cobra.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			fopts, err := cmdutil.CheckFormatFlag(c)
			if err != nil {
				return err
			}
			fopts.ResolveDefault(iostreams.IO.IsStdoutTTY())
			return runView(c, f, fopts)
		},
	}
	cmdutil.AddFormatFlag(cmd, viewFields...)
	cmdutil.SetAgentHelp(cmd, cmdutil.AgentHelp{
		UsedFor: "inspect the resolved CLI config and resolution chain (active profile, host, kb, log level, default format, file paths) without mutating or hitting the network",
		Examples: []string{
			"weknora config view",
			"weknora config view --format json",
			"weknora config view --jq '.data.active_profile'",
		},
		Output: "envelope.data is {active_profile, profile_source, auth_source, host, kb_id, kb_source, log_level, log_level_source, format_default, config_file, cache_dir, secrets, project_link}. auth_source reports whether the active credential is the profile+keyring or a stateless WEKNORA_TOKEN/WEKNORA_API_KEY env override.",
	})
	return cmd
}

func runView(cmd *cobra.Command, f *cmdutil.Factory, fopts *cmdutil.FormatOptions) error {
	d := resolveView(cmd, f)

	if fopts.WantsJSON() {
		return fopts.Emit(iostreams.IO.Out, d, nil)
	}

	tw := tabwriter.NewWriter(iostreams.IO.Out, 0, 0, 2, ' ', 0)
	row := func(k, v string) { fmt.Fprintf(tw, "%s\t%s\n", k, format.DashIfEmpty(v)) }
	row("active_profile", d.ActiveProfile)
	row("profile_source", d.ProfileSource)
	row("auth_source", d.AuthSource)
	row("host", d.Host)
	row("kb_id", d.KBID)
	row("kb_source", d.KBSource)
	row("log_level", d.LogLevel)
	row("log_level_source", d.LogLevelSource)
	row("format_default", d.FormatDefault)
	row("config_file", d.ConfigFile)
	row("cache_dir", d.CacheDir)
	row("secrets", d.Secrets)
	row("project_link", d.ProjectLink)
	return tw.Flush()
}

// resolveView assembles the resolved-config snapshot. Pure read: reads
// config.yaml (no network), env vars, flags, and the local project link.
func resolveView(cmd *cobra.Command, f *cmdutil.Factory) viewData {
	d := viewData{}

	// Profile + its source.
	d.ActiveProfile, d.ProfileSource = resolveProfile(f)

	// Host: looked up from the active profile in config (best-effort; empty
	// when unset, no profile, or config unreadable).
	if d.ActiveProfile != "" && f.Config != nil {
		if cfg, err := f.Config(); err == nil && cfg != nil {
			if prof, ok := cfg.Profiles[d.ActiveProfile]; ok {
				d.Host = prof.Host
			}
		}
	}

	// Auth source: stateless env credentials (WEKNORA_TOKEN/WEKNORA_API_KEY)
	// override the profile + keyring for the actual client, so report that —
	// otherwise host/profile above would silently describe a profile the env
	// credential bypassed. WEKNORA_HOST (when set) is the host that env cred
	// authenticates against.
	if active, kind := cmdutil.EnvCredential(); active {
		d.AuthSource = kind + " env (stateless; bypasses profile + keyring)"
		if h := strings.TrimSpace(os.Getenv("WEKNORA_HOST")); h != "" {
			d.Host = h
		}
	} else if d.ActiveProfile != "" {
		d.AuthSource = "profile + keyring"
	} else {
		d.AuthSource = "(none)"
	}

	// KB id + source (never network — ResolveKBLocal).
	d.KBID, d.KBSource = resolveKB(cmd, f)

	// Log level + source.
	d.LogLevel, d.LogLevelSource = resolveLogLevel(cmd)

	// Default output format (configured default, not the per-invocation flag).
	d.FormatDefault = resolveFormatDefault()

	// Paths.
	if p, err := config.Path(); err == nil {
		d.ConfigFile = p
	}
	if p, err := xdg.Path("XDG_CACHE_HOME", ".cache"); err == nil {
		d.CacheDir = p
	}
	d.Secrets = resolveSecrets(f)
	d.ProjectLink = resolveProjectLink()

	return d
}

// resolveProfile mirrors Factory.ActiveProfile's precedence but also reports
// which level supplied the value, so `config view` can explain the choice.
func resolveProfile(f *cmdutil.Factory) (name, source string) {
	if f.ProfileOverride != "" {
		return f.ProfileOverride, "--profile flag"
	}
	if v := os.Getenv("WEKNORA_PROFILE"); v != "" {
		return v, "WEKNORA_PROFILE env"
	}
	if f.Config != nil {
		if cfg, err := f.Config(); err == nil && cfg != nil && cfg.CurrentProfile != "" {
			return cfg.CurrentProfile, "config current_profile"
		}
	}
	return "", "(none)"
}

// resolveKB reports the locally-resolved KB id and its source. ResolveKBLocal
// never hits the SDK; a CodeKBIDRequired error means nothing is in scope, which
// config view reports as "(unresolved)" rather than failing.
func resolveKB(cmd *cobra.Command, f *cmdutil.Factory) (id, source string) {
	id, err := f.ResolveKBLocal(cmd)
	if err != nil {
		return "", "(unresolved)"
	}
	// Re-derive which level supplied it (ResolveKBLocal returns only the value).
	if v, _ := cmd.Flags().GetString("kb"); v != "" {
		return id, "--kb flag"
	}
	if v := os.Getenv("WEKNORA_KB_ID"); v != "" {
		return id, "WEKNORA_KB_ID env"
	}
	if cwd, werr := os.Getwd(); werr == nil {
		if path, found, derr := projectlink.Discover(cwd); derr == nil && found {
			return id, "project link " + path
		}
	}
	return id, "(unresolved)"
}

// resolveLogLevel reports the effective level and its source, matching
// ResolveLogLevel's precedence (flag > env > default).
func resolveLogLevel(cmd *cobra.Command) (level, source string) {
	level, _ = cmdutil.ResolveLogLevel(cmd, iostreams.IO.Err)
	if cmd != nil {
		if fl := cmd.Flags().Lookup("log-level"); fl != nil && fl.Changed && cmdutil.IsValidLogLevel(fl.Value.String()) {
			return level, "--log-level flag"
		}
	}
	if v := os.Getenv("WEKNORA_LOG_LEVEL"); v != "" && cmdutil.IsValidLogLevel(v) {
		return level, "WEKNORA_LOG_LEVEL env"
	}
	return level, "default"
}

// resolveFormatDefault reports the configured default output format: the
// config.yaml defaults.format value, else WEKNORA_FORMAT, else the hard
// default. This is the *default* used when --format is unset — not the
// per-invocation --format flag value.
func resolveFormatDefault() string {
	cfg, err := config.Load()
	if err == nil && cfg != nil && cfg.Defaults.Format != "" {
		return cfg.Defaults.Format
	}
	if v := os.Getenv("WEKNORA_FORMAT"); v == "text" || v == "json" || v == "ndjson" {
		return v
	}
	return string(cmdutil.DefaultFormatMode)
}

// resolveSecrets reports where credentials are stored: "file <path-root>" when
// the keyring is unavailable and the layer fell back to a 0600 FileStore,
// otherwise "keyring". Detection mirrors doctor's credential_storage check
// (type-assert on the best-effort store). On construction failure, reports the
// file fallback root so the user knows where to look.
func resolveSecrets(f *cmdutil.Factory) string {
	var store secrets.Store
	var err error
	if f.Secrets != nil {
		store, err = f.Secrets()
	} else {
		store, err = secrets.NewBestEffortStore()
	}
	if err == nil && store != nil {
		if _, isFile := store.(*secrets.FileStore); !isFile {
			return "keyring"
		}
	}
	if p, perr := xdg.Path("XDG_CONFIG_HOME", ".config", "secrets"); perr == nil {
		return "file " + p
	}
	return "file"
}

// resolveProjectLink reports the discovered .weknora/project.yaml path or
// "(none)". Walks up from cwd like ResolveKBLocal does.
func resolveProjectLink() string {
	cwd, err := os.Getwd()
	if err != nil {
		return "(none)"
	}
	if path, found, derr := projectlink.Discover(cwd); derr == nil && found {
		return path
	}
	return "(none)"
}
