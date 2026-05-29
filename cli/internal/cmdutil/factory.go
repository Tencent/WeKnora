package cmdutil

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"sync"

	"github.com/spf13/cobra"

	"github.com/Tencent/WeKnora/cli/internal/config"
	"github.com/Tencent/WeKnora/cli/internal/iostreams"
	"github.com/Tencent/WeKnora/cli/internal/projectlink"
	"github.com/Tencent/WeKnora/cli/internal/prompt"
	"github.com/Tencent/WeKnora/cli/internal/secrets"
	sdk "github.com/Tencent/WeKnora/client"
)

// Factory is the dependency container injected at command construction. Each
// closure is lazy: --help / completion / `weknora version` must NOT trigger
// HTTP, keyring access, or filesystem I/O beyond the bare minimum.
//
// Four closures:
//   - Config:   parses ~/.config/weknora/config.yaml (no network)
//   - Client:   constructs the SDK client; only Secrets is sync.Once-cached,
//     so callers should hold the returned *sdk.Client across
//     multiple SDK calls within one invocation
//   - Prompter: returns interactive prompter; agent mode returns AgentPrompter
//   - Secrets:  builds the OS keyring / file fallback credential store the
//     first time it is requested (probing the keyring at startup
//     would fork+exec on macOS and DBus-touch on Linux,
//     defeating the lazy contract above).
//
// IOStreams is intentionally NOT a Factory closure - it is the package singleton
// iostreams.IO. The bar to add a new closure is at least 2 commands sharing the
// same dependency; resist factory bloat.
//
// Client returns a *sdk.Client (the WeKnora SDK). Commands that want narrow
// service interfaces declare them in their own files and let the real SDK
// satisfy them implicitly via duck typing.
type Factory struct {
	Config   func() (*config.Config, error)
	Client   func() (*sdk.Client, error)
	Prompter func() prompt.Prompter
	Secrets  func() (secrets.Store, error)

	// ProfileOverride, if non-empty, replaces config.CurrentProfile for this
	// invocation only - set by the global --profile flag in PersistentPreRun.
	// Buildable Config() / Client() honor it without writing to disk.
	ProfileOverride string
}

// New constructs a production Factory wired to real config / SDK client.
//
// All closures are lazy: invoking --help, version, or shell completion runs
// none of them. Client and Secrets closures memoize via sync.Once so the
// SDK client is built (and the keyring is probed) at most once per process,
// even when Factory.ResolveKB internally calls f.Client() before the
// command's RunE calls it again - without this, name-resolved --kb paths
// would build two clients with two AuthRetryTransports holding independent
// token state.
func New() *Factory {
	var (
		secretsOnce  sync.Once
		secretsStore secrets.Store
		secretsErr   error

		clientOnce sync.Once
		client     *sdk.Client
		clientErr  error
	)
	f := &Factory{}
	f.Config = func() (*config.Config, error) {
		cfg, err := config.Load()
		if err != nil {
			// Map raw fs / parse errors to typed codes so the stderr line
			// doesn't surface bare `server.error` for what's actually a
			// local IO / corrupt-config problem.
			if errors.Is(err, config.ErrCorrupt) {
				return nil, Wrapf(CodeLocalConfigCorrupt, err, "config malformed")
			}
			return nil, Wrapf(CodeLocalFileIO, err, "load config")
		}
		if f.ProfileOverride != "" {
			cfg.CurrentProfile = f.ProfileOverride
		}
		return cfg, nil
	}
	f.Client = func() (*sdk.Client, error) {
		clientOnce.Do(func() { client, clientErr = buildClient(f) })
		return client, clientErr
	}
	f.Prompter = func() prompt.Prompter {
		if iostreams.IO.IsStdoutTTY() && iostreams.IO.IsStderrTTY() {
			return prompt.NewTTYPrompter()
		}
		return prompt.AgentPrompter{}
	}
	f.Secrets = func() (secrets.Store, error) {
		secretsOnce.Do(func() {
			secretsStore, secretsErr = secrets.NewBestEffortStore()
		})
		return secretsStore, secretsErr
	}
	return f
}

