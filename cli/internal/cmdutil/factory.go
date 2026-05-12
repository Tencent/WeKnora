package cmdutil

import (
	"context"
	"errors"
	"fmt"
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
// Four closures (ADR-4):
//   - Config:   parses ~/.config/weknora/config.yaml (no network)
//   - Client:   constructs the SDK client; only Secrets is sync.Once-cached,
//               so callers should hold the returned *sdk.Client across
//               multiple SDK calls within one invocation
//   - Prompter: returns interactive prompter; agent mode returns AgentPrompter
//   - Secrets:  builds the OS keyring / file fallback credential store the
//     first time it is requested (probing the keyring at startup
//     would fork+exec on macOS and DBus-touch on Linux,
//     defeating the lazy contract above).
//
// IOStreams is intentionally NOT a Factory closure — it is the package singleton
// iostreams.IO. The bar to add a new closure is at least 2 commands sharing the
// same dependency; resist factory bloat.
//
// Client returns a *sdk.Client (the WeKnora SDK). Commands that want narrow
// service interfaces (per ADR-4) declare them in their own files and let the
// real SDK satisfy them implicitly via duck typing.
type Factory struct {
	Config   func() (*config.Config, error)
	Client   func() (*sdk.Client, error)
	Prompter func() prompt.Prompter
	Secrets  func() (secrets.Store, error)

	// ContextOverride, if non-empty, replaces config.CurrentContext for this
	// invocation only — set by the global --context flag in PersistentPreRun.
	// Buildable Config() / Client() honor it without writing to disk; matches
	// spec §1.2 "weknora --context foo kb list = single-shot override".
	ContextOverride string
}

// New constructs a production Factory wired to real config / SDK client.
//
// All closures are lazy: invoking --help, version, or shell completion runs
// none of them. Client and Secrets closures memoize via sync.Once so the
// SDK client is built (and the keyring is probed) at most once per process,
// even when Factory.ResolveKB internally calls f.Client() before the
// command's RunE calls it again — without this, name-resolved --kb paths
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
			// Map raw fs / parse errors to typed codes so envelopes don't
			// surface bare `server.error` for what's actually a local IO /
			// corrupt-config problem.
			if errors.Is(err, config.ErrCorrupt) {
				return nil, Wrapf(CodeLocalConfigCorrupt, err, "config malformed")
			}
			return nil, Wrapf(CodeLocalFileIO, err, "load config")
		}
		if f.ContextOverride != "" {
			cfg.CurrentContext = f.ContextOverride
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

// buildClient resolves the active context, loads the credentials from secrets,
// and constructs a *sdk.Client. Returns CodeAuthUnauthenticated when no
// credentials are available so the user gets the right hint to run
// `weknora auth login`.
func buildClient(f *Factory) (*sdk.Client, error) {
	cfg, err := f.Config()
	if err != nil {
		return nil, err
	}
	ctxName := cfg.CurrentContext
	if ctxName == "" {
		return nil, NewError(CodeAuthUnauthenticated, "no current context configured; run `weknora auth login` to set one up")
	}
	ctx, ok := cfg.Contexts[ctxName]
	if !ok {
		return nil, NewError(CodeLocalConfigCorrupt, fmt.Sprintf("config references unknown context %q", ctxName))
	}
	if ctx.Host == "" {
		return nil, NewError(CodeLocalConfigCorrupt, fmt.Sprintf("context %q has no host", ctxName))
	}

	opts := []sdk.ClientOption{}
	store, err := f.Secrets()
	if err != nil {
		return nil, Wrapf(CodeLocalKeychainDenied, err, "init secrets store")
	}
	// Only fetch the secrets the context actually references. Skipping the
	// unused fetch avoids a `security` exec (macOS) / DBus call (Linux) per
	// authenticated invocation.
	var accessToken string
	if ctx.TokenRef != "" {
		if access, err := loadSecret(store, ctxName, "access"); err != nil {
			return nil, err
		} else if access != "" {
			accessToken = access
			opts = append(opts, sdk.WithBearerToken(access))
		}
	}
	if ctx.APIKeyRef != "" {
		if apiKey, err := loadSecret(store, ctxName, "api_key"); err != nil {
			return nil, err
		} else if apiKey != "" {
			opts = append(opts, sdk.WithAPIKey(apiKey))
		}
	}
	// JWT contexts (have both access + refresh refs) get the transparent
	// 401-retry transport: on the first 401 from a non-/auth/* endpoint, the
	// transport reads the stored refresh token, calls /api/v1/auth/refresh,
	// persists the new pair, and replays the original request with the new
	// bearer. API-key contexts skip this (no refresh semantic) — a 401 from
	// them propagates as auth.unauthenticated for the caller to handle.
	if ctx.TokenRef != "" && ctx.RefreshRef != "" {
		refreshFn := func(rctx context.Context) (string, error) {
			return refreshAccessToken(rctx, store, ctx.Host, ctxName)
		}
		opts = append(opts, sdk.WithTransport(
			NewAuthRetryTransport(http.DefaultTransport, accessToken, refreshFn),
		))
	}
	// ctx.TenantID is intentionally NOT injected as X-Tenant-ID. Servers derive
	// tenant from the credential itself (JWT claim or API key prefix); the
	// header is only meaningful for explicit cross-tenant switching by users
	// with CanAccessAllTenants. Auto-mirroring the persisted tenant from config
	// breaks that contract — explicit flags (`--tenant=N` is the planned v0.1
	// entry point) are required before sending it. `tenant_id` stays in config
	// for `auth status` display only.
	return sdk.NewClient(ctx.Host, opts...), nil
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

// loadSecret returns the stored value for (context, key); ErrNotFound becomes
// ("", nil) so callers can treat "not configured" as success.
func loadSecret(store secrets.Store, context, key string) (string, error) {
	v, err := store.Get(context, key)
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
// being constructed — that one is itself wrapped by the transport, which
// would recurse on refresh. The refresh endpoint is unauthenticated apart
// from the refresh token in the body, so no credential options are needed.
func refreshAccessToken(ctx context.Context, store secrets.Store, host, ctxName string) (string, error) {
	return RefreshAndPersist(ctx, store, sdk.NewClient(host), ctxName)
}