// buildClient resolves the active profile, loads the credentials from secrets,
// and constructs a *sdk.Client. Returns CodeAuthUnauthenticated when no
// credentials are available so the user gets the right hint to run
// `weknora auth login`.
func buildClient(f *Factory) (*sdk.Client, error) {
	cfg, err := f.Config()
	if err != nil {
		return nil, err
	}
	profileName := cfg.CurrentProfile
	if profileName == "" {
		return nil, NewError(CodeAuthUnauthenticated, "no current profile configured; run `weknora auth login` to set one up")
	}
	prof, ok := cfg.Profiles[profileName]
	if !ok {
		// If the user explicitly overrode the profile (via --profile flag or
		// WEKNORA_PROFILE env), it's a bad argument - not a corrupt config file.
		// The destructive "remove config.yaml" hint would be catastrophic for a typo.
		if f.ProfileOverride != "" {
			return nil, NewError(CodeInputInvalidArgument,
				fmt.Sprintf("profile %q not configured", profileName)).
				WithHint("list available profiles with `weknora profile list`").
				WithRetryCommand("weknora profile list")
		}
		// ProfileOverride is empty: config.CurrentProfile points at a missing entry.
		// That's a genuinely corrupt config file.
		return nil, NewError(CodeLocalConfigCorrupt, fmt.Sprintf("config references unknown profile %q", profileName))
	}
	if prof.Host == "" {
		return nil, NewError(CodeLocalConfigCorrupt, fmt.Sprintf("profile %q has no host", profileName))
	}

	opts := []sdk.ClientOption{}
	store, err := f.Secrets()
	if err != nil {
		return nil, Wrapf(CodeLocalKeychainDenied, err, "init secrets store")
	}
	// Only fetch the secrets the profile actually references. Skipping the
	// unused fetch avoids a `security` exec (macOS) / DBus call (Linux) per
	// authenticated invocation.
	var accessToken string
	if prof.TokenRef != "" {
		if access, err := LoadSecret(store, profileName, "access"); err != nil {
			return nil, err
		} else if access != "" {
			accessToken = access
			opts = append(opts, sdk.WithBearerToken(access))
		}
	}
	if prof.APIKeyRef != "" {
		if apiKey, err := LoadSecret(store, profileName, "api_key"); err != nil {
			return nil, err
		} else if apiKey != "" {
			opts = append(opts, sdk.WithAPIKey(apiKey))
		}
	}
	// JWT profiles (have both access + refresh refs) get the transparent
	// 401-retry transport: on the first 401 from a non-/auth/* endpoint, the
	// transport reads the stored refresh token, calls /api/v1/auth/refresh,
	// persists the new pair, and replays the original request with the new
	// bearer. API-key profiles skip this (no refresh semantic) - a 401 from
	// them propagates as auth.unauthenticated for the caller to handle.
	if prof.TokenRef != "" && prof.RefreshRef != "" {
		refreshFn := func(rctx context.Context) (string, error) {
			return refreshAccessToken(rctx, store, prof.Host, profileName)
		}
		opts = append(opts, sdk.WithTransport(
			NewAuthRetryTransport(http.DefaultTransport, accessToken, refreshFn),
		))
	}
	// prof.TenantID is intentionally NOT injected as X-Tenant-ID. Servers derive
	// tenant from the credential itself (JWT claim or API key prefix); the
	// header is only meaningful for explicit cross-tenant switching by users
	// with CanAccessAllTenants. Auto-mirroring the persisted tenant from config
	// breaks that contract - explicit cross-tenant flags would be required
	// before sending it. `tenant_id` stays in config for `auth status` display only.
	return sdk.NewClient(prof.Host, opts...), nil
}

// AddKBFlag registers the standard `--kb` flag that ResolveKB reads. Use this
// instead of duplicating the flag declaration in every command that scopes to
// a knowledge base — one source of truth for flag name and help text.
func AddKBFlag(cmd *cobra.Command) {
	cmd.Flags().String("kb", "", "Knowledge base UUID or name (overrides env / project link)")
}

// ResolveKB returns the active KB id for the running command, applying the
// 4-level fallback chain (highest to lowest):
//  1. --kb flag (kb_<...> id passed through; anything else resolved via
//     ListKnowledgeBases as a name → id lookup)
//  2. WEKNORA_KB_ID env (always an explicit id)
//  3. .weknora/project.yaml (walk-up from cwd)
//  4. error: kb required
func (f *Factory) ResolveKB(cmd *cobra.Command) (string, error) {
	if v, _ := cmd.Flags().GetString("kb"); v != "" {
		if IsKBID(v) {
			return v, nil
		}
		c, err := f.Client()
		if err != nil {
			return "", err
		}
		return ResolveKBNameToID(cmd.Context(), c, v)
	}
	if v := os.Getenv("WEKNORA_KB_ID"); v != "" {
		return v, nil
	}
	cwd, err := os.Getwd()
	if err == nil {
		if path, found, derr := projectlink.Discover(cwd); derr == nil && found {
			p, lerr := projectlink.Load(path)
			if lerr != nil {
				return "", Wrapf(CodeProjectLinkCorrupt, lerr, "read project link")
			}
			if p.KBID != "" {
				return p.KBID, nil
			}
		}
	}
	return "", NewError(CodeKBIDRequired, "kb is required")
}

// ResolveKBLocal mirrors ResolveKB but never calls the SDK. When --kb is a
// name (not a UUID) it returns the raw value as-is instead of looking up the
// id server-side. Intended for dry-run paths where SDK side effects must be
// avoided; the dry-run plan reports the user-supplied identifier verbatim,
// and the live execution path resolves the name → id at call time.
func (f *Factory) ResolveKBLocal(cmd *cobra.Command) (string, error) {
	if v, _ := cmd.Flags().GetString("kb"); v != "" {
		return v, nil
	}
	if v := os.Getenv("WEKNORA_KB_ID"); v != "" {
		return v, nil
	}
	cwd, err := os.Getwd()
	if err == nil {
		if path, found, derr := projectlink.Discover(cwd); derr == nil && found {
			p, lerr := projectlink.Load(path)
			if lerr != nil {
				return "", Wrapf(CodeProjectLinkCorrupt, lerr, "read project link")
			}
			if p.KBID != "" {
				return p.KBID, nil
			}
		}
	}
	return "", NewError(CodeKBIDRequired, "kb is required")
}

// ApplyLogLevel resolves --log-level / WEKNORA_LOG_LEVEL (in priority order)
// and applies the result to the SDK's debug logger. Intended to be called
// from the root command's PersistentPreRunE so the resolved level is in
// effect before any SDK call.
//
// Returns a typed error if the user passed an explicit --log-level with
// an invalid value — matches the strictness of --format validation
// (env values stay silent-fallthrough; flag values are strict).
func (f *Factory) ApplyLogLevel(cmd *cobra.Command, stderr io.Writer) error {
	if cmd != nil {
		if fl := cmd.Flags().Lookup("log-level"); fl != nil && fl.Changed {
			if !IsValidLogLevel(fl.Value.String()) {
				return NewFlagError(fmt.Errorf(
					"invalid --log-level %q: must be error | warn | info | debug", fl.Value.String()))
			}
		}
	}
	level, _ := ResolveLogLevel(cmd, stderr)
	sdk.SetDebugLevel(level)
	return nil
}

// LoadSecret fetches a named secret for the given profile from the keyring.
// Returns ("", nil) when the secret is absent (ErrNotFound); a real keyring
// access failure surfaces as CodeLocalKeychainDenied. Used by buildClient
// to assemble SDK auth options and by `auth token` to expose the raw
// credential for shell scripting.
func LoadSecret(store secrets.Store, profile, key string) (string, error) {
	v, err := store.Get(profile, key)
	if errors.Is(err, secrets.ErrNotFound) {
		return "", nil
	}
	if err != nil {
		return "", Wrapf(CodeLocalKeychainDenied, err, "load %s", key)
	}
	return v, nil
}

// refreshAccessToken is the closure target injected into AuthRetryTransport's
// refreshFn. A fresh SDK Client is built here rather than reusing the one
// being constructed - that one is itself wrapped by the transport, which
// would recurse on refresh. The refresh endpoint is unauthenticated apart
// from the refresh token in the body, so no credential options are needed.
func refreshAccessToken(ctx context.Context, store secrets.Store, host, profileName string) (string, error) {
	return RefreshAndPersist(ctx, store, sdk.NewClient(host), profileName)
}

// ActiveProfile returns the resolved profile name for this invocation:
//  1. ProfileOverride (set by --profile flag in root PersistentPreRunE)
//  2. WEKNORA_PROFILE env var
//  3. Config's CurrentProfile (the persisted active profile name)
//  4. Empty string when nothing is configured (envelope omits the field).
func (f *Factory) ActiveProfile() string {
	if f.ProfileOverride != "" {
		return f.ProfileOverride
	}
	if v := os.Getenv("WEKNORA_PROFILE"); v != "" {
		return v
	}
	if f.Config == nil {
		return ""
	}
	cfg, err := f.Config()
	if err != nil || cfg == nil {
		return ""
	}
	return cfg.CurrentProfile
}
